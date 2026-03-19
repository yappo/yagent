package orchestrator

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"yagent/internal/domain"
)

const (
	workspaceFactRepoPathPrefix    = "repo_path:"
	workspaceFactChangedPathPrefix = "changed_path:"
)

func typedWorkspaceFacts(artifacts []domain.RunArtifact) []domain.WorkspaceFact {
	facts := make([]domain.WorkspaceFact, 0, len(artifacts))
	for _, artifact := range artifacts {
		switch artifact.Kind {
		case "repo_map":
			var payload domain.RepoMapArtifactPayload
			if err := json.Unmarshal(artifact.Payload, &payload); err != nil {
				continue
			}
			for _, entry := range payload.Entries {
				if strings.TrimSpace(entry.Path) == "" {
					continue
				}
				summary := "Relevant path: " + entry.Path
				if entry.Observation != "" {
					summary += " - " + truncateSummary(entry.Observation)
				}
				facts = append(facts, domain.WorkspaceFact{
					ID:         workspaceFactRepoPathPrefix + entry.Path,
					Kind:       "repo_path",
					Summary:    summary,
					ArtifactID: artifact.ID,
					UpdatedAt:  time.Now(),
				})
			}
		case "change_set":
			var payload domain.ChangeSetArtifactPayload
			if err := json.Unmarshal(artifact.Payload, &payload); err != nil {
				continue
			}
			for _, file := range payload.Files {
				if strings.TrimSpace(file.Path) == "" {
					continue
				}
				summary := "Recently mutated path: " + file.Path
				if file.ToolName != "" {
					summary += fmt.Sprintf(" via %s", file.ToolName)
				}
				facts = append(facts, domain.WorkspaceFact{
					ID:         workspaceFactChangedPathPrefix + file.Path,
					Kind:       "changed_path",
					Summary:    summary,
					ArtifactID: artifact.ID,
					UpdatedAt:  time.Now(),
				})
			}
		}
	}
	return facts
}

func knownFailuresFromArtifacts(artifacts []domain.RunArtifact) []string {
	failures := []string{}
	for _, artifact := range artifacts {
		switch artifact.Kind {
		case "review_findings":
			var payload domain.ReviewFindingsArtifactPayload
			if err := json.Unmarshal(artifact.Payload, &payload); err != nil {
				continue
			}
			if strings.EqualFold(payload.Result.Status, "fail") && payload.Result.Summary != "" {
				failures = appendUnique(failures, payload.Result.Summary)
			}
		case "test_report":
			var payload domain.TestReportArtifactPayload
			if err := json.Unmarshal(artifact.Payload, &payload); err != nil {
				continue
			}
			for _, entry := range payload.Entries {
				if strings.EqualFold(entry.Status, "fail") && entry.Summary != "" {
					failures = appendUnique(failures, entry.Summary)
				}
			}
		}
	}
	return failures
}

func workspaceFactPath(fact domain.WorkspaceFact) (string, bool) {
	switch {
	case strings.HasPrefix(fact.ID, workspaceFactRepoPathPrefix):
		return strings.TrimPrefix(fact.ID, workspaceFactRepoPathPrefix), true
	case strings.HasPrefix(fact.ID, workspaceFactChangedPathPrefix):
		return strings.TrimPrefix(fact.ID, workspaceFactChangedPathPrefix), true
	default:
		return "", false
	}
}
