package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
		run.WorkUnits = workUnitsFromExecutionPlan(run, plan)
		run.Artifacts = append(run.Artifacts, newExecutionPlanArtifact(run, domain.RunPhasePlan, fallbackString(plan.Primary.AgentID, "manager"), plan))
		run.Checkpoints = append(run.Checkpoints, checkpoint(run, domain.RunPhasePlan, plan.Summary))
		_ = s.saveRun(ctx, run)
		return plan, nil, nil
	}

	invocation := s.phaseInvocation(run, planner, request, domain.RunPhasePlan, 1, plannerMessages(run.Messages, inventory), "Create the execution plan for this request.")
	invocation.Context.AgentInventory = inventory
	invocation.Context.ExpectedOutput = plannerOutputContract()
	invocation.Context.TaskBrief = "Create the execution plan for this request and return strict JSON only."
	invocation.ResponseFormat = executionPlanResponseFormat()
	result, err := s.runAgent(ctx, invocation, 0)
	events := append([]domain.ExecutionEvent(nil), result.Events...)
	if err != nil {
		plan := buildFallbackExecutionPlan(inventory, "planner call failed: "+err.Error())
		run.ExecutionPlan = plan
		run.Plan = planNodesFromExecutionPlan(plan)
		run.WorkUnits = workUnitsFromExecutionPlan(run, plan)
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
		repairInvocation.ResponseFormat = executionPlanResponseFormat()
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
	run.WorkUnits = workUnitsFromExecutionPlan(run, plan)
	markPlanNodeStatus(run, domain.RunPhasePlan, fallbackString(planAgentID(plan), "planner"), "done")
	run.Artifacts = append(run.Artifacts, newExecutionPlanArtifact(run, domain.RunPhasePlan, fallbackString(planAgentID(plan), "planner"), plan))
	run.Checkpoints = append(run.Checkpoints, checkpoint(run, domain.RunPhasePlan, plan.Summary))
	s.maybeCompactRun(run)
	_ = s.saveRun(ctx, run)
	return plan, events, nil
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
		Stream:    request.Stream,
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
	extra := `Return strict JSON only: {"status":"pass|fail","summary":"<one sentence>","repair_brief":"<short actionable brief>"}.`
	if strings.Contains(agent.Instruction, extra) {
		return agent
	}
	agent.Instruction = strings.TrimSpace(agent.Instruction + "\n\n" + extra)
	return agent
}

func withFinalResponseInstruction(agent domain.AgentSpec) domain.AgentSpec {
	extra := `Return strict JSON only: {"response":"<complete user-facing answer>","summary":"<one sentence>","verification_summary":"<verification status or empty string>","remaining_risks":[],"next_steps":[]}.`
	if strings.Contains(agent.Instruction, extra) {
		return agent
	}
	agent.Instruction = strings.TrimSpace(agent.Instruction + "\n\n" + extra)
	return agent
}

func parseVerification(content string, agentID string, attempt int) domain.VerificationResult {
	if result, ok := parseVerificationJSON(content, agentID, attempt); ok {
		return normalizeVerificationResult(result)
	}
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
	return normalizeVerificationResult(result)
}

func parseVerificationJSON(content string, agentID string, attempt int) (domain.VerificationResult, bool) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return domain.VerificationResult{}, false
	}
	var payload struct {
		Status      string `json:"status"`
		Summary     string `json:"summary"`
		RepairBrief string `json:"repair_brief"`
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return domain.VerificationResult{}, false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return domain.VerificationResult{}, false
	}
	return domain.VerificationResult{
		Attempt:     attempt,
		SourceAgent: agentID,
		Status:      payload.Status,
		Summary:     truncateSummary(payload.Summary),
		RepairBrief: strings.TrimSpace(payload.RepairBrief),
		CreatedAt:   time.Now(),
	}, true
}

const finalResponseRawJSONMetadataKey = "final_response_raw_json"

func normalizeFinalResponseMessage(message domain.Message) domain.Message {
	payload, ok := parseFinalResponseJSON(message.Content)
	if !ok || strings.TrimSpace(payload.Response) == "" {
		return message
	}
	if message.Metadata == nil {
		message.Metadata = map[string]string{}
	}
	message.Metadata[finalResponseRawJSONMetadataKey] = message.Content
	message.Content = strings.TrimSpace(payload.Response)
	return message
}

func parseFinalResponseJSON(content string) (domain.FinalResponseArtifactPayload, bool) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return domain.FinalResponseArtifactPayload{}, false
	}
	var payload struct {
		Response            string   `json:"response"`
		Summary             string   `json:"summary"`
		VerificationSummary string   `json:"verification_summary"`
		RemainingRisks      []string `json:"remaining_risks"`
		NextSteps           []string `json:"next_steps"`
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return domain.FinalResponseArtifactPayload{}, false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return domain.FinalResponseArtifactPayload{}, false
	}
	return domain.FinalResponseArtifactPayload{
		Response:            strings.TrimSpace(payload.Response),
		Summary:             strings.TrimSpace(payload.Summary),
		VerificationSummary: strings.TrimSpace(payload.VerificationSummary),
		RemainingRisks:      cleanStringList(payload.RemainingRisks),
		NextSteps:           cleanStringList(payload.NextSteps),
	}, true
}

func cleanStringList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func normalizeVerificationResult(result domain.VerificationResult) domain.VerificationResult {
	result.Status = strings.ToLower(strings.TrimSpace(result.Status))
	if result.Status != "fail" {
		result.Status = "pass"
	}
	result.Summary = strings.TrimSpace(result.Summary)
	if result.Summary == "" {
		result.Summary = "Verification completed."
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
	for _, failure := range knownFailuresFromArtifacts(lastArtifacts(run.Artifacts, 8)) {
		memory.KnownFailures = appendUnique(memory.KnownFailures, failure)
	}
	for _, fact := range typedWorkspaceFacts(lastArtifacts(run.Artifacts, 8)) {
		memory.StableFacts = appendOrReplaceWorkspaceFact(memory.StableFacts, fact)
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

func workUnitsFromExecutionPlan(run *domain.RunState, plan *domain.ExecutionPlan) []domain.WorkUnit {
	if plan == nil {
		return nil
	}
	artifactRefs := []domain.ArtifactReference{}
	knownFailures := []string{}
	if run != nil {
		artifactRefs = recentArtifactReferences(lastArtifacts(run.Artifacts, 8), 8)
		knownFailures = append([]string(nil), run.KnownFailures...)
	}
	units := make([]domain.WorkUnit, 0, len(plan.Preparation)+len(plan.Verify)+2)
	prepIDs := make([]string, 0, len(plan.Preparation))
	for _, item := range plan.Preparation {
		id := "execute:prep:" + item.AgentID
		prepIDs = append(prepIDs, id)
		unit := domain.WorkUnit{
			ID:      id,
			Kind:    "preparation",
			Role:    item.AgentID,
			Phase:   domain.RunPhaseExecute,
			Attempt: 1,
			Task:    item.Reason,
			Status:  "pending",
			Source:  item.AgentID,
		}
		unit.ArtifactRefs = append([]domain.ArtifactReference(nil), artifactRefs...)
		unit.KnownFailureRefs = append([]string(nil), knownFailures...)
		hydrateWorkUnit(run, &unit)
		units = append(units, unit)
	}
	primaryID := "execute:primary:" + plan.Primary.AgentID
	primaryUnit := domain.WorkUnit{
		ID:               primaryID,
		Kind:             "primary",
		Role:             plan.Primary.AgentID,
		Phase:            domain.RunPhaseExecute,
		Attempt:          1,
		Task:             plan.Primary.Reason,
		Status:           "pending",
		DependsOn:        append([]string(nil), prepIDs...),
		Source:           plan.Primary.AgentID,
		SideEffectClass:  workUnitSideEffect(plan.TaskKind, "primary"),
		ArtifactRefs:     append([]domain.ArtifactReference(nil), artifactRefs...),
		KnownFailureRefs: append([]string(nil), knownFailures...),
	}
	hydrateWorkUnit(run, &primaryUnit)
	units = append(units, primaryUnit)
	for _, item := range plan.Verify {
		id := verifyUnitID(item.AgentID, 1)
		unit := domain.WorkUnit{
			ID:               id,
			Kind:             "verification",
			Role:             item.AgentID,
			Phase:            domain.RunPhaseVerify,
			Attempt:          1,
			Task:             item.Reason,
			Status:           "pending",
			DependsOn:        []string{primaryID},
			Source:           item.AgentID,
			ArtifactRefs:     append([]domain.ArtifactReference(nil), artifactRefs...),
			KnownFailureRefs: append([]string(nil), knownFailures...),
		}
		hydrateWorkUnit(run, &unit)
		units = append(units, unit)
	}
	if len(plan.Verify) == 0 && plan.Finalize != nil && plan.Finalize.AgentID != "" {
		unit := domain.WorkUnit{
			ID:               finalizeUnitID(plan, 1),
			Kind:             "finalize",
			Role:             plan.Finalize.AgentID,
			Phase:            domain.RunPhaseFinalize,
			Attempt:          1,
			Task:             plan.Finalize.Reason,
			Status:           "pending",
			DependsOn:        []string{primaryID},
			Source:           plan.Finalize.AgentID,
			ArtifactRefs:     append([]domain.ArtifactReference(nil), artifactRefs...),
			KnownFailureRefs: append([]string(nil), knownFailures...),
		}
		hydrateWorkUnit(run, &unit)
		units = append(units, unit)
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
