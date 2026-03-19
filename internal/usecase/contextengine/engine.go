package contextengine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"yagent/internal/config"
	"yagent/internal/domain"
)

type Engine struct {
	config   config.ContextConfig
	memory   domain.RepoMemoryStore
	maxFacts int
}

func New(cfg config.ContextConfig, memory domain.RepoMemoryStore, maxFacts int) *Engine {
	return &Engine{
		config:   cfg,
		memory:   memory,
		maxFacts: maxFacts,
	}
}

func (e *Engine) Build(run *domain.RunState, agent domain.AgentSpec, phase domain.RunPhase, messages []domain.Message, tools []domain.ToolDefinition) domain.RunContext {
	role := packetRole(agent, phase)
	ctx := domain.RunContext{
		CurrentPhase:       phase,
		AvailableToolNames: toolNames(tools),
		ExpectedOutput:     map[string]any{},
		PacketRole:         role,
		PacketKind:         role,
	}
	if run == nil {
		ctx.UserGoal = latestUserMessage(messages)
		ctx.TaskBrief = latestUserMessage(messages)
		ctx.RecentMessages = tailMessages(messages, e.config.MaxRecentMessages)
		ctx.RelevantFiles = extractRelevantFiles(messages, e.config.MaxRelevantFiles)
		return ctx
	}

	ctx.UserGoal = run.UserGoal
	ctx.TaskBrief = phaseTaskBrief(run, phase)
	ctx.RecentMessages = roleScopedMessages(messages, role, e.config.MaxRecentMessages)
	ctx.RelevantFiles = extractRelevantFiles(messages, e.config.MaxRelevantFiles)
	relevantArtifacts := selectArtifactsForRole(run.Artifacts, role, e.config.MaxArtifacts)
	ctx.ArtifactRefs = artifactRefs(relevantArtifacts, e.config.MaxArtifacts)
	ctx.Artifacts = artifactReferences(relevantArtifacts, e.config.MaxArtifacts)
	ctx.UnresolvedTODOs = unresolvedTODOs(run.Plan)
	ctx.RecentFailures = recentFailures(run.Verification)
	ctx.VerificationNotes = verificationNotes(run.Verification)
	ctx.KnownFailures = append([]string(nil), run.KnownFailures...)
	ctx.KnownFailures = append(ctx.KnownFailures, ctx.RecentFailures...)
	ctx.EnabledCapabilities = append([]string(nil), run.EnabledCapabilities...)
	ctx.ExpectedOutput["agent"] = agent.ID
	ctx.ExpectedOutput["phase"] = phase
	if e.memory != nil {
		if memory, err := e.memory.LoadMemory(context.Background()); err == nil && memory != nil {
			ctx.StableFacts = stableFacts(memory, e.maxFacts)
			ctx.Observations = selectObservationsForRole(memory.ReusableObservations, role)
			ctx.KnownFailures = append(ctx.KnownFailures, memory.KnownFailures...)
		}
	}
	if phase == domain.RunPhaseRecover {
		ctx.ExpectedOutput["repair"] = "Provide a focused patch or exact remediation steps."
	}
	return ctx
}

func (e *Engine) MaybeCompact(run *domain.RunState) (*domain.RunArtifact, bool) {
	if run == nil {
		return nil, false
	}
	if !e.shouldCompact(run) {
		return nil, false
	}
	summary := compactSummary(run)
	payload, _ := json.Marshal(map[string]any{
		"latest_artifacts": artifactReferences(lastArtifacts(run.Artifacts, 4), 4),
		"known_failures":   run.KnownFailures,
		"work_units":       len(run.WorkUnits),
	})
	artifact := &domain.RunArtifact{
		ID:            fmt.Sprintf("compact-%d", len(run.Artifacts)+1),
		Name:          "Packet Digest",
		Kind:          "packet_digest",
		SchemaVersion: "packet_digest.v1",
		Phase:         run.CurrentPhase,
		Summary:       summary,
		Text:          summary,
		Content:       summary,
		Payload:       payload,
		CreatedAt:     run.UpdatedAt,
	}
	run.Artifacts = append(run.Artifacts, *artifact)
	return artifact, true
}

func (e *Engine) shouldCompact(run *domain.RunState) bool {
	if len(run.Messages) >= e.config.CompactAfterTurns {
		return true
	}
	if len(run.Artifacts) >= e.config.CompactAfterToolCalls {
		return true
	}
	if estimatedTokens(run.Messages) >= e.config.CompactAfterEstTokens {
		return true
	}
	failures := 0
	for _, item := range run.Verification {
		if strings.EqualFold(item.Status, "fail") {
			failures++
		}
	}
	return failures >= e.config.CompactAfterVerifyCycles
}

func tailMessages(messages []domain.Message, limit int) []domain.Message {
	if limit <= 0 || len(messages) <= limit {
		return cloneMessages(messages)
	}
	return cloneMessages(messages[len(messages)-limit:])
}

func cloneMessages(messages []domain.Message) []domain.Message {
	out := make([]domain.Message, 0, len(messages))
	for _, message := range messages {
		item := message
		item.ToolCalls = append([]domain.ToolCall(nil), message.ToolCalls...)
		if message.Metadata != nil {
			item.Metadata = map[string]string{}
			for key, value := range message.Metadata {
				item.Metadata[key] = value
			}
		}
		out = append(out, item)
	}
	return out
}

func latestUserMessage(messages []domain.Message) string {
	for idx := len(messages) - 1; idx >= 0; idx-- {
		if messages[idx].Role == domain.RoleUser {
			return messages[idx].Content
		}
	}
	return ""
}

func phaseTaskBrief(run *domain.RunState, phase domain.RunPhase) string {
	switch phase {
	case domain.RunPhasePlan:
		return "Create a concrete implementation plan for the user request."
	case domain.RunPhaseExecute:
		return "Execute the agreed plan and produce the requested change."
	case domain.RunPhaseVerify:
		return "Validate the implementation and identify any remaining gaps."
	case domain.RunPhaseRecover:
		return "Repair the implementation using the latest verification findings."
	case domain.RunPhaseFinalize:
		return "Summarize completed work, verification results, and remaining risks."
	default:
		return run.UserGoal
	}
}

func extractRelevantFiles(messages []domain.Message, limit int) []string {
	seen := map[string]struct{}{}
	files := make([]string, 0, limit)
	for _, message := range messages {
		for _, token := range strings.Fields(message.Content) {
			token = strings.Trim(token, "[](){}<>\"'`,.:;!?")
			if token == "" || strings.Contains(token, "://") {
				continue
			}
			if !strings.Contains(token, "/") && !strings.Contains(token, ".") {
				continue
			}
			if _, ok := seen[token]; ok {
				continue
			}
			seen[token] = struct{}{}
			files = append(files, token)
			if limit > 0 && len(files) >= limit {
				return files
			}
		}
	}
	return files
}

func artifactRefs(artifacts []domain.RunArtifact, limit int) []string {
	if limit <= 0 || len(artifacts) <= limit {
		return artifactNames(artifacts)
	}
	return artifactNames(artifacts[len(artifacts)-limit:])
}

func artifactNames(artifacts []domain.RunArtifact) []string {
	out := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		out = append(out, artifact.Name)
	}
	return out
}

func artifactReferences(artifacts []domain.RunArtifact, limit int) []domain.ArtifactReference {
	if limit > 0 && len(artifacts) > limit {
		artifacts = artifacts[len(artifacts)-limit:]
	}
	out := make([]domain.ArtifactReference, 0, len(artifacts))
	for _, artifact := range artifacts {
		out = append(out, domain.ArtifactReference{ID: artifact.ID, Kind: artifact.Kind, Name: artifact.Name})
	}
	return out
}

func unresolvedTODOs(plan []domain.PlanNode) []string {
	out := []string{}
	for _, node := range plan {
		if node.Status != "done" {
			out = append(out, node.Title)
		}
	}
	return out
}

func recentFailures(results []domain.VerificationResult) []string {
	out := []string{}
	for i := len(results) - 1; i >= 0 && len(out) < 4; i-- {
		if strings.EqualFold(results[i].Status, "fail") {
			out = append(out, results[i].Summary)
		}
	}
	return out
}

func verificationNotes(results []domain.VerificationResult) []string {
	out := []string{}
	for i := len(results) - 1; i >= 0 && len(out) < 4; i-- {
		out = append(out, strings.TrimSpace(results[i].SourceAgent+": "+results[i].Summary))
	}
	return out
}

func stableFacts(memory *domain.WorkspaceMemory, maxFacts int) []string {
	if memory == nil {
		return nil
	}
	facts := make([]string, 0, len(memory.StableFacts))
	for _, item := range memory.StableFacts {
		if item.Summary == "" {
			continue
		}
		facts = append(facts, item.Summary)
	}
	if maxFacts > 0 && len(facts) > maxFacts {
		return facts[len(facts)-maxFacts:]
	}
	return facts
}

func observationRecords(items []domain.ObservationSummary) []domain.ObservationRecord {
	out := make([]domain.ObservationRecord, 0, len(items))
	for _, item := range items {
		out = append(out, domain.ObservationRecord{
			ID:        item.ObservationID,
			ToolName:  item.ToolName,
			Summary:   item.Summary,
			Reusable:  true,
			UpdatedAt: item.UpdatedAt,
		})
	}
	return out
}

func compactSummary(run *domain.RunState) string {
	parts := []string{}
	if run.UserGoal != "" {
		parts = append(parts, "Goal: "+run.UserGoal)
	}
	if len(run.Plan) > 0 {
		items := []string{}
		for _, node := range run.Plan {
			items = append(items, node.Title+" ["+node.Status+"]")
		}
		parts = append(parts, "Plan: "+strings.Join(items, "; "))
	}
	if len(run.Artifacts) > 0 {
		items := []string{}
		for _, artifact := range lastArtifacts(run.Artifacts, 4) {
			items = append(items, artifact.Name)
		}
		parts = append(parts, "Artifacts: "+strings.Join(items, "; "))
	}
	if len(run.Verification) > 0 {
		last := run.Verification[len(run.Verification)-1]
		parts = append(parts, "Verification: "+last.SourceAgent+"="+last.Status+" "+last.Summary)
	}
	return strings.Join(parts, "\n")
}

func lastArtifacts(items []domain.RunArtifact, limit int) []domain.RunArtifact {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[len(items)-limit:]
}

func estimatedTokens(messages []domain.Message) int {
	total := 0
	for _, message := range messages {
		total += len(message.Content) / 4
	}
	return total
}

func toolNames(tools []domain.ToolDefinition) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

func packetRole(agent domain.AgentSpec, phase domain.RunPhase) string {
	switch {
	case agent.ID != "":
		return agent.ID
	case phase != "":
		return string(phase)
	default:
		return "runtime"
	}
}

func roleScopedMessages(messages []domain.Message, role string, limit int) []domain.Message {
	if role == "coder" || role == "tester" || role == "reviewer" {
		limit = minInt(limit, 4)
	}
	return tailMessages(messages, limit)
}

func selectArtifactsForRole(artifacts []domain.RunArtifact, role string, limit int) []domain.RunArtifact {
	if len(artifacts) == 0 {
		return nil
	}
	allowed := map[string]bool{}
	switch role {
	case "planner":
		allowed["agent_inventory"] = true
		allowed["repo_map"] = true
		allowed["evidence_bundle"] = true
	case "researcher":
		allowed["execution_plan"] = true
		allowed["repo_map"] = true
		allowed["evidence_bundle"] = true
	case "coder":
		allowed["execution_plan"] = true
		allowed["repo_map"] = true
		allowed["evidence_bundle"] = true
		allowed["review_findings"] = true
		allowed["test_report"] = true
		allowed["change_set"] = true
	case "tester":
		allowed["execution"] = true
		allowed["change_set"] = true
		allowed["evidence_bundle"] = true
		allowed["test_report"] = true
	case "reviewer":
		allowed["execution"] = true
		allowed["change_set"] = true
		allowed["evidence_bundle"] = true
		allowed["review_findings"] = true
	case "manager", "finalizer":
		allowed["execution_plan"] = true
		allowed["execution"] = true
		allowed["evidence_bundle"] = true
		allowed["test_report"] = true
		allowed["review_findings"] = true
		allowed["final_response"] = true
	default:
		return lastArtifacts(artifacts, limit)
	}

	filtered := make([]domain.RunArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		if allowed[artifact.Kind] {
			filtered = append(filtered, artifact)
		}
	}
	if len(filtered) == 0 {
		filtered = lastArtifacts(artifacts, limit)
	}
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}
	return filtered
}

func selectObservationsForRole(items []domain.ObservationSummary, role string) []domain.ObservationRecord {
	if len(items) == 0 {
		return nil
	}
	records := observationRecords(items)
	filtered := make([]domain.ObservationRecord, 0, len(records))
	for _, item := range records {
		switch role {
		case "coder":
			if strings.HasPrefix(item.ToolName, "fs_") || strings.HasPrefix(item.ToolName, "search_") || strings.HasPrefix(item.ToolName, "git_") {
				filtered = append(filtered, item)
			}
		case "tester", "reviewer":
			if strings.HasPrefix(item.ToolName, "task_") || strings.HasPrefix(item.ToolName, "git_") || strings.HasPrefix(item.ToolName, "fs_") {
				filtered = append(filtered, item)
			}
		default:
			filtered = append(filtered, item)
		}
	}
	if len(filtered) == 0 {
		filtered = records
	}
	if len(filtered) > 6 {
		filtered = filtered[len(filtered)-6:]
	}
	return filtered
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}
