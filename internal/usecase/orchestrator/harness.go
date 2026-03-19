package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"yagent/internal/domain"
)

func (s *Service) newRunState(request domain.TurnRequest) *domain.RunState {
	now := time.Now()
	runID := s.nextRunID("run")
	run := &domain.RunState{
		ID:           runID,
		RootRunID:    runID,
		Status:       domain.RunStatusRunning,
		CurrentPhase: domain.RunPhaseIntake,
		Attempt:      1,
		Profile:      request.Profile,
		UserGoal:     latestUserMessage(request.Messages),
		Messages:     cloneMessages(request.Messages),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	run.Checkpoints = append(run.Checkpoints, checkpoint(run, domain.RunPhaseIntake, run.UserGoal))
	return run
}

func (s *Service) loadResumeState(ctx context.Context, resumeID string) (*domain.RunState, error) {
	if s.config.RunStore == nil {
		return nil, nil
	}
	if resumeID == "latest" {
		return s.config.RunStore.LoadLatestRun(ctx)
	}
	return s.config.RunStore.LoadRun(ctx, resumeID)
}

func (s *Service) saveRun(ctx context.Context, run *domain.RunState) error {
	if s.config.RunStore == nil || run == nil {
		return nil
	}
	run.UpdatedAt = time.Now()
	return s.config.RunStore.SaveRun(ctx, run)
}

func (s *Service) runPlanPhase(ctx context.Context, run *domain.RunState, request domain.TurnRequest, inventory []domain.AgentInventoryEntry) (*domain.ExecutionPlan, []domain.ExecutionEvent, error) {
	run.CurrentPhase = domain.RunPhasePlan
	run.Attempt = 1
	_ = s.saveRun(ctx, run)

	planner, ok := s.catalog.Resolve("planner")
	if !ok {
		plan := buildFallbackExecutionPlan(inventory, "planner agent was not available")
		run.ExecutionPlan = plan
		run.Plan = planNodesFromExecutionPlan(plan)
		run.WorkUnits = workUnitsFromExecutionPlan(plan)
		run.Artifacts = append(run.Artifacts, newExecutionPlanArtifact(run, domain.RunPhasePlan, fallbackString(plan.Primary.AgentID, "manager"), plan))
		run.Checkpoints = append(run.Checkpoints, checkpoint(run, domain.RunPhasePlan, plan.Summary))
		_ = s.saveRun(ctx, run)
		return plan, nil, nil
	}

	invocation := s.phaseInvocation(run, planner, request, domain.RunPhasePlan, 1, plannerMessages(run.Messages, inventory), "Create the execution plan for this request.")
	invocation.Context.AgentInventory = inventory
	invocation.Context.ExpectedOutput = plannerOutputContract()
	invocation.Context.TaskBrief = "Create the execution plan for this request and return strict JSON only."
	result, err := s.runAgent(ctx, invocation, 0)
	events := append([]domain.ExecutionEvent(nil), result.Events...)
	if err != nil {
		plan := buildFallbackExecutionPlan(inventory, "planner call failed: "+err.Error())
		run.ExecutionPlan = plan
		run.Plan = planNodesFromExecutionPlan(plan)
		run.WorkUnits = workUnitsFromExecutionPlan(plan)
		run.Artifacts = append(run.Artifacts, newExecutionPlanArtifact(run, domain.RunPhasePlan, planner.ID, plan))
		run.Checkpoints = append(run.Checkpoints, checkpoint(run, domain.RunPhasePlan, plan.Summary))
		_ = s.saveRun(ctx, run)
		return plan, events, nil
	}

	plan, parseErr := parseExecutionPlan(result.Message.Content)
	if parseErr == nil {
		parseErr = validateAndNormalizeExecutionPlan(plan, inventory)
	}
	if parseErr != nil {
		repairMessages := plannerMessages(run.Messages, inventory)
		repairMessages = phaseMessages(repairMessages, repairPromptForPlan(result.Message.Content, parseErr))
		repairInvocation := s.phaseInvocation(run, planner, request, domain.RunPhasePlan, 1, repairMessages, "Repair the invalid execution plan JSON and return strict JSON only.")
		repairInvocation.Context.AgentInventory = inventory
		repairInvocation.Context.ExpectedOutput = plannerOutputContract()
		repaired, repairErr := s.runAgent(ctx, repairInvocation, 0)
		events = append(events, repaired.Events...)
		if repairErr == nil {
			plan, parseErr = parseExecutionPlan(repaired.Message.Content)
			if parseErr == nil {
				parseErr = validateAndNormalizeExecutionPlan(plan, inventory)
			}
		}
	}
	if parseErr != nil {
		plan = buildFallbackExecutionPlan(inventory, defaultFallbackPlanReason(parseErr))
	}

	run.ExecutionPlan = plan
	run.Plan = planNodesFromExecutionPlan(plan)
	run.WorkUnits = workUnitsFromExecutionPlan(plan)
	markPlanNodeStatus(run, domain.RunPhasePlan, fallbackString(planAgentID(plan), "planner"), "done")
	run.Artifacts = append(run.Artifacts, newExecutionPlanArtifact(run, domain.RunPhasePlan, fallbackString(planAgentID(plan), "planner"), plan))
	run.Checkpoints = append(run.Checkpoints, checkpoint(run, domain.RunPhasePlan, plan.Summary))
	s.maybeCompactRun(run)
	_ = s.saveRun(ctx, run)
	return plan, events, nil
}

func (s *Service) runDirectPhase(ctx context.Context, run *domain.RunState, manager domain.AgentSpec, request domain.TurnRequest) (domain.AgentResult, []domain.ExecutionEvent, error) {
	run.CurrentPhase = domain.RunPhaseExecute
	run.Attempt = 1
	_ = s.saveRun(ctx, run)

	result, err := s.runAgent(ctx, s.phaseInvocation(run, manager, request, domain.RunPhaseExecute, 1, run.Messages, "Handle the request directly and produce the final answer."), 0)
	if err != nil {
		return domain.AgentResult{}, nil, err
	}
	markPlanNodeStatus(run, domain.RunPhaseExecute, result.Message.AgentID, "done")
	run.Artifacts = append(run.Artifacts, newAgentMessageArtifact(run, domain.RunPhaseExecute, result.Message.AgentID, "Execution result", "execution", result.Message.Content, nil))
	run.Messages = append(run.Messages, domain.Message{Role: domain.RoleAssistant, AgentID: result.Message.AgentID, Content: result.Message.Content})
	s.maybeCompactRun(run)
	_ = s.saveRun(ctx, run)
	return result, result.Events, nil
}

func (s *Service) runExecutePhase(ctx context.Context, run *domain.RunState, plan *domain.ExecutionPlan, request domain.TurnRequest) (domain.AgentResult, []domain.ExecutionEvent, error) {
	run.CurrentPhase = domain.RunPhaseExecute
	run.Attempt = 1
	_ = s.saveRun(ctx, run)

	messages := cloneMessages(run.Messages)
	if plan == nil {
		return domain.AgentResult{}, nil, fmt.Errorf("execution plan がありません")
	}
	messages = phaseMessages(messages, "Execution plan:\n"+stablePlanJSON(plan))

	prepEvents := []domain.ExecutionEvent{}
	prepArtifacts := []domain.RunArtifact{}
	if len(plan.Preparation) > 0 {
		scheduler := newRuntimeScheduler(s.config.MaxParallelAgents)
		specs := make([]scheduleSpec, 0, len(plan.Preparation))
		assignments := make([]domain.PlannedAgentAssignment, 0, len(plan.Preparation))
		for _, item := range plan.Preparation {
			specs = append(specs, scheduleSpec{
				ID:              "prep:" + item.AgentID,
				Source:          item.AgentID,
				SourceLimit:     1,
				SideEffectClass: domain.SideEffectNone,
			})
			assignments = append(assignments, item)
			markWorkUnitStatus(run, "execute:prep:"+item.AgentID, "running")
		}
		completed := map[string]bool{}
		for len(completed) < len(specs) {
			batch := scheduler.nextBatch(specs, completed)
			if len(batch) == 0 {
				break
			}
			group, groupCtx := errgroup.WithContext(ctx)
			type prepResult struct {
				index    int
				artifact domain.RunArtifact
				events   []domain.ExecutionEvent
			}
			results := make([]prepResult, len(batch))
			for batchIdx, specIdx := range batch {
				batchIdx := batchIdx
				specIdx := specIdx
				group.Go(func() error {
					item := assignments[specIdx]
					agent, ok := s.catalog.Resolve(item.AgentID)
					if !ok {
						return nil
					}
					prepTask := fallbackString(strings.TrimSpace(item.Reason), fmt.Sprintf("Prepare focused context for the primary agent %s. Return only findings that materially help the task.", fallbackString(plan.Primary.AgentID, "manager")))
					invocation := s.phaseInvocation(run, agent, request, domain.RunPhaseExecute, 1, messages, prepTask)
					invocation.Context.ExpectedOutput = map[string]any{
						"goal": "Return only the findings that materially help the primary agent.",
					}
					research, err := s.runAgent(groupCtx, invocation, 0)
					if err != nil {
						return nil
					}
					results[batchIdx] = prepResult{
						index:    specIdx,
						artifact: newAgentMessageArtifact(run, domain.RunPhaseExecute, agent.ID, agent.Name+" evidence", "evidence_bundle", research.Message.Content, nil),
						events:   research.Events,
					}
					return nil
				})
			}
			if err := group.Wait(); err != nil {
				return domain.AgentResult{}, prepEvents, err
			}
			for _, item := range results {
				if item.artifact.ID == "" {
					continue
				}
				prepArtifacts = append(prepArtifacts, item.artifact)
				prepEvents = append(prepEvents, item.events...)
				run.Artifacts = append(run.Artifacts, item.artifact)
				markPlanNodeStatus(run, domain.RunPhaseExecute, item.artifact.AgentID, "done")
				markWorkUnitStatus(run, "execute:prep:"+item.artifact.AgentID, "done")
				completed[specs[item.index].ID] = true
			}
		}
	}

	if bundle := buildEvidenceBundleArtifact(run, prepArtifacts); bundle.ID != "" {
		run.Artifacts = append(run.Artifacts, bundle)
		messages = phaseMessages(messages, "Evidence bundle:\n"+bundle.Summary)
	}

	primaryID := fallbackString(plan.Primary.AgentID, "manager")
	primary, ok := s.catalog.Resolve(primaryID)
	if !ok {
		return domain.AgentResult{}, prepEvents, fmt.Errorf("primary agent %q が見つかりません", primaryID)
	}
	taskBrief := fallbackString(strings.TrimSpace(plan.Primary.Reason), "Handle the request directly using the prepared context and available tools.")
	if primary.ID == "manager" {
		taskBrief = fallbackString(strings.TrimSpace(plan.Primary.Reason), "Coordinate execution using the prepared context and produce the implementation result.")
	}
	result, err := s.runAgent(ctx, s.phaseInvocation(run, primary, request, domain.RunPhaseExecute, 1, messages, taskBrief), 0)
	if err != nil {
		return domain.AgentResult{}, prepEvents, err
	}
	markPlanNodeStatus(run, domain.RunPhaseExecute, primary.ID, "done")
	markWorkUnitStatus(run, "execute:primary:"+primary.ID, "done")
	run.Artifacts = append(run.Artifacts, newAgentMessageArtifact(run, domain.RunPhaseExecute, result.Message.AgentID, "Execution result", "execution", result.Message.Content, nil))
	run.Checkpoints = append(run.Checkpoints, checkpoint(run, domain.RunPhaseExecute, result.Message.Content))
	run.Messages = append(run.Messages, domain.Message{Role: domain.RoleAssistant, AgentID: result.Message.AgentID, Content: result.Message.Content})
	s.maybeCompactRun(run)
	_ = s.saveRun(ctx, run)
	return result, append(prepEvents, result.Events...), nil
}

func (s *Service) runVerifyPhase(ctx context.Context, run *domain.RunState, plan *domain.ExecutionPlan, request domain.TurnRequest, execution domain.AgentResult, attempt int) (domain.VerificationResult, []domain.ExecutionEvent, error) {
	run.CurrentPhase = domain.RunPhaseVerify
	run.Attempt = attempt
	_ = s.saveRun(ctx, run)

	input := phaseMessages(run.Messages, "Execution summary:\n"+execution.Message.Content, "Execution plan:\n"+stablePlanJSON(plan))
	results := []domain.VerificationResult{}
	allEvents := []domain.ExecutionEvent{}
	if len(plan.Verify) > 0 {
		scheduler := newRuntimeScheduler(s.config.MaxParallelAgents)
		specs := make([]scheduleSpec, 0, len(plan.Verify))
		for _, item := range plan.Verify {
			specs = append(specs, scheduleSpec{
				ID:              "verify:" + item.AgentID,
				Source:          item.AgentID,
				SourceLimit:     1,
				SideEffectClass: domain.SideEffectNone,
			})
			markWorkUnitStatus(run, "verify:"+item.AgentID, "running")
		}
		completed := map[string]bool{}
		for len(completed) < len(specs) {
			batch := scheduler.nextBatch(specs, completed)
			if len(batch) == 0 {
				break
			}
			group, groupCtx := errgroup.WithContext(ctx)
			type verifyResult struct {
				index    int
				parsed   domain.VerificationResult
				artifact domain.RunArtifact
				events   []domain.ExecutionEvent
			}
			batchResults := make([]verifyResult, len(batch))
			for batchIdx, specIdx := range batch {
				batchIdx := batchIdx
				specIdx := specIdx
				group.Go(func() error {
					item := plan.Verify[specIdx]
					agent, ok := s.catalog.Resolve(item.AgentID)
					if !ok {
						return nil
					}
					agent = withVerificationInstruction(agent)
					taskBrief := fallbackString(strings.TrimSpace(item.Reason), "Verify the latest implementation. Return VERIFICATION_STATUS, SUMMARY, and REPAIR_BRIEF.")
					invocation := s.phaseInvocation(run, agent, request, domain.RunPhaseVerify, attempt, input, taskBrief)
					invocation.Context.ExpectedOutput = verificationOutputContract()
					result, err := s.runAgent(groupCtx, invocation, 0)
					if err != nil {
						return err
					}
					parsed := parseVerification(result.Message.Content, agent.ID, attempt)
					batchResults[batchIdx] = verifyResult{
						index:    specIdx,
						parsed:   parsed,
						artifact: newVerificationArtifact(run, domain.RunPhaseVerify, agent.ID, agent.Name+" verification", result.Message.Content, parsed),
						events:   result.Events,
					}
					return nil
				})
			}
			if err := group.Wait(); err != nil {
				return domain.VerificationResult{}, allEvents, err
			}
			for _, item := range batchResults {
				if item.parsed.SourceAgent == "" {
					continue
				}
				results = append(results, item.parsed)
				allEvents = append(allEvents, item.events...)
				run.Verification = append(run.Verification, item.parsed)
				run.Artifacts = append(run.Artifacts, item.artifact)
				markPlanNodeStatus(run, domain.RunPhaseVerify, item.parsed.SourceAgent, "done")
				markWorkUnitStatus(run, "verify:"+item.parsed.SourceAgent, "done")
				completed[specs[item.index].ID] = true
			}
		}
	}
	merged := mergeVerification(results, attempt)
	run.Checkpoints = append(run.Checkpoints, checkpoint(run, domain.RunPhaseVerify, merged.Summary))
	s.maybeCompactRun(run)
	_ = s.saveRun(ctx, run)
	return merged, allEvents, nil
}

func (s *Service) runRecoverPhase(ctx context.Context, run *domain.RunState, plan *domain.ExecutionPlan, request domain.TurnRequest, verification domain.VerificationResult, attempt int) (domain.AgentResult, []domain.ExecutionEvent, error) {
	run.CurrentPhase = domain.RunPhaseRecover
	run.Attempt = attempt
	_ = s.saveRun(ctx, run)

	if plan == nil || plan.Recovery == nil {
		return domain.AgentResult{}, nil, fmt.Errorf("recovery plan がありません")
	}
	coder, ok := s.catalog.Resolve(fallbackString(plan.Recovery.AgentID, "coder"))
	if !ok {
		return domain.AgentResult{}, nil, fmt.Errorf("recovery agent %q が見つかりません", plan.Recovery.AgentID)
	}
	repairPrompt := strings.TrimSpace("Repair the implementation using this brief:\n" + verification.RepairBrief)
	taskBrief := fallbackString(strings.TrimSpace(plan.Recovery.Reason), repairPrompt)
	invocation := s.phaseInvocation(run, coder, request, domain.RunPhaseRecover, attempt, phaseMessages(run.Messages, repairPrompt, "Execution plan:\n"+stablePlanJSON(plan)), taskBrief)
	invocation.Context.ExpectedOutput = repairOutputContract()
	result, err := s.runAgent(ctx, invocation, 0)
	if err != nil {
		return domain.AgentResult{}, nil, err
	}
	markPlanNodeStatus(run, domain.RunPhaseRecover, coder.ID, "done")
	run.Artifacts = append(run.Artifacts, newAgentMessageArtifact(run, domain.RunPhaseRecover, coder.ID, "Recovery result", "recovery", result.Message.Content, nil))
	run.Checkpoints = append(run.Checkpoints, checkpoint(run, domain.RunPhaseRecover, result.Message.Content))
	run.Messages = append(run.Messages, domain.Message{Role: domain.RoleAssistant, AgentID: coder.ID, Content: result.Message.Content})
	s.maybeCompactRun(run)
	_ = s.saveRun(ctx, run)
	return result, result.Events, nil
}

func (s *Service) runFinalizePhase(ctx context.Context, run *domain.RunState, plan *domain.ExecutionPlan, request domain.TurnRequest, execution domain.AgentResult) (domain.AgentResult, []domain.ExecutionEvent, error) {
	if plan == nil || plan.Finalize == nil || plan.Finalize.AgentID == "" {
		return execution, execution.Events, nil
	}
	manager, ok := s.catalog.Resolve(plan.Finalize.AgentID)
	if !ok {
		return domain.AgentResult{}, nil, fmt.Errorf("finalize agent %q が見つかりません", plan.Finalize.AgentID)
	}
	run.CurrentPhase = domain.RunPhaseFinalize
	_ = s.saveRun(ctx, run)
	input := phaseMessages(run.Messages,
		"Execution summary:\n"+execution.Message.Content,
		"Verification summary:\n"+latestVerificationSummary(run),
		"Execution plan:\n"+stablePlanJSON(plan),
	)
	taskBrief := fallbackString(strings.TrimSpace(plan.Finalize.Reason), "Summarize the completed work, verification status, and remaining risks.")
	result, err := s.runAgent(ctx, s.phaseInvocation(run, manager, request, domain.RunPhaseFinalize, run.Attempt, input, taskBrief), 0)
	if err != nil {
		return domain.AgentResult{}, nil, err
	}
	markPlanNodeStatus(run, domain.RunPhaseFinalize, manager.ID, "done")
	s.maybeCompactRun(run)
	return result, result.Events, nil
}

func (s *Service) phaseInvocation(run *domain.RunState, agent domain.AgentSpec, request domain.TurnRequest, phase domain.RunPhase, attempt int, messages []domain.Message, task string) domain.AgentInvocation {
	agent = s.resolveModel(agent, request.Model)
	if agent.RoutingProfile == "" && request.Profile != "" {
		agent.RoutingProfile = request.Profile
	}
	contextPack := s.buildContext(run, agent, phase, messages)
	if task != "" {
		contextPack.TaskBrief = task
	}
	return domain.AgentInvocation{
		RunID:     s.nextRunID(agent.ID),
		RootRunID: run.RootRunID,
		Agent:     agent,
		Messages:  cloneMessages(messages),
		Context:   contextPack,
		Phase:     phase,
		Attempt:   attempt,
		Model:     request.Model,
		Stream:    false,
	}
}

func (s *Service) buildContext(run *domain.RunState, agent domain.AgentSpec, phase domain.RunPhase, messages []domain.Message) domain.RunContext {
	enabledCapabilities := []string(nil)
	if run != nil {
		enabledCapabilities = append(enabledCapabilities, run.EnabledCapabilities...)
	}
	allTools := s.toolDefinitionsForAgent(agent)
	visible := visibleTools(agent, allTools, newAgentSession(domain.AgentInvocation{
		Context: domain.RunContext{EnabledCapabilities: enabledCapabilities},
	}))
	if s.config.ContextEngine == nil {
		userGoal := latestUserMessage(messages)
		if run != nil && strings.TrimSpace(run.UserGoal) != "" {
			userGoal = run.UserGoal
		}
		contextPack := domain.RunContext{
			UserGoal:           userGoal,
			CurrentPhase:       phase,
			TaskBrief:          userGoal,
			RecentMessages:     cloneMessages(messages),
			RelevantFiles:      extractRelevantFiles(messages),
			PacketRole:         agent.ID,
			PacketKind:         agent.ID,
			AvailableToolNames: toolNames(visible),
		}
		contextPack.ToolState = buildToolState(agent, allTools, visible)
		return contextPack
	}
	contextPack := s.config.ContextEngine.Build(run, agent, phase, messages, visible)
	contextPack.ToolState = buildToolState(agent, allTools, visible)
	return contextPack
}

func (s *Service) buildContextForInvocation(parent domain.AgentInvocation, agent domain.AgentSpec, call domain.ToolCall, phase domain.RunPhase) domain.RunContext {
	contextPack := parent.Context
	contextPack.CurrentPhase = phase
	contextPack.TaskBrief = stringArg(call.Arguments, "task")
	allTools := s.toolDefinitionsForAgent(agent)
	visible := visibleTools(agent, allTools, newAgentSession(domain.AgentInvocation{
		Context: domain.RunContext{EnabledCapabilities: contextPack.EnabledCapabilities},
	}))
	contextPack.AvailableToolNames = toolNames(visible)
	contextPack.ToolState = buildToolState(agent, allTools, visible)
	return contextPack
}

func (s *Service) toolDefinitionsForAgent(agent domain.AgentSpec) []domain.ToolDefinition {
	return append(s.tools.Definitions(agent), s.agentToolDefinitions(agent)...)
}

func phaseMessages(base []domain.Message, additions ...string) []domain.Message {
	out := cloneMessages(base)
	for _, addition := range additions {
		addition = strings.TrimSpace(addition)
		if addition == "" {
			continue
		}
		out = append(out, domain.Message{Role: domain.RoleUser, Content: addition})
	}
	return out
}

func checkpoint(run *domain.RunState, phase domain.RunPhase, summary string) domain.RunCheckpoint {
	return domain.RunCheckpoint{
		ID:        fmt.Sprintf("checkpoint-%d", len(run.Checkpoints)+1),
		Phase:     phase,
		Status:    run.Status,
		Attempt:   run.Attempt,
		Summary:   truncateSummary(summary),
		CreatedAt: time.Now(),
	}
}

func (s *Service) maybeCompactRun(run *domain.RunState) {
	if s.config.ContextEngine == nil || run == nil {
		return
	}
	_, _ = s.config.ContextEngine.MaybeCompact(run)
}

func truncateSummary(content string) string {
	content = strings.TrimSpace(content)
	if len(content) <= 240 {
		return content
	}
	return content[:237] + "..."
}

func extractPlan(content string) []domain.PlanNode {
	lines := strings.Split(content, "\n")
	nodes := []domain.PlanNode{}
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimLeft(line, "-*0123456789. "))
		if line == "" {
			continue
		}
		nodes = append(nodes, domain.PlanNode{
			ID:        fmt.Sprintf("plan-%d", len(nodes)+1),
			Title:     truncateSummary(line),
			Status:    "pending",
			CreatedAt: time.Now(),
		})
		if len(nodes) >= 8 {
			break
		}
	}
	if len(nodes) == 0 && strings.TrimSpace(content) != "" {
		nodes = append(nodes, domain.PlanNode{
			ID:        "plan-1",
			Title:     truncateSummary(content),
			Status:    "pending",
			CreatedAt: time.Now(),
		})
	}
	return nodes
}

func withVerificationInstruction(agent domain.AgentSpec) domain.AgentSpec {
	extra := "Return lines in this exact format: VERIFICATION_STATUS: pass|fail, SUMMARY: <one sentence>, REPAIR_BRIEF: <short actionable brief>."
	if strings.Contains(agent.Instruction, extra) {
		return agent
	}
	agent.Instruction = strings.TrimSpace(agent.Instruction + "\n\n" + extra)
	return agent
}

func parseVerification(content string, agentID string, attempt int) domain.VerificationResult {
	result := domain.VerificationResult{
		Attempt:     attempt,
		SourceAgent: agentID,
		Status:      "pass",
		Summary:     truncateSummary(content),
		CreatedAt:   time.Now(),
	}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "VERIFICATION_STATUS:"):
			result.Status = strings.TrimSpace(strings.TrimPrefix(line, "VERIFICATION_STATUS:"))
		case strings.HasPrefix(line, "SUMMARY:"):
			result.Summary = strings.TrimSpace(strings.TrimPrefix(line, "SUMMARY:"))
		case strings.HasPrefix(line, "REPAIR_BRIEF:"):
			result.RepairBrief = strings.TrimSpace(strings.TrimPrefix(line, "REPAIR_BRIEF:"))
		}
	}
	if result.RepairBrief == "" && looksLikeFailure(result.Summary) {
		result.RepairBrief = result.Summary
	}
	if looksLikeFailure(result.Summary) {
		result.Status = "fail"
	}
	return result
}

func mergeVerification(results []domain.VerificationResult, attempt int) domain.VerificationResult {
	merged := domain.VerificationResult{
		Attempt:     attempt,
		SourceAgent: "verification",
		Status:      "pass",
		CreatedAt:   time.Now(),
	}
	summaries := []string{}
	briefs := []string{}
	for _, result := range results {
		if strings.EqualFold(result.Status, "fail") {
			merged.Status = "fail"
		}
		if result.Summary != "" {
			summaries = append(summaries, result.SourceAgent+": "+result.Summary)
		}
		if result.RepairBrief != "" {
			briefs = append(briefs, result.SourceAgent+": "+result.RepairBrief)
		}
	}
	merged.Summary = strings.Join(summaries, " | ")
	merged.RepairBrief = strings.Join(briefs, "\n")
	return merged
}

func latestVerificationSummary(run *domain.RunState) string {
	if run == nil || len(run.Verification) == 0 {
		return "No verification results."
	}
	last := run.Verification[len(run.Verification)-1]
	return fmt.Sprintf("%s: %s", last.Status, last.Summary)
}

func (s *Service) rememberRun(ctx context.Context, run *domain.RunState) error {
	if s.config.MemoryStore == nil || run == nil {
		return nil
	}
	memory, err := s.config.MemoryStore.LoadMemory(ctx)
	if err != nil {
		return err
	}
	if memory == nil {
		memory = &domain.RepoMemory{}
	}
	for _, artifact := range lastArtifacts(run.Artifacts, 8) {
		memory.RecentArtifacts = appendArtifactRef(memory.RecentArtifacts, domain.ArtifactReference{
			ID:   artifact.ID,
			Kind: artifact.Kind,
			Name: artifact.Name,
		})
	}
	for _, verification := range run.Verification {
		if strings.EqualFold(verification.Status, "fail") && verification.Summary != "" {
			memory.KnownFailures = appendUnique(memory.KnownFailures, verification.Summary)
		}
	}
	for _, artifact := range lastArtifacts(run.Artifacts, 4) {
		if artifact.Summary == "" {
			continue
		}
		memory.StableFacts = appendOrReplaceWorkspaceFact(memory.StableFacts, domain.WorkspaceFact{
			ID:         artifact.ID,
			Kind:       artifact.Kind,
			Summary:    artifact.Summary,
			ArtifactID: artifact.ID,
			UpdatedAt:  time.Now(),
		})
	}
	return s.config.MemoryStore.SaveMemory(ctx, memory)
}

func looksLikeFailure(content string) bool {
	text := strings.ToLower(content)
	for _, token := range []string{"fail", "failing", "missing", "error", "regression", "not fixed", "issue"} {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

func appendUnique(items []string, value string) []string {
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
}

func appendArtifactRef(items []domain.ArtifactReference, ref domain.ArtifactReference) []domain.ArtifactReference {
	for _, item := range items {
		if item.ID == ref.ID {
			return items
		}
	}
	return append(items, ref)
}

func appendOrReplaceWorkspaceFact(items []domain.WorkspaceFact, fact domain.WorkspaceFact) []domain.WorkspaceFact {
	for idx := range items {
		if items[idx].ID != fact.ID {
			continue
		}
		items[idx] = fact
		return items
	}
	return append(items, fact)
}

func workUnitsFromExecutionPlan(plan *domain.ExecutionPlan) []domain.WorkUnit {
	if plan == nil {
		return nil
	}
	units := make([]domain.WorkUnit, 0, len(plan.Preparation)+len(plan.Verify)+4)
	prepIDs := make([]string, 0, len(plan.Preparation))
	for _, item := range plan.Preparation {
		id := "execute:prep:" + item.AgentID
		prepIDs = append(prepIDs, id)
		units = append(units, domain.WorkUnit{
			ID:     id,
			Kind:   "preparation",
			Role:   item.AgentID,
			Phase:  domain.RunPhaseExecute,
			Task:   item.Reason,
			Status: "pending",
			Source: item.AgentID,
		})
	}
	primaryID := "execute:primary:" + plan.Primary.AgentID
	units = append(units, domain.WorkUnit{
		ID:        primaryID,
		Kind:      "primary",
		Role:      plan.Primary.AgentID,
		Phase:     domain.RunPhaseExecute,
		Task:      plan.Primary.Reason,
		Status:    "pending",
		DependsOn: append([]string(nil), prepIDs...),
		Source:    plan.Primary.AgentID,
	})
	verifyIDs := make([]string, 0, len(plan.Verify))
	for _, item := range plan.Verify {
		id := "verify:" + item.AgentID
		verifyIDs = append(verifyIDs, id)
		units = append(units, domain.WorkUnit{
			ID:        id,
			Kind:      "verification",
			Role:      item.AgentID,
			Phase:     domain.RunPhaseVerify,
			Task:      item.Reason,
			Status:    "pending",
			DependsOn: []string{primaryID},
			Source:    item.AgentID,
		})
	}
	if plan.Recovery != nil && plan.Recovery.AgentID != "" {
		units = append(units, domain.WorkUnit{
			ID:              "recover:" + plan.Recovery.AgentID,
			Kind:            "recovery",
			Role:            plan.Recovery.AgentID,
			Phase:           domain.RunPhaseRecover,
			Task:            plan.Recovery.Reason,
			Status:          "pending",
			DependsOn:       append([]string(nil), verifyIDs...),
			Source:          plan.Recovery.AgentID,
			SideEffectClass: domain.SideEffectExternal,
		})
	}
	if plan.Finalize != nil && plan.Finalize.AgentID != "" {
		dependsOn := []string{primaryID}
		if plan.Recovery != nil && plan.Recovery.AgentID != "" {
			dependsOn = []string{"recover:" + plan.Recovery.AgentID}
		} else if len(verifyIDs) > 0 {
			dependsOn = append([]string(nil), verifyIDs...)
		}
		units = append(units, domain.WorkUnit{
			ID:        "finalize:" + plan.Finalize.AgentID,
			Kind:      "finalize",
			Role:      plan.Finalize.AgentID,
			Phase:     domain.RunPhaseFinalize,
			Task:      plan.Finalize.Reason,
			Status:    "pending",
			DependsOn: dependsOn,
			Source:    plan.Finalize.AgentID,
		})
	}
	return units
}

func markWorkUnitStatus(run *domain.RunState, id string, status string) {
	if run == nil || id == "" {
		return
	}
	for idx := range run.WorkUnits {
		if run.WorkUnits[idx].ID != id {
			continue
		}
		run.WorkUnits[idx].Status = status
		if status == "running" {
			run.WorkUnits[idx].StartedAt = time.Now()
		}
		if status == "done" || status == "failed" {
			run.WorkUnits[idx].CompletedAt = time.Now()
		}
		return
	}
}

func buildEvidenceBundleArtifact(run *domain.RunState, artifacts []domain.RunArtifact) domain.RunArtifact {
	if len(artifacts) == 0 {
		return domain.RunArtifact{}
	}
	summaries := make([]string, 0, len(artifacts))
	refs := make([]domain.ArtifactReference, 0, len(artifacts))
	entries := make([]domain.EvidenceBundleEntry, 0, len(artifacts))
	for _, artifact := range artifacts {
		summaries = append(summaries, artifact.Name+": "+artifact.Summary)
		ref := domain.ArtifactReference{ID: artifact.ID, Kind: artifact.Kind, Name: artifact.Name}
		refs = append(refs, ref)
		entries = append(entries, domain.EvidenceBundleEntry{
			Artifact: ref,
			AgentID:  artifact.AgentID,
			Summary:  artifact.Summary,
		})
	}
	return newTypedArtifact(run, domain.RunPhaseExecute, "", "Evidence bundle", "evidence_bundle", strings.Join(summaries, "\n"), domain.EvidenceBundleArtifactPayload{
		Entries: entries,
	}, refs)
}

func lastArtifacts(items []domain.RunArtifact, limit int) []domain.RunArtifact {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[len(items)-limit:]
}
