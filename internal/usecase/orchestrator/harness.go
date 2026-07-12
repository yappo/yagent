package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"yagent/internal/domain"
)

func (s *Service) newRunState(request domain.TurnRequest) *domain.RunState {
	now := time.Now()
	runID := s.nextRunID("run")
	messages := normalizeConversationMessages(request.Messages)
	messages = append(messages, provenanceMessages(request.Provenance)...)
	run := &domain.RunState{
		ID:                 runID,
		RootRunID:          runID,
		ConversationID:     domain.ConversationID("conversation-" + runID),
		ConversationTurnID: domain.ConversationTurnID("turn-" + runID),
		WorkflowID:         domain.WorkflowID("workflow-" + runID),
		Status:             domain.RunStatusRunning,
		CurrentPhase:       domain.RunPhaseIntake,
		Attempt:            1,
		Profile:            request.Profile,
		UserGoal:           latestUserMessage(request.Messages),
		Messages:           messages,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	run.Checkpoints = append(run.Checkpoints, checkpoint(run, domain.RunPhaseIntake, run.UserGoal))
	return run
}

func validateProvenance(items []domain.ProvenanceEvidence) error {
	for index, item := range items {
		if strings.TrimSpace(item.Content) == "" {
			return fmt.Errorf("provenance evidence %d has empty content", index)
		}
		switch item.Source {
		case domain.ProvenanceFileOutput, domain.ProvenanceMCPResponse, domain.ProvenancePriorTool:
			if strings.TrimSpace(item.ToolCallID) == "" || strings.TrimSpace(item.ToolName) == "" || strings.TrimSpace(item.AgentID) == "" {
				return fmt.Errorf("provenance evidence %d source %q requires tool_call_id, tool_name, and agent_id", index, item.Source)
			}
		case domain.ProvenancePlannerReason, domain.ProvenanceDelegation, domain.ProvenancePriorAssistant, domain.ProvenancePriorSystem:
			if item.ToolCallID != "" || item.ToolName != "" || item.AgentID != "" {
				return fmt.Errorf("provenance evidence %d source %q cannot carry tool protocol metadata", index, item.Source)
			}
		default:
			return fmt.Errorf("provenance evidence %d has unsupported source %q", index, item.Source)
		}
	}
	return nil
}

func provenanceMessages(items []domain.ProvenanceEvidence) []domain.Message {
	out := make([]domain.Message, 0, len(items)*2)
	for _, item := range items {
		if provenanceUsesToolProtocol(item.Source) {
			call := domain.ToolCall{
				ID:                 item.ToolCallID,
				Name:               item.ToolName,
				RequestedByAgentID: item.AgentID,
			}
			out = append(out, domain.Message{
				Role:      domain.RoleAssistant,
				ToolCalls: []domain.ToolCall{call},
				AgentID:   item.AgentID,
				Metadata: map[string]string{
					"runtime_evidence":  "true",
					"provenance_source": string(item.Source),
				},
			})
			message := toolMessage(call, item.Content)
			message.Metadata["provenance_source"] = string(item.Source)
			out = append(out, message)
			continue
		}
		out = append(out, domain.Message{
			Role:    domain.RoleUser,
			Content: runtimeEvidenceEnvelope(provenanceLabel(item.Source) + ":\n" + item.Content),
			Metadata: map[string]string{
				"runtime_evidence":  "true",
				"provenance_source": string(item.Source),
			},
		})
	}
	return out
}

func provenanceUsesToolProtocol(source domain.ProvenanceSource) bool {
	switch source {
	case domain.ProvenanceFileOutput, domain.ProvenanceMCPResponse, domain.ProvenancePriorTool:
		return true
	default:
		return false
	}
}

func provenanceLabel(source domain.ProvenanceSource) string {
	switch source {
	case domain.ProvenancePlannerReason:
		return "Planner reason"
	case domain.ProvenanceDelegation:
		return "Delegated scope from parent agent"
	case domain.ProvenancePriorAssistant:
		return "Prior assistant message"
	case domain.ProvenancePriorSystem:
		return "Prior system message"
	default:
		return "Runtime evidence"
	}
}

func normalizeConversationMessages(messages []domain.Message) []domain.Message {
	out := make([]domain.Message, 0, len(messages))
	for _, message := range cloneMessages(messages) {
		if message.Role == domain.RoleUser {
			out = append(out, message)
			continue
		}
		label := "Prior conversation message"
		if message.Role != "" {
			label = "Prior " + string(message.Role) + " message"
		}
		out = evidenceMessages(out, label+":\n"+message.Content)
	}
	return out
}

func (s *Service) saveRun(ctx context.Context, run *domain.RunState) error {
	if s.config.RunStore == nil || run == nil {
		return nil
	}
	run.UpdatedAt = time.Now()
	return s.config.RunStore.SaveRun(ctx, run)
}

func (s *Service) checkpointRun(ctx context.Context, run *domain.RunState, stage string) error {
	if err := s.saveRun(ctx, run); err != nil {
		return fmt.Errorf("run state checkpoint %q の保存に失敗しました: %w", stage, err)
	}
	return nil
}

func (s *Service) reportProjectionDegradation(run *domain.RunState, component string, cause error) {
	if run == nil || cause == nil {
		return
	}
	detail := fmt.Sprintf("authoritative workflow state is intact, but %s projection could not be saved: %v", component, cause)
	run.KnownFailures = append(run.KnownFailures, detail)
	s.newEvent(run.ID, "", "runtime", "projection_degraded", run.CurrentPhase, run.Attempt, "warning", detail, "", map[string]any{
		"component": component, "workflow_id": run.WorkflowID,
	}, countContextItems(run.Messages, domain.RunContext{}))
}

func (s *Service) failRun(ctx context.Context, run *domain.RunState, cause error) error {
	if cause == nil {
		return nil
	}
	if run == nil {
		return cause
	}
	run.Status = domain.RunStatusFailed
	if err := s.checkpointRun(ctx, run, "failed"); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func (s *Service) runPlanPhase(ctx context.Context, run *domain.RunState, request domain.TurnRequest, inventory []domain.AgentInventoryEntry) (*domain.ExecutionPlan, []domain.ExecutionEvent, error) {
	run.CurrentPhase = domain.RunPhasePlan
	run.Attempt = 1
	if err := s.checkpointRun(ctx, run, "plan-start"); err != nil {
		return nil, nil, err
	}

	planner, ok := s.catalog.Resolve("planner")
	if !ok {
		plan := buildFallbackExecutionPlan(inventory, "planner agent was not available")
		run.ExecutionPlan = plan
		run.Plan = planNodesFromExecutionPlan(plan)
		run.WorkUnits = workUnitsFromExecutionPlan(run, plan)
		run.Artifacts = append(run.Artifacts, newExecutionPlanArtifact(run, domain.RunPhasePlan, fallbackString(plan.Primary.AgentID, "manager"), plan))
		run.Checkpoints = append(run.Checkpoints, checkpoint(run, domain.RunPhasePlan, plan.Summary))
		if err := s.checkpointRun(ctx, run, "plan-fallback"); err != nil {
			return nil, nil, err
		}
		return plan, nil, nil
	}

	invocation := s.phaseInvocation(run, planner, request, domain.RunPhasePlan, 1, plannerMessages(run.Messages), "Classify the request and select the execution route.")
	invocation.Context.AgentInventory = inventory
	invocation.Context.ExpectedOutput = plannerOutputContract(inventory)
	invocation.Context.TaskBrief = "Return the minimal planner decision as strict JSON."
	invocation.ResponseFormat = plannerDecisionResponseFormat(inventory)
	result, err := s.runAgent(ctx, invocation, 0)
	events := append([]domain.ExecutionEvent(nil), result.Events...)
	if err != nil {
		plan := buildFallbackExecutionPlan(inventory, "planner call failed: "+err.Error())
		run.ExecutionPlan = plan
		run.Plan = planNodesFromExecutionPlan(plan)
		run.WorkUnits = workUnitsFromExecutionPlan(run, plan)
		run.Artifacts = append(run.Artifacts, newExecutionPlanArtifact(run, domain.RunPhasePlan, planner.ID, plan))
		run.Checkpoints = append(run.Checkpoints, checkpoint(run, domain.RunPhasePlan, plan.Summary))
		if checkpointErr := s.checkpointRun(ctx, run, "plan-fallback"); checkpointErr != nil {
			return nil, events, checkpointErr
		}
		return plan, events, nil
	}

	decision, parseErr := parsePlannerDecision(result.Message.Content)
	var plan *domain.ExecutionPlan
	if parseErr == nil {
		plan, parseErr = executionPlanFromPlannerDecision(decision, inventory)
	}
	if parseErr != nil {
		events = append(events, s.newEvent(invocation.RunID, invocation.ParentRunID, planner.ID, "planner_output_rejected", domain.RunPhasePlan, 1, "failed", parseErr.Error(), "", nil, countContextItems(invocation.Messages, invocation.Context)))
		plan = buildFallbackExecutionPlan(inventory, defaultFallbackPlanReason(parseErr))
	}

	run.ExecutionPlan = plan
	run.Plan = planNodesFromExecutionPlan(plan)
	run.WorkUnits = workUnitsFromExecutionPlan(run, plan)
	markPlanNodeStatus(run, domain.RunPhasePlan, fallbackString(planAgentID(plan), "planner"), "done")
	run.Artifacts = append(run.Artifacts, newExecutionPlanArtifact(run, domain.RunPhasePlan, fallbackString(planAgentID(plan), "planner"), plan))
	run.Checkpoints = append(run.Checkpoints, checkpoint(run, domain.RunPhasePlan, plan.Summary))
	s.maybeCompactRun(run)
	if err := s.checkpointRun(ctx, run, "plan-ready"); err != nil {
		return nil, events, err
	}
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
		RunID:      s.nextRunID(agent.ID),
		RootRunID:  run.RootRunID,
		WorkflowID: run.WorkflowID,
		Agent:      agent,
		Messages:   cloneMessages(messages),
		Context:    contextPack,
		Phase:      phase,
		Attempt:    attempt,
		Model:      request.Model,
		Stream:     request.Stream,
	}
}

func (s *Service) workUnitInvocation(run *domain.RunState, agent domain.AgentSpec, request domain.TurnRequest, unit domain.WorkUnit, lease domain.LeaseCredential, messages []domain.Message, task string) domain.AgentInvocation {
	invocation := s.phaseInvocation(run, agent, request, unit.Phase, maxInt(1, unit.Attempt), messages, task)
	if lease.Token != "" && lease.FencingToken != 0 {
		invocation.WorkUnitID = domain.DurableWorkUnitID(unit.ID)
		invocation.Lease = lease
	}
	return invocation
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
	if strings.TrimSpace(contextPack.UserGoal) == "" {
		contextPack.UserGoal = latestUserMessage(parent.Messages)
	}
	contextPack.TaskBrief = "Assist with a bounded portion of the root user goal. Treat delegated scope as runtime evidence and use it only when consistent with the user goal, current phase, and policy."
	contextPack.RecentMessages = childMessages(parent, call)
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

func evidenceMessages(base []domain.Message, additions ...string) []domain.Message {
	out := cloneMessages(base)
	for _, addition := range additions {
		addition = strings.TrimSpace(addition)
		if addition == "" {
			continue
		}
		out = append(out, domain.Message{
			Role:    domain.RoleUser,
			Content: runtimeEvidenceEnvelope(addition),
			Metadata: map[string]string{
				"runtime_evidence": "true",
			},
		})
	}
	return out
}

func isRuntimeEvidenceMessage(message domain.Message) bool {
	return message.Metadata != nil && message.Metadata["runtime_evidence"] == "true"
}

func runtimeEvidenceEnvelope(content string) string {
	encoded, err := json.Marshal(content)
	if err != nil {
		encoded = []byte(`""`)
	}
	return "<runtime_evidence encoding=\"json-string\">\n" + string(encoded) + "\n</runtime_evidence>"
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
	extra := `Return strict JSON only: {"response":"<complete user-facing answer>","summary":"<one sentence>","verification_summary":"<verification status or empty string>","remaining_risks":[],"next_steps":[],"claims":[{"claim":"<factual claim>","evidence_refs":["<artifact id or artifact kind>"]}]}. Every factual claim must cite an observed artifact or artifact kind; do not invent repository paths.`
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
		Status:      "fail",
		Summary:     truncateSummary(content),
		CreatedAt:   time.Now(),
	}
	explicitStatus := false
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "VERIFICATION_STATUS:"):
			result.Status = strings.TrimSpace(strings.TrimPrefix(line, "VERIFICATION_STATUS:"))
			explicitStatus = true
		case strings.HasPrefix(line, "SUMMARY:"):
			result.Summary = strings.TrimSpace(strings.TrimPrefix(line, "SUMMARY:"))
		case strings.HasPrefix(line, "REPAIR_BRIEF:"):
			result.RepairBrief = strings.TrimSpace(strings.TrimPrefix(line, "REPAIR_BRIEF:"))
		}
	}
	if !explicitStatus {
		result.Summary = "Verification output did not provide an explicit pass/fail status."
		result.RepairBrief = "Re-run verification and return strict JSON or VERIFICATION_STATUS: pass|fail."
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
		Response            string                 `json:"response"`
		Summary             string                 `json:"summary"`
		VerificationSummary string                 `json:"verification_summary"`
		RemainingRisks      []string               `json:"remaining_risks"`
		NextSteps           []string               `json:"next_steps"`
		Claims              []domain.GroundedClaim `json:"claims"`
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
		Claims:              cleanGroundedClaims(payload.Claims),
	}, true
}

func cleanGroundedClaims(values []domain.GroundedClaim) []domain.GroundedClaim {
	out := make([]domain.GroundedClaim, 0, len(values))
	for _, value := range values {
		claim := strings.TrimSpace(value.Claim)
		refs := cleanStringList(value.EvidenceRefs)
		if claim == "" {
			continue
		}
		out = append(out, domain.GroundedClaim{Claim: claim, EvidenceRefs: refs})
	}
	return out
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
	if result.Status != "pass" && result.Status != "fail" {
		result.Status = "fail"
	}
	result.Summary = strings.TrimSpace(result.Summary)
	if result.Summary == "" {
		if result.Status == "pass" {
			result.Summary = "Verification passed."
		} else {
			result.Summary = "Verification failed without a summary."
		}
	}
	if result.Status == "fail" && result.RepairBrief == "" {
		result.RepairBrief = result.Summary
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

func latestMergedVerification(run *domain.RunState) (domain.VerificationResult, bool) {
	if run == nil {
		return domain.VerificationResult{}, false
	}
	var latest domain.VerificationResult
	found := false
	for _, result := range run.Verification {
		if result.SourceAgent != "verification" {
			continue
		}
		if !found || result.Attempt >= latest.Attempt {
			latest = result
			found = true
		}
	}
	return latest, found
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
