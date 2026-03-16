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

func (s *Service) runPlanPhase(ctx context.Context, run *domain.RunState, request domain.TurnRequest) (domain.AgentResult, []domain.ExecutionEvent, error) {
	run.CurrentPhase = domain.RunPhasePlan
	run.Attempt = 1
	_ = s.saveRun(ctx, run)
	if !s.config.ForcePlanner {
		result := domain.AgentResult{
			Status:  "completed",
			Message: domain.Message{Role: domain.RoleAssistant, AgentID: "planner", Content: "Planner skipped by configuration."},
			Summary: "Planner skipped by configuration.",
		}
		return result, nil, nil
	}
	planner, ok := s.catalog.Resolve("planner")
	if !ok {
		return domain.AgentResult{}, nil, fmt.Errorf("planner agent が見つかりません")
	}
	result, err := s.runAgent(ctx, s.phaseInvocation(run, planner, request, domain.RunPhasePlan, 1, run.Messages, "Create a detailed execution plan before implementation."), 0)
	if err != nil {
		return domain.AgentResult{}, nil, err
	}
	run.Plan = extractPlan(result.Message.Content)
	run.Artifacts = append(run.Artifacts, newArtifact(run, domain.RunPhasePlan, planner.ID, "Execution plan", "plan", result.Message.Content))
	run.Checkpoints = append(run.Checkpoints, checkpoint(run, domain.RunPhasePlan, result.Message.Content))
	s.maybeCompactRun(run)
	_ = s.saveRun(ctx, run)
	return result, result.Events, nil
}

func (s *Service) runDirectPhase(ctx context.Context, run *domain.RunState, manager domain.AgentSpec, request domain.TurnRequest) (domain.AgentResult, []domain.ExecutionEvent, error) {
	run.CurrentPhase = domain.RunPhaseExecute
	run.Attempt = 1
	_ = s.saveRun(ctx, run)

	result, err := s.runAgent(ctx, s.phaseInvocation(run, manager, request, domain.RunPhaseExecute, 1, run.Messages, "Handle the request directly and produce the final answer."), 0)
	if err != nil {
		return domain.AgentResult{}, nil, err
	}
	run.Artifacts = append(run.Artifacts, newArtifact(run, domain.RunPhaseExecute, result.Message.AgentID, "Execution result", "execution", result.Message.Content))
	run.Messages = append(run.Messages, domain.Message{Role: domain.RoleAssistant, AgentID: result.Message.AgentID, Content: result.Message.Content})
	s.maybeCompactRun(run)
	_ = s.saveRun(ctx, run)
	return result, result.Events, nil
}

func (s *Service) runExecutePhase(ctx context.Context, run *domain.RunState, manager domain.AgentSpec, request domain.TurnRequest, plan domain.AgentResult) (domain.AgentResult, []domain.ExecutionEvent, error) {
	run.CurrentPhase = domain.RunPhaseExecute
	run.Attempt = 1
	_ = s.saveRun(ctx, run)

	messages := cloneMessages(run.Messages)
	if strings.TrimSpace(plan.Message.Content) != "" && plan.Message.Content != "Planner skipped by configuration." {
		messages = phaseMessages(messages, "Planning summary:\n"+plan.Message.Content)
	}
	if s.config.ForceResearcher {
		if researcher, ok := s.catalog.Resolve("researcher"); ok {
			research, err := s.runAgent(ctx, s.phaseInvocation(run, researcher, request, domain.RunPhaseExecute, 1, messages, "Inspect the repository and return only findings needed for implementation."), 0)
			if err == nil {
				messages = phaseMessages(messages, "Research summary:\n"+research.Message.Content)
				run.Artifacts = append(run.Artifacts, newArtifact(run, domain.RunPhaseExecute, researcher.ID, "Research summary", "research", research.Message.Content))
			}
		}
	}

	result, err := s.runAgent(ctx, s.phaseInvocation(run, manager, request, domain.RunPhaseExecute, 1, messages, "Coordinate implementation. Use subagents and tools when needed, then produce the implementation result."), 0)
	if err != nil {
		return domain.AgentResult{}, nil, err
	}
	run.Artifacts = append(run.Artifacts, newArtifact(run, domain.RunPhaseExecute, result.Message.AgentID, "Execution result", "execution", result.Message.Content))
	run.Checkpoints = append(run.Checkpoints, checkpoint(run, domain.RunPhaseExecute, result.Message.Content))
	run.Messages = append(run.Messages, domain.Message{Role: domain.RoleAssistant, AgentID: result.Message.AgentID, Content: result.Message.Content})
	s.maybeCompactRun(run)
	_ = s.saveRun(ctx, run)
	return result, result.Events, nil
}

func (s *Service) runVerifyPhase(ctx context.Context, run *domain.RunState, request domain.TurnRequest, execution domain.AgentResult, attempt int) (domain.VerificationResult, []domain.ExecutionEvent, error) {
	run.CurrentPhase = domain.RunPhaseVerify
	run.Attempt = attempt
	_ = s.saveRun(ctx, run)

	input := phaseMessages(run.Messages, "Execution summary:\n"+execution.Message.Content)
	results := []domain.VerificationResult{}
	allEvents := []domain.ExecutionEvent{}
	for _, agentID := range []string{"tester", "reviewer"} {
		agent, ok := s.catalog.Resolve(agentID)
		if !ok {
			continue
		}
		agent = withVerificationInstruction(agent)
		result, err := s.runAgent(ctx, s.phaseInvocation(run, agent, request, domain.RunPhaseVerify, attempt, input, "Verify the latest implementation. Return VERIFICATION_STATUS, SUMMARY, and REPAIR_BRIEF."), 0)
		if err != nil {
			return domain.VerificationResult{}, allEvents, err
		}
		parsed := parseVerification(result.Message.Content, agent.ID, attempt)
		results = append(results, parsed)
		allEvents = append(allEvents, result.Events...)
		run.Verification = append(run.Verification, parsed)
		run.Artifacts = append(run.Artifacts, newArtifact(run, domain.RunPhaseVerify, agent.ID, agent.Name+" verification", "verification", result.Message.Content))
	}
	merged := mergeVerification(results, attempt)
	run.Checkpoints = append(run.Checkpoints, checkpoint(run, domain.RunPhaseVerify, merged.Summary))
	s.maybeCompactRun(run)
	_ = s.saveRun(ctx, run)
	return merged, allEvents, nil
}

func (s *Service) runRecoverPhase(ctx context.Context, run *domain.RunState, request domain.TurnRequest, verification domain.VerificationResult, attempt int) (domain.AgentResult, []domain.ExecutionEvent, error) {
	run.CurrentPhase = domain.RunPhaseRecover
	run.Attempt = attempt
	_ = s.saveRun(ctx, run)

	coder, ok := s.catalog.Resolve("coder")
	if !ok {
		return domain.AgentResult{}, nil, fmt.Errorf("coder agent が見つかりません")
	}
	repairPrompt := strings.TrimSpace("Repair the implementation using this brief:\n" + verification.RepairBrief)
	result, err := s.runAgent(ctx, s.phaseInvocation(run, coder, request, domain.RunPhaseRecover, attempt, phaseMessages(run.Messages, repairPrompt), repairPrompt), 0)
	if err != nil {
		return domain.AgentResult{}, nil, err
	}
	run.Artifacts = append(run.Artifacts, newArtifact(run, domain.RunPhaseRecover, coder.ID, "Recovery result", "recovery", result.Message.Content))
	run.Checkpoints = append(run.Checkpoints, checkpoint(run, domain.RunPhaseRecover, result.Message.Content))
	run.Messages = append(run.Messages, domain.Message{Role: domain.RoleAssistant, AgentID: coder.ID, Content: result.Message.Content})
	s.maybeCompactRun(run)
	_ = s.saveRun(ctx, run)
	return result, result.Events, nil
}

func (s *Service) runFinalizePhase(ctx context.Context, run *domain.RunState, manager domain.AgentSpec, request domain.TurnRequest, execution domain.AgentResult) (domain.AgentResult, []domain.ExecutionEvent, error) {
	if len(run.Verification) == 0 {
		return execution, execution.Events, nil
	}
	run.CurrentPhase = domain.RunPhaseFinalize
	_ = s.saveRun(ctx, run)
	input := phaseMessages(run.Messages,
		"Execution summary:\n"+execution.Message.Content,
		"Verification summary:\n"+latestVerificationSummary(run),
	)
	result, err := s.runAgent(ctx, s.phaseInvocation(run, manager, request, domain.RunPhaseFinalize, run.Attempt, input, "Summarize the completed work, verification status, and remaining risks."), 0)
	if err != nil {
		return domain.AgentResult{}, nil, err
	}
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
		return domain.RunContext{
			UserGoal:           latestUserMessage(messages),
			CurrentPhase:       phase,
			TaskBrief:          latestUserMessage(messages),
			RecentMessages:     cloneMessages(messages),
			RelevantFiles:      extractRelevantFiles(messages),
			RecentSummary:      latestUserMessage(messages),
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
