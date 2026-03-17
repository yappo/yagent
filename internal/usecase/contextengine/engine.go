package contextengine

import (
	"context"
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
	ctx := domain.RunContext{
		CurrentPhase:       phase,
		AvailableToolNames: toolNames(tools),
		ExpectedOutput:     map[string]any{},
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
	ctx.RecentMessages = tailMessages(messages, e.config.MaxRecentMessages)
	ctx.RelevantFiles = extractRelevantFiles(messages, e.config.MaxRelevantFiles)
	ctx.ArtifactRefs = artifactRefs(run.Artifacts, e.config.MaxArtifacts)
	ctx.UnresolvedTODOs = unresolvedTODOs(run.Plan)
	ctx.RecentFailures = recentFailures(run.Verification)
	ctx.VerificationNotes = verificationNotes(run.Verification)
	ctx.RecentSummary = strings.TrimSpace(run.ConversationSummary)
	ctx.EnabledCapabilities = append([]string(nil), run.EnabledCapabilities...)
	ctx.ExpectedOutput["agent"] = agent.ID
	ctx.ExpectedOutput["phase"] = phase
	if e.memory != nil {
		if memory, err := e.memory.LoadMemory(context.Background()); err == nil && memory != nil {
			ctx.StableFacts = stableFacts(memory, e.maxFacts)
			ctx.Constraints = append(ctx.Constraints, memory.Constraints...)
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
	run.ConversationSummary = summary
	artifact := &domain.RunArtifact{
		ID:        fmt.Sprintf("compact-%d", len(run.Artifacts)+1),
		Name:      "Adaptive Context Summary",
		Kind:      "context_summary",
		Phase:     run.CurrentPhase,
		Summary:   summary,
		Content:   summary,
		CreatedAt: run.UpdatedAt,
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
		return "Summarize completed work, verification results, and any remaining risks."
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

func stableFacts(memory *domain.RepoMemory, maxFacts int) []string {
	if memory == nil {
		return nil
	}
	facts := append([]string(nil), memory.Constraints...)
	for _, item := range memory.SuccessfulCommands {
		if item.Summary == "" {
			continue
		}
		facts = append(facts, "Command: "+item.Summary)
	}
	if maxFacts > 0 && len(facts) > maxFacts {
		return facts[len(facts)-maxFacts:]
	}
	return facts
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
