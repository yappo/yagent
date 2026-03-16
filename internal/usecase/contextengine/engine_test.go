package contextengine

import (
	"context"
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
		Constraints: []string{"Keep README updated."},
		SuccessfulCommands: []domain.CommandMemoryEntry{{
			Summary: "go test ./...",
		}},
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
	if artifact.Kind != "context_summary" {
		t.Fatalf("unexpected artifact: %+v", artifact)
	}
	if run.ConversationSummary == "" {
		t.Fatalf("expected run summary to be updated")
	}
}
