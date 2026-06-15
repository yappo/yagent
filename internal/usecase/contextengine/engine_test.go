package contextengine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"yagent/internal/config"
	"yagent/internal/domain"
	"yagent/internal/infra/state"
)

type stubMemoryStore struct {
	memory *domain.RepoMemory
}

func (s stubMemoryStore) LoadMemory(context.Context) (*domain.RepoMemory, error) {
	return s.memory, nil
}

func (s stubMemoryStore) SaveMemory(context.Context, *domain.RepoMemory) error {
	return nil
}

func (s stubMemoryStore) RecordCommand(context.Context, domain.CommandMemoryEntry) error {
	return nil
}

func TestBuildIncludesPlanArtifactsAndMemory(t *testing.T) {
	engine := New(config.ContextConfig{
		MaxRecentMessages:        4,
		MaxArtifacts:             4,
		MaxRelevantFiles:         4,
		CompactAfterTurns:        10,
		CompactAfterToolCalls:    10,
		CompactAfterEstTokens:    1000,
		CompactAfterVerifyCycles: 2,
	}, stubMemoryStore{memory: &domain.RepoMemory{
		StableFacts: []domain.WorkspaceFact{{
			ID:      "fact-1",
			Summary: "Keep README updated.",
		}, {
			ID:      "fact-2",
			Summary: "go test ./...",
		}},
		KnownFailures: []string{"missing regression coverage"},
	}}, nil, 8)

	run := &domain.RunState{
		UserGoal: "Improve the coding agent.",
		Plan: []domain.PlanNode{
			{Title: "Implement harness", Status: "done"},
			{Title: "Add tests", Status: "pending"},
		},
		Artifacts: []domain.RunArtifact{{
			Name: "Execution result",
		}},
		Verification: []domain.VerificationResult{{
			Status:      "fail",
			Summary:     "reviewer: missing regression coverage",
			SourceAgent: "reviewer",
			CreatedAt:   time.Now(),
		}},
		EnabledCapabilities: []string{"patch"},
	}
	messages := []domain.Message{{Role: domain.RoleUser, Content: "Check README.md and internal/app/bootstrap.go"}}
	ctx := engine.Build(run, domain.AgentSpec{ID: "coder"}, domain.RunPhaseRecover, messages, []domain.ToolDefinition{{Name: "patch_apply"}})

	if ctx.UserGoal != run.UserGoal {
		t.Fatalf("unexpected user goal: %+v", ctx)
	}
	if len(ctx.ArtifactRefs) != 1 || ctx.ArtifactRefs[0] != "Execution result" {
		t.Fatalf("expected artifact refs, got %+v", ctx.ArtifactRefs)
	}
	if len(ctx.UnresolvedTODOs) != 1 || ctx.UnresolvedTODOs[0] != "Add tests" {
		t.Fatalf("expected unresolved TODOs, got %+v", ctx.UnresolvedTODOs)
	}
	if len(ctx.StableFacts) == 0 || len(ctx.RecentFailures) == 0 {
		t.Fatalf("expected memory and failures, got %+v", ctx)
	}
}

func TestMaybeCompactCreatesArtifactWhenThresholdExceeded(t *testing.T) {
	engine := New(config.ContextConfig{
		MaxRecentMessages:        4,
		MaxArtifacts:             4,
		MaxRelevantFiles:         4,
		CompactAfterTurns:        2,
		CompactAfterToolCalls:    10,
		CompactAfterEstTokens:    1000,
		CompactAfterVerifyCycles: 2,
	}, nil, nil, 8)
	run := &domain.RunState{
		CurrentPhase: domain.RunPhaseExecute,
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "one"},
			{Role: domain.RoleAssistant, Content: "two"},
		},
		Plan: []domain.PlanNode{{Title: "Task", Status: "pending"}},
	}

	artifact, ok := engine.MaybeCompact(run)
	if !ok || artifact == nil {
		t.Fatalf("expected compaction artifact")
	}
	if artifact.Kind != "packet_digest" {
		t.Fatalf("unexpected artifact: %+v", artifact)
	}
	if artifact.SchemaVersion != "packet_digest.v1" {
		t.Fatalf("expected schema version, got %+v", artifact)
	}
	var payload domain.PacketDigestArtifactPayload
	if err := json.Unmarshal(artifact.Payload, &payload); err != nil {
		t.Fatalf("expected typed payload: %v", err)
	}
}

func TestBuildScopesArtifactsAndObservationsByRole(t *testing.T) {
	engine := New(config.ContextConfig{
		MaxRecentMessages:        8,
		MaxArtifacts:             8,
		MaxRelevantFiles:         8,
		CompactAfterTurns:        99,
		CompactAfterToolCalls:    99,
		CompactAfterEstTokens:    99999,
		CompactAfterVerifyCycles: 99,
	}, stubMemoryStore{memory: &domain.RepoMemory{
		ReusableObservations: []domain.ObservationSummary{
			{ObservationID: "obs-1", ToolName: "fs_read", Summary: "read file"},
			{ObservationID: "obs-2", ToolName: "task_run", Summary: "ran tests"},
		},
	}}, nil, 8)

	run := &domain.RunState{
		UserGoal: "Refactor runtime.",
		Artifacts: []domain.RunArtifact{
			{ID: "a1", Name: "Execution plan", Kind: "execution_plan"},
			{ID: "a2", Name: "Evidence bundle", Kind: "evidence_bundle"},
			{ID: "a3", Name: "Final response", Kind: "final_response"},
		},
	}
	messages := []domain.Message{
		{Role: domain.RoleUser, Content: "please refactor"},
		{Role: domain.RoleAssistant, Content: "thinking"},
		{Role: domain.RoleUser, Content: "check internal/usecase/orchestrator"},
	}

	coder := engine.Build(run, domain.AgentSpec{ID: "coder"}, domain.RunPhaseExecute, messages, nil)
	if len(coder.Artifacts) == 0 || coder.Artifacts[len(coder.Artifacts)-1].Kind == "final_response" {
		t.Fatalf("coder packet should exclude final response artifacts: %+v", coder.Artifacts)
	}
	if len(coder.Observations) != 1 || coder.Observations[0].ToolName != "fs_read" {
		t.Fatalf("coder packet should keep file observations, got %+v", coder.Observations)
	}

	finalizer := engine.Build(run, domain.AgentSpec{ID: "manager"}, domain.RunPhaseFinalize, messages, nil)
	foundFinal := false
	for _, item := range finalizer.Artifacts {
		if item.Kind == "final_response" {
			foundFinal = true
			break
		}
	}
	if !foundFinal {
		t.Fatalf("finalizer packet should include final response artifacts: %+v", finalizer.Artifacts)
	}
}

func TestBuildRanksArtifactsAndObservationsByRelevance(t *testing.T) {
	now := time.Now()
	engine := New(config.ContextConfig{
		MaxRecentMessages:        8,
		MaxArtifacts:             8,
		MaxRelevantFiles:         8,
		CompactAfterTurns:        99,
		CompactAfterToolCalls:    99,
		CompactAfterEstTokens:    99999,
		CompactAfterVerifyCycles: 99,
	}, stubMemoryStore{memory: &domain.RepoMemory{
		ReusableObservations: []domain.ObservationSummary{
			{ObservationID: "obs-unrelated", ToolName: "fs_read", Summary: "package cache notes", UpdatedAt: now},
			{ObservationID: "obs-readme", ToolName: "fs_read", Summary: "README.md documents permission audit export", UpdatedAt: now.Add(-time.Hour)},
		},
	}}, nil, 8)

	run := &domain.RunState{
		UserGoal: "Update README.md for audit export.",
		Artifacts: []domain.RunArtifact{
			{ID: "a-unrelated", Name: "Cache notes", Kind: "evidence_bundle", Summary: "package cache", CreatedAt: now},
			{ID: "a-readme", Name: "README audit evidence", Kind: "evidence_bundle", Summary: "README.md audit export details", CreatedAt: now.Add(-time.Hour)},
		},
	}

	ctx := engine.Build(run, domain.AgentSpec{ID: "coder"}, domain.RunPhaseExecute, []domain.Message{{Role: domain.RoleUser, Content: "Please update README.md audit docs"}}, nil)
	if len(ctx.Artifacts) < 2 || ctx.Artifacts[0].ID != "a-readme" {
		t.Fatalf("expected README artifact first, got %+v", ctx.Artifacts)
	}
	if len(ctx.Observations) < 2 || ctx.Observations[0].ID != "obs-readme" {
		t.Fatalf("expected README observation first, got %+v", ctx.Observations)
	}
}

func TestBuildRanksObservationsByReadSetPathMatch(t *testing.T) {
	now := time.Now()
	engine := New(config.ContextConfig{
		MaxRecentMessages:        8,
		MaxArtifacts:             8,
		MaxRelevantFiles:         8,
		CompactAfterTurns:        99,
		CompactAfterToolCalls:    99,
		CompactAfterEstTokens:    99999,
		CompactAfterVerifyCycles: 99,
	}, stubMemoryStore{memory: &domain.RepoMemory{
		ReusableObservations: []domain.ObservationSummary{
			{ObservationID: "obs-same-dir", ToolName: "fs_read", Summary: "permission card model notes", ReadSet: []string{"internal/tui/permissions.go"}, UpdatedAt: now},
			{ObservationID: "obs-exact", ToolName: "fs_read", Summary: "older permission card notes", ReadSet: []string{"internal/tui/model.go"}, UpdatedAt: now.Add(-time.Hour)},
		},
	}}, nil, 8)
	run := &domain.RunState{UserGoal: "Fix permission card in internal/tui/model.go"}

	ctx := engine.Build(run, domain.AgentSpec{ID: "coder"}, domain.RunPhaseExecute, []domain.Message{{Role: domain.RoleUser, Content: "Please inspect internal/tui/model.go"}}, nil)
	if len(ctx.Observations) < 2 || ctx.Observations[0].ID != "obs-exact" {
		t.Fatalf("expected exact read-set path observation first, got %+v", ctx.Observations)
	}
}

func TestBuildRanksTesterTaskObservationsAheadOfEquivalentFileNotes(t *testing.T) {
	now := time.Now()
	engine := New(config.ContextConfig{
		MaxRecentMessages:        8,
		MaxArtifacts:             8,
		MaxRelevantFiles:         8,
		CompactAfterTurns:        99,
		CompactAfterToolCalls:    99,
		CompactAfterEstTokens:    99999,
		CompactAfterVerifyCycles: 99,
	}, stubMemoryStore{memory: &domain.RepoMemory{
		ReusableObservations: []domain.ObservationSummary{
			{ObservationID: "obs-file", ToolName: "fs_read", Summary: "verify regression", UpdatedAt: now},
			{ObservationID: "obs-task", ToolName: "task_run", Summary: "verify regression", UpdatedAt: now.Add(-time.Hour)},
		},
	}}, nil, 8)
	run := &domain.RunState{UserGoal: "Verify regression status"}

	ctx := engine.Build(run, domain.AgentSpec{ID: "tester"}, domain.RunPhaseVerify, []domain.Message{{Role: domain.RoleUser, Content: "verify regression"}}, nil)
	if len(ctx.Observations) < 2 || ctx.Observations[0].ID != "obs-task" {
		t.Fatalf("expected tester packet to prefer task observation, got %+v", ctx.Observations)
	}
}

func TestBuildUsesFreshRuntimeObservations(t *testing.T) {
	store, err := state.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore returned error: %v", err)
	}
	snapshot := &domain.WorkspaceSnapshot{
		Revision: 1,
		Paths: map[string]domain.WorkspacePathState{
			"README.md": {
				Path:          "README.md",
				Exists:        true,
				Size:          10,
				ModTimeUnix:   100,
				ContentSHA256: "fresh",
			},
			"old.txt": {
				Path:        "old.txt",
				Exists:      true,
				Size:        20,
				ModTimeUnix: 200,
			},
		},
		UpdatedAt: time.Now(),
	}
	if err := store.SaveWorkspaceSnapshot(context.Background(), snapshot); err != nil {
		t.Fatalf("SaveWorkspaceSnapshot returned error: %v", err)
	}
	if err := store.SaveObservation(context.Background(), domain.ObservationRecord{
		ID:         "obs-fresh",
		ToolName:   "fs_read",
		Summary:    "README.md fresh observation",
		ReadSet:    []string{"README.md"},
		PathStates: []domain.WorkspacePathState{snapshot.Paths["README.md"]},
		Reusable:   true,
	}); err != nil {
		t.Fatalf("SaveObservation fresh returned error: %v", err)
	}
	staleState := snapshot.Paths["old.txt"]
	staleState.Size = 999
	if err := store.SaveObservation(context.Background(), domain.ObservationRecord{
		ID:         "obs-stale",
		ToolName:   "fs_read",
		Summary:    "old.txt stale observation",
		ReadSet:    []string{"old.txt"},
		PathStates: []domain.WorkspacePathState{staleState},
		Reusable:   true,
	}); err != nil {
		t.Fatalf("SaveObservation stale returned error: %v", err)
	}
	engine := New(config.ContextConfig{
		MaxRecentMessages:        4,
		MaxArtifacts:             4,
		MaxRelevantFiles:         4,
		CompactAfterTurns:        99,
		CompactAfterToolCalls:    99,
		CompactAfterEstTokens:    99999,
		CompactAfterVerifyCycles: 99,
	}, store, store, 8)

	ctx := engine.Build(&domain.RunState{ID: "run-1", RootRunID: "run-1", UserGoal: "Update README.md"}, domain.AgentSpec{ID: "coder"}, domain.RunPhaseExecute, []domain.Message{{Role: domain.RoleUser, Content: "check README.md and old.txt"}}, nil)
	if len(ctx.Observations) != 1 || ctx.Observations[0].ID != "obs-fresh" {
		t.Fatalf("expected only fresh runtime observation, got %+v", ctx.Observations)
	}
	if len(ctx.Observations[0].ReadSet) != 1 || ctx.Observations[0].ReadSet[0] != "README.md" {
		t.Fatalf("expected read set on fresh observation, got %+v", ctx.Observations[0])
	}
}

func TestBuildAppliesAgentPacketBudget(t *testing.T) {
	now := time.Now()
	engine := New(config.ContextConfig{
		MaxRecentMessages:        8,
		MaxArtifacts:             8,
		MaxRelevantFiles:         8,
		CompactAfterTurns:        99,
		CompactAfterToolCalls:    99,
		CompactAfterEstTokens:    99999,
		CompactAfterVerifyCycles: 99,
	}, stubMemoryStore{memory: &domain.RepoMemory{
		StableFacts: []domain.WorkspaceFact{
			{ID: "fact-1", Summary: strings.Repeat("stable fact ", 20)},
			{ID: "fact-2", Summary: strings.Repeat("secondary fact ", 20)},
		},
		ReusableObservations: []domain.ObservationSummary{
			{ObservationID: "obs-1", ToolName: "fs_read", Summary: strings.Repeat("README observation ", 20), UpdatedAt: now},
			{ObservationID: "obs-2", ToolName: "fs_read", Summary: strings.Repeat("audit observation ", 20), UpdatedAt: now.Add(-time.Minute)},
		},
	}}, nil, 8)
	run := &domain.RunState{
		UserGoal: "Update README audit docs.",
		Artifacts: []domain.RunArtifact{
			{ID: "a1", Name: strings.Repeat("README audit artifact ", 15), Kind: "evidence_bundle", Summary: "README", CreatedAt: now},
			{ID: "a2", Name: strings.Repeat("secondary audit artifact ", 15), Kind: "evidence_bundle", Summary: "audit", CreatedAt: now.Add(-time.Minute)},
		},
	}
	messages := []domain.Message{
		{Role: domain.RoleUser, Content: strings.Repeat("message one README audit ", 40)},
		{Role: domain.RoleAssistant, Content: strings.Repeat("message two README audit ", 40)},
		{Role: domain.RoleUser, Content: strings.Repeat("message three README audit ", 40)},
	}

	ctx := engine.Build(run, domain.AgentSpec{ID: "coder", TokenBudget: 180}, domain.RunPhaseExecute, messages, nil)
	if ctx.PacketBudgetTokens != 180 {
		t.Fatalf("expected packet budget to be recorded, got %+v", ctx)
	}
	if ctx.PacketEstimatedTokens > ctx.PacketBudgetTokens {
		t.Fatalf("expected packet estimate <= budget, got estimate=%d budget=%d", ctx.PacketEstimatedTokens, ctx.PacketBudgetTokens)
	}
	if len(ctx.RecentMessages) >= len(messages) {
		t.Fatalf("expected recent messages to be trimmed by budget, got %d", len(ctx.RecentMessages))
	}
	if len(ctx.Artifacts) >= len(run.Artifacts) && len(ctx.Observations) >= 2 && len(ctx.StableFacts) >= 2 {
		t.Fatalf("expected optional context to be trimmed by budget, got artifacts=%+v observations=%+v facts=%+v", ctx.Artifacts, ctx.Observations, ctx.StableFacts)
	}
}

func TestMaybeCompactPayloadIncludesWorkUnits(t *testing.T) {
	engine := New(config.ContextConfig{
		MaxRecentMessages:        1,
		MaxArtifacts:             4,
		MaxRelevantFiles:         4,
		CompactAfterTurns:        1,
		CompactAfterToolCalls:    10,
		CompactAfterEstTokens:    1000,
		CompactAfterVerifyCycles: 2,
	}, nil, nil, 8)
	run := &domain.RunState{
		CurrentPhase: domain.RunPhaseExecute,
		Messages:     []domain.Message{{Role: domain.RoleUser, Content: "compact"}},
		WorkUnits: []domain.WorkUnit{{
			ID:     "execute:primary:coder",
			Kind:   "primary",
			Role:   "coder",
			Phase:  domain.RunPhaseExecute,
			Status: "done",
			Task:   "Implement runtime changes",
		}},
	}

	artifact, ok := engine.MaybeCompact(run)
	if !ok || artifact == nil {
		t.Fatalf("expected compaction artifact")
	}
	var payload domain.PacketDigestArtifactPayload
	if err := json.Unmarshal(artifact.Payload, &payload); err != nil {
		t.Fatalf("expected typed payload: %v", err)
	}
	if len(payload.WorkUnits) != 1 || payload.WorkUnits[0].ID != "execute:primary:coder" {
		t.Fatalf("expected work unit digest, got %+v", payload.WorkUnits)
	}
}

func TestBuildRecordsPacketScratch(t *testing.T) {
	store, err := state.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore returned error: %v", err)
	}
	engine := New(config.ContextConfig{
		MaxRecentMessages:        4,
		MaxArtifacts:             4,
		MaxRelevantFiles:         4,
		CompactAfterTurns:        99,
		CompactAfterToolCalls:    99,
		CompactAfterEstTokens:    99999,
		CompactAfterVerifyCycles: 99,
	}, nil, store, 8)

	run := &domain.RunState{
		ID:        "run-1",
		RootRunID: "run-1",
		UserGoal:  "Ship the refactor.",
		Artifacts: []domain.RunArtifact{{ID: "a1", Name: "Execution plan", Kind: "execution_plan"}},
	}
	ctx := engine.Build(run, domain.AgentSpec{ID: "coder"}, domain.RunPhaseExecute, []domain.Message{{Role: domain.RoleUser, Content: "update README.md"}}, nil)
	if ctx.PacketRole != "coder" {
		t.Fatalf("unexpected packet role: %+v", ctx)
	}

	items, err := store.ListScratch(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListScratch returned error: %v", err)
	}
	if len(items) == 0 || items[0].Kind != "agent_packet" {
		t.Fatalf("expected packet scratch record, got %+v", items)
	}
}
