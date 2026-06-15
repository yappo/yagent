package contextengine

import (
	"fmt"
	"strings"

	"yagent/internal/domain"
)

func applyPacketBudget(ctx domain.RunContext, budget int) domain.RunContext {
	if budget <= 0 {
		ctx.PacketEstimatedTokens = estimateContextTokens(ctx)
		return ctx
	}

	ctx.PacketBudgetTokens = budget
	for estimateContextTokens(ctx) > budget {
		switch {
		case len(ctx.RecentMessages) > 0:
			ctx.RecentMessages = ctx.RecentMessages[1:]
		case len(ctx.Artifacts) > 0:
			ctx.Artifacts = ctx.Artifacts[:len(ctx.Artifacts)-1]
			ctx.ArtifactRefs = artifactReferenceNamesForBudget(ctx.Artifacts)
		case len(ctx.Observations) > 0:
			ctx.Observations = ctx.Observations[:len(ctx.Observations)-1]
		case len(ctx.StableFacts) > 0:
			ctx.StableFacts = ctx.StableFacts[:len(ctx.StableFacts)-1]
		case len(ctx.KnownFailures) > 0:
			ctx.KnownFailures = ctx.KnownFailures[:len(ctx.KnownFailures)-1]
		case len(ctx.VerificationNotes) > 0:
			ctx.VerificationNotes = ctx.VerificationNotes[:len(ctx.VerificationNotes)-1]
		case len(ctx.RelevantFiles) > 0:
			ctx.RelevantFiles = ctx.RelevantFiles[:len(ctx.RelevantFiles)-1]
		default:
			ctx.PacketEstimatedTokens = estimateContextTokens(ctx)
			return ctx
		}
	}
	ctx.PacketEstimatedTokens = estimateContextTokens(ctx)
	return ctx
}

func estimateContextTokens(ctx domain.RunContext) int {
	parts := []string{
		ctx.UserGoal,
		ctx.TaskBrief,
		ctx.PacketRole,
		ctx.PacketKind,
		strings.Join(ctx.ScopedConstraints, " "),
		strings.Join(ctx.KnownFailures, " "),
		strings.Join(ctx.RelevantFiles, " "),
		strings.Join(ctx.ArtifactRefs, " "),
		strings.Join(ctx.UnresolvedTODOs, " "),
		strings.Join(ctx.RecentFailures, " "),
		strings.Join(ctx.VerificationNotes, " "),
		strings.Join(ctx.StableFacts, " "),
		strings.Join(ctx.AvailableToolNames, " "),
		strings.Join(ctx.EnabledCapabilities, " "),
	}
	for _, message := range ctx.RecentMessages {
		parts = append(parts, message.Content)
	}
	for _, artifact := range ctx.Artifacts {
		parts = append(parts, artifact.ID, artifact.Kind, artifact.Name)
	}
	for _, observation := range ctx.Observations {
		parts = append(parts, observation.ID, observation.ToolName, observation.Summary, strings.Join(observation.ReadSet, " "))
	}
	for key, value := range ctx.ExpectedOutput {
		parts = append(parts, key, anyToBudgetText(value))
	}
	return estimateTextTokens(strings.Join(parts, " "))
}

func estimateTextTokens(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	tokens := len(text) / 4
	if tokens < 1 {
		return 1
	}
	return tokens
}

func artifactReferenceNamesForBudget(items []domain.ArtifactReference) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item.Name != "" {
			out = append(out, item.Name)
			continue
		}
		out = append(out, item.ID)
	}
	return out
}

func anyToBudgetText(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case []string:
		return strings.Join(typed, " ")
	default:
		return fmt.Sprint(value)
	}
}
