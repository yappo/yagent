package contextengine

import (
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"yagent/internal/domain"
)

func relevanceQuery(ctx domain.RunContext, messages []domain.Message, role string) map[string]int {
	parts := []string{
		ctx.UserGoal,
		ctx.TaskBrief,
		role,
		strings.Join(ctx.RelevantFiles, " "),
		strings.Join(ctx.UnresolvedTODOs, " "),
		strings.Join(ctx.RecentFailures, " "),
		strings.Join(ctx.VerificationNotes, " "),
	}
	for _, message := range tailMessages(messages, 4) {
		parts = append(parts, message.Content)
	}
	query := weightedTokens(strings.Join(parts, " "))
	for _, token := range roleToolRelevanceTokens(role) {
		query[token] += 3
	}
	return query
}

func rankArtifacts(artifacts []domain.RunArtifact, query map[string]int) []domain.RunArtifact {
	if len(artifacts) == 0 || len(query) == 0 {
		return artifacts
	}
	ranked := append([]domain.RunArtifact(nil), artifacts...)
	sort.SliceStable(ranked, func(i, j int) bool {
		leftScore := scoreText(query, artifactSearchText(ranked[i]))
		rightScore := scoreText(query, artifactSearchText(ranked[j]))
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		return ranked[i].CreatedAt.After(ranked[j].CreatedAt)
	})
	return ranked
}

func rankObservations(observations []domain.ObservationRecord, query map[string]int) []domain.ObservationRecord {
	if len(observations) == 0 || len(query) == 0 {
		return observations
	}
	ranked := append([]domain.ObservationRecord(nil), observations...)
	sort.SliceStable(ranked, func(i, j int) bool {
		leftScore := scoreObservation(query, ranked[i])
		rightScore := scoreObservation(query, ranked[j])
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		return ranked[i].UpdatedAt.After(ranked[j].UpdatedAt)
	})
	return ranked
}

func roleToolRelevanceTokens(role string) []string {
	switch role {
	case "coder":
		return []string{"fs_read", "fs_write", "search_text", "search_files", "git_diff", "git_blame", "git_file_history"}
	case "tester":
		return []string{"task_run", "git_status", "git_diff"}
	case "reviewer":
		return []string{"git_diff", "git_status", "git_blame", "git_file_history", "fs_read", "task_run"}
	default:
		return nil
	}
}

func artifactSearchText(artifact domain.RunArtifact) string {
	return strings.Join([]string{
		artifact.Name,
		artifact.Kind,
		artifact.Summary,
		artifact.Text,
		artifact.Content,
	}, " ")
}

func observationSearchText(observation domain.ObservationRecord) string {
	return strings.Join([]string{
		observation.ID,
		observation.ToolName,
		observation.Summary,
		strings.Join(observation.ReadSet, " "),
	}, " ")
}

func scoreObservation(query map[string]int, observation domain.ObservationRecord) int {
	return scoreText(query, observationSearchText(observation)) + scoreReadSetPaths(query, observation.ReadSet)
}

func scoreReadSetPaths(query map[string]int, readSet []string) int {
	score := 0
	for token, weight := range query {
		if !looksLikePathToken(token) {
			continue
		}
		queryPath := normalizeRelevancePath(token)
		if queryPath == "" {
			continue
		}
		for _, item := range readSet {
			readPath := normalizeRelevancePath(item)
			if readPath == "" {
				continue
			}
			switch {
			case readPath == queryPath:
				score += 80 * weight
			case path.Base(readPath) == path.Base(queryPath):
				score += 40 * weight
			case path.Dir(readPath) == path.Dir(queryPath) && path.Dir(queryPath) != ".":
				score += 24 * weight
			case strings.HasPrefix(readPath, queryPath+"/") || strings.HasPrefix(queryPath, readPath+"/"):
				score += 16 * weight
			}
		}
	}
	return score
}

func looksLikePathToken(token string) bool {
	return strings.Contains(token, "/") || strings.Contains(token, ".")
}

func normalizeRelevancePath(value string) string {
	value = filepath.ToSlash(strings.TrimSpace(value))
	value = strings.Trim(value, "[](){}<>\"'`,.:;!?")
	value = strings.Trim(value, "/")
	if value == "" || strings.Contains(value, "://") {
		return ""
	}
	return path.Clean(value)
}

func scoreText(query map[string]int, text string) int {
	score := 0
	tokens := weightedTokens(text)
	for token, queryWeight := range query {
		if weight, ok := tokens[token]; ok {
			score += queryWeight * weight
		}
	}
	return score
}

func weightedTokens(text string) map[string]int {
	out := map[string]int{}
	for _, token := range tokenizeRelevance(text) {
		out[token]++
		base := filepath.Base(token)
		if base != token && base != "." && base != "/" {
			out[base] += 2
		}
		for _, part := range splitTokenParts(token) {
			out[part]++
		}
	}
	return out
}

func tokenizeRelevance(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '/' || r == '.')
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.Trim(field, "._-/")
		if len([]rune(field)) < 2 {
			continue
		}
		out = append(out, field)
	}
	return out
}

func splitTokenParts(token string) []string {
	parts := strings.FieldsFunc(token, func(r rune) bool {
		return r == '_' || r == '-' || r == '/' || r == '.'
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if len([]rune(part)) < 2 {
			continue
		}
		out = append(out, part)
	}
	return out
}
