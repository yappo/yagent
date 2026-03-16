package orchestrator

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"yagent/internal/domain"
)

type Config struct {
	MaxParallelAgents int
	MaxHandoffDepth   int
	DefaultTimeout    time.Duration
	DefaultModel      string
	EnablePlanning    bool
	TraceSink         domain.TraceSink
	Approver          domain.Approver
}

type Service struct {
	model      domain.ModelClient
	tools      domain.ToolExecutor
	catalog    domain.AgentCatalog
	config     Config
	observer   domain.ToolObserver
	cache      *sessionCache
	runCounter atomic.Uint64
	mu         sync.Mutex
	listeners  map[chan domain.ExecutionEvent]struct{}
}

type sessionCache struct {
	mu            sync.Mutex
	results       map[string]domain.ToolResult
	fileSummaries map[string]string
}

type executionOutcome struct {
	message     domain.Message
	direct      *domain.AgentResult
	events      []domain.ExecutionEvent
	observation *domain.ToolObservation
}

const (
	maxSameObservationRepeats = 2
	maxNoNoveltyIterations    = 2
	maxRecentObservations     = 6
	maxWorkingSetFindings     = 8
)

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
	return &Service{
		model:   model,
		tools:   tools,
		catalog: catalog,
		config:  config,
		cache: &sessionCache{
			results:       map[string]domain.ToolResult{},
			fileSummaries: map[string]string{},
		},
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
	manager, ok := s.catalog.Resolve("manager")
	if !ok {
		return domain.TurnResult{}, fmt.Errorf("manager agent が見つかりません")
	}
	manager = s.withManagerDelegationBias(manager, request.Messages)
	var plan *domain.ExecutionPlan
	var prefetchedMessages []domain.Message
	var planEvents []domain.ExecutionEvent
	if s.config.EnablePlanning {
		var err error
		plan, prefetchedMessages, planEvents, err = s.planAndPrime(ctx, manager, request)
		if err != nil {
			return domain.TurnResult{}, err
		}
	}

	messages := cloneMessages(request.Messages)
	messages = append(messages, prefetchedMessages...)

	result, err := s.runAgent(ctx, domain.AgentInvocation{
		RunID:    s.nextRunID("run"),
		Agent:    s.resolveModel(manager, request.Model),
		Messages: messages,
		Model:    request.Model,
		Stream:   request.Stream,
		Context:  s.buildContext(messages, "", s.tools.Definitions(manager), plan),
	}, 0)
	if err != nil {
		return domain.TurnResult{}, err
	}

	result.Events = append(planEvents, result.Events...)
	return domain.TurnResult{Message: result.Message, Events: result.Events}, nil
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

func (s *Service) planAndPrime(ctx context.Context, manager domain.AgentSpec, request domain.TurnRequest) (*domain.ExecutionPlan, []domain.Message, []domain.ExecutionEvent, error) {
	plan, planEvent, err := s.generateExecutionPlan(ctx, manager, request)
	if err != nil || plan == nil {
		return nil, nil, nil, err
	}
	if !s.approvePlan(ctx, manager, *plan) {
		return nil, nil, []domain.ExecutionEvent{planEvent, s.newEvent("plan", "", manager.ID, "agent_failed", "実行計画が拒否されました", 0)}, fmt.Errorf("実行計画が承認されませんでした")
	}
	prefetched, prefetchEvents := s.executePlannedBatches(ctx, manager, *plan, request)
	events := []domain.ExecutionEvent{planEvent}
	events = append(events, prefetchEvents...)
	return plan, prefetched, events, nil
}

func (s *Service) runAgent(ctx context.Context, invocation domain.AgentInvocation, depth int) (domain.AgentResult, error) {
	if depth > s.config.MaxHandoffDepth {
		err := fmt.Errorf("最大 handoff 深度 (%d) に達しました", s.config.MaxHandoffDepth)
		failed := s.newEvent(
			invocation.RunID,
			invocation.ParentRunID,
			invocation.Agent.ID,
			"agent_failed",
			err.Error(),
			countContextItems(invocation.Messages, invocation.Context),
		)
		return domain.AgentResult{Status: "failed", Events: []domain.ExecutionEvent{failed}}, err
	}

	events := []domain.ExecutionEvent{s.newEvent(
		invocation.RunID,
		invocation.ParentRunID,
		invocation.Agent.ID,
		"agent_started",
		invocation.Context.TaskBrief,
		countContextItems(invocation.Messages, invocation.Context),
	)}

	messages := cloneMessages(invocation.Messages)
	state := newWorkingSet(invocation.Context)
	currentPhase := initialPhase(invocation, state)
	events = append(events, s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "phase_started", string(currentPhase), countContextItems(messages, invocation.Context)))
	maxTurns := invocation.Agent.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 200
	}

	for {
		for turn := 0; turn < maxTurns; turn++ {
			tools := s.toolsForPhase(invocation.Agent, currentPhase)
			invocation.Context = s.contextWithWorkingSet(invocation.Context, state, tools)
			llmCtx, cancel := context.WithTimeout(ctx, s.timeoutFor(invocation.Agent))
			response, err := s.model.Generate(llmCtx, domain.ModelRequest{
				Agent:        invocation.Agent,
				Instructions: s.composeInstructions(invocation),
				Messages:     messages,
				Model:        s.modelName(invocation),
				Stream:       invocation.Stream,
				Tools:        tools,
			})
			cancel()
			if err != nil {
				events = append(events, s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "agent_failed", err.Error(), countContextItems(messages, invocation.Context)))
				return domain.AgentResult{}, err
			}
			events = append(events, s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "llm_called", response.FinishReason, countContextItems(messages, invocation.Context)))

			if len(response.Message.ToolCalls) == 0 {
				response.Message.AgentID = invocation.Agent.ID
				state.Phase = domain.ExecutionPhaseFinish
				events = append(events, s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "phase_started", string(domain.ExecutionPhaseFinish), countContextItems(messages, invocation.Context)))
				events = append(events, s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "agent_completed", response.Message.Content, countContextItems(messages, invocation.Context)))
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
			if currentPhase != domain.ExecutionPhaseSynthesize {
				messages = append(messages, assistantMessage)
			}

			results, direct, childEvents, observations, err := s.executeCalls(ctx, invocation, state, assistantMessage.ToolCalls, depth)
			events = append(events, childEvents...)
			if err != nil {
				events = append(events, s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "agent_failed", err.Error(), countContextItems(messages, invocation.Context)))
				return domain.AgentResult{}, err
			}
			if direct != nil {
				direct.Events = append(events, direct.Events...)
				return *direct, nil
			}
			messages = append(messages, results...)
			noveltyEvents, nextPhase := s.applyObservations(invocation, state, observations)
			events = append(events, noveltyEvents...)
			if nextPhase != currentPhase {
				currentPhase = nextPhase
				state.Phase = currentPhase
				events = append(events, s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "phase_started", string(currentPhase), countContextItems(messages, invocation.Context)))
			}
			messages = compactMessages(messages)
		}

		if !s.approveContinue(ctx, invocation, maxTurns) {
			err := fmt.Errorf("agent %q が最大ターン数 (%d) に達しました", invocation.Agent.ID, maxTurns)
			events = append(events, s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "agent_failed", err.Error(), countContextItems(messages, invocation.Context)))
			return domain.AgentResult{}, err
		}

		events = append(events, s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "agent_continued", fmt.Sprintf("最大ターン数 %d 到達後に継続が承認されました", maxTurns), countContextItems(messages, invocation.Context)))
	}
}

func (s *Service) executeCalls(ctx context.Context, invocation domain.AgentInvocation, state *domain.WorkingSet, calls []domain.ToolCall, depth int) ([]domain.Message, *domain.AgentResult, []domain.ExecutionEvent, []domain.ToolObservation, error) {
	if len(calls) == 0 {
		return nil, nil, nil, nil, nil
	}

	executable := s.prepareExecutableCalls(invocation.Agent, calls)
	if s.shouldRunSequentially(executable) {
		return s.executeSequential(ctx, invocation, state, executable, depth)
	}
	return s.executeParallel(ctx, invocation, state, executable, depth)
}

type executableCall struct {
	call        domain.ToolCall
	definition  domain.ToolDefinition
	targetAgent *domain.AgentSpec
	handoff     bool
	ephemeral   *domain.AgentSpec
	blockedBy   string
}

func (s *Service) prepareExecutableCalls(agent domain.AgentSpec, calls []domain.ToolCall) []executableCall {
	executable := make([]executableCall, 0, len(calls))
	definitions := definitionMap(s.tools.Definitions(agent))
	for _, spec := range s.catalog.List() {
		definitions[delegateToolName(spec.ID)] = agentToolDefinition(spec, false)
		if spec.Mode == domain.AgentModeHandoff {
			definitions[handoffToolName(spec.ID)] = agentToolDefinition(spec, true)
		}
	}
	definitions["run_ephemeral_agent"] = ephemeralToolDefinition()

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
		}
		executable = append(executable, item)
	}
	return executable
}

func (s *Service) executeSequential(ctx context.Context, invocation domain.AgentInvocation, state *domain.WorkingSet, executable []executableCall, depth int) ([]domain.Message, *domain.AgentResult, []domain.ExecutionEvent, []domain.ToolObservation, error) {
	results := make([]domain.Message, 0, len(executable))
	events := []domain.ExecutionEvent{}
	observations := []domain.ToolObservation{}
	for _, item := range executable {
		outcome, err := s.executeOne(ctx, invocation, state, item, depth)
		callEvents := outcome.events
		events = append(events, callEvents...)
		if err != nil {
			return nil, nil, events, observations, err
		}
		if outcome.direct != nil {
			return nil, outcome.direct, events, observations, nil
		}
		results = append(results, outcome.message)
		if outcome.observation != nil {
			observations = append(observations, *outcome.observation)
		}
	}
	return results, nil, events, observations, nil
}

func (s *Service) executeParallel(ctx context.Context, invocation domain.AgentInvocation, state *domain.WorkingSet, executable []executableCall, depth int) ([]domain.Message, *domain.AgentResult, []domain.ExecutionEvent, []domain.ToolObservation, error) {
	events := make([][]domain.ExecutionEvent, len(executable))
	results := make([]domain.Message, len(executable))
	observations := make([]*domain.ToolObservation, len(executable))
	var directMu sync.Mutex
	var direct *domain.AgentResult

	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(s.config.MaxParallelAgents)

	for idx := range executable {
		idx := idx
		group.Go(func() error {
			outcome, err := s.executeOne(groupCtx, invocation, state, executable[idx], depth)
			events[idx] = outcome.events
			if err != nil {
				return err
			}
			if outcome.direct != nil {
				directMu.Lock()
				if direct == nil {
					direct = outcome.direct
				}
				directMu.Unlock()
				return nil
			}
			results[idx] = outcome.message
			observations[idx] = outcome.observation
			return nil
		})
	}

	if err := group.Wait(); err != nil {
		return nil, nil, flattenEvents(events), nil, err
	}
	if direct != nil {
		return nil, direct, flattenEvents(events), nil, nil
	}
	return results, nil, flattenEvents(events), flattenObservations(observations), nil
}

func (s *Service) executeOne(ctx context.Context, invocation domain.AgentInvocation, state *domain.WorkingSet, item executableCall, depth int) (executionOutcome, error) {
	if state != nil && item.targetAgent == nil && item.ephemeral == nil {
		if blocked, reason := s.blockedByExecutionState(*state, item); blocked {
			events := []domain.ExecutionEvent{s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "tool_failed", item.call.Name+": "+reason, countContextItems(invocation.Messages, invocation.Context))}
			return executionOutcome{
				message: toolMessage(item.call, "エラー: "+reason),
				events:  events,
				observation: &domain.ToolObservation{
					ToolName:    item.call.Name,
					Capability:  capabilityDescriptor(item.call, item.definition),
					Target:      normalizedTarget(item.call),
					Fingerprint: fingerprintFor(reason),
					Summary:     reason,
					Cached:      false,
					Changed:     false,
				},
			}, nil
		}
	}

	switch {
	case item.blockedBy != "":
		detail := item.call.Name + ": " + item.blockedBy
		events := []domain.ExecutionEvent{s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "tool_failed", detail, countContextItems(invocation.Messages, invocation.Context))}
		return executionOutcome{message: toolMessage(item.call, "エラー: "+item.blockedBy), events: events}, nil
	case item.targetAgent != nil:
		return s.executeDelegation(ctx, invocation, *item.targetAgent, item.call, item.handoff, depth)
	case item.ephemeral != nil:
		result, err := s.runAgent(ctx, domain.AgentInvocation{
			RunID:       s.nextRunID("ephemeral"),
			ParentRunID: invocation.RunID,
			Agent:       s.resolveModel(*item.ephemeral, invocation.Model),
			Messages:    childMessages(item.call),
			Context:     s.buildContext(invocation.Messages, stringArg(item.call.Arguments, "task"), s.tools.Definitions(*item.ephemeral), nil),
			Model:       invocation.Model,
			Stream:      false,
		}, depth+1)
		if err != nil {
			events := []domain.ExecutionEvent{s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "agent_failed", err.Error(), countContextItems(invocation.Messages, invocation.Context))}
			return executionOutcome{message: toolMessage(item.call, "エラー: "+err.Error()), events: events}, nil
		}
		observation := s.observationForResult(item.call, item.definition, domain.ToolResult{
			CallID:  item.call.ID,
			Name:    item.call.Name,
			Success: true,
			Output:  result.Message.Content,
		}, false)
		return executionOutcome{message: toolMessage(item.call, result.Message.Content), events: result.Events, observation: &observation}, nil
	default:
		s.notifyToolEvent(ctx, domain.ToolEvent{Phase: "start", Call: item.call})
		result, cached := s.executeTool(ctx, invocation.Agent, item.call)
		s.notifyToolEvent(ctx, domain.ToolEvent{Phase: "finish", Call: item.call, Result: result})
		eventType := "tool_called"
		detail := item.call.Name
		if cached {
			detail = item.call.Name + " [cached]"
		}
		if !result.Success {
			eventType = "tool_failed"
			detail = item.call.Name + ": " + result.Output
		}
		events := []domain.ExecutionEvent{s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, eventType, detail, countContextItems(invocation.Messages, invocation.Context))}
		observation := s.observationForResult(item.call, item.definition, result, cached)
		return executionOutcome{message: toolMessage(item.call, result.Output), events: events, observation: &observation}, nil
	}
}

func (s *Service) notifyToolEvent(ctx context.Context, event domain.ToolEvent) {
	if s.observer != nil {
		s.observer.OnToolEvent(ctx, event)
	}
}

func (s *Service) executeDelegation(ctx context.Context, invocation domain.AgentInvocation, target domain.AgentSpec, call domain.ToolCall, handoff bool, depth int) (executionOutcome, error) {
	child := domain.AgentInvocation{
		RunID:       s.nextRunID(target.ID),
		ParentRunID: invocation.RunID,
		Agent:       s.resolveModel(target, invocation.Model),
		Messages:    childMessages(call),
		Context:     s.buildContext(invocation.Messages, stringArg(call.Arguments, "task"), s.tools.Definitions(target), nil),
		Model:       invocation.Model,
		Stream:      false,
	}
	startType := "delegate_started"
	if handoff {
		startType = "handoff_started"
	}
	startEvent := s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, startType, target.ID+": "+stringArg(call.Arguments, "task"), countContextItems(invocation.Messages, invocation.Context))
	result, err := s.runAgent(ctx, child, depth+1)
	if err != nil {
		events := []domain.ExecutionEvent{s.newEvent(child.RunID, invocation.RunID, target.ID, "agent_failed", err.Error(), countContextItems(child.Messages, child.Context))}
		return executionOutcome{message: toolMessage(call, "エラー: "+err.Error()), events: events}, nil
	}
	if handoff {
		return executionOutcome{direct: &result, events: append([]domain.ExecutionEvent{startEvent}, result.Events...)}, nil
	}
	observation := s.observationForResult(call, domain.ToolDefinition{Name: call.Name, ReadOnly: target.ReadOnly}, domain.ToolResult{
		CallID:  call.ID,
		Name:    call.Name,
		Success: true,
		Output:  result.Message.Content,
	}, false)
	return executionOutcome{
		message:     toolMessage(call, result.Message.Content),
		events:      append([]domain.ExecutionEvent{startEvent}, result.Events...),
		observation: &observation,
	}, nil
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
	tools := make([]domain.ToolDefinition, 0, len(s.catalog.List())+1)
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
		Name:         name,
		Description:  description,
		ReadOnly:     spec.ReadOnly,
		ParallelSafe: spec.ReadOnly,
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
		Name:         "run_ephemeral_agent",
		Description:  "一時的なサブエージェントを作成して実行します。",
		ReadOnly:     false,
		ParallelSafe: false,
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

func (s *Service) buildContext(messages []domain.Message, task string, tools []domain.ToolDefinition, plan *domain.ExecutionPlan) domain.ContextPack {
	relevant := extractRelevantFiles(messages)
	if plan != nil {
		relevant = uniqueStrings(append(relevant, plan.TargetFiles...))
	}
	return domain.ContextPack{
		Phase:              domain.ExecutionPhaseGather,
		UserGoal:           latestUserMessage(messages),
		TaskBrief:          task,
		RelevantFiles:      relevant,
		FileSummaries:      s.fileSummaries(relevant),
		RecentSummary:      latestUserMessage(messages),
		AvailableToolNames: toolNames(tools),
		ApprovedPlan:       summarizePlan(plan),
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

func flattenEvents(groups [][]domain.ExecutionEvent) []domain.ExecutionEvent {
	var out []domain.ExecutionEvent
	for _, group := range groups {
		out = append(out, group...)
	}
	return out
}

func flattenObservations(values []*domain.ToolObservation) []domain.ToolObservation {
	out := make([]domain.ToolObservation, 0, len(values))
	for _, value := range values {
		if value != nil {
			out = append(out, *value)
		}
	}
	return out
}

func delegateToolName(id string) string {
	return "delegate_to_" + id
}

func handoffToolName(id string) string {
	return "handoff_to_" + id
}

func (s *Service) toolsForPhase(agent domain.AgentSpec, phase domain.ExecutionPhase) []domain.ToolDefinition {
	if phase == domain.ExecutionPhaseSynthesize || phase == domain.ExecutionPhaseFinish {
		return nil
	}
	return append(s.tools.Definitions(agent), s.agentToolDefinitions(agent)...)
}

func (s *Service) blockedByExecutionState(state domain.WorkingSet, item executableCall) (bool, string) {
	if state.Phase == domain.ExecutionPhaseSynthesize || state.Phase == domain.ExecutionPhaseFinish {
		return true, "synthesize フェーズでは追加の tool 実行はできません。収集済み情報を使って回答してください"
	}
	capability := capabilityDescriptor(item.call, item.definition)
	if !isBroadDiscovery(capability) {
		return false, ""
	}
	target := normalizedTarget(item.call)
	if target == "" {
		return false, ""
	}
	if !containsString(state.ObservedResources, target) {
		return false, ""
	}
	pending := unresolvedTargets(state, target)
	if len(pending) == 0 {
		return false, ""
	}
	return true, "同じ広域探索は不要です。既に見つかった候補を先に確認してください: " + strings.Join(limitStrings(pending, 5), ", ")
}

func initialPhase(invocation domain.AgentInvocation, state *domain.WorkingSet) domain.ExecutionPhase {
	if state.Plan != nil {
		state.Phase = domain.ExecutionPhaseGather
		return domain.ExecutionPhaseGather
	}
	state.Phase = domain.ExecutionPhaseGather
	return domain.ExecutionPhaseGather
}

func newWorkingSet(context domain.ContextPack) *domain.WorkingSet {
	set := &domain.WorkingSet{
		Phase:              context.Phase,
		OpenQuestions:      append([]string(nil), context.OpenQuestions...),
		Targets:            append([]string(nil), context.RelevantFiles...),
		ObservedResources:  append([]string(nil), context.RelevantFiles...),
		PendingTargets:     append([]string(nil), context.RelevantFiles...),
		Artifacts:          append([]string(nil), context.ArtifactRefs...),
		Findings:           append([]string(nil), context.Findings...),
		Summaries:          append([]string(nil), context.FileSummaries...),
		RepeatCounts:       map[string]int{},
		SeenFingerprints:   map[string]string{},
		RecentObservations: []domain.ToolObservation{},
	}
	if context.ApprovedPlan != "" {
		set.Plan = &domain.ExecutionPlan{Summary: context.ApprovedPlan}
	}
	if set.Phase == "" {
		set.Phase = domain.ExecutionPhaseGather
	}
	return set
}

func (s *Service) contextWithWorkingSet(base domain.ContextPack, state *domain.WorkingSet, tools []domain.ToolDefinition) domain.ContextPack {
	base.Phase = state.Phase
	candidates := append([]string(nil), state.PendingTargets...)
	if len(candidates) == 0 {
		candidates = append([]string(nil), state.Targets...)
	}
	base.RelevantFiles = uniqueStrings(candidates)
	base.FileSummaries = uniqueStrings(append(append([]string(nil), base.FileSummaries...), state.Summaries...))
	base.ArtifactRefs = uniqueStrings(append([]string(nil), state.Artifacts...))
	base.AvailableToolNames = toolNames(tools)
	base.OpenQuestions = uniqueStrings(append([]string(nil), state.OpenQuestions...))
	base.Findings = uniqueStrings(append([]string(nil), state.Findings...))
	base.RecentObservations = summarizeRecentObservations(state.RecentObservations)
	if state.Plan != nil {
		base.ApprovedPlan = summarizePlan(state.Plan)
	}
	return base
}

func (s *Service) composeInstructions(invocation domain.AgentInvocation) string {
	parts := []string{invocation.Agent.Instruction}
	if contextBlock := formatContextPack(invocation.Context); contextBlock != "" {
		parts = append(parts, contextBlock)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func formatContextPack(context domain.ContextPack) string {
	lines := []string{}
	if context.Phase != "" {
		lines = append(lines, "Execution phase: "+string(context.Phase))
	}
	if context.UserGoal != "" {
		lines = append(lines, "Execution context user goal: "+context.UserGoal)
	}
	if context.TaskBrief != "" {
		lines = append(lines, "Execution context task brief: "+context.TaskBrief)
	}
	if len(context.RelevantFiles) > 0 {
		lines = append(lines, "Relevant files: "+strings.Join(context.RelevantFiles, ", "))
		lines = append(lines, "If these candidate targets are already available, continue with focused reads/searches instead of repeating a broad listing.")
	}
	if len(context.FileSummaries) > 0 {
		lines = append(lines, "Cached file summaries:")
		for _, summary := range context.FileSummaries {
			lines = append(lines, "- "+summary)
		}
	}
	if len(context.Findings) > 0 {
		lines = append(lines, "Current findings:")
		for _, finding := range context.Findings {
			lines = append(lines, "- "+finding)
		}
	}
	if len(context.OpenQuestions) > 0 {
		lines = append(lines, "Open questions:")
		for _, question := range context.OpenQuestions {
			lines = append(lines, "- "+question)
		}
	}
	if len(context.RecentObservations) > 0 {
		lines = append(lines, "Recent observations:")
		for _, observation := range context.RecentObservations {
			lines = append(lines, "- "+observation)
		}
	}
	if context.ApprovedPlan != "" {
		lines = append(lines, "Approved execution plan:")
		lines = append(lines, context.ApprovedPlan)
		lines = append(lines, "Follow the approved plan closely. Gather can expand automatically when you discover genuinely new targets, but do not repeat the same no-new-information call.")
	}
	if context.Phase == domain.ExecutionPhaseSynthesize {
		lines = append(lines, "Synthesize now. Do not call more tools unless absolutely required to answer a still-open question.")
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func summarizePlan(plan *domain.ExecutionPlan) string {
	if plan == nil {
		return ""
	}
	lines := []string{}
	if plan.Summary != "" {
		lines = append(lines, "Summary: "+plan.Summary)
	}
	if len(plan.TargetFiles) > 0 {
		lines = append(lines, "Target files: "+strings.Join(plan.TargetFiles, ", "))
	}
	if len(plan.ExitConditions) > 0 {
		lines = append(lines, "Exit conditions: "+strings.Join(plan.ExitConditions, "; "))
	}
	for idx, batch := range plan.Batches {
		parts := make([]string, 0, len(batch.ToolCalls))
		for _, call := range batch.ToolCalls {
			parts = append(parts, call.Name)
		}
		lines = append(lines, fmt.Sprintf("Batch %d (%s): %s", idx+1, fallbackText(batch.Purpose, "no purpose"), strings.Join(parts, ", ")))
	}
	return strings.Join(lines, "\n")
}

func fallbackText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
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

func (s *Service) generateExecutionPlan(ctx context.Context, manager domain.AgentSpec, request domain.TurnRequest) (*domain.ExecutionPlan, domain.ExecutionEvent, error) {
	event := s.newEvent("plan", "", manager.ID, "phase_started", string(domain.ExecutionPhasePlan), len(request.Messages))
	tools := []domain.ToolDefinition{executionPlanToolDefinition()}
	llmCtx, cancel := context.WithTimeout(ctx, s.timeoutFor(manager))
	defer cancel()

	response, err := s.model.Generate(llmCtx, domain.ModelRequest{
		Agent:        manager,
		Instructions: strings.TrimSpace(manager.Instruction + "\n\nBefore executing any tools, produce a concrete execution plan. You must call submit_execution_plan once. Include batched read-only tool calls when they can be executed without another model round-trip. Prefer fs_list, fs_read, search_text, search_files, git_status, git_diff, git_log, git_show, and task_list in plan batches. Do not include mutating tools in plan batches."),
		Messages:     cloneMessages(request.Messages),
		Model:        fallbackText(request.Model, fallbackText(manager.Model, s.config.DefaultModel)),
		Tools:        tools,
	})
	if err != nil {
		return nil, event, nil
	}
	for _, call := range response.Message.ToolCalls {
		if call.Name != "submit_execution_plan" {
			continue
		}
		plan := parseExecutionPlan(call)
		return &plan, event, nil
	}
	return nil, event, nil
}

func executionPlanToolDefinition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "submit_execution_plan",
		Description: "Submit an execution plan before any tools are executed.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"summary": map[string]any{"type": "string"},
				"target_files": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
				"exit_conditions": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
				"batches": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"purpose": map[string]any{"type": "string"},
							"tool_calls": map[string]any{
								"type": "array",
								"items": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"name":      map[string]any{"type": "string"},
										"arguments": map[string]any{"type": "object"},
									},
									"required": []string{"name", "arguments"},
								},
							},
						},
						"required": []string{"purpose", "tool_calls"},
					},
				},
			},
			"required": []string{"summary", "exit_conditions"},
		},
	}
}

func parseExecutionPlan(call domain.ToolCall) domain.ExecutionPlan {
	plan := domain.ExecutionPlan{
		Summary:        stringArg(call.Arguments, "summary"),
		TargetFiles:    stringSliceArg(call.Arguments["target_files"]),
		ExitConditions: stringSliceArg(call.Arguments["exit_conditions"]),
	}
	if rawBatches, ok := call.Arguments["batches"].([]any); ok {
		for _, rawBatch := range rawBatches {
			batchMap, ok := rawBatch.(map[string]any)
			if !ok {
				continue
			}
			batch := domain.PlannedBatch{Purpose: stringValue(batchMap["purpose"])}
			if rawCalls, ok := batchMap["tool_calls"].([]any); ok {
				for _, rawCall := range rawCalls {
					callMap, ok := rawCall.(map[string]any)
					if !ok {
						continue
					}
					args, _ := callMap["arguments"].(map[string]any)
					batch.ToolCalls = append(batch.ToolCalls, domain.PlannedToolCall{
						Name:      stringValue(callMap["name"]),
						Arguments: cloneAnyMap(args),
					})
				}
			}
			if len(batch.ToolCalls) > 0 {
				plan.Batches = append(plan.Batches, batch)
			}
		}
	}
	return plan
}

func (s *Service) approvePlan(ctx context.Context, manager domain.AgentSpec, plan domain.ExecutionPlan) bool {
	if s.config.Approver == nil {
		return true
	}
	decision, err := s.config.Approver.Approve(ctx, domain.PermissionRequest{
		ToolName:  "execution_plan",
		Operation: "実行計画の承認",
		Resource:  strings.Join(plan.TargetFiles, ", "),
		Scope:     strings.Join(plan.ExitConditions, "; "),
		Summary:   summarizePlan(&plan),
		AgentID:   manager.ID,
		Purpose:   "execution_plan",
	})
	return err == nil && decision != domain.PermissionDeny
}

func (s *Service) executePlannedBatches(ctx context.Context, manager domain.AgentSpec, plan domain.ExecutionPlan, request domain.TurnRequest) ([]domain.Message, []domain.ExecutionEvent) {
	var messages []domain.Message
	var events []domain.ExecutionEvent
	for batchIdx, batch := range plan.Batches {
		assistant := domain.Message{Role: domain.RoleAssistant, AgentID: manager.ID}
		var batchMessages []domain.Message
		for callIdx, planned := range batch.ToolCalls {
			definitionMap := definitionMap(s.tools.Definitions(manager))
			definition, ok := definitionMap[planned.Name]
			if !ok || !definition.ReadOnly || definition.MutatesWorkspace {
				continue
			}
			call := domain.ToolCall{
				ID:                 fmt.Sprintf("plan-%d-%d", batchIdx+1, callIdx+1),
				Name:               planned.Name,
				Arguments:          cloneAnyMap(planned.Arguments),
				RequestedByAgentID: manager.ID,
				Purpose:            fallbackText(batch.Purpose, planned.Name),
			}
			assistant.ToolCalls = append(assistant.ToolCalls, call)
			result, cached := s.executeTool(ctx, manager, call)
			detail := call.Name
			if cached {
				detail += " [cached]"
			}
			events = append(events, s.newEvent("plan", "", manager.ID, "tool_called", detail, len(request.Messages)))
			batchMessages = append(batchMessages, toolMessage(call, result.Output))
		}
		if len(assistant.ToolCalls) > 0 {
			messages = append(messages, assistant)
			messages = append(messages, batchMessages...)
		}
	}
	return messages, events
}

func (s *Service) executeTool(ctx context.Context, agent domain.AgentSpec, call domain.ToolCall) (domain.ToolResult, bool) {
	if key, ok := cacheKey(call); ok {
		s.cache.mu.Lock()
		cached, found := s.cache.results[key]
		s.cache.mu.Unlock()
		if found {
			return cached, true
		}
		result := s.tools.Execute(ctx, agent, call)
		s.cacheToolResult(call, result)
		return result, false
	}
	result := s.tools.Execute(ctx, agent, call)
	s.cacheToolResult(call, result)
	return result, false
}

func cacheKey(call domain.ToolCall) (string, bool) {
	switch call.Name {
	case "fs_read", "fs_list", "fs_stat", "search_text", "search_files", "git_status", "git_diff", "git_log", "git_show":
	default:
		return "", false
	}
	data, err := json.Marshal(call.Arguments)
	if err != nil {
		return "", false
	}
	return call.Name + ":" + string(data), true
}

func (s *Service) cacheToolResult(call domain.ToolCall, result domain.ToolResult) {
	s.cache.mu.Lock()
	defer s.cache.mu.Unlock()
	if result.Success {
		if key, ok := cacheKey(call); ok {
			s.cache.results[key] = result
		}
		s.updateFileSummary(call, result)
	}
	if result.Success && mutatesWorkspace(call.Name) {
		s.invalidateCachesFor(call)
	}
}

func mutatesWorkspace(name string) bool {
	switch name {
	case "fs_write", "fs_remove", "fs_move", "patch_apply", "task_run":
		return true
	default:
		return false
	}
}

func (s *Service) invalidateCachesFor(call domain.ToolCall) {
	s.cache.results = map[string]domain.ToolResult{}
	switch call.Name {
	case "fs_write":
		delete(s.cache.fileSummaries, stringArg(call.Arguments, "path"))
	case "fs_remove":
		delete(s.cache.fileSummaries, stringArg(call.Arguments, "path"))
	case "fs_move":
		delete(s.cache.fileSummaries, stringArg(call.Arguments, "source_path"))
		delete(s.cache.fileSummaries, stringArg(call.Arguments, "destination_path"))
	default:
		s.cache.fileSummaries = map[string]string{}
	}
}

func (s *Service) updateFileSummary(call domain.ToolCall, result domain.ToolResult) {
	if call.Name != "fs_read" {
		return
	}
	path := stringArg(call.Arguments, "path")
	if path == "" {
		return
	}
	s.cache.fileSummaries[path] = summarizeFile(path, result.Output)
}

func (s *Service) fileSummaries(paths []string) []string {
	s.cache.mu.Lock()
	defer s.cache.mu.Unlock()
	var summaries []string
	for _, path := range uniqueStrings(paths) {
		if summary := s.cache.fileSummaries[path]; summary != "" {
			summaries = append(summaries, summary)
		}
	}
	return summaries
}

func summarizeFile(path, content string) string {
	lineCount := strings.Count(content, "\n") + 1
	headers := regexp.MustCompile(`(?m)^(#{1,3}\s+.+)$`).FindAllStringSubmatch(content, -1)
	symbols := regexp.MustCompile(`(?m)^(?:export\s+)?(?:func|function|class|type|interface|struct|enum|const|var|def)\s+([A-Za-z_][A-Za-z0-9_]*)`).FindAllStringSubmatch(content, -1)
	imports := regexp.MustCompile(`(?m)^(?:import|from|require|use)\s+(.+)$`).FindAllStringSubmatch(content, -1)
	comment := firstCommentSummary(content)
	parts := []string{
		fmt.Sprintf("%s: lines=%d", path, lineCount),
		"symbols=" + joinSubmatches(symbols),
		fmt.Sprintf("imports=%d", len(imports)),
	}
	if len(headers) > 0 {
		parts = append(parts, "headings="+joinSubmatches(headers))
	}
	if comment != "" {
		parts = append(parts, "summary="+comment)
	}
	return strings.Join(parts, " ")
}

func joinSubmatches(matches [][]string) string {
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			names = append(names, match[1])
		}
	}
	if len(names) == 0 {
		return "-"
	}
	if len(names) > 8 {
		names = append(names[:8], "...")
	}
	return strings.Join(names, ",")
}

func firstCommentSummary(content string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		for _, prefix := range []string{"//", "#", "/*", "*", "--"} {
			if strings.HasPrefix(trimmed, prefix) {
				text := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
				if text != "" {
					return text
				}
			}
		}
		break
	}
	return ""
}

func (s *Service) applyObservations(invocation domain.AgentInvocation, state *domain.WorkingSet, observations []domain.ToolObservation) ([]domain.ExecutionEvent, domain.ExecutionPhase) {
	if len(observations) == 0 {
		return nil, state.Phase
	}

	events := []domain.ExecutionEvent{}
	hadNovelty := false
	for _, observation := range observations {
		decision := applyObservation(state, observation)
		if decision.NewInformation {
			hadNovelty = true
			for _, discovered := range observation.Discovered {
				if appendUnique(&state.PendingTargets, discovered) {
					events = append(events, s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "target_discovered", discovered, countContextItems(invocation.Messages, invocation.Context)))
				}
			}
		}
	}
	if hadNovelty {
		state.NoNoveltyIterations = 0
		return events, state.Phase
	}

	state.NoNoveltyIterations++
	reason := "新情報のない取得が繰り返されたため gather を終了します"
	events = append(events, s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "novelty_exhausted", reason, countContextItems(invocation.Messages, invocation.Context)))
	if state.NoNoveltyIterations >= maxNoNoveltyIterations {
		state.Phase = domain.ExecutionPhaseSynthesize
	}
	return events, state.Phase
}

func applyObservation(state *domain.WorkingSet, observation domain.ToolObservation) domain.NoveltyDecision {
	if state.RepeatCounts == nil {
		state.RepeatCounts = map[string]int{}
	}
	if state.SeenFingerprints == nil {
		state.SeenFingerprints = map[string]string{}
	}

	if observation.Target != "" {
		appendUnique(&state.ObservedResources, observation.Target)
		if observation.Capability.Kind == "read" || observation.Capability.Kind == "inspect" || observation.Capability.Kind == "external_query" || observation.Capability.Kind == "task_readonly" {
			state.PendingTargets = removeString(state.PendingTargets, observation.Target)
		}
	}
	for _, discovered := range observation.Discovered {
		appendCappedUnique(&state.Targets, discovered, 24)
		appendCappedUnique(&state.PendingTargets, discovered, 24)
	}
	if observation.Summary != "" {
		appendCappedUnique(&state.Findings, observation.Summary, maxWorkingSetFindings)
	}
	appendRecentObservation(state, observation)

	key := observation.Capability.Kind + "|" + observation.Target + "|" + observation.ToolName
	prevFingerprint, seen := state.SeenFingerprints[key]
	changed := !seen || prevFingerprint != observation.Fingerprint || observation.Changed
	if changed {
		state.SeenFingerprints[key] = observation.Fingerprint
		state.RepeatCounts[key] = 0
		if observation.Summary != "" {
			appendCappedUnique(&state.Summaries, observation.Summary, maxWorkingSetFindings)
		}
		state.NoveltyState = "new_information"
		return domain.NoveltyDecision{NewInformation: true, Reason: "working set updated"}
	}

	state.RepeatCounts[key]++
	state.NoveltyState = fmt.Sprintf("repeat:%d", state.RepeatCounts[key])
	if state.RepeatCounts[key] >= maxSameObservationRepeats {
		state.Phase = domain.ExecutionPhaseSynthesize
	}
	return domain.NoveltyDecision{NewInformation: false, Reason: "same observation with no new information"}
}

func appendRecentObservation(state *domain.WorkingSet, observation domain.ToolObservation) {
	state.RecentObservations = append(state.RecentObservations, observation)
	if len(state.RecentObservations) > maxRecentObservations {
		state.RecentObservations = append([]domain.ToolObservation(nil), state.RecentObservations[len(state.RecentObservations)-maxRecentObservations:]...)
	}
}

func appendUnique(items *[]string, value string) bool {
	for _, existing := range *items {
		if existing == value {
			return false
		}
	}
	*items = append(*items, value)
	return true
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func removeString(values []string, target string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != target {
			out = append(out, value)
		}
	}
	return out
}

func appendCappedUnique(items *[]string, value string, max int) {
	if value == "" {
		return
	}
	if appendUnique(items, value) && max > 0 && len(*items) > max {
		*items = append([]string(nil), (*items)[len(*items)-max:]...)
	}
}

func summarizeRecentObservations(observations []domain.ToolObservation) []string {
	if len(observations) == 0 {
		return nil
	}
	lines := make([]string, 0, len(observations))
	for _, observation := range observations {
		parts := []string{observation.ToolName}
		if observation.Target != "" {
			parts = append(parts, observation.Target)
		}
		if len(observation.Discovered) > 0 {
			parts = append(parts, "next="+strings.Join(limitStrings(observation.Discovered, 4), ", "))
		}
		if observation.Summary != "" {
			parts = append(parts, observation.Summary)
		}
		lines = append(lines, strings.Join(parts, " -> "))
	}
	return uniqueStrings(lines)
}

func isBroadDiscovery(capability domain.CapabilityDescriptor) bool {
	switch capability.Kind {
	case "list", "search":
		return true
	default:
		return false
	}
}

func unresolvedTargets(state domain.WorkingSet, parent string) []string {
	var values []string
	for _, target := range state.PendingTargets {
		if target == "" || target == parent {
			continue
		}
		if strings.HasPrefix(target, parent+"/") || parent == "." {
			values = append(values, target)
		}
	}
	return uniqueStrings(values)
}

func compactMessages(messages []domain.Message) []domain.Message {
	compacted := make([]domain.Message, 0, len(messages))
	for _, message := range messages {
		if message.Role == domain.RoleTool {
			continue
		}
		if message.Role == domain.RoleAssistant && len(message.ToolCalls) > 0 {
			continue
		}
		compacted = append(compacted, message)
	}
	return compacted
}

func (s *Service) observationForResult(call domain.ToolCall, definition domain.ToolDefinition, result domain.ToolResult, cached bool) domain.ToolObservation {
	capability := capabilityDescriptor(call, definition)
	target := normalizedTarget(call)
	summary := summarizeToolResult(call, result)
	return domain.ToolObservation{
		ToolName:    call.Name,
		Capability:  capability,
		Target:      target,
		Discovered:  discoveredTargets(call, result),
		Fingerprint: fingerprintFor(result.Output),
		Summary:     summary,
		Cached:      cached,
		Changed:     result.Success && !cached,
	}
}

func capabilityDescriptor(call domain.ToolCall, definition domain.ToolDefinition) domain.CapabilityDescriptor {
	kind := "inspect"
	switch {
	case call.Name == "fs_read":
		kind = "read"
	case call.Name == "fs_list":
		kind = "list"
	case strings.HasPrefix(call.Name, "search_"):
		kind = "search"
	case strings.HasPrefix(call.Name, "git_") || call.Name == "fs_stat":
		kind = "inspect"
	case call.Name == "task_list":
		kind = "task_readonly"
	case strings.HasPrefix(call.Name, "delegate_to_"), strings.HasPrefix(call.Name, "handoff_to_"), call.Name == "run_ephemeral_agent":
		kind = "external_query"
	default:
		if category, _ := definition.Metadata["category"].(string); category != "" {
			switch category {
			case "fs":
				kind = "inspect"
			case "search":
				kind = "search"
			case "git":
				kind = "inspect"
			case "task":
				if definition.ReadOnly {
					kind = "task_readonly"
				}
			default:
				if definition.ReadOnly {
					kind = "external_query"
				}
			}
		} else if definition.ReadOnly {
			kind = "external_query"
		}
	}
	return domain.CapabilityDescriptor{Name: call.Name, Kind: kind}
}

func normalizedTarget(call domain.ToolCall) string {
	for _, key := range []string{"path", "root", "query", "name_pattern", "task_id", "resource", "source_path", "destination_path", "task"} {
		if value := stringArg(call.Arguments, key); value != "" {
			return value
		}
	}
	return normalizedArguments(call.Arguments)
}

func normalizedArguments(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", key, args[key]))
	}
	return strings.Join(parts, ",")
}

func summarizeToolResult(call domain.ToolCall, result domain.ToolResult) string {
	if !result.Success {
		return strings.TrimSpace(result.Output)
	}
	switch call.Name {
	case "fs_read":
		path := stringArg(call.Arguments, "path")
		if path != "" {
			return summarizeFile(path, result.Output)
		}
	case "fs_list", "search_files":
		items := discoveredTargets(call, result)
		if len(items) > 0 {
			return fmt.Sprintf("%s discovered %d candidates: %s", call.Name, len(items), strings.Join(limitStrings(items, 5), ", "))
		}
	case "search_text":
		items := discoveredTargets(call, result)
		if len(items) > 0 {
			return fmt.Sprintf("%s found matches in %d files: %s", call.Name, len(items), strings.Join(limitStrings(items, 5), ", "))
		}
	case "git_diff":
		return "git_diff produced a diff summary"
	case "task_list":
		return "task_list returned registered tasks"
	}
	text := strings.TrimSpace(result.Output)
	if text == "" {
		return call.Name + " completed"
	}
	lines := strings.Split(text, "\n")
	if len(lines[0]) > 120 {
		return lines[0][:120]
	}
	return lines[0]
}

func fingerprintFor(value string) string {
	sum := sha1.Sum([]byte(value))
	return fmt.Sprintf("%x", sum[:8])
}

func discoveredTargets(call domain.ToolCall, result domain.ToolResult) []string {
	if !result.Success {
		return nil
	}
	switch call.Name {
	case "fs_list":
		var entries []struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(result.Output), &entries); err == nil {
			values := make([]string, 0, len(entries))
			for _, entry := range entries {
				values = append(values, entry.Path)
			}
			return uniqueStrings(limitStrings(values, 12))
		}
	case "search_files":
		var entries []string
		if err := json.Unmarshal([]byte(result.Output), &entries); err == nil {
			return uniqueStrings(limitStrings(entries, 12))
		}
	case "search_text":
		var matches []struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(result.Output), &matches); err == nil {
			values := make([]string, 0, len(matches))
			for _, match := range matches {
				values = append(values, match.Path)
			}
			return uniqueStrings(limitStrings(values, 12))
		}
	}
	return nil
}

func limitStrings(values []string, max int) []string {
	if max <= 0 || len(values) <= max {
		return values
	}
	return append([]string(nil), values[:max]...)
}

func stringSliceArg(value any) []string {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if text, ok := item.(string); ok && text != "" {
			out = append(out, text)
		}
	}
	return out
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func cloneAnyMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
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

func (s *Service) newEvent(runID, parentRunID, agentID, typ, detail string, contextCount int) domain.ExecutionEvent {
	event := domain.ExecutionEvent{
		RunID:        runID,
		ParentRunID:  parentRunID,
		AgentID:      agentID,
		Type:         typ,
		Detail:       detail,
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

func countContextItems(messages []domain.Message, context domain.ContextPack) int {
	messageCount := len(messages)
	fileCount := len(uniqueStrings(append([]string(nil), context.RelevantFiles...)))
	artifactCount := len(uniqueStrings(append([]string(nil), context.ArtifactRefs...)))
	summaryCount := len(uniqueStrings(append([]string(nil), context.FileSummaries...)))
	findingCount := len(uniqueStrings(append([]string(nil), context.Findings...)))
	observationCount := len(uniqueStrings(append([]string(nil), context.RecentObservations...)))
	return messageCount + fileCount + artifactCount + summaryCount + findingCount + observationCount
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
