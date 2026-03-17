package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

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
		run.Artifacts = append(run.Artifacts, newArtifact(run, domain.RunPhasePlan, fallbackString(plan.Primary.AgentID, "manager"), "Execution plan", "execution_plan", stablePlanJSON(plan)))
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
		run.Artifacts = append(run.Artifacts, newArtifact(run, domain.RunPhasePlan, planner.ID, "Execution plan", "execution_plan", stablePlanJSON(plan)))
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
	markPlanNodeStatus(run, domain.RunPhasePlan, fallbackString(planAgentID(plan), "planner"), "done")
	run.Artifacts = append(run.Artifacts, newArtifact(run, domain.RunPhasePlan, fallbackString(planAgentID(plan), "planner"), "Execution plan", "execution_plan", stablePlanJSON(plan)))
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
	run.Artifacts = append(run.Artifacts, newArtifact(run, domain.RunPhaseExecute, result.Message.AgentID, "Execution result", "execution", result.Message.Content))
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

	for _, item := range plan.Preparation {
		agent, ok := s.catalog.Resolve(item.AgentID)
		if !ok {
			continue
		}
		prepTask := fallbackString(strings.TrimSpace(item.Reason), fmt.Sprintf("Prepare focused context for the primary agent %s. Return only findings that materially help the task.", fallbackString(plan.Primary.AgentID, "manager")))
		invocation := s.phaseInvocation(run, agent, request, domain.RunPhaseExecute, 1, messages, prepTask)
		invocation.Context.ExpectedOutput = map[string]any{
			"goal": "Return only the findings that materially help the primary agent.",
		}
		research, err := s.runAgent(ctx, invocation, 0)
		if err == nil {
			messages = phaseMessages(messages, agent.Name+" summary:\n"+research.Message.Content)
			run.Artifacts = append(run.Artifacts, newArtifact(run, domain.RunPhaseExecute, agent.ID, agent.Name+" summary", "research", research.Message.Content))
			markPlanNodeStatus(run, domain.RunPhaseExecute, agent.ID, "done")
		}
	}

	primaryID := fallbackString(plan.Primary.AgentID, "manager")
	primary, ok := s.catalog.Resolve(primaryID)
	if !ok {
		return domain.AgentResult{}, nil, fmt.Errorf("primary agent %q が見つかりません", primaryID)
	}
	taskBrief := fallbackString(strings.TrimSpace(plan.Primary.Reason), "Handle the request directly using the prepared context and available tools.")
	if primary.ID == "manager" {
		taskBrief = fallbackString(strings.TrimSpace(plan.Primary.Reason), "Coordinate execution using the prepared context and produce the implementation result.")
	}
	result, err := s.runAgent(ctx, s.phaseInvocation(run, primary, request, domain.RunPhaseExecute, 1, messages, taskBrief), 0)
	if err != nil {
		return domain.AgentResult{}, nil, err
	}
	markPlanNodeStatus(run, domain.RunPhaseExecute, primary.ID, "done")
	run.Artifacts = append(run.Artifacts, newArtifact(run, domain.RunPhaseExecute, result.Message.AgentID, "Execution result", "execution", result.Message.Content))
	run.Checkpoints = append(run.Checkpoints, checkpoint(run, domain.RunPhaseExecute, result.Message.Content))
	run.Messages = append(run.Messages, domain.Message{Role: domain.RoleAssistant, AgentID: result.Message.AgentID, Content: result.Message.Content})
	s.maybeCompactRun(run)
	_ = s.saveRun(ctx, run)
	return result, result.Events, nil
}

func (s *Service) runVerifyPhase(ctx context.Context, run *domain.RunState, plan *domain.ExecutionPlan, request domain.TurnRequest, execution domain.AgentResult, attempt int) (domain.VerificationResult, []domain.ExecutionEvent, error) {
	run.CurrentPhase = domain.RunPhaseVerify
	run.Attempt = attempt
	_ = s.saveRun(ctx, run)

	input := phaseMessages(run.Messages, "Execution summary:\n"+execution.Message.Content, "Execution plan:\n"+stablePlanJSON(plan))
	results := []domain.VerificationResult{}
	allEvents := []domain.ExecutionEvent{}
	for _, item := range plan.Verify {
		agent, ok := s.catalog.Resolve(item.AgentID)
		if !ok {
			continue
		}
		agent = withVerificationInstruction(agent)
		taskBrief := fallbackString(strings.TrimSpace(item.Reason), "Verify the latest implementation. Return VERIFICATION_STATUS, SUMMARY, and REPAIR_BRIEF.")
		invocation := s.phaseInvocation(run, agent, request, domain.RunPhaseVerify, attempt, input, taskBrief)
		invocation.Context.ExpectedOutput = verificationOutputContract()
		result, err := s.runAgent(ctx, invocation, 0)
		if err != nil {
			return domain.VerificationResult{}, allEvents, err
		}
		parsed := parseVerification(result.Message.Content, agent.ID, attempt)
		results = append(results, parsed)
		allEvents = append(allEvents, result.Events...)
		run.Verification = append(run.Verification, parsed)
		run.Artifacts = append(run.Artifacts, newArtifact(run, domain.RunPhaseVerify, agent.ID, agent.Name+" verification", "verification", result.Message.Content))
		markPlanNodeStatus(run, domain.RunPhaseVerify, agent.ID, "done")
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
	run.Artifacts = append(run.Artifacts, newArtifact(run, domain.RunPhaseRecover, coder.ID, "Recovery result", "recovery", result.Message.Content))
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
	allTools := append(s.tools.Definitions(agent), s.agentToolDefinitions(agent)...)
	if s.config.ContextEngine == nil {
		userGoal := latestUserMessage(messages)
		if run != nil && strings.TrimSpace(run.UserGoal) != "" {
			userGoal = run.UserGoal
		}
		return domain.RunContext{
			UserGoal:           userGoal,
			CurrentPhase:       phase,
			TaskBrief:          userGoal,
			RecentMessages:     cloneMessages(messages),
			RelevantFiles:      extractRelevantFiles(messages),
			RecentSummary:      userGoal,
			AvailableToolNames: toolNames(allTools),
		}
	}
	return s.config.ContextEngine.Build(run, agent, phase, messages, visibleTools(agent, allTools, newAgentSession(domain.AgentInvocation{Context: domain.RunContext{EnabledCapabilities: run.EnabledCapabilities}})))
}

func (s *Service) buildContextForInvocation(parent domain.AgentInvocation, agent domain.AgentSpec, call domain.ToolCall, phase domain.RunPhase) domain.RunContext {
	contextPack := parent.Context
	contextPack.CurrentPhase = phase
	contextPack.TaskBrief = stringArg(call.Arguments, "task")
	allTools := append(s.tools.Definitions(agent), s.agentToolDefinitions(agent)...)
	contextPack.AvailableToolNames = toolNames(allTools)
	return contextPack
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

func newArtifact(run *domain.RunState, phase domain.RunPhase, agentID string, name string, kind string, content string) domain.RunArtifact {
	return domain.RunArtifact{
		ID:        fmt.Sprintf("artifact-%d", len(run.Artifacts)+1),
		Name:      name,
		Kind:      kind,
		Phase:     phase,
		AgentID:   agentID,
		Summary:   truncateSummary(content),
		Content:   content,
		CreatedAt: time.Now(),
	}
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
		memory.RecentArtifacts = appendUnique(memory.RecentArtifacts, artifact.Name+": "+artifact.Summary)
	}
	for _, verification := range run.Verification {
		if strings.EqualFold(verification.Status, "fail") && verification.Summary != "" {
			memory.FailurePatterns = appendUnique(memory.FailurePatterns, verification.Summary)
		}
	}
	if summary := strings.TrimSpace(run.ConversationSummary); summary != "" {
		memory.Constraints = appendUnique(memory.Constraints, summary)
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

func lastArtifacts(items []domain.RunArtifact, limit int) []domain.RunArtifact {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[len(items)-limit:]
}
