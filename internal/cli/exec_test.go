package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"yagent/internal/domain"
)

func TestExecCommandIncludesEventSummaryFlags(t *testing.T) {
	configPath := ""
	logPath := ""
	command := newExecCommand(&configPath, &logPath)
	for _, flag := range []string{"format", "show-events"} {
		if command.Flag(flag) == nil {
			t.Fatalf("expected %s flag", flag)
		}
	}
}

func TestRenderExecTextWithEventSummary(t *testing.T) {
	result := sampleExecTurnResult()
	text := renderExecText(result, true)
	for _, want := range []string{
		"final answer",
		"Execution summary",
		"run: run-1 status=completed phase=finalize attempt=2",
		"events: 5 agents=coder,planner phases=execute,plan failed=1",
		"tools: calls=3 failures=1 cache_hits=1 names=fs_read=2,fs_write=2",
		"models: calls=1 fallbacks=1 names=openai/gpt-5.5=1",
		"run_state: artifacts=1 work_units=1 plan_nodes=1 checkpoints=1 verification=1 mutations=1",
		"failures:",
		"coder tool_failed fs_write: denied",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in exec text, got:\n%s", want, text)
		}
	}
}

func TestExecOutputFromResultIsJSONReady(t *testing.T) {
	output := execOutputFromResult(sampleExecTurnResult())
	if output.Message != "final answer" {
		t.Fatalf("unexpected message: %+v", output)
	}
	if output.Run == nil || output.Run.ID != "run-1" || output.Run.Artifacts != 1 {
		t.Fatalf("unexpected run summary: %+v", output.Run)
	}
	if output.Summary.Events != 5 || output.Summary.ModelCalls != 1 || output.Summary.ModelFallbacks != 1 || output.Summary.ToolFailures != 1 || output.Summary.Mutations != 1 {
		t.Fatalf("unexpected summary: %+v", output.Summary)
	}
	if len(output.Events) != 5 || output.Events[4].Detail == "" || output.Events[4].Display != "fs_write: denied" {
		t.Fatalf("unexpected event records: %+v", output.Events)
	}
	data, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if !strings.Contains(string(data), `"model_fallbacks":1`) || !strings.Contains(string(data), `"openai/gpt-5.5"`) {
		t.Fatalf("expected model summary in json: %s", string(data))
	}
}

func sampleExecTurnResult() domain.TurnResult {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	return domain.TurnResult{
		Message: domain.Message{Role: domain.RoleAssistant, Content: "final answer\n"},
		Run: &domain.RunState{
			ID:           "run-1",
			RootRunID:    "run-1",
			Status:       domain.RunStatusCompleted,
			CurrentPhase: domain.RunPhaseFinalize,
			Attempt:      2,
			Artifacts:    []domain.RunArtifact{{ID: "artifact-1"}},
			WorkUnits:    []domain.WorkUnit{{ID: "unit-1"}},
			Plan:         []domain.PlanNode{{ID: "plan-1"}},
			Checkpoints:  []domain.RunCheckpoint{{ID: "checkpoint-1"}},
			Verification: []domain.VerificationResult{{Status: "pass"}},
		},
		Events: []domain.ExecutionEvent{
			{
				RunID:     "planner-1",
				AgentID:   "planner",
				Type:      "llm_called",
				Phase:     domain.RunPhasePlan,
				Attempt:   1,
				Status:    "running",
				Timestamp: now,
				Metrics: map[string]any{
					"server_name": "openai",
					"model":       "gpt-5.5",
					"fallback":    true,
				},
			},
			{
				RunID:     "coder-1",
				AgentID:   "coder",
				Type:      "tool_called",
				Phase:     domain.RunPhaseExecute,
				Attempt:   2,
				Status:    "done",
				Detail:    "fs_read",
				Timestamp: now.Add(time.Second),
			},
			{
				RunID:     "coder-1",
				AgentID:   "coder",
				Type:      "cache_hit",
				Phase:     domain.RunPhaseExecute,
				Attempt:   2,
				Status:    "done",
				Detail:    "fs_read",
				Timestamp: now.Add(2 * time.Second),
			},
			{
				RunID:     "coder-1",
				AgentID:   "coder",
				Type:      "tool_called",
				Phase:     domain.RunPhaseExecute,
				Attempt:   2,
				Status:    "done",
				Detail:    "fs_write",
				Timestamp: now.Add(3 * time.Second),
				Metrics: map[string]any{
					"mutation_id": "mut-1",
				},
			},
			{
				RunID:     "coder-1",
				AgentID:   "coder",
				Type:      "tool_failed",
				Phase:     domain.RunPhaseExecute,
				Attempt:   2,
				Status:    "failed",
				Detail:    "fs_write: denied\nfull stderr omitted from text summary",
				Display:   "fs_write: denied",
				Timestamp: now.Add(4 * time.Second),
			},
		},
	}
}
