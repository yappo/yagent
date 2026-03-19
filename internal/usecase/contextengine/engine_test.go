package contextengine

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"yagent/internal/config"
	"yagent/internal/domain"
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
	}}, 8)

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
	}, nil, 8)
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
	}}, 8)

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

func TestMaybeCompactPayloadIncludesWorkUnits(t *testing.T) {
	engine := New(config.ContextConfig{
		MaxRecentMessages:        1,
		MaxArtifacts:             4,
		MaxRelevantFiles:         4,
		CompactAfterTurns:        1,
		CompactAfterToolCalls:    10,
		CompactAfterEstTokens:    1000,
		CompactAfterVerifyCycles: 2,
	}, nil, 8)
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
