package orchestrator

import (
	"encoding/json"
	"strings"

	"yagent/internal/domain"
)

func hydrateWorkUnit(run *domain.RunState, unit *domain.WorkUnit) {
	if run == nil || unit == nil {
		return
	}
	unit.ArtifactRefs = workUnitArtifactRefs(run, unit.Kind)
	unit.KnownFailureRefs = append([]string(nil), run.KnownFailures...)
	unit.ReadSet = workUnitReadSet(run, *unit)
	unit.WriteSet = workUnitWriteSet(run, *unit)
}

func workUnitArtifactRefs(run *domain.RunState, kind string) []domain.ArtifactReference {
	if run == nil {
		return nil
	}
	allowed := artifactKindsForWorkUnit(kind)
	filtered := make([]domain.RunArtifact, 0, len(run.Artifacts))
	for _, artifact := range run.Artifacts {
		if len(allowed) == 0 || allowed[artifact.Kind] {
			filtered = append(filtered, artifact)
		}
	}
	if len(filtered) == 0 {
		filtered = lastArtifacts(run.Artifacts, 8)
	}
	return recentArtifactReferences(lastArtifacts(filtered, 8), 8)
}

func artifactKindsForWorkUnit(kind string) map[string]bool {
	switch kind {
	case "preparation":
		return artifactKindSet("agent_inventory", "execution_plan", "repo_map", "evidence_bundle")
	case "primary":
		return artifactKindSet("execution_plan", "repo_map", "evidence_bundle", "review_findings", "test_report")
	case "verification":
		return artifactKindSet("execution", "change_set", "evidence_bundle", "review_findings", "test_report")
	case "recovery":
		return artifactKindSet("execution_plan", "change_set", "review_findings", "test_report")
	case "finalize":
		return artifactKindSet("execution_plan", "execution", "change_set", "review_findings", "test_report", "final_response")
	default:
		return nil
	}
}

func artifactKindSet(values ...string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func workUnitReadSet(run *domain.RunState, unit domain.WorkUnit) []string {
	switch unit.Kind {
	case "verification", "recovery", "finalize":
		paths := changedWorkspacePaths(run)
		if len(paths) > 0 {
			return paths
		}
	}
	return scopedWorkspacePaths(run)
}

func workUnitWriteSet(run *domain.RunState, unit domain.WorkUnit) []string {
	if unit.SideEffectClass != domain.SideEffectWorkspace {
		return nil
	}
	paths := changedWorkspacePaths(run)
	if len(paths) > 0 {
		return paths
	}
	paths = scopedWorkspacePaths(run)
	if len(paths) > 0 {
		return paths
	}
	return []string{"workspace"}
}

func scopedWorkspacePaths(run *domain.RunState) []string {
	if run == nil {
		return nil
	}
	paths := []string{}
	for _, path := range repoMapPaths(run) {
		paths = append(paths, path)
	}
	for _, path := range extractRelevantFiles(run.Messages) {
		paths = append(paths, normalizePathForWorkspace(path))
	}
	for _, artifact := range run.Artifacts {
		switch artifact.Kind {
		case "repo_map", "change_set":
			continue
		}
	}
	return compactPaths(paths)
}

func repoMapPaths(run *domain.RunState) []string {
	if run == nil {
		return nil
	}
	paths := []string{}
	for _, artifact := range run.Artifacts {
		if artifact.Kind != "repo_map" {
			continue
		}
		var payload domain.RepoMapArtifactPayload
		if err := json.Unmarshal(artifact.Payload, &payload); err != nil {
			continue
		}
		for _, entry := range payload.Entries {
			if strings.TrimSpace(entry.Path) == "" {
				continue
			}
			paths = append(paths, normalizePathForWorkspace(entry.Path))
		}
	}
	return compactPaths(paths)
}

func changedWorkspacePaths(run *domain.RunState) []string {
	if run == nil {
		return nil
	}
	paths := []string{}
	for _, artifact := range run.Artifacts {
		if artifact.Kind != "change_set" {
			continue
		}
		var payload domain.ChangeSetArtifactPayload
		if err := json.Unmarshal(artifact.Payload, &payload); err != nil {
			continue
		}
		for _, file := range payload.Files {
			if strings.TrimSpace(file.Path) == "" {
				continue
			}
			paths = append(paths, normalizePathForWorkspace(file.Path))
		}
	}
	return compactPaths(paths)
}
