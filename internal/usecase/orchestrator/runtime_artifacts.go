package orchestrator

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"yagent/internal/domain"
)

func (s *Service) ensureRepoMapArtifact(ctx context.Context, run *domain.RunState, phase domain.RunPhase, agentID string) {
	if run == nil {
		return
	}
	entries := s.buildRepoMapEntries(ctx, run)
	if len(entries) == 0 {
		return
	}
	run.Artifacts = append(run.Artifacts, newRepoMapArtifact(run, phase, agentID, entries))
}

func (s *Service) buildRepoMapEntries(ctx context.Context, run *domain.RunState) []domain.RepoMapEntry {
	entries := []domain.RepoMapEntry{}
	seen := map[string]struct{}{}

	add := func(path string, kind string, source string, observation string, tags ...string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		path = filepath.Clean(path)
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		entries = append(entries, domain.RepoMapEntry{
			Path:        path,
			Kind:        kind,
			Source:      source,
			Observation: observation,
			Tags:        compactStrings(tags),
		})
	}

	for _, path := range extractRelevantFiles(run.Messages) {
		add(path, repoMapKind(path), "messages", "mentioned in recent request")
	}

	if s.config.RuntimeStore != nil {
		if observations, err := s.config.RuntimeStore.ListObservations(ctx, 24); err == nil {
			for _, observation := range observations {
				for _, path := range observation.ReadSet {
					add(path, repoMapKind(path), "observation:"+observation.ToolName, observation.Summary)
				}
			}
		}
	}

	if s.config.MemoryStore != nil {
		if memory, err := s.config.MemoryStore.LoadMemory(ctx); err == nil && memory != nil {
			for _, fact := range memory.StableFacts {
				if path, ok := workspaceFactPath(fact); ok {
					add(path, repoMapKind(path), "memory", fact.Summary, fact.Kind)
				}
			}
		}
	}

	for _, artifact := range run.Artifacts {
		if artifact.Kind != "change_set" {
			continue
		}
		var payload domain.ChangeSetArtifactPayload
		if err := json.Unmarshal(artifact.Payload, &payload); err != nil {
			continue
		}
		for _, file := range payload.Files {
			add(file.Path, repoMapKind(file.Path), "change_set", "recently mutated path", file.Operation)
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})
	if len(entries) > 16 {
		entries = entries[:16]
	}
	return entries
}

func repoMapKind(path string) string {
	switch {
	case strings.HasSuffix(path, ".go"), strings.HasSuffix(path, ".md"), strings.HasSuffix(path, ".toml"), strings.HasSuffix(path, ".json"), strings.HasSuffix(path, ".yaml"), strings.HasSuffix(path, ".yml"):
		return "file"
	default:
		return "path"
	}
}

func (s *Service) buildChangeSetArtifact(ctx context.Context, run *domain.RunState, phase domain.RunPhase, agentID string, since time.Time) domain.RunArtifact {
	if run == nil || s.config.RuntimeStore == nil {
		return domain.RunArtifact{}
	}
	mutations, err := s.config.RuntimeStore.ListMutations(ctx, 128)
	if err != nil {
		return domain.RunArtifact{}
	}
	executions, err := s.config.RuntimeStore.ListExecutions(ctx, 256)
	if err != nil {
		return domain.RunArtifact{}
	}
	snapshot, _ := s.config.RuntimeStore.LoadWorkspaceSnapshot(ctx)

	files := []domain.ChangeSetFile{}
	seen := map[string]struct{}{}
	mutationRefs := []string{}
	executionRefs := []string{}
	artifactRefs := []domain.ArtifactReference{}
	for _, mutation := range mutations {
		if mutation.SessionID != run.RootRunID || mutation.AgentID != agentID {
			continue
		}
		if !since.IsZero() && mutation.CreatedAt.Before(since) {
			continue
		}
		mutationRefs = append(mutationRefs, mutation.ID)
		for _, path := range mutation.WriteSet {
			key := mutation.ID + ":" + path
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			file := domain.ChangeSetFile{
				Path:                path,
				Operation:           "write",
				ToolName:            mutation.ToolName,
				MutationFingerprint: mutation.MutationFingerprint,
				MutationID:          mutation.ID,
				ExecutionID:         mutation.ExecutionID,
			}
			if mutation.ExecutionID != "" {
				executionRefs = appendUnique(executionRefs, mutation.ExecutionID)
			}
			for _, execution := range executions {
				if execution.ID != mutation.ExecutionID || execution.OutputArtifactID == "" {
					continue
				}
				ref := domain.ArtifactReference{ID: execution.OutputArtifactID, Kind: "tool_output", Name: "Tool output " + execution.ToolName}
				file.Artifacts = append(file.Artifacts, ref)
				artifactRefs = appendArtifactRef(artifactRefs, ref)
				break
			}
			files = append(files, file)
		}
	}
	if len(files) == 0 {
		return domain.RunArtifact{}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return newChangeSetArtifact(run, phase, agentID, domain.ChangeSetArtifactPayload{
		AgentID:          agentID,
		SessionID:        run.RootRunID,
		Phase:            phase,
		Files:            files,
		MutationRefs:     mutationRefs,
		ExecutionRefs:    executionRefs,
		SourceArtifacts:  artifactRefs,
		WorkspaceVersion: workspaceRevision(snapshot),
	})
}

func (s *Service) buildPermissionAuditArtifact(ctx context.Context, run *domain.RunState, phase domain.RunPhase) domain.RunArtifact {
	if run == nil || s.config.RuntimeStore == nil {
		return domain.RunArtifact{}
	}
	items, err := s.config.RuntimeStore.ListScratch(ctx, 512)
	if err != nil {
		return domain.RunArtifact{}
	}
	sessionID := fallbackString(run.RootRunID, run.ID)
	records := make([]domain.PermissionDecisionRecord, 0)
	for _, item := range items {
		if item.Kind != "permission_decision" || item.SessionID != sessionID {
			continue
		}
		var record domain.PermissionDecisionRecord
		if err := json.Unmarshal(item.Payload, &record); err != nil {
			continue
		}
		records = append(records, record)
	}
	if len(records) == 0 {
		return domain.RunArtifact{}
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].CreatedAt.Before(records[j].CreatedAt)
	})
	return newPermissionAuditArtifact(run, phase, domain.PermissionAuditArtifactPayload{
		SessionID: sessionID,
		Records:   records,
	})
}

func (s *Service) appendPermissionAuditArtifact(ctx context.Context, run *domain.RunState, phase domain.RunPhase) {
	if artifact := s.buildPermissionAuditArtifact(ctx, run, phase); artifact.ID != "" {
		run.Artifacts = append(run.Artifacts, artifact)
	}
}

func (s *Service) buildTestReportArtifact(run *domain.RunState, phase domain.RunPhase, attempt int, status string, results []domain.VerificationResult) domain.RunArtifact {
	if run == nil || len(results) == 0 {
		return domain.RunArtifact{}
	}
	entries := make([]domain.TestReportEntry, 0, len(results))
	for _, result := range results {
		if result.Attempt != attempt {
			continue
		}
		if result.SourceAgent == "verification" {
			continue
		}
		entries = append(entries, domain.TestReportEntry{
			AgentID:     result.SourceAgent,
			Status:      result.Status,
			Summary:     result.Summary,
			RepairBrief: result.RepairBrief,
			ArtifactID:  result.ArtifactID,
		})
	}
	if len(entries) == 0 {
		return domain.RunArtifact{}
	}
	return newTestReportArtifact(run, phase, "verification", domain.TestReportArtifactPayload{
		Attempt:   attempt,
		Status:    status,
		Entries:   entries,
		KnownFail: append([]string(nil), run.KnownFailures...),
	})
}

func workspaceRevision(snapshot *domain.WorkspaceSnapshot) int64 {
	if snapshot == nil {
		return 0
	}
	return snapshot.Revision
}

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
