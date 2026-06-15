package benchmark

import (
	"bytes"
	"context"
	"encoding/json"
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
	if report.Results[0].Summary.EvaluationPasses != 2 {
		t.Fatalf("expected prompt case evaluation passes, got %+v", report.Results[0].Summary)
	}
}

func TestRunReportFromTurnCollectsMetrics(t *testing.T) {
	now := time.Now()
	got := runReportFromTurn(1, 2*time.Second, domain.TurnResult{
		Message: domain.Message{Role: domain.RoleAssistant, Content: "done"},
		Events: []domain.ExecutionEvent{
			{Type: "agent_started", Status: "running", Timestamp: now},
			{Type: "llm_called", Status: "running", Timestamp: now, Metrics: map[string]any{
				"server_name":          "openai",
				"api":                  "responses",
				"model":                "gpt-5.5",
				"profile_name":         "strong",
				"fallback":             true,
				"fallback_from_server": "local",
				"duration_ms":          int64(1200),
			}},
			{Type: "tool_called", Status: "running", Detail: "fs_read", Timestamp: now},
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
	if len(got.ModelServers) != 1 || got.ModelServers[0] != "openai" || len(got.ModelNames) != 1 || got.ModelNames[0] != "gpt-5.5" {
		t.Fatalf("expected model routing metadata, got servers=%+v models=%+v", got.ModelServers, got.ModelNames)
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
	if len(got.ToolNames) != 1 || got.ToolNames[0] != "fs_read" {
		t.Fatalf("expected tool names to be normalized, got %+v", got.ToolNames)
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
	}, func(cfg config.Config) (domain.Orchestrator, func(), error) {
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
		Results: []ProfileReport{{
			Profile: Profile{Name: "local", RoutingProfile: "fast"},
			Runs: []RunReport{{
				Index:           1,
				CaseID:          "case-1",
				CaseName:        "Case 1",
				Status:          domain.RunStatusCompleted,
				Phase:           domain.RunPhaseFinalize,
				ToolCalls:       1,
				ModelCalls:      1,
				ModelDurationMS: 1250,
				ModelServers:    []string{"local"},
				ModelNames:      []string{"Qwen/Qwen3.6-35B-A3B"},
				ModelAPIs:       []string{"chat_completions"},
				ModelProfiles:   []string{"fast"},
				ToolNames:       []string{"fs_read"},
				Message:         "ok",
				Evaluation:      Evaluation{Passed: true},
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
	if len(csvRecords) != 1 || !csvRecords[0].PreflightDoctor || csvRecords[0].PreflightRuntimeQuantization != "Q4_K_M" || csvRecords[0].ModelCalls != 1 || csvRecords[0].ModelNames[0] != "Qwen/Qwen3.6-35B-A3B" {
		t.Fatalf("expected preflight metadata from csv read, got %+v", csvRecords)
	}
	recordReport := BuildRecordReport(csvRecords, RecordReportOptions{})
	if len(recordReport.Groups) != 1 || !recordReport.Groups[0].PreflightDoctor || recordReport.Groups[0].PreflightRuntimeContextLength != 32768 || recordReport.Groups[0].AvgModelCalls != 1 || recordReport.Groups[0].ModelNames[0] != "Qwen/Qwen3.6-35B-A3B" {
		t.Fatalf("expected preflight metadata in record report, got %+v", recordReport.Groups)
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
