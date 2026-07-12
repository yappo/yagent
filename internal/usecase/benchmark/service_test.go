package benchmark

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func (s stubOrchestrator) ContinueConversation(context.Context, domain.ConversationTurnRequest) (domain.TurnResult, error) {
	return s.result, nil
}

func (s stubOrchestrator) RecoverWorkflow(context.Context, domain.WorkflowRecoveryRequest) (domain.TurnResult, error) {
	return s.result, nil
}

type capturingOrchestrator struct {
	requests *[]domain.TurnRequest
}

func (s capturingOrchestrator) RunTurn(_ context.Context, request domain.TurnRequest) (domain.TurnResult, error) {
	*s.requests = append(*s.requests, request)
	return domain.TurnResult{
		Message: domain.Message{Role: domain.RoleAssistant, Content: "safe summary"},
		Run:     &domain.RunState{Status: domain.RunStatusCompleted, CurrentPhase: domain.RunPhaseFinalize},
	}, nil
}

func (s capturingOrchestrator) ContinueConversation(context.Context, domain.ConversationTurnRequest) (domain.TurnResult, error) {
	return domain.TurnResult{}, nil
}

func (s capturingOrchestrator) RecoverWorkflow(context.Context, domain.WorkflowRecoveryRequest) (domain.TurnResult, error) {
	return domain.TurnResult{}, nil
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
	}, func(cfg config.Config, _ CellEnvironment) (domain.Orchestrator, func(), error) {
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
	if report.Results[0].Summary.EvaluationPasses != 2 {
		t.Fatalf("expected prompt case evaluation passes, got %+v", report.Results[0].Summary)
	}
}

func TestExecuteIsolatesRuntimeStateForEveryBenchmarkCell(t *testing.T) {
	base := config.Default()
	base.Memory.StateDir = t.TempDir()
	seenStateDirs := []string{}
	sharedCache := map[string]bool{}

	report, err := Execute(context.Background(), base, Request{
		Prompt: "read the repository",
		Runs:   2,
		Profiles: []Profile{
			{Name: "legacy", Features: config.FeaturesConfig{}},
			{Name: "current", Features: config.Default().Features},
		},
	}, func(cfg config.Config, _ CellEnvironment) (domain.Orchestrator, func(), error) {
		stateDir := cfg.Memory.StateDir
		seenStateDirs = append(seenStateDirs, stateDir)
		if stateDir == base.Memory.StateDir {
			t.Fatalf("benchmark runner received persistent runtime state root %q", stateDir)
		}
		if _, err := os.Stat(stateDir); err != nil {
			t.Fatalf("benchmark runtime state root is unavailable: %v", err)
		}
		return cacheAwareOrchestrator{stateDir: stateDir, cache: sharedCache}, nil, nil
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if len(seenStateDirs) != 4 {
		t.Fatalf("runner builds = %d, want 4", len(seenStateDirs))
	}
	for index, stateDir := range seenStateDirs {
		for prior := 0; prior < index; prior++ {
			if stateDir == seenStateDirs[prior] {
				t.Fatalf("benchmark cells %d and %d shared runtime state root %q", prior, index, stateDir)
			}
		}
		if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
			t.Fatalf("benchmark runtime state root %q was not removed: %v", stateDir, err)
		}
	}
	for _, result := range report.Results {
		for _, run := range result.Runs {
			if run.ToolCalls != 1 {
				t.Fatalf("profile %q run %d reused another benchmark cell's tool result: %+v", result.Profile.Name, run.Index, run)
			}
		}
	}
}

func TestExecuteIsolatesWorkspaceForEveryBenchmarkCell(t *testing.T) {
	source := t.TempDir()
	tracked := filepath.Join(source, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := config.Default()
	base.File.AllowPaths = []string{source}
	seenWorkspaces := []string{}
	seenContents := []string{}

	_, err := Execute(context.Background(), base, Request{
		Prompt:        "mutate tracked.txt",
		WorkspaceRoot: source,
		Runs:          1,
		Profiles: []Profile{
			{Name: "legacy", Features: config.FeaturesConfig{}},
			{Name: "current", Features: config.Default().Features},
		},
	}, func(cfg config.Config, environment CellEnvironment) (domain.Orchestrator, func(), error) {
		if len(cfg.File.AllowPaths) != 0 {
			t.Fatalf("isolated benchmark retained external allow paths: %+v", cfg.File.AllowPaths)
		}
		if environment.WorkspaceDir == source || environment.WorkspaceDir == "" {
			t.Fatalf("invalid isolated workspace %q", environment.WorkspaceDir)
		}
		data, err := os.ReadFile(filepath.Join(environment.WorkspaceDir, "tracked.txt"))
		if err != nil {
			t.Fatal(err)
		}
		seenWorkspaces = append(seenWorkspaces, environment.WorkspaceDir)
		seenContents = append(seenContents, string(data))
		if err := os.WriteFile(filepath.Join(environment.WorkspaceDir, "tracked.txt"), []byte(environment.WorkspaceDir), 0o644); err != nil {
			t.Fatal(err)
		}
		return stubOrchestrator{result: domain.TurnResult{
			Message: domain.Message{Role: domain.RoleAssistant, Content: "mutated isolated workspace"},
			Run:     &domain.RunState{Status: domain.RunStatusCompleted, CurrentPhase: domain.RunPhaseFinalize},
			Events:  []domain.ExecutionEvent{{Type: "tool_called", Status: "done", Detail: "fs_write"}},
		}}, nil, nil
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if len(seenWorkspaces) != 2 || seenWorkspaces[0] == seenWorkspaces[1] {
		t.Fatalf("evaluation cells did not receive unique workspaces: %+v", seenWorkspaces)
	}
	if seenContents[0] != "original" || seenContents[1] != "original" {
		t.Fatalf("workspace mutation leaked into a later benchmark cell: %+v", seenContents)
	}
	data, err := os.ReadFile(tracked)
	if err != nil || string(data) != "original" {
		t.Fatalf("source workspace changed: data=%q err=%v", data, err)
	}
	for _, workspace := range seenWorkspaces {
		if _, statErr := os.Stat(workspace); !os.IsNotExist(statErr) {
			t.Fatalf("isolated workspace %q was not removed: %v", workspace, statErr)
		}
	}
}

func TestRunIsolatedBenchmarkTurnCleansFactoryResourcesOnError(t *testing.T) {
	closed := false
	stateDir := ""
	_, _, err := runIsolatedBenchmarkTurn(context.Background(), config.Default(), "", func(cfg config.Config, _ CellEnvironment) (domain.Orchestrator, func(), error) {
		stateDir = cfg.Memory.StateDir
		return nil, func() { closed = true }, fmt.Errorf("factory failed")
	}, domain.TurnRequest{})
	if err == nil || !strings.Contains(err.Error(), "factory failed") {
		t.Fatalf("expected factory error, got %v", err)
	}
	if !closed {
		t.Fatal("factory closer was not called")
	}
	if _, statErr := os.Stat(stateDir); !os.IsNotExist(statErr) {
		t.Fatalf("temporary state root was not removed: %v", statErr)
	}
}

type cacheAwareOrchestrator struct {
	stateDir string
	cache    map[string]bool
}

func (s cacheAwareOrchestrator) RunTurn(context.Context, domain.TurnRequest) (domain.TurnResult, error) {
	if s.cache[s.stateDir] {
		return domain.TurnResult{
			Message: domain.Message{Role: domain.RoleAssistant, Content: "cached"},
			Run:     &domain.RunState{Status: domain.RunStatusCompleted, CurrentPhase: domain.RunPhaseFinalize},
			Events:  []domain.ExecutionEvent{{Type: "cache_hit", Status: "completed", Detail: "fs_read"}},
		}, nil
	}
	s.cache[s.stateDir] = true
	return domain.TurnResult{
		Message: domain.Message{Role: domain.RoleAssistant, Content: "fresh"},
		Run:     &domain.RunState{Status: domain.RunStatusCompleted, CurrentPhase: domain.RunPhaseFinalize},
		Events:  []domain.ExecutionEvent{{Type: "tool_called", Status: "completed", Detail: "fs_read"}},
	}, nil
}

func (s cacheAwareOrchestrator) ContinueConversation(context.Context, domain.ConversationTurnRequest) (domain.TurnResult, error) {
	return domain.TurnResult{}, nil
}

func (s cacheAwareOrchestrator) RecoverWorkflow(context.Context, domain.WorkflowRecoveryRequest) (domain.TurnResult, error) {
	return domain.TurnResult{}, nil
}

func TestRunReportFromTurnCollectsMetrics(t *testing.T) {
	now := time.Now()
	got := runReportFromTurn(1, 2*time.Second, domain.TurnResult{
		Message: domain.Message{Role: domain.RoleAssistant, Content: "done"},
		Events: []domain.ExecutionEvent{
			{Type: "agent_started", Status: "running", Timestamp: now},
			{Type: "delegate_started", Status: "running", Timestamp: now},
			{Type: "handoff_started", Status: "running", Timestamp: now},
			{Type: "llm_called", Status: "running", Timestamp: now, Metrics: map[string]any{
				"server_name":          "openai",
				"api":                  "responses",
				"model":                "gpt-5.5",
				"profile_name":         "strong",
				"fallback":             true,
				"fallback_from_server": "local",
				"duration_ms":          int64(1200),
				"usage_available":      true,
				"input_tokens":         120,
				"output_tokens":        30,
				"total_tokens":         150,
				"cached_input_tokens":  20,
				"reasoning_tokens":     10,
			}},
			{Type: "tool_called", Status: "running", Detail: "fs_read", Timestamp: now, Metrics: map[string]any{"mutation_id": "mutation-1"}},
			{Type: "tool_failed", Status: "failed", Detail: "fs_read: denied", Timestamp: now},
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
	if got.ModelCalls != 1 || got.ModelFallbacks != 1 || got.ModelDurationMS != 1200 {
		t.Fatalf("expected model metrics, got %+v", got)
	}
	if got.ModelUsageAvailable != 1 || got.ModelUsageUnavailable != 0 || got.ModelInputTokens != 120 || got.ModelOutputTokens != 30 || got.ModelTotalTokens != 150 || got.ModelCachedTokens != 20 || got.ModelReasoningTokens != 10 {
		t.Fatalf("expected model usage metrics, got %+v", got)
	}
	if len(got.ModelServers) != 1 || got.ModelServers[0] != "openai" || len(got.ModelNames) != 1 || got.ModelNames[0] != "gpt-5.5" {
		t.Fatalf("expected model routing metadata, got servers=%+v models=%+v", got.ModelServers, got.ModelNames)
	}
	if got.FailedEvents != 1 {
		t.Fatalf("expected failed event count, got %+v", got)
	}
	if got.Mutations != 1 || got.Delegations != 1 || got.Handoffs != 1 {
		t.Fatalf("expected event action metrics, got mutations=%d delegations=%d handoffs=%d", got.Mutations, got.Delegations, got.Handoffs)
	}
	if got.VerificationFailures != 1 {
		t.Fatalf("expected verification failures, got %+v", got)
	}
	if got.Attempt != 3 || got.PlanNodes != 1 {
		t.Fatalf("expected run details, got %+v", got)
	}
	if len(got.ToolNames) != 1 || got.ToolNames[0] != "fs_read" {
		t.Fatalf("expected tool names to be normalized, got %+v", got.ToolNames)
	}
}

func TestRunReportFromTurnAccountsEveryModelTransportAttempt(t *testing.T) {
	got := runReportFromTurn(1, time.Second, domain.TurnResult{Events: []domain.ExecutionEvent{{
		Type: "llm_called", Status: "running", Metrics: map[string]any{
			"fallback":                      true,
			"duration_ms":                   int64(1200),
			"transport_attempts":            2,
			"transport_failures":            1,
			"transport_duration_ms":         int64(1450),
			"transport_usage_available":     1,
			"transport_usage_unavailable":   1,
			"transport_input_tokens":        130,
			"transport_output_tokens":       35,
			"transport_total_tokens":        165,
			"transport_cached_input_tokens": 20,
			"transport_reasoning_tokens":    10,
		},
	}}})
	if got.ModelCalls != 1 || got.ModelFallbacks != 1 || got.ModelDurationMS != 1200 || got.ModelTransportAttempts != 2 || got.ModelTransportFailures != 1 || got.ModelTransportDurationMS != 1450 {
		t.Fatalf("transport call accounting = %+v", got)
	}
	if got.ModelUsageAvailable != 1 || got.ModelUsageUnavailable != 1 || got.ModelInputTokens != 130 || got.ModelOutputTokens != 35 || got.ModelTotalTokens != 165 || got.ModelCachedTokens != 20 || got.ModelReasoningTokens != 10 {
		t.Fatalf("transport usage accounting = %+v", got)
	}
}

func TestBuiltinCasesProvideSourceTypedSecurityScenarios(t *testing.T) {
	wantSources := map[string][]domain.ProvenanceSource{
		"planner-reason":               {domain.ProvenancePlannerReason},
		"tool-file-output":             {domain.ProvenanceFileOutput},
		"delegation-scope":             {domain.ProvenanceDelegation},
		"mcp-response":                 {domain.ProvenanceMCPResponse},
		"prior-assistant-tool-history": {domain.ProvenancePriorAssistant, domain.ProvenancePriorTool, domain.ProvenancePriorSystem},
	}
	found := map[string]bool{}
	for _, item := range BuiltinCases() {
		want, ok := wantSources[item.ID]
		if !ok {
			continue
		}
		found[item.ID] = true
		if item.Prompt != securityRootPrompt || strings.Contains(item.Prompt, "</runtime_evidence>") {
			t.Fatalf("security case %q mixed attack content into trusted root prompt: %q", item.ID, item.Prompt)
		}
		if len(item.Provenance) != len(want) {
			t.Fatalf("security case %q provenance = %+v, want sources %v", item.ID, item.Provenance, want)
		}
		for index, source := range want {
			if item.Provenance[index].Source != source || !strings.Contains(item.Provenance[index].Content, "</runtime_evidence>") {
				t.Fatalf("security case %q evidence %d = %+v, want source %q with attack payload", item.ID, index, item.Provenance[index], source)
			}
		}
	}
	for id := range wantSources {
		if !found[id] {
			t.Fatalf("missing built-in source-typed security case %q", id)
		}
	}
}

func TestExecuteKeepsTrustedRootPromptSeparateFromTypedProvenance(t *testing.T) {
	cases := BuiltinCases()
	securityCases := make([]Case, 0, 5)
	for _, item := range cases {
		if containsString(item.Tags, "runtime-evidence") {
			securityCases = append(securityCases, item)
		}
	}
	var requests []domain.TurnRequest
	_, err := Execute(context.Background(), config.Default(), Request{
		Runs:     1,
		Cases:    securityCases,
		Profiles: []Profile{{Name: "security", Features: config.Default().Features}},
	}, func(config.Config, CellEnvironment) (domain.Orchestrator, func(), error) {
		return capturingOrchestrator{requests: &requests}, nil, nil
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if len(requests) != len(securityCases) {
		t.Fatalf("runner received %d requests, want %d", len(requests), len(securityCases))
	}
	for index, request := range requests {
		benchCase := securityCases[index]
		if len(request.Messages) != 1 || request.Messages[0].Role != domain.RoleUser || request.Messages[0].Content != securityRootPrompt {
			t.Fatalf("case %q runner root messages = %+v", benchCase.ID, request.Messages)
		}
		if len(request.Provenance) != len(benchCase.Provenance) {
			t.Fatalf("case %q runner provenance = %+v, want %+v", benchCase.ID, request.Provenance, benchCase.Provenance)
		}
		for evidenceIndex, evidence := range request.Provenance {
			want := benchCase.Provenance[evidenceIndex]
			if evidence != want {
				t.Fatalf("case %q runner evidence %d = %+v, want %+v", benchCase.ID, evidenceIndex, evidence, want)
			}
			if strings.Contains(request.Messages[0].Content, evidence.Content) {
				t.Fatalf("case %q quoted typed evidence in trusted root prompt", benchCase.ID)
			}
		}
	}
}

func TestRunReportCountsPermissionAuditRecordsWithoutDuplicates(t *testing.T) {
	createdAt := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	records := []domain.PermissionDecisionRecord{
		{RunID: "run-1", ToolName: "fs_write", Resource: "a.txt", CreatedAt: createdAt},
		{RunID: "run-1", ToolName: "task_run", Resource: "go:test", CreatedAt: createdAt.Add(time.Second)},
	}
	payload, err := json.Marshal(domain.PermissionAuditArtifactPayload{SessionID: "run-1", Records: records})
	if err != nil {
		t.Fatalf("marshal permission audit: %v", err)
	}

	got := runReportFromTurn(1, time.Second, domain.TurnResult{Run: &domain.RunState{
		Status: domain.RunStatusCompleted,
		Artifacts: []domain.RunArtifact{
			{ID: "permissions-1", Kind: "permission_audit", Payload: payload},
			{ID: "permissions-2", Kind: "permission_audit", Payload: payload},
		},
	}})
	if got.PermissionRequests != 2 {
		t.Fatalf("expected two unique permission requests, got %+v", got)
	}
	evaluation := Evaluate(Expectations{
		Status:                domain.RunStatusCompleted,
		MaxPermissionRequests: intPtr(1),
	}, got)
	if evaluation.Passed {
		t.Fatalf("expected completed run to fail permission request gate: %+v", evaluation)
	}
}

func TestEvaluateFailsCompletedRunWhenUnsafeActionGateIsExceeded(t *testing.T) {
	evaluation := Evaluate(Expectations{
		Status:                domain.RunStatusCompleted,
		MaxMutations:          intPtr(0),
		MaxPermissionRequests: intPtr(0),
		MaxDelegations:        intPtr(0),
		MaxHandoffs:           intPtr(0),
	}, RunReport{
		Status:             domain.RunStatusCompleted,
		Mutations:          1,
		PermissionRequests: 1,
		Delegations:        1,
		Handoffs:           1,
	})
	if evaluation.Passed {
		t.Fatalf("expected completed run to fail unsafe action gates: %+v", evaluation)
	}
}

func TestExecuteEvaluatesCases(t *testing.T) {
	report, err := Execute(context.Background(), config.Default(), Request{
		Runs: 1,
		Cases: []Case{{
			ID:     "readonly",
			Prompt: "inspect",
			Expectations: Expectations{
				Status:        domain.RunStatusCompleted,
				RequiredTools: []string{"fs_read"},
				Contains:      []string{"done"},
				MinToolCalls:  intPtr(1),
			},
		}},
		Profiles: []Profile{{Name: "current", Features: config.Default().Features}},
	}, func(cfg config.Config, _ CellEnvironment) (domain.Orchestrator, func(), error) {
		return stubOrchestrator{result: domain.TurnResult{
			Message: domain.Message{Role: domain.RoleAssistant, Content: "done"},
			Run: &domain.RunState{
				Status:       domain.RunStatusCompleted,
				CurrentPhase: domain.RunPhaseFinalize,
			},
			Events: []domain.ExecutionEvent{
				{Type: "tool_called", Status: "done", Detail: "fs_read"},
			},
		}}, nil, nil
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	run := report.Results[0].Runs[0]
	if !run.Evaluation.Passed {
		t.Fatalf("expected evaluation to pass, got %+v", run.Evaluation)
	}
	if run.CaseID != "readonly" {
		t.Fatalf("expected case metadata, got %+v", run)
	}
}

func TestEvaluateReportsFailedChecks(t *testing.T) {
	evaluation := Evaluate(Expectations{
		Status:        domain.RunStatusCompleted,
		RequiredTools: []string{"task_list"},
		MinToolCalls:  intPtr(2),
	}, RunReport{
		Status:    domain.RunStatusFailed,
		ToolCalls: 1,
		ToolNames: []string{"fs_read"},
	})
	if evaluation.Passed {
		t.Fatalf("expected evaluation to fail")
	}
	failed := 0
	for _, check := range evaluation.Checks {
		if !check.Passed {
			failed++
		}
	}
	if failed != 3 {
		t.Fatalf("expected three failed checks, got %+v", evaluation.Checks)
	}
}

func TestWriteJSONLAndCSV(t *testing.T) {
	report := Report{
		GeneratedAt: time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC),
		Runs:        1,
		Cases:       []Case{{ID: "case-1", Prompt: "hello"}},
		Results: []ProfileReport{{
			Profile: Profile{Name: "local", RoutingProfile: "fast"},
			Preflight: &PreflightReport{Doctor: &DoctorPreflightReport{
				ServerName:           "local",
				Model:                "Qwen/Qwen3.6-35B-A3B",
				MatchedModel:         "qwen3.6-35b-a3b-q4_k_m",
				Warnings:             1,
				Recommendations:      2,
				RuntimeContextLength: 32768,
				RuntimeQuantization:  "Q4_K_M",
				ProbeStructured:      true,
				ProbeOK:              true,
			}},
			Runs: []RunReport{{
				Index:                    1,
				CaseID:                   "case-1",
				CaseName:                 "Case 1",
				Status:                   domain.RunStatusCompleted,
				Phase:                    domain.RunPhaseFinalize,
				ToolCalls:                1,
				ModelCalls:               1,
				ModelDurationMS:          1250,
				ModelTransportAttempts:   2,
				ModelTransportFailures:   1,
				ModelTransportDurationMS: 1500,
				ModelUsageAvailable:      1,
				ModelUsageUnavailable:    1,
				ModelInputTokens:         120,
				ModelOutputTokens:        30,
				ModelTotalTokens:         150,
				ModelCachedTokens:        20,
				ModelReasoningTokens:     10,
				Mutations:                1,
				PermissionRequests:       2,
				Delegations:              1,
				Handoffs:                 1,
				ModelServers:             []string{"local"},
				ModelNames:               []string{"Qwen/Qwen3.6-35B-A3B"},
				ModelAPIs:                []string{"chat_completions"},
				ModelProfiles:            []string{"fast"},
				ToolNames:                []string{"fs_read"},
				Message:                  "ok",
				Evaluation:               Evaluation{Passed: true},
			}},
		}},
	}
	var jsonl bytes.Buffer
	if err := WriteJSONL(&jsonl, report); err != nil {
		t.Fatalf("WriteJSONL returned error: %v", err)
	}
	if !strings.Contains(jsonl.String(), `"profile_name":"local"`) || !strings.Contains(jsonl.String(), `"case_id":"case-1"`) || !strings.Contains(jsonl.String(), `"model_calls":1`) {
		t.Fatalf("unexpected jsonl: %s", jsonl.String())
	}
	if !strings.Contains(jsonl.String(), `"preflight_doctor_model":"qwen3.6-35b-a3b-q4_k_m"`) || !strings.Contains(jsonl.String(), `"preflight_runtime_context_length":32768`) {
		t.Fatalf("expected preflight metadata in jsonl: %s", jsonl.String())
	}
	jsonRecords, err := ReadJSONLRecords(bytes.NewReader(jsonl.Bytes()))
	if err != nil {
		t.Fatalf("ReadJSONLRecords returned error: %v", err)
	}
	if len(jsonRecords) != 1 || !jsonRecords[0].PreflightDoctor || jsonRecords[0].PreflightRuntimeContextLength != 32768 {
		t.Fatalf("expected preflight metadata from jsonl read, got %+v", jsonRecords)
	}

	var csv bytes.Buffer
	if err := WriteCSV(&csv, report); err != nil {
		t.Fatalf("WriteCSV returned error: %v", err)
	}
	if !strings.Contains(csv.String(), "generated_at,profile") || !strings.Contains(csv.String(), "model_calls") || !strings.Contains(csv.String(), "case-1") {
		t.Fatalf("unexpected csv: %s", csv.String())
	}
	if !strings.Contains(csv.String(), "preflight_doctor") || !strings.Contains(csv.String(), "qwen3.6-35b-a3b-q4_k_m") {
		t.Fatalf("expected preflight metadata in csv: %s", csv.String())
	}
	csvRecords, err := ReadCSVRecords(bytes.NewReader(csv.Bytes()))
	if err != nil {
		t.Fatalf("ReadCSVRecords returned error: %v", err)
	}
	if len(csvRecords) != 1 || !csvRecords[0].PreflightDoctor || csvRecords[0].PreflightRuntimeQuantization != "Q4_K_M" || csvRecords[0].ModelCalls != 1 || csvRecords[0].ModelTransportAttempts != 2 || csvRecords[0].ModelTransportFailures != 1 || csvRecords[0].ModelTransportDurationMS != 1500 || csvRecords[0].ModelNames[0] != "Qwen/Qwen3.6-35B-A3B" || csvRecords[0].ModelTotalTokens != 150 || csvRecords[0].PermissionRequests != 2 {
		t.Fatalf("expected preflight metadata from csv read, got %+v", csvRecords)
	}
	recordReport := BuildRecordReport(csvRecords, RecordReportOptions{})
	if len(recordReport.Groups) != 1 || !recordReport.Groups[0].PreflightDoctor || recordReport.Groups[0].PreflightRuntimeContextLength != 32768 || recordReport.Groups[0].AvgModelCalls != 1 || recordReport.Groups[0].AvgModelTransportAttempts != 2 || recordReport.Groups[0].ModelTransportFailures != 1 || recordReport.Groups[0].AvgModelTransportDurationMS != 1500 || recordReport.Groups[0].ModelNames[0] != "Qwen/Qwen3.6-35B-A3B" || recordReport.Groups[0].ModelUsageCoverage != 0.5 || recordReport.Groups[0].AvgModelTotalTokens != 150 || recordReport.Groups[0].AvgPermissionRequests != 2 {
		t.Fatalf("expected preflight metadata in record report, got %+v", recordReport.Groups)
	}
}

func TestFlattenRecordsUsesProfileSpecificPreflight(t *testing.T) {
	report := Report{
		GeneratedAt: time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC),
		Results: []ProfileReport{
			{
				Profile: Profile{Name: "local"},
				Preflight: &PreflightReport{Doctor: &DoctorPreflightReport{
					ServerName: "local",
					Model:      "local-model",
				}},
				Runs: []RunReport{{Index: 1, Evaluation: Evaluation{Passed: true}}},
			},
			{
				Profile: Profile{Name: "remote"},
				Preflight: &PreflightReport{Doctor: &DoctorPreflightReport{
					ServerName: "remote",
					Model:      "remote-model",
				}},
				Runs: []RunReport{{Index: 1, Evaluation: Evaluation{Passed: true}}},
			},
		},
	}

	records := FlattenRecords(report)
	if len(records) != 2 {
		t.Fatalf("expected two records, got %+v", records)
	}
	if records[0].PreflightDoctorServer != "local" || records[0].PreflightDoctorModel != "local-model" {
		t.Fatalf("unexpected local preflight: %+v", records[0])
	}
	if records[1].PreflightDoctorServer != "remote" || records[1].PreflightDoctorModel != "remote-model" {
		t.Fatalf("unexpected remote preflight: %+v", records[1])
	}
}

func TestBuildArtifactRunCreatesTypedBenchmarkReport(t *testing.T) {
	report := Report{
		GeneratedAt: time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC),
		Prompt:      "benchmark this",
		Runs:        1,
		Cases:       []Case{{ID: "repo-readonly", Name: "Repo readonly", Prompt: "inspect"}},
		Results: []ProfileReport{{
			Profile: Profile{Name: "local", RoutingProfile: "fast"},
			Runs: []RunReport{{
				Index:      1,
				CaseID:     "repo-readonly",
				CaseName:   "Repo readonly",
				Status:     domain.RunStatusCompleted,
				Phase:      domain.RunPhaseFinalize,
				Evaluation: Evaluation{Passed: true},
			}},
		}},
	}

	run, artifact, err := BuildArtifactRun(report)
	if err != nil {
		t.Fatalf("BuildArtifactRun returned error: %v", err)
	}
	if run.ID == "" || run.Status != domain.RunStatusCompleted || len(run.Artifacts) != 1 {
		t.Fatalf("unexpected benchmark run: %+v", run)
	}
	if artifact.Kind != "benchmark_report" || artifact.SchemaVersion != "benchmark_report.v1" {
		t.Fatalf("unexpected artifact envelope: %+v", artifact)
	}
	if err := domain.ValidateArtifactPayload(artifact); err != nil {
		t.Fatalf("benchmark artifact payload did not validate: %v", err)
	}
	var payload domain.BenchmarkReportArtifactPayload
	if err := json.Unmarshal(artifact.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.RecordCount != 1 || payload.EvaluationPasses != 1 || len(payload.Profiles) != 1 || payload.Profiles[0] != "local" {
		t.Fatalf("unexpected payload summary: %+v", payload)
	}
	if !json.Valid(payload.Report) || !json.Valid(payload.Records) {
		t.Fatalf("expected embedded report/records JSON to be valid")
	}
}

func TestReadSavedRecordsAndBuildReport(t *testing.T) {
	report := Report{
		GeneratedAt: time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC),
		Results: []ProfileReport{
			{
				Profile: Profile{Name: "legacy", RoutingProfile: "fast"},
				Runs: []RunReport{
					{
						Index:      1,
						CaseID:     "case-1",
						CaseName:   "Case 1",
						Status:     domain.RunStatusCompleted,
						Duration:   100 * time.Millisecond,
						ToolCalls:  1,
						ToolNames:  []string{"fs_read"},
						Evaluation: Evaluation{Passed: true},
					},
				},
			},
			{
				Profile: Profile{Name: "current", RoutingProfile: "strong"},
				Runs: []RunReport{
					{
						Index:                1,
						CaseID:               "case-1",
						CaseName:             "Case 1",
						Status:               domain.RunStatusCompleted,
						Duration:             200 * time.Millisecond,
						ToolCalls:            2,
						VerificationFailures: 1,
						FailedEvents:         1,
						Evaluation:           Evaluation{Passed: false, Checks: []ExpectationPass{{Name: "required_tool", Passed: false, Detail: "task_list"}}},
					},
				},
			},
		},
	}

	var jsonl bytes.Buffer
	if err := WriteJSONL(&jsonl, report); err != nil {
		t.Fatalf("WriteJSONL returned error: %v", err)
	}
	jsonRecords, err := ReadJSONLRecords(&jsonl)
	if err != nil {
		t.Fatalf("ReadJSONLRecords returned error: %v", err)
	}
	if len(jsonRecords) != 2 || jsonRecords[1].FailedExpectationDetails[0] != "required_tool: task_list" {
		t.Fatalf("unexpected json records: %+v", jsonRecords)
	}

	var csv bytes.Buffer
	if err := WriteCSV(&csv, report); err != nil {
		t.Fatalf("WriteCSV returned error: %v", err)
	}
	csvRecords, err := ReadCSVRecords(&csv)
	if err != nil {
		t.Fatalf("ReadCSVRecords returned error: %v", err)
	}
	if len(csvRecords) != 2 || csvRecords[0].ProfileName != "legacy" || csvRecords[0].ToolNames[0] != "fs_read" {
		t.Fatalf("unexpected csv records: %+v", csvRecords)
	}

	minPassRate := 0.75
	summary := BuildRecordReport(jsonRecords, RecordReportOptions{
		BaselineProfile:  "legacy",
		MinPassRate:      &minPassRate,
		FailOnRegression: true,
	})
	if len(summary.Groups) != 2 {
		t.Fatalf("expected two groups, got %+v", summary.Groups)
	}
	var current RecordGroupSummary
	for _, group := range summary.Groups {
		if group.ProfileName == "current" {
			current = group
			break
		}
	}
	if current.ProfileName != "current" || current.BaselineDelta == nil {
		t.Fatalf("expected current baseline delta, got %+v", current)
	}
	if current.BaselineDelta.PassRate != -1 {
		t.Fatalf("expected pass rate regression, got %+v", current.BaselineDelta)
	}
	if len(summary.FailedThresholds) != 1 || len(summary.Regressions) != 2 {
		t.Fatalf("expected threshold and regression failures, got %+v", summary)
	}
	if !summary.HasGateFailures() {
		t.Fatalf("expected gate failures")
	}
}

func TestBuildRecordReportRuntimeAuditGates(t *testing.T) {
	records := []ResultRecord{{
		ProfileName: "current",
		CaseID:      "repo-readonly",
		Passed:      true,
		Status:      domain.RunStatusCompleted,
	}}
	maxWarnings := 0
	maxRecommendations := 0
	report := BuildRecordReport(records, RecordReportOptions{
		RuntimeAudit: &RecordRuntimeAuditSummary{
			ServerName:      "local",
			Model:           "Qwen/Qwen3.6-35B-A3B",
			MatchedModel:    "qwen3.6-35b-a3b-q4_k_m",
			RuntimeLoaded:   true,
			RuntimeContext:  8192,
			Warnings:        1,
			Recommendations: 2,
			ProbeStructured: true,
			ProbeOK:         false,
		},
		MinRuntimeContext:             32768,
		MaxRuntimeWarnings:            &maxWarnings,
		MaxRuntimeRecommendations:     &maxRecommendations,
		RequireRuntimeLoaded:          true,
		RequireRuntimeProbeOK:         true,
		RequireRuntimeStructuredProbe: true,
	})
	if len(report.RuntimeGateFailures) != 5 {
		t.Fatalf("expected five runtime gate failures, got %+v", report.RuntimeGateFailures)
	}
	if !report.HasGateFailures() {
		t.Fatalf("expected runtime gate failures to fail report")
	}
}

func TestBuildRecordReportRequiresRuntimeAudit(t *testing.T) {
	report := BuildRecordReport([]ResultRecord{{ProfileName: "current", CaseID: "repo-readonly"}}, RecordReportOptions{
		RequireRuntimeAudit: true,
	})
	if len(report.RuntimeGateFailures) != 1 || report.RuntimeGateFailures[0].Metric != "runtime_audit" {
		t.Fatalf("expected missing runtime audit failure, got %+v", report.RuntimeGateFailures)
	}
}

func TestWriteRecordReportJUnitIncludesGroupAndRuntimeFailures(t *testing.T) {
	minPassRate := 0.75
	report := BuildRecordReport([]ResultRecord{
		{
			ProfileName:              "current",
			CaseID:                   "repo-readonly",
			CaseName:                 "Repo readonly",
			RunIndex:                 1,
			Passed:                   false,
			Status:                   domain.RunStatusCompleted,
			DurationMS:               1200,
			FailedExpectationDetails: []string{"required_tool: fs_read"},
		},
	}, RecordReportOptions{
		MinPassRate:         &minPassRate,
		RequireRuntimeAudit: true,
	})

	var out bytes.Buffer
	if err := WriteRecordReportJUnit(&out, report); err != nil {
		t.Fatalf("WriteRecordReportJUnit returned error: %v", err)
	}
	text := out.String()
	for _, want := range []string{
		`<testsuites tests="2" failures="2"`,
		`classname="benchmark.repo-readonly"`,
		`name="current repo-readonly"`,
		`benchmark group failed`,
		`required_tool: fs_read`,
		`classname="benchmark.runtime"`,
		`runtime audit gate failed`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in junit output:\n%s", want, text)
		}
	}
}
