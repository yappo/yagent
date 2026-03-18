package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"yagent/internal/domain"
)

type Config struct {
	MaxParallelAgents       int
	MaxHandoffDepth         int
	MaxVerificationAttempts int
	DefaultTimeout          time.Duration
	DefaultModel            string
	DisablePhaseHarness     bool
	ForcePlanner            bool
	ForceResearcher         bool
	TraceSink               domain.TraceSink
	Approver                domain.Approver
	ContextEngine           domain.ContextEngine
	RunStore                domain.RunStateStore
	MemoryStore             domain.RepoMemoryStore
}

type Service struct {
	model      domain.ModelClient
	tools      domain.ToolExecutor
	catalog    domain.AgentCatalog
	config     Config
	observer   domain.ToolObserver
	runCounter atomic.Uint64
	mu         sync.Mutex
	listeners  map[chan domain.ExecutionEvent]struct{}
}

type agentSessionState struct {
	enabledCapabilities map[string]bool
}

func New(model domain.ModelClient, tools domain.ToolExecutor, catalog domain.AgentCatalog, config Config) *Service {
	if config.MaxParallelAgents < 1 {
		config.MaxParallelAgents = 1
	}
	if config.MaxHandoffDepth < 1 {
		config.MaxHandoffDepth = 1
	}
	if config.DefaultTimeout <= 0 {
		config.DefaultTimeout = 120 * time.Second
	}
	if config.MaxVerificationAttempts < 1 {
		config.MaxVerificationAttempts = 2
	}
	if config.DisablePhaseHarness {
		config.MaxVerificationAttempts = 1
	}
	return &Service{
		model:     model,
		tools:     tools,
		catalog:   catalog,
		config:    config,
		listeners: map[chan domain.ExecutionEvent]struct{}{},
	}
}

func (s *Service) SubscribeEvents() (<-chan domain.ExecutionEvent, func()) {
	ch := make(chan domain.ExecutionEvent, 64)
	s.mu.Lock()
	s.listeners[ch] = struct{}{}
	s.mu.Unlock()

	cancel := func() {
		s.mu.Lock()
		if _, ok := s.listeners[ch]; ok {
			delete(s.listeners, ch)
			close(ch)
		}
		s.mu.Unlock()
	}

	return ch, cancel
}

func (s *Service) SetObserver(observer domain.ToolObserver) {
	s.observer = observer
}

func (s *Service) RunTurn(ctx context.Context, request domain.TurnRequest) (domain.TurnResult, error) {
	run := s.newRunState(request)
	if request.ResumeID != "" && s.config.RunStore != nil {
		if restored, err := s.loadResumeState(ctx, request.ResumeID); err == nil && restored != nil {
			run = restored
			run.Profile = fallbackString(request.Profile, run.Profile)
			run.Messages = append(run.Messages, cloneMessages(request.Messages)...)
			if latest := latestUserMessage(request.Messages); latest != "" {
				run.UserGoal = latest
			}
		}
	}
	if err := s.saveRun(ctx, run); err != nil {
		return domain.TurnResult{}, err
	}

	manager, ok := s.catalog.Resolve("manager")
	if !ok {
		return domain.TurnResult{}, fmt.Errorf("manager agent が見つかりません")
	}
	prompt := strings.TrimSpace(run.UserGoal)
	if prompt == "" {
		prompt = strings.TrimSpace(latestUserMessage(request.Messages))
	}
	inventory := s.buildAgentInventory()
	run.Artifacts = append(run.Artifacts, newArtifact(run, domain.RunPhaseIntake, "planner", "Agent inventory", "agent_inventory", inventoryArtifactSummary(inventory)))
	_ = s.saveRun(ctx, run)

	var allEvents []domain.ExecutionEvent
	if s.config.DisablePhaseHarness {
		run.ExecutionPlan = disabledHarnessExecutionPlan(prompt)
		run.Plan = planNodesFromExecutionPlan(run.ExecutionPlan)
		run.Artifacts = append(run.Artifacts, newArtifact(run, domain.RunPhaseIntake, "manager", "Execution plan", "execution_plan", stablePlanJSON(run.ExecutionPlan)))
		run.Checkpoints = append(run.Checkpoints, checkpoint(run, domain.RunPhaseIntake, run.ExecutionPlan.Summary))
		result, events, err := s.runDirectPhase(ctx, run, manager, request)
		allEvents = append(allEvents, events...)
		if err != nil {
			run.Status = domain.RunStatusFailed
			_ = s.saveRun(ctx, run)
			return domain.TurnResult{}, err
		}
		run.Status = domain.RunStatusCompleted
		run.CurrentPhase = domain.RunPhaseExecute
		run.Checkpoints = append(run.Checkpoints, checkpoint(run, domain.RunPhaseExecute, result.Message.Content))
		artifact := domain.RunArtifact{
			ID:        fmt.Sprintf("artifact-%d", len(run.Artifacts)+1),
			Name:      "Final response",
			Kind:      "final_response",
			Phase:     domain.RunPhaseExecute,
			AgentID:   result.Message.AgentID,
			Summary:   truncateSummary(result.Message.Content),
			Content:   result.Message.Content,
			CreatedAt: time.Now(),
		}
		run.Artifacts = append(run.Artifacts, artifact)
		if s.config.MemoryStore != nil {
			_ = s.rememberRun(ctx, run)
		}
		_ = s.saveRun(ctx, run)
		return domain.TurnResult{Message: result.Message, Events: allEvents, Run: run}, nil
	}
	if shouldBypassPlanner(prompt) {
		run.ExecutionPlan = directConversationPlan(prompt)
		run.Plan = planNodesFromExecutionPlan(run.ExecutionPlan)
		run.Artifacts = append(run.Artifacts, newArtifact(run, domain.RunPhaseIntake, "manager", "Execution plan", "execution_plan", stablePlanJSON(run.ExecutionPlan)))
		run.Checkpoints = append(run.Checkpoints, checkpoint(run, domain.RunPhaseIntake, run.ExecutionPlan.Summary))
		result, events, err := s.runDirectPhase(ctx, run, manager, request)
		allEvents = append(allEvents, events...)
		if err != nil {
			run.Status = domain.RunStatusFailed
			_ = s.saveRun(ctx, run)
			return domain.TurnResult{}, err
		}
		run.Status = domain.RunStatusCompleted
		run.CurrentPhase = domain.RunPhaseExecute
		run.Checkpoints = append(run.Checkpoints, checkpoint(run, domain.RunPhaseExecute, result.Message.Content))
		artifact := domain.RunArtifact{
			ID:        fmt.Sprintf("artifact-%d", len(run.Artifacts)+1),
			Name:      "Final response",
			Kind:      "final_response",
			Phase:     domain.RunPhaseExecute,
			AgentID:   result.Message.AgentID,
			Summary:   truncateSummary(result.Message.Content),
			Content:   result.Message.Content,
			CreatedAt: time.Now(),
		}
		run.Artifacts = append(run.Artifacts, artifact)
		if s.config.MemoryStore != nil {
			_ = s.rememberRun(ctx, run)
		}
		_ = s.saveRun(ctx, run)
		return domain.TurnResult{Message: result.Message, Events: allEvents, Run: run}, nil
	}

	executionPlan, planEvents, err := s.runPlanPhase(ctx, run, request, inventory)
	allEvents = append(allEvents, planEvents...)
	if err != nil {
		run.Status = domain.RunStatusFailed
		_ = s.saveRun(ctx, run)
		return domain.TurnResult{}, err
	}

	executionResult, executeEvents, err := s.runExecutePhase(ctx, run, executionPlan, request)
	allEvents = append(allEvents, executeEvents...)
	if err != nil {
		run.Status = domain.RunStatusFailed
		_ = s.saveRun(ctx, run)
		return domain.TurnResult{}, err
	}

	finalExecution := executionResult
	if len(executionPlan.Verify) > 0 {
		for attempt := 1; attempt <= s.config.MaxVerificationAttempts; attempt++ {
			verification, verifyEvents, err := s.runVerifyPhase(ctx, run, executionPlan, request, finalExecution, attempt)
			allEvents = append(allEvents, verifyEvents...)
			if err != nil {
				run.Status = domain.RunStatusFailed
				_ = s.saveRun(ctx, run)
				return domain.TurnResult{}, err
			}
			if verification.Status == "pass" {
				break
			}
			if attempt >= s.config.MaxVerificationAttempts {
				break
			}
			repaired, recoverEvents, err := s.runRecoverPhase(ctx, run, executionPlan, request, verification, attempt+1)
			allEvents = append(allEvents, recoverEvents...)
			if err != nil {
				run.Status = domain.RunStatusFailed
				_ = s.saveRun(ctx, run)
				return domain.TurnResult{}, err
			}
			finalExecution = repaired
		}
	}

	final, finalizeEvents, err := s.runFinalizePhase(ctx, run, executionPlan, request, finalExecution)
	allEvents = append(allEvents, finalizeEvents...)
	if err != nil {
		run.Status = domain.RunStatusFailed
		_ = s.saveRun(ctx, run)
		return domain.TurnResult{}, err
	}

	run.Status = domain.RunStatusCompleted
	run.CurrentPhase = lastExecutionPlanPhase(executionPlan)
	artifact := domain.RunArtifact{
		ID:        fmt.Sprintf("artifact-%d", len(run.Artifacts)+1),
		Name:      "Final response",
		Kind:      "final_response",
		Phase:     run.CurrentPhase,
		AgentID:   final.Message.AgentID,
		Summary:   truncateSummary(final.Message.Content),
		Content:   final.Message.Content,
		CreatedAt: time.Now(),
	}
	run.Artifacts = append(run.Artifacts, artifact)
	run.Checkpoints = append(run.Checkpoints, checkpoint(run, run.CurrentPhase, final.Message.Content))
	_ = s.rememberRun(ctx, run)
	_ = s.saveRun(ctx, run)
	return domain.TurnResult{Message: final.Message, Events: allEvents, Run: run}, nil
}

func (s *Service) withManagerDelegationBias(manager domain.AgentSpec, messages []domain.Message) domain.AgentSpec {
	if !shouldBiasManagerTowardDelegation(messages) {
		return manager
	}
	extra := "You must actively use subagents for this request. Start by delegating planning or research to planner and/or researcher before doing broad repository inspection yourself. Manager should coordinate, merge findings, and produce the final answer rather than personally traversing many directories or files."
	if strings.Contains(manager.Instruction, extra) {
		return manager
	}
	if manager.Instruction == "" {
		manager.Instruction = extra
		return manager
	}
	manager.Instruction += "\n\n" + extra
	return manager
}

func (s *Service) runAgent(ctx context.Context, invocation domain.AgentInvocation, depth int) (domain.AgentResult, error) {
	if depth > s.config.MaxHandoffDepth {
		err := fmt.Errorf("最大 handoff 深度 (%d) に達しました", s.config.MaxHandoffDepth)
		failed := s.newEvent(
			invocation.RunID,
			invocation.ParentRunID,
			invocation.Agent.ID,
			"agent_failed",
			invocation.Phase,
			invocation.Attempt,
			"failed",
			err.Error(),
			"",
			nil,
			countContextItems(invocation.Messages, invocation.Context),
		)
		return domain.AgentResult{Status: "failed", Events: []domain.ExecutionEvent{failed}}, err
	}

	session := newAgentSession(invocation)
	events := []domain.ExecutionEvent{s.newEvent(
		invocation.RunID,
		invocation.ParentRunID,
		invocation.Agent.ID,
		"agent_started",
		invocation.Phase,
		invocation.Attempt,
		"running",
		invocation.Context.TaskBrief,
		"",
		nil,
		countContextItems(invocation.Messages, invocation.Context),
	)}

	messages := cloneMessages(invocation.Messages)
	maxTurns := invocation.Agent.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 200
	}

	for {
		for turn := 0; turn < maxTurns; turn++ {
			allTools := append(s.tools.Definitions(invocation.Agent), s.agentToolDefinitions(invocation.Agent)...)
			tools := visibleTools(invocation.Agent, allTools, session)
			llmCtx, cancel := context.WithTimeout(ctx, s.timeoutFor(invocation.Agent))
			response, err := s.model.Generate(llmCtx, domain.ModelRequest{
				Agent:        invocation.Agent,
				Instructions: buildInvocationInstructions(invocation.Agent.Instruction, invocation.Context),
				Messages:     messages,
				Phase:        invocation.Phase,
				Model:        s.modelName(invocation),
				Stream:       invocation.Stream,
				Tools:        tools,
			})
			cancel()
			if err != nil {
				events = append(events, s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "agent_failed", invocation.Phase, invocation.Attempt, "failed", err.Error(), "", nil, countContextItems(messages, invocation.Context)))
				return domain.AgentResult{}, err
			}
			events = append(events, s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "llm_called", invocation.Phase, invocation.Attempt, "running", response.FinishReason, "", map[string]any{"visible_tools": len(tools)}, countContextItems(messages, invocation.Context)))

			if len(response.Message.ToolCalls) == 0 {
				response.Message.AgentID = invocation.Agent.ID
				events = append(events, s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "agent_completed", invocation.Phase, invocation.Attempt, "done", response.Message.Content, "", nil, countContextItems(messages, invocation.Context)))
				return domain.AgentResult{
					Status:    "completed",
					Message:   response.Message,
					Summary:   response.Message.Content,
					Artifacts: map[string]any{},
					Events:    events,
				}, nil
			}

			assistantMessage := response.Message
			assistantMessage.AgentID = invocation.Agent.ID
			for idx := range assistantMessage.ToolCalls {
				assistantMessage.ToolCalls[idx].RequestedByAgentID = invocation.Agent.ID
				if assistantMessage.ToolCalls[idx].Purpose == "" {
					assistantMessage.ToolCalls[idx].Purpose = defaultPurpose(assistantMessage.ToolCalls[idx])
				}
			}
			messages = append(messages, assistantMessage)

			results, direct, childEvents, err := s.executeCalls(ctx, invocation, assistantMessage.ToolCalls, depth, session)
			events = append(events, childEvents...)
			if err != nil {
				events = append(events, s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "agent_failed", invocation.Phase, invocation.Attempt, "failed", err.Error(), "", nil, countContextItems(messages, invocation.Context)))
				return domain.AgentResult{}, err
			}
			if direct != nil {
				direct.Events = append(events, direct.Events...)
				return *direct, nil
			}
			messages = append(messages, results...)
		}

		if !s.approveContinue(ctx, invocation, maxTurns) {
			err := fmt.Errorf("agent %q が最大ターン数 (%d) に達しました", invocation.Agent.ID, maxTurns)
			events = append(events, s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "agent_failed", invocation.Phase, invocation.Attempt, "failed", err.Error(), "", nil, countContextItems(messages, invocation.Context)))
			return domain.AgentResult{}, err
		}

		events = append(events, s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "agent_continued", invocation.Phase, invocation.Attempt, "running", fmt.Sprintf("最大ターン数 %d 到達後に継続が承認されました", maxTurns), "", nil, countContextItems(messages, invocation.Context)))
	}
}

func (s *Service) executeCalls(ctx context.Context, invocation domain.AgentInvocation, calls []domain.ToolCall, depth int, session *agentSessionState) ([]domain.Message, *domain.AgentResult, []domain.ExecutionEvent, error) {
	if len(calls) == 0 {
		return nil, nil, nil, nil
	}

	executable := s.prepareExecutableCalls(invocation.Agent, calls, session)
	if s.shouldRunSequentially(executable) {
		return s.executeSequential(ctx, invocation, executable, depth, session)
	}
	return s.executeParallel(ctx, invocation, executable, depth, session)
}

type executableCall struct {
	call             domain.ToolCall
	definition       domain.ToolDefinition
	targetAgent      *domain.AgentSpec
	handoff          bool
	ephemeral        *domain.AgentSpec
	enableCapability string
	discovery        bool
	blockedBy        string
}

func (s *Service) prepareExecutableCalls(agent domain.AgentSpec, calls []domain.ToolCall, session *agentSessionState) []executableCall {
	executable := make([]executableCall, 0, len(calls))
	definitions := definitionMap(s.tools.Definitions(agent))
	for _, spec := range s.catalog.List() {
		definitions[delegateToolName(spec.ID)] = agentToolDefinition(spec, false)
		if spec.Mode == domain.AgentModeHandoff {
			definitions[handoffToolName(spec.ID)] = agentToolDefinition(spec, true)
		}
	}
	definitions["run_ephemeral_agent"] = ephemeralToolDefinition()
	definitions["list_capabilities"] = capabilityListDefinition()
	definitions["enable_capability"] = capabilityEnableDefinition()

	for _, call := range calls {
		item := executableCall{call: call, definition: definitions[call.Name]}
		switch {
		case strings.HasPrefix(call.Name, "delegate_to_"):
			id := strings.TrimPrefix(call.Name, "delegate_to_")
			if spec, ok := s.catalog.Resolve(id); ok {
				if reason := blockedDelegationReason(agent, spec, false); reason != "" {
					item.blockedBy = reason
				} else {
					item.targetAgent = &spec
				}
			}
		case strings.HasPrefix(call.Name, "handoff_to_"):
			id := strings.TrimPrefix(call.Name, "handoff_to_")
			if spec, ok := s.catalog.Resolve(id); ok {
				if reason := blockedDelegationReason(agent, spec, true); reason != "" {
					item.blockedBy = reason
				} else {
					item.targetAgent = &spec
					item.handoff = true
				}
			}
		case call.Name == "run_ephemeral_agent":
			spec := ephemeralSpecFromCall(call)
			item.ephemeral = &spec
			item.definition = ephemeralToolDefinition()
		case call.Name == "list_capabilities":
			item.discovery = true
			item.definition = capabilityListDefinition()
		case call.Name == "enable_capability":
			if capability := stringArg(call.Arguments, "capability"); capability != "" {
				item.enableCapability = capability
			}
			item.definition = capabilityEnableDefinition()
		case requiresCapabilityEnable(item.definition, session):
			item.blockedBy = fmt.Sprintf("capability %q を有効化してから再試行してください", item.definition.CapabilityGroup)
		}
		executable = append(executable, item)
	}
	return executable
}

func (s *Service) executeSequential(ctx context.Context, invocation domain.AgentInvocation, executable []executableCall, depth int, session *agentSessionState) ([]domain.Message, *domain.AgentResult, []domain.ExecutionEvent, error) {
	results := make([]domain.Message, 0, len(executable))
	events := []domain.ExecutionEvent{}
	for _, item := range executable {
		message, direct, callEvents, err := s.executeOne(ctx, invocation, item, depth, session)
		events = append(events, callEvents...)
		if err != nil {
			return nil, nil, events, err
		}
		if direct != nil {
			return nil, direct, events, nil
		}
		results = append(results, message)
	}
	return results, nil, events, nil
}

func (s *Service) executeParallel(ctx context.Context, invocation domain.AgentInvocation, executable []executableCall, depth int, session *agentSessionState) ([]domain.Message, *domain.AgentResult, []domain.ExecutionEvent, error) {
	type result struct {
		index  int
		msg    domain.Message
		direct *domain.AgentResult
		events []domain.ExecutionEvent
	}

	events := make([][]domain.ExecutionEvent, len(executable))
	results := make([]domain.Message, len(executable))
	var directMu sync.Mutex
	var direct *domain.AgentResult

	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(s.config.MaxParallelAgents)

	for idx := range executable {
		idx := idx
		group.Go(func() error {
			msg, directResult, callEvents, err := s.executeOne(groupCtx, invocation, executable[idx], depth, session)
			events[idx] = callEvents
			if err != nil {
				return err
			}
			if directResult != nil {
				directMu.Lock()
				if direct == nil {
					direct = directResult
				}
				directMu.Unlock()
				return nil
			}
			results[idx] = msg
			return nil
		})
	}

	if err := group.Wait(); err != nil {
		return nil, nil, flattenEvents(events), err
	}
	if direct != nil {
		return nil, direct, flattenEvents(events), nil
	}
	return results, nil, flattenEvents(events), nil
}

func (s *Service) executeOne(ctx context.Context, invocation domain.AgentInvocation, item executableCall, depth int, session *agentSessionState) (domain.Message, *domain.AgentResult, []domain.ExecutionEvent, error) {
	switch {
	case item.blockedBy != "":
		detail := item.call.Name + ": " + item.blockedBy
		events := []domain.ExecutionEvent{s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "tool_failed", invocation.Phase, invocation.Attempt, "failed", detail, "", nil, countContextItems(invocation.Messages, invocation.Context))}
		return toolMessage(item.call, "エラー: "+item.blockedBy), nil, events, nil
	case item.discovery:
		output := describeCapabilities(invocation.Agent, append(s.tools.Definitions(invocation.Agent), s.agentToolDefinitions(invocation.Agent)...), session)
		events := []domain.ExecutionEvent{s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "tool_called", invocation.Phase, invocation.Attempt, "done", item.call.Name, "", map[string]any{"capability_count": strings.Count(output, "\n") + 1}, countContextItems(invocation.Messages, invocation.Context))}
		return toolMessage(item.call, output), nil, events, nil
	case item.enableCapability != "":
		if session.enabledCapabilities == nil {
			session.enabledCapabilities = map[string]bool{}
		}
		session.enabledCapabilities[item.enableCapability] = true
		output := fmt.Sprintf("capability %q を有効化しました", item.enableCapability)
		events := []domain.ExecutionEvent{s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "tool_called", invocation.Phase, invocation.Attempt, "done", output, "", map[string]any{"capability": item.enableCapability}, countContextItems(invocation.Messages, invocation.Context))}
		return toolMessage(item.call, output), nil, events, nil
	case item.targetAgent != nil:
		return s.executeDelegation(ctx, invocation, *item.targetAgent, item.call, item.handoff, depth)
	case item.ephemeral != nil:
		result, err := s.runAgent(ctx, domain.AgentInvocation{
			RunID:       s.nextRunID("ephemeral"),
			ParentRunID: invocation.RunID,
			RootRunID:   invocation.RootRunID,
			Agent:       s.resolveModel(*item.ephemeral, invocation.Model),
			Messages:    childMessages(item.call),
			Context:     s.buildContextForInvocation(invocation, *item.ephemeral, item.call, invocation.Phase),
			Phase:       invocation.Phase,
			Attempt:     invocation.Attempt,
			Model:       invocation.Model,
			Stream:      false,
		}, depth+1)
		if err != nil {
			events := []domain.ExecutionEvent{s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "agent_failed", invocation.Phase, invocation.Attempt, "failed", err.Error(), "", nil, countContextItems(invocation.Messages, invocation.Context))}
			return toolMessage(item.call, "エラー: "+err.Error()), nil, events, nil
		}
		return toolMessage(item.call, result.Message.Content), nil, result.Events, nil
	default:
		s.notifyToolEvent(ctx, domain.ToolEvent{Phase: "start", Call: item.call})
		result := s.tools.Execute(ctx, invocation.Agent, item.call)
		s.notifyToolEvent(ctx, domain.ToolEvent{Phase: "finish", Call: item.call, Result: result})
		eventType := "tool_called"
		detail := item.call.Name
		if !result.Success {
			eventType = "tool_failed"
			detail = item.call.Name + ": " + result.Output
		}
		status := "done"
		if !result.Success {
			status = "failed"
		}
		events := []domain.ExecutionEvent{s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, eventType, invocation.Phase, invocation.Attempt, status, detail, "", nil, countContextItems(invocation.Messages, invocation.Context))}
		return toolMessage(item.call, result.Output), nil, events, nil
	}
}

func (s *Service) notifyToolEvent(ctx context.Context, event domain.ToolEvent) {
	if s.observer != nil {
		s.observer.OnToolEvent(ctx, event)
	}
}

func (s *Service) executeDelegation(ctx context.Context, invocation domain.AgentInvocation, target domain.AgentSpec, call domain.ToolCall, handoff bool, depth int) (domain.Message, *domain.AgentResult, []domain.ExecutionEvent, error) {
	child := domain.AgentInvocation{
		RunID:       s.nextRunID(target.ID),
		ParentRunID: invocation.RunID,
		RootRunID:   invocation.RootRunID,
		Agent:       s.resolveModel(target, invocation.Model),
		Messages:    childMessages(call),
		Context:     s.buildContextForInvocation(invocation, target, call, invocation.Phase),
		Phase:       invocation.Phase,
		Attempt:     invocation.Attempt,
		Model:       invocation.Model,
		Stream:      false,
	}
	startType := "delegate_started"
	if handoff {
		startType = "handoff_started"
	}
	startEvent := s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, startType, invocation.Phase, invocation.Attempt, "running", target.ID+": "+stringArg(call.Arguments, "task"), "", nil, countContextItems(invocation.Messages, invocation.Context))
	result, err := s.runAgent(ctx, child, depth+1)
	if err != nil {
		events := []domain.ExecutionEvent{s.newEvent(child.RunID, invocation.RunID, target.ID, "agent_failed", invocation.Phase, invocation.Attempt, "failed", err.Error(), "", nil, countContextItems(child.Messages, child.Context))}
		return toolMessage(call, "エラー: "+err.Error()), nil, events, nil
	}
	if handoff {
		return domain.Message{}, &result, append([]domain.ExecutionEvent{startEvent}, result.Events...), nil
	}
	return toolMessage(call, result.Message.Content), nil, append([]domain.ExecutionEvent{startEvent}, result.Events...), nil
}

func (s *Service) shouldRunSequentially(executable []executableCall) bool {
	if s.config.MaxParallelAgents <= 1 || len(executable) <= 1 {
		return true
	}
	for _, item := range executable {
		if item.handoff || item.definition.MutatesWorkspace || !item.definition.ParallelSafe {
			return true
		}
	}
	return false
}

func (s *Service) agentToolDefinitions(current domain.AgentSpec) []domain.ToolDefinition {
	tools := make([]domain.ToolDefinition, 0, len(s.catalog.List())+3)
	for _, spec := range s.catalog.List() {
		if spec.ID == current.ID || spec.Disabled || spec.Mode == domain.AgentModeManager {
			continue
		}
		tools = append(tools, agentToolDefinition(spec, false))
		if spec.Mode == domain.AgentModeHandoff {
			tools = append(tools, agentToolDefinition(spec, true))
		}
	}
	tools = append(tools, ephemeralToolDefinition())
	tools = append(tools, capabilityListDefinition(), capabilityEnableDefinition())
	return tools
}

func agentToolDefinition(spec domain.AgentSpec, handoff bool) domain.ToolDefinition {
	name := delegateToolName(spec.ID)
	description := fmt.Sprintf("%s にタスクを委譲します。", spec.Name)
	if handoff {
		name = handoffToolName(spec.ID)
		description = fmt.Sprintf("%s に現在のターンを handoff します。", spec.Name)
	}
	return domain.ToolDefinition{
		Name:            name,
		Description:     description,
		CapabilityGroup: "agent",
		Risk:            "low",
		ReadOnly:        spec.ReadOnly,
		ParallelSafe:    spec.ReadOnly,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task": map[string]any{
					"type":        "string",
					"description": "サブエージェントに依頼するタスク",
				},
			},
			"required": []string{"task"},
		},
	}
}

func ephemeralToolDefinition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:            "run_ephemeral_agent",
		Description:     "一時的なサブエージェントを作成して実行します。",
		CapabilityGroup: "agent",
		Risk:            "medium",
		ReadOnly:        false,
		ParallelSafe:    false,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task":        map[string]any{"type": "string"},
				"instruction": map[string]any{"type": "string"},
				"allowed_tools": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
				"read_only": map[string]any{"type": "boolean"},
				"mode":      map[string]any{"type": "string"},
			},
			"required": []string{"task", "instruction"},
		},
	}
}

func capabilityListDefinition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:            "list_capabilities",
		Description:     "利用可能な capability と、それぞれで公開される tool を返します。",
		CapabilityGroup: "agent",
		Risk:            "low",
		ReadOnly:        true,
		ParallelSafe:    true,
		DiscoveryOnly:   true,
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

func capabilityEnableDefinition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:            "enable_capability",
		Description:     "指定 capability の tool 群を現在の agent turn に追加します。",
		CapabilityGroup: "agent",
		Risk:            "medium",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"capability": map[string]any{"type": "string", "description": "有効化する capability 名"},
			},
			"required": []string{"capability"},
		},
	}
}

func ephemeralSpecFromCall(call domain.ToolCall) domain.AgentSpec {
	allowed := []string{}
	if raw, ok := call.Arguments["allowed_tools"].([]any); ok {
		for _, item := range raw {
			if value, ok := item.(string); ok {
				allowed = append(allowed, value)
			}
		}
	}
	mode := domain.AgentModeTool
	if value, ok := call.Arguments["mode"].(string); ok {
		mode = domain.AgentMode(value)
	}
	readOnly, _ := call.Arguments["read_only"].(bool)
	return domain.AgentSpec{
		ID:           "ephemeral",
		Name:         "Ephemeral Agent",
		Instruction:  stringArg(call.Arguments, "instruction"),
		Mode:         mode,
		AllowedTools: allowed,
		ReadOnly:     readOnly,
		MaxTurns:     4,
	}
}

func latestUserMessage(messages []domain.Message) string {
	for idx := len(messages) - 1; idx >= 0; idx-- {
		if messages[idx].Role == domain.RoleUser {
			return messages[idx].Content
		}
	}
	return ""
}

func shouldBiasManagerTowardDelegation(messages []domain.Message) bool {
	latest := strings.ToLower(latestUserMessage(messages))
	if latest == "" {
		return false
	}
	keywords := []string{
		"subagent",
		"sub-agent",
		"サブエージェント",
		"委譲",
		"品質レポート",
		"quality report",
		"audit",
		"調査",
		"investigate",
		"analysis",
		"analyze",
		"analyse",
		"review",
		"report",
		"repository",
		"repo",
		"codebase",
		"コードベース",
		"全て",
		"すべて",
		"全ファイル",
		"all files",
		"entire",
		"以下のファイル",
		"internal/",
		"directory",
		"ディレクトリ",
	}
	for _, keyword := range keywords {
		if strings.Contains(latest, keyword) {
			return true
		}
	}
	return false
}

func childMessages(call domain.ToolCall) []domain.Message {
	task := stringArg(call.Arguments, "task")
	if task == "" {
		task = "Subtask requested by parent agent."
	}
	return []domain.Message{{Role: domain.RoleUser, Content: task}}
}

func toolMessage(call domain.ToolCall, output string) domain.Message {
	return domain.Message{
		Role:       domain.RoleTool,
		Content:    output,
		ToolCallID: call.ID,
		AgentID:    call.RequestedByAgentID,
		Metadata: map[string]string{
			"tool_name": call.Name,
		},
	}
}

func definitionMap(defs []domain.ToolDefinition) map[string]domain.ToolDefinition {
	out := make(map[string]domain.ToolDefinition, len(defs))
	for _, def := range defs {
		out[def.Name] = def
	}
	return out
}

func newAgentSession(invocation domain.AgentInvocation) *agentSessionState {
	enabled := map[string]bool{}
	for _, capability := range invocation.Context.EnabledCapabilities {
		enabled[capability] = true
	}
	return &agentSessionState{enabledCapabilities: enabled}
}

func visibleTools(agent domain.AgentSpec, defs []domain.ToolDefinition, session *agentSessionState) []domain.ToolDefinition {
	visible := make([]domain.ToolDefinition, 0, len(defs))
	for _, def := range defs {
		if !isCapabilityVisible(agent, def, session) {
			continue
		}
		visible = append(visible, def)
	}
	return visible
}

func buildToolState(agent domain.AgentSpec, visible []domain.ToolDefinition) domain.ToolState {
	state := domain.ToolState{
		CurrentAgentID: agent.ID,
		ReadOnly:       agent.ReadOnly,
	}
	for _, def := range visible {
		switch {
		case def.Name == "task_list":
			state.TaskDiscoveryAvailable = true
		case def.Name == "task_bind":
			state.MCPBindingAvailable = true
		}
		if isVisibleWriteTool(def) {
			state.VisibleWriteTools = append(state.VisibleWriteTools, def.Name)
		}
		if strings.HasPrefix(def.Name, "mcp__") {
			state.VisibleMCPTools = append(state.VisibleMCPTools, def.Name)
		}
	}
	state.VisibleWriteTools = uniqueStrings(state.VisibleWriteTools)
	state.VisibleMCPTools = uniqueStrings(state.VisibleMCPTools)
	state.FileWriteAllowed = len(state.VisibleWriteTools) > 0
	state.MCPToolsLazyBind = state.MCPBindingAvailable || len(state.VisibleMCPTools) > 0 || allowsLazyMCPTools(agent)
	return state
}

func isVisibleWriteTool(def domain.ToolDefinition) bool {
	if def.Name == "patch_apply" {
		return true
	}
	if strings.HasPrefix(def.Name, "fs_") && def.MutatesWorkspace {
		return true
	}
	return false
}

func allowsLazyMCPTools(agent domain.AgentSpec) bool {
	for _, name := range agent.AllowedTools {
		if name == "task_bind" || strings.HasPrefix(name, "mcp__") {
			return true
		}
	}
	return false
}

func describeCapabilities(agent domain.AgentSpec, defs []domain.ToolDefinition, session *agentSessionState) string {
	grouped := map[string][]string{}
	enabled := map[string]bool{}
	for group := range defaultVisibleCapabilityGroups(agent) {
		enabled[group] = true
	}
	if session != nil {
		for group := range session.enabledCapabilities {
			enabled[group] = true
		}
	}
	for _, def := range defs {
		group := def.CapabilityGroup
		if group == "" {
			group = "core"
		}
		grouped[group] = append(grouped[group], def.Name)
	}
	parts := make([]string, 0, len(grouped))
	for group, names := range grouped {
		status := "disabled"
		if enabled[group] {
			status = "enabled"
		}
		parts = append(parts, fmt.Sprintf("%s [%s]: %s", group, status, strings.Join(uniqueStrings(names), ", ")))
	}
	return strings.Join(parts, "\n")
}

func requiresCapabilityEnable(def domain.ToolDefinition, session *agentSessionState) bool {
	group := def.CapabilityGroup
	if group == "" || group == "agent" || group == "core" {
		return false
	}
	if defaultVisibleCapabilityGroups(domain.AgentSpec{})[group] {
		return false
	}
	return session == nil || !session.enabledCapabilities[group]
}

func isCapabilityVisible(agent domain.AgentSpec, def domain.ToolDefinition, session *agentSessionState) bool {
	group := def.CapabilityGroup
	if group == "" {
		return true
	}
	if defaultVisibleCapabilityGroups(agent)[group] {
		return true
	}
	return session != nil && session.enabledCapabilities[group]
}

func defaultVisibleCapabilityGroups(agent domain.AgentSpec) map[string]bool {
	visible := map[string]bool{
		"agent":     true,
		"fs_read":   true,
		"search":    true,
		"git_read":  true,
		"mcp":       true,
		"task_read": true,
	}
	if agent.ReadOnly {
		return visible
	}
	return visible
}

func flattenEvents(groups [][]domain.ExecutionEvent) []domain.ExecutionEvent {
	var out []domain.ExecutionEvent
	for _, group := range groups {
		out = append(out, group...)
	}
	return out
}

func delegateToolName(id string) string {
	return "delegate_to_" + id
}

func handoffToolName(id string) string {
	return "handoff_to_" + id
}

func (s *Service) modelName(invocation domain.AgentInvocation) string {
	if invocation.Agent.Model != "" {
		return invocation.Agent.Model
	}
	if invocation.Model != "" {
		return invocation.Model
	}
	return s.config.DefaultModel
}

func (s *Service) resolveModel(agent domain.AgentSpec, model string) domain.AgentSpec {
	if agent.Model == "" {
		agent.Model = model
	}
	return agent
}

func (s *Service) timeoutFor(agent domain.AgentSpec) time.Duration {
	if agent.Timeout > 0 {
		return agent.Timeout
	}
	return s.config.DefaultTimeout
}

func (s *Service) nextRunID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, s.runCounter.Add(1))
}

func (s *Service) newEvent(runID, parentRunID, agentID, typ string, phase domain.RunPhase, attempt int, status string, detail string, artifactRef string, metrics map[string]any, contextCount int) domain.ExecutionEvent {
	event := domain.ExecutionEvent{
		RunID:        runID,
		ParentRunID:  parentRunID,
		AgentID:      agentID,
		Type:         typ,
		Phase:        phase,
		Attempt:      attempt,
		Status:       status,
		Detail:       detail,
		ArtifactRef:  artifactRef,
		Metrics:      metrics,
		Timestamp:    time.Now(),
		ContextCount: contextCount,
	}
	s.broadcast(event)
	return event
}

func (s *Service) broadcast(event domain.ExecutionEvent) {
	if s.config.TraceSink != nil {
		_ = s.config.TraceSink.Append(context.Background(), event)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.listeners {
		select {
		case ch <- event:
		default:
		}
	}
}

func toolNames(tools []domain.ToolDefinition) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

func cloneMessages(messages []domain.Message) []domain.Message {
	cloned := make([]domain.Message, 0, len(messages))
	for _, message := range messages {
		item := message
		item.ToolCalls = append([]domain.ToolCall(nil), message.ToolCalls...)
		if message.Metadata != nil {
			item.Metadata = map[string]string{}
			for key, value := range message.Metadata {
				item.Metadata[key] = value
			}
		}
		cloned = append(cloned, item)
	}
	return cloned
}

func stringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return value
}

func defaultPurpose(call domain.ToolCall) string {
	if task := stringArg(call.Arguments, "task"); task != "" {
		return task
	}
	return call.Name
}

func extractRelevantFiles(messages []domain.Message) []string {
	var files []string
	for _, message := range messages {
		for _, token := range strings.Fields(message.Content) {
			candidate := trimToken(token)
			if looksLikeFileRef(candidate) {
				files = append(files, candidate)
			}
		}
	}
	return uniqueStrings(files)
}

func trimToken(token string) string {
	return strings.Trim(token, "[](){}<>\"'`,.:;!?")
}

func looksLikeFileRef(token string) bool {
	if token == "" {
		return false
	}
	if strings.Contains(token, "://") {
		return false
	}
	if strings.Contains(token, "/") || strings.HasPrefix(token, ".") {
		return true
	}
	dot := strings.LastIndex(token, ".")
	if dot <= 0 || dot == len(token)-1 {
		return false
	}
	ext := strings.ToLower(token[dot+1:])
	switch ext {
	case "go", "md", "txt", "toml", "yaml", "yml", "json", "ts", "tsx", "js", "jsx", "py", "rb", "rs", "java", "c", "cc", "cpp", "h", "hpp", "sh", "sql", "html", "css":
		return true
	default:
		return false
	}
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func fallbackString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func blockedDelegationReason(current domain.AgentSpec, target domain.AgentSpec, handoff bool) string {
	if current.ID == target.ID {
		return fmt.Sprintf("agent %q は自分自身へ委譲できません", current.ID)
	}
	if current.ID == "planner" && target.ID == "coder" {
		return "planner から coder への委譲は禁止されています。探索や計画には read-only ツールを使用してください"
	}
	if current.ID == "coder" && target.ID == "planner" {
		return "coder から planner への再委譲は禁止されています。現在の handoff 内で直接作業してください"
	}
	if handoff && current.ID == "planner" && target.ID == "coder" {
		return "planner から coder への handoff は禁止されています。manager が handoff を判断してください"
	}
	return ""
}

func (s *Service) approveContinue(ctx context.Context, invocation domain.AgentInvocation, maxTurns int) bool {
	if s.config.Approver == nil {
		return false
	}
	decision, err := s.config.Approver.Approve(ctx, domain.PermissionRequest{
		ToolName:  "agent_turn_limit",
		Operation: fmt.Sprintf("継続実行 (上限 %d 到達)", maxTurns),
		Resource:  fmt.Sprintf("%s [%s]", invocation.Agent.ID, invocation.RunID),
		AgentID:   invocation.Agent.ID,
		Purpose:   "turn_limit_reached",
		Task:      invocation.Context.TaskBrief,
	})
	if err != nil {
		return false
	}
	return decision == domain.PermissionAllowOnce || decision == domain.PermissionAllowSession
}
