package benchmark

import (
	"context"
	"testing"
	"time"

	"yagent/internal/config"
	"yagent/internal/domain"
)

type stubOrchestrator struct {
	result domain.TurnResult
}

func (s stubOrchestrator) RunTurn(context.Context, domain.TurnRequest) (domain.TurnResult, error) {
	return s.result, nil
}

func TestExecuteAppliesFeatureProfiles(t *testing.T) {
	base := config.Default()
	seen := []config.FeaturesConfig{}

	report, err := Execute(context.Background(), base, Request{
		Prompt: "benchmark this",
		Runs:   2,
		Profiles: []Profile{
			{
				Name:     "legacy",
				Features: config.FeaturesConfig{PhaseHarness: false, AdaptiveCompaction: false, RoleRouting: false, RepoMemory: false},
			},
			{
				Name:     "modern",
				Features: config.FeaturesConfig{PhaseHarness: true, AdaptiveCompaction: true, RoleRouting: true, RepoMemory: true},
			},
		},
	}, func(cfg config.Config) (domain.Orchestrator, func(), error) {
		seen = append(seen, cfg.Features)
		return stubOrchestrator{result: domain.TurnResult{
			Message: domain.Message{Role: domain.RoleAssistant, Content: "ok"},
			Run: &domain.RunState{
				Status:       domain.RunStatusCompleted,
				CurrentPhase: domain.RunPhaseFinalize,
				Attempt:      2,
				Plan:         []domain.PlanNode{{Title: "plan"}},
				Artifacts:    []domain.RunArtifact{{Name: "artifact"}},
				Verification: []domain.VerificationResult{{Status: "fail"}},
			},
			Events: []domain.ExecutionEvent{
				{Type: "agent_started", Status: "running"},
				{Type: "tool_called", Status: "running"},
			},
		}}, func() {}, nil
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if len(seen) != 4 {
		t.Fatalf("expected one build per run, got %d", len(seen))
	}
	if seen[0].PhaseHarness || seen[0].RoleRouting || seen[0].RepoMemory {
		t.Fatalf("expected legacy flags to be disabled, got %+v", seen[0])
	}
	if !seen[3].PhaseHarness || !seen[3].RoleRouting || !seen[3].RepoMemory {
		t.Fatalf("expected modern flags to be enabled, got %+v", seen[3])
	}
	if len(report.Results) != 2 {
		t.Fatalf("expected two profile results, got %+v", report.Results)
	}
	if report.Results[0].Summary.Successes != 2 {
		t.Fatalf("expected legacy success summary, got %+v", report.Results[0].Summary)
	}
	if report.Results[0].Summary.VerificationFailures != 2 {
		t.Fatalf("expected verification failures to aggregate, got %+v", report.Results[0].Summary)
	}
}

func TestRunReportFromTurnCollectsMetrics(t *testing.T) {
	now := time.Now()
	got := runReportFromTurn(1, 2*time.Second, domain.TurnResult{
		Message: domain.Message{Role: domain.RoleAssistant, Content: "done"},
		Events: []domain.ExecutionEvent{
			{Type: "agent_started", Status: "running", Timestamp: now},
			{Type: "tool_called", Status: "running", Timestamp: now},
			{Type: "tool_failed", Status: "failed", Timestamp: now},
		},
		Run: &domain.RunState{
			Status:       domain.RunStatusCompleted,
			CurrentPhase: domain.RunPhaseFinalize,
			Attempt:      3,
			Artifacts:    []domain.RunArtifact{{Name: "summary"}},
			Plan:         []domain.PlanNode{{Title: "Implement"}},
			Verification: []domain.VerificationResult{
				{Status: "pass"},
				{Status: "fail"},
			},
		},
	})

	if got.ToolCalls != 2 {
		t.Fatalf("expected tool calls, got %+v", got)
	}
	if got.FailedEvents != 1 {
		t.Fatalf("expected failed event count, got %+v", got)
	}
	if got.VerificationFailures != 1 {
		t.Fatalf("expected verification failures, got %+v", got)
	}
	if got.Attempt != 3 || got.PlanNodes != 1 {
		t.Fatalf("expected run details, got %+v", got)
	}
}
