package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"yagent/internal/config"
	"yagent/internal/domain"
	"yagent/internal/infra/state"
	benchmarkusecase "yagent/internal/usecase/benchmark"
	"yagent/internal/usecase/llmcheck"
)

func TestResolveBenchmarkProfileLegacy(t *testing.T) {
	profile, err := resolveBenchmarkProfile(config.FeaturesConfig{
		PhaseHarness:       true,
		AdaptiveCompaction: true,
		RoleRouting:        true,
		RepoMemory:         true,
	}, "legacy")
	if err != nil {
		t.Fatalf("resolveBenchmarkProfile returned error: %v", err)
	}
	if profile.Features.PhaseHarness || profile.Features.RoleRouting || profile.Features.RepoMemory {
		t.Fatalf("expected legacy flags to be disabled, got %+v", profile.Features)
	}
}

func TestRenderBenchmarkTableIncludesFeatureSummary(t *testing.T) {
	text := renderBenchmarkTable(benchmarkusecase.Report{
		Prompt: "hello",
		Runs:   1,
		Cases:  []benchmarkusecase.Case{{ID: "prompt", Name: "Prompt", Prompt: "hello"}},
		Results: []benchmarkusecase.ProfileReport{{
			Profile: benchmarkusecase.Profile{
				Name: "current",
				Features: config.FeaturesConfig{
					PhaseHarness: true,
					RoleRouting:  true,
				},
			},
			Summary: benchmarkusecase.Summary{
				Runs:        1,
				Successes:   1,
				AvgDuration: 12,
			},
			Runs: []benchmarkusecase.RunReport{{
				Index:    1,
				Status:   "completed",
				Duration: 12,
				Phase:    "finalize",
				Evaluation: benchmarkusecase.Evaluation{
					Passed: true,
				},
			}},
		}},
	})

	if !strings.Contains(text, "harness,routing") {
		t.Fatalf("expected feature summary, got %q", text)
	}
	if !strings.Contains(text, "eval=pass") {
		t.Fatalf("expected evaluation status, got %q", text)
	}
}

func TestResolveBenchmarkProfilesWithRoutingCandidates(t *testing.T) {
	profiles, err := resolveBenchmarkProfiles(config.FeaturesConfig{PhaseHarness: true}, nil, []string{"local=fast", "remote=strong:gpt-5.5"})
	if err != nil {
		t.Fatalf("resolveBenchmarkProfiles returned error: %v", err)
	}
	if len(profiles) != 2 {
		t.Fatalf("expected two routing profiles, got %+v", profiles)
	}
	if profiles[0].Name != "current@local" || profiles[0].RoutingProfile != "fast" {
		t.Fatalf("unexpected local profile: %+v", profiles[0])
	}
	if profiles[1].Name != "current@remote" || profiles[1].RoutingProfile != "strong" || profiles[1].Model != "gpt-5.5" {
		t.Fatalf("unexpected remote profile: %+v", profiles[1])
	}
}

func TestResolveBenchmarkDoctorTargetUsesRoutingAndCandidateModel(t *testing.T) {
	cfg := config.Default()
	cfg.Routing.Profiles["remote"] = config.RoutingProfileConfig{
		Server: "remote-server",
		Model:  "routing-model",
	}
	profile := benchmarkusecase.Profile{
		Name:           "current@remote",
		RoutingProfile: "remote",
		Model:          "candidate-model",
	}

	server, model, err := resolveBenchmarkDoctorTarget(cfg, profile, "", "")
	if err != nil {
		t.Fatalf("resolveBenchmarkDoctorTarget returned error: %v", err)
	}
	if server != "remote-server" || model != "candidate-model" {
		t.Fatalf("expected routing server and candidate model, got server=%q model=%q", server, model)
	}
	profile.Model = ""
	server, model, err = resolveBenchmarkDoctorTarget(cfg, profile, "", "")
	if err != nil {
		t.Fatalf("resolveBenchmarkDoctorTarget without candidate model returned error: %v", err)
	}
	if server != "remote-server" || model != "routing-model" {
		t.Fatalf("expected routing profile model fallback, got server=%q model=%q", server, model)
	}
}

func TestResolveBenchmarkCasesUsesBuiltins(t *testing.T) {
	cases, err := resolveBenchmarkCases([]string{"repo-readonly"}, nil)
	if err != nil {
		t.Fatalf("resolveBenchmarkCases returned error: %v", err)
	}
	if len(cases) != 1 || cases[0].ID != "repo-readonly" {
		t.Fatalf("unexpected cases: %+v", cases)
	}
}

func TestBenchmarkCommandIncludesReportSubcommand(t *testing.T) {
	configPath := ""
	logPath := ""
	command := newBenchmarkCommand(&configPath, &logPath)
	for _, child := range command.Commands() {
		if child.Name() == "report" {
			return
		}
	}
	t.Fatalf("expected benchmark report subcommand")
}

func TestBenchmarkReportCommandIncludesRuntimeAuditFlags(t *testing.T) {
	configPath := ""
	command := newBenchmarkReportCommand(&configPath)
	if command.Flag("format").DefValue != "text" {
		t.Fatalf("expected default text format, got %s", command.Flag("format").DefValue)
	}
	if !strings.Contains(command.Flag("format").Usage, "junit") {
		t.Fatalf("expected junit format in flag usage: %s", command.Flag("format").Usage)
	}
	for _, flag := range []string{
		"runtime-audit-server",
		"require-runtime-audit",
		"min-runtime-context",
		"max-runtime-warnings",
		"max-runtime-recommendations",
		"require-runtime-loaded",
		"require-runtime-probe-ok",
		"require-runtime-structured-probe",
	} {
		if command.Flag(flag) == nil {
			t.Fatalf("expected %s flag", flag)
		}
	}
}

func TestBenchmarkCommandIncludesPreflightDoctorFlags(t *testing.T) {
	configPath := ""
	logPath := ""
	command := newBenchmarkCommand(&configPath, &logPath)
	for _, flag := range []string{
		"save-artifact",
		"preflight-doctor",
		"preflight-doctor-runtime",
		"preflight-doctor-probe-structured",
		"preflight-fail-on-warning",
		"preflight-fail-on-recommendation",
	} {
		if command.Flag(flag) == nil {
			t.Fatalf("expected %s flag", flag)
		}
	}
}

func TestBenchmarkCommandDoesNotExposeResume(t *testing.T) {
	configPath := ""
	logPath := ""
	command := newBenchmarkCommand(&configPath, &logPath)
	if command.Flag("resume") != nil {
		t.Fatal("benchmark must not expose --resume")
	}
}

func TestSaveBenchmarkArtifactPersistsRunState(t *testing.T) {
	cfg := config.Default()
	cfg.Memory.Enabled = true
	cfg.Memory.StateDir = t.TempDir()
	report := benchmarkusecase.Report{
		GeneratedAt: time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC),
		Runs:        1,
		Cases:       []benchmarkusecase.Case{{ID: "repo-readonly", Prompt: "inspect"}},
		Results: []benchmarkusecase.ProfileReport{{
			Profile: benchmarkusecase.Profile{Name: "local", RoutingProfile: "fast"},
			Runs: []benchmarkusecase.RunReport{{
				Index:      1,
				CaseID:     "repo-readonly",
				Status:     domain.RunStatusCompleted,
				Phase:      domain.RunPhaseFinalize,
				Evaluation: benchmarkusecase.Evaluation{Passed: true},
			}},
		}},
	}

	run, artifact, err := saveBenchmarkArtifact(context.Background(), cfg, report)
	if err != nil {
		t.Fatalf("saveBenchmarkArtifact returned error: %v", err)
	}
	store, err := state.NewFileStore(cfg.Memory.StateDir)
	if err != nil {
		t.Fatalf("NewFileStore returned error: %v", err)
	}
	loaded, err := store.LoadRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("LoadRun returned error: %v", err)
	}
	if loaded.ID != run.ID || len(loaded.Artifacts) != 1 || loaded.Artifacts[0].ID != artifact.ID {
		t.Fatalf("unexpected saved benchmark run: %+v", loaded)
	}
	if latest, err := store.LoadLatestRun(context.Background()); err != nil || latest == nil || latest.ID != run.ID {
		t.Fatalf("expected latest benchmark run, got latest=%+v err=%v", latest, err)
	}
}

func TestShouldRunBenchmarkDoctorPreflight(t *testing.T) {
	if shouldRunBenchmarkDoctorPreflight(benchmarkDoctorPreflightOptions{}) {
		t.Fatalf("did not expect default preflight options to run")
	}
	for _, options := range []benchmarkDoctorPreflightOptions{
		{Enabled: true},
		{ProbeStructured: true},
		{FailOnWarning: true},
		{ServerName: "local"},
	} {
		if !shouldRunBenchmarkDoctorPreflight(options) {
			t.Fatalf("expected preflight to run for %+v", options)
		}
	}
}

func TestRuntimeAuditSummaryFromDoctorAudit(t *testing.T) {
	summary := runtimeAuditSummaryFromDoctorAudit(llmcheck.AuditRecord{
		ID:           "llm-doctor-1",
		ServerName:   "local",
		URL:          "http://127.0.0.1:1234",
		API:          "chat_completions",
		Model:        "Qwen/Qwen3.6-35B-A3B",
		MatchedModel: "qwen3.6-35b-a3b-q4_k_m",
		Warnings:     []string{"context"},
		Recommendations: []llmcheck.Recommendation{{
			Setting: "loaded context_length",
		}},
		Runtime: llmcheck.RuntimeResult{
			ModelFound:       true,
			Loaded:           true,
			ContextLength:    32768,
			MaxContextLength: 131072,
			MatchedModel: llmcheck.RuntimeModelSummary{
				Quantization: "Q4_K_M",
			},
		},
		Probe: llmcheck.ProbeResult{
			Requested:  true,
			Structured: true,
			OK:         true,
		},
		CreatedAt: time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC),
	})
	if summary.ID != "llm-doctor-1" || summary.RuntimeContext != 32768 || summary.RuntimeQuantization != "Q4_K_M" {
		t.Fatalf("unexpected runtime audit summary: %+v", summary)
	}
	if summary.Warnings != 1 || summary.Recommendations != 1 || !summary.ProbeStructured || !summary.ProbeOK {
		t.Fatalf("unexpected runtime audit counts/probe: %+v", summary)
	}
}

func TestShouldLoadBenchmarkRuntimeAudit(t *testing.T) {
	if shouldLoadBenchmarkRuntimeAudit(benchmarkRuntimeAuditOptions{}) {
		t.Fatalf("did not expect default runtime audit options to load")
	}
	for _, options := range []benchmarkRuntimeAuditOptions{
		{ServerName: "local"},
		{RequireAudit: true},
		{MinContext: 32768},
		{MaxWarnings: intPtrFromFlag(0)},
		{MaxRecommendations: intPtrFromFlag(0)},
		{RequireLoaded: true},
		{RequireProbeOK: true},
		{RequireStructuredProbe: true},
	} {
		if !shouldLoadBenchmarkRuntimeAudit(options) {
			t.Fatalf("expected runtime audit load for %+v", options)
		}
	}
}

func TestRenderBenchmarkDoctorPreflightSummary(t *testing.T) {
	text := renderBenchmarkDoctorPreflightSummary(llmcheck.Result{
		ServerName:      "local",
		Model:           "Qwen/Qwen3.6-35B-A3B",
		MatchedModel:    "qwen3.6-35b-a3b-q4_k_m",
		Warnings:        []string{"context"},
		Recommendations: []llmcheck.Recommendation{{Setting: "loaded context_length"}},
	})
	for _, want := range []string{
		"Benchmark preflight doctor: ok",
		"server=local",
		"model=qwen3.6-35b-a3b-q4_k_m",
		"warnings=1",
		"recommendations=1",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in summary, got %q", want, text)
		}
	}
}

func TestDoctorPreflightReportFromResult(t *testing.T) {
	report := doctorPreflightReportFromResult(llmcheck.Result{
		ServerName:      "local",
		URL:             "http://127.0.0.1:1234",
		API:             "chat_completions",
		Model:           "Qwen/Qwen3.6-35B-A3B",
		MatchedModel:    "qwen3.6-35b-a3b-q4_k_m",
		Warnings:        []string{"context"},
		Recommendations: []llmcheck.Recommendation{{Setting: "loaded context_length"}},
		Runtime: llmcheck.RuntimeResult{
			ModelFound:       true,
			Loaded:           true,
			ContextLength:    32768,
			MaxContextLength: 131072,
			MatchedModel: llmcheck.RuntimeModelSummary{
				ID:           "qwen3.6-35b-a3b-q4_k_m",
				Quantization: "Q4_K_M",
			},
		},
		Probe: llmcheck.ProbeResult{
			Requested:  true,
			Structured: true,
			OK:         true,
		},
	})
	if report.ServerName != "local" || report.RuntimeContextLength != 32768 || report.RuntimeQuantization != "Q4_K_M" {
		t.Fatalf("unexpected preflight report: %+v", report)
	}
	if report.Warnings != 1 || report.Recommendations != 1 || !report.ProbeStructured || !report.ProbeOK {
		t.Fatalf("unexpected preflight counts/probe: %+v", report)
	}
}

func TestRenderBenchmarkRecordReportIncludesBaselineDelta(t *testing.T) {
	text := renderBenchmarkRecordReport(benchmarkusecase.RecordReport{
		Records:         2,
		Inputs:          []string{"bench.jsonl"},
		BaselineProfile: "legacy",
		RuntimeAudit: &benchmarkusecase.RecordRuntimeAuditSummary{
			ServerName:          "local",
			Model:               "Qwen/Qwen3.6-35B-A3B",
			MatchedModel:        "qwen3.6-35b-a3b-q4_k_m",
			RuntimeContext:      32768,
			RuntimeMaxContext:   131072,
			RuntimeQuantization: "Q4_K_M",
			Warnings:            1,
			Recommendations:     2,
		},
		Groups: []benchmarkusecase.RecordGroupSummary{
			{
				ProfileName:                    "current",
				CaseID:                         "repo-readonly",
				Runs:                           2,
				Passes:                         1,
				PassRate:                       0.5,
				Successes:                      2,
				SuccessRate:                    1,
				AvgDurationMS:                  1200,
				AvgToolCalls:                   3,
				VerificationFailures:           1,
				PreflightDoctor:                true,
				PreflightDoctorWarnings:        1,
				PreflightDoctorRecommendations: 2,
				PreflightRuntimeContextLength:  32768,
				BaselineDelta: &benchmarkusecase.RecordBaselineDelta{
					PassRate:             -0.5,
					VerificationFailures: 1,
				},
			},
		},
		FailedThresholds: []benchmarkusecase.RecordGateFailure{{
			ProfileName: "current",
			CaseID:      "repo-readonly",
			Metric:      "pass_rate",
			Got:         0.5,
			Want:        0.75,
		}},
		Regressions: []benchmarkusecase.RecordRegression{{
			ProfileName: "current",
			CaseID:      "repo-readonly",
			Metric:      "pass_rate",
			Detail:      "delta=-0.500",
		}},
		RuntimeGateFailures: []benchmarkusecase.RecordRuntimeGateFailure{{
			Metric: "runtime_context",
			Got:    "8192",
			Want:   ">=32768",
		}},
	})

	for _, want := range []string{"bench.jsonl", "baseline: legacy", "runtime: server=local model=qwen3.6-35b-a3b-q4_k_m context=32768/131072 quant=Q4_K_M warnings=1 recommendations=2", "50%", "ctx=32768 warn=1 rec=2", "pass -50%", "Threshold failures", "Runtime gate failures", "runtime_context got=8192 want=>=32768", "Regressions"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in report, got:\n%s", want, text)
		}
	}
}
