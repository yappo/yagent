package cli

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"yagent/internal/app"
	"yagent/internal/config"
	"yagent/internal/domain"
	"yagent/internal/infra/state"
	benchmarkusecase "yagent/internal/usecase/benchmark"
	"yagent/internal/usecase/llmcheck"
)

func newBenchmarkCommand(configPath *string, logPath *string) *cobra.Command {
	var prompt string
	var model string
	var routingProfile string
	var output string
	var runs int
	var featureProfiles []string
	var routingCandidates []string
	var caseIDs []string
	var caseFiles []string
	var listCases bool
	var writeJSONL string
	var writeCSV string
	var saveArtifact bool
	var preflightDoctor bool
	var preflightDoctorServer string
	var preflightDoctorRuntime bool
	var preflightDoctorProbe bool
	var preflightDoctorProbeStructured bool
	var preflightFailOnWarning bool
	var preflightFailOnRecommendation bool

	command := &cobra.Command{
		Use:   "benchmark",
		Short: "feature / routing profile ごとの差分と harness eval を実行",
	}
	command.AddCommand(newBenchmarkReportCommand(configPath))

	command.Flags().StringVar(&prompt, "prompt", "", "benchmark に使うプロンプト。--case 指定時は省略できます")
	command.Flags().StringVar(&model, "model", "", "使用するモデル名")
	command.Flags().StringVar(&routingProfile, "profile", "", "routing profile 名")
	command.Flags().StringVar(&output, "output", "table", "出力形式: table, json, jsonl, csv")
	command.Flags().IntVar(&runs, "runs", 0, "各 feature profile を何回実行するか。0 なら config の既定値")
	command.Flags().StringSliceVar(&featureProfiles, "feature-profile", nil, "比較する feature profile 名")
	command.Flags().StringSliceVar(&routingCandidates, "routing-candidate", nil, "比較する routing profile。例: fast / strong / openai=strong:gpt-5.5")
	command.Flags().StringSliceVar(&caseIDs, "case", nil, "実行する benchmark case ID。組み込み: repo-readonly, swe-like, terminal-like, permission-gate")
	command.Flags().StringSliceVar(&caseFiles, "case-file", nil, "追加 benchmark case TOML。[[cases]] を読み込みます")
	command.Flags().BoolVar(&listCases, "list-cases", false, "利用できる benchmark case を表示して終了")
	command.Flags().StringVar(&writeJSONL, "write-jsonl", "", "flat result record を JSONL で保存")
	command.Flags().StringVar(&writeCSV, "write-csv", "", "flat result record を CSV で保存")
	command.Flags().BoolVar(&saveArtifact, "save-artifact", false, "benchmark report を memory.state_dir の benchmark_report artifact として保存する")
	command.Flags().BoolVar(&preflightDoctor, "preflight-doctor", false, "benchmark 実行前に LLM doctor gate を実行する")
	command.Flags().StringVar(&preflightDoctorServer, "preflight-doctor-server", "", "preflight doctor で診断する server 名。未指定なら server.default")
	command.Flags().BoolVar(&preflightDoctorRuntime, "preflight-doctor-runtime", true, "preflight doctor で LM Studio runtime metadata を確認する")
	command.Flags().BoolVar(&preflightDoctorProbe, "preflight-doctor-probe", false, "preflight doctor で軽い生成 probe を実行する")
	command.Flags().BoolVar(&preflightDoctorProbeStructured, "preflight-doctor-probe-structured", false, "preflight doctor で JSON schema structured output probe を実行する。probe を兼ねます")
	command.Flags().BoolVar(&preflightFailOnWarning, "preflight-fail-on-warning", false, "preflight doctor の warning を benchmark failure にする")
	command.Flags().BoolVar(&preflightFailOnRecommendation, "preflight-fail-on-recommendation", false, "preflight doctor の recommendation を benchmark failure にする")

	command.RunE = func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(*configPath)
		if err != nil {
			return err
		}
		if runs <= 0 {
			runs = cfg.Benchmark.DefaultRuns
		}
		preflightOptions := benchmarkDoctorPreflightOptions{
			Enabled:              preflightDoctor,
			ServerName:           preflightDoctorServer,
			Runtime:              preflightDoctorRuntime,
			Probe:                preflightDoctorProbe,
			ProbeStructured:      preflightDoctorProbeStructured,
			FailOnWarning:        preflightFailOnWarning,
			FailOnRecommendation: preflightFailOnRecommendation,
		}
		cases, err := resolveBenchmarkCases(caseIDs, caseFiles)
		if err != nil {
			return err
		}
		if listCases {
			fmt.Print(renderBenchmarkCases(cases))
			return nil
		}

		profiles, err := resolveBenchmarkProfiles(cfg.Features, featureProfiles, routingCandidates)
		if err != nil {
			return err
		}
		preflight := map[string]*benchmarkusecase.PreflightReport{}
		if shouldRunBenchmarkDoctorPreflight(preflightOptions) {
			preflight, err = runBenchmarkDoctorPreflight(cmd.Context(), cfg, profiles, model, preflightOptions)
			if err != nil {
				return err
			}
		}

		report, err := benchmarkusecase.Execute(cmd.Context(), cfg, benchmarkusecase.Request{
			Prompt:         prompt,
			Model:          model,
			Runs:           runs,
			RoutingProfile: routingProfile,
			Profiles:       profiles,
			Cases:          cases,
		}, func(runCfg config.Config, environment benchmarkusecase.CellEnvironment) (domain.Orchestrator, func(), error) {
			container, err := app.BuildFromConfig(runCfg, NewStdinApprover(), app.BuildOptions{LogPath: *logPath, WorkingDir: environment.WorkspaceDir, IsolatedWorkspace: true})
			if err != nil {
				return nil, nil, err
			}
			closeFn := func() {
				if container.Closer != nil {
					_ = container.Closer.Close()
				}
			}
			return container.Orchestrator, closeFn, nil
		})
		if err != nil {
			return err
		}
		for idx := range report.Results {
			if item, ok := preflight[report.Results[idx].Profile.Name]; ok {
				report.Results[idx].Preflight = item
			}
		}
		if writeJSONL != "" {
			if err := writeBenchmarkFile(writeJSONL, func(file *os.File) error {
				return benchmarkusecase.WriteJSONL(file, report)
			}); err != nil {
				return err
			}
		}
		if writeCSV != "" {
			if err := writeBenchmarkFile(writeCSV, func(file *os.File) error {
				return benchmarkusecase.WriteCSV(file, report)
			}); err != nil {
				return err
			}
		}
		if saveArtifact {
			run, artifact, err := saveBenchmarkArtifact(cmd.Context(), cfg, report)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "Benchmark artifact saved: run=%s artifact=%s\n", run.ID, artifact.ID)
		}

		switch output {
		case "json":
			data, err := json.MarshalIndent(report, "", "  ")
			if err != nil {
				return err
			}
			fmt.Println(string(data))
			return nil
		case "table":
			fmt.Print(renderBenchmarkTable(report))
			return nil
		case "jsonl":
			return benchmarkusecase.WriteJSONL(os.Stdout, report)
		case "csv":
			return benchmarkusecase.WriteCSV(os.Stdout, report)
		default:
			return fmt.Errorf("unsupported output %q", output)
		}
	}

	return command
}

func saveBenchmarkArtifact(ctx context.Context, cfg config.Config, report benchmarkusecase.Report) (*domain.RunState, domain.RunArtifact, error) {
	if !cfg.Memory.Enabled {
		return nil, domain.RunArtifact{}, fmt.Errorf("memory.enabled=false のため benchmark artifact を保存できません")
	}
	stateDir := cfg.Memory.StateDir
	if !filepath.IsAbs(stateDir) {
		pwd, err := os.Getwd()
		if err != nil {
			return nil, domain.RunArtifact{}, err
		}
		stateDir = filepath.Join(pwd, stateDir)
	}
	store, err := state.NewFileStore(stateDir)
	if err != nil {
		return nil, domain.RunArtifact{}, err
	}
	run, artifact, err := benchmarkusecase.BuildArtifactRun(report)
	if err != nil {
		return nil, domain.RunArtifact{}, err
	}
	if err := store.SaveRun(ctx, run); err != nil {
		return nil, domain.RunArtifact{}, err
	}
	return run, artifact, nil
}

type benchmarkDoctorPreflightOptions struct {
	Enabled              bool
	ServerName           string
	Runtime              bool
	Probe                bool
	ProbeStructured      bool
	FailOnWarning        bool
	FailOnRecommendation bool
}

func shouldRunBenchmarkDoctorPreflight(options benchmarkDoctorPreflightOptions) bool {
	return options.Enabled || options.Probe || options.ProbeStructured || options.FailOnWarning || options.FailOnRecommendation || strings.TrimSpace(options.ServerName) != ""
}

func runBenchmarkDoctorPreflight(ctx context.Context, cfg config.Config, profiles []benchmarkusecase.Profile, requestModel string, options benchmarkDoctorPreflightOptions) (map[string]*benchmarkusecase.PreflightReport, error) {
	results := make(map[string]*benchmarkusecase.PreflightReport, len(profiles))
	var gateErr error
	for _, profile := range profiles {
		serverName, model, err := resolveBenchmarkDoctorTarget(cfg, profile, requestModel, options.ServerName)
		if err != nil {
			return nil, err
		}
		result, err := llmcheck.New(nil).CheckWithOptions(ctx, cfg, llmcheck.CheckOptions{
			ServerName:      serverName,
			Model:           model,
			Probe:           options.Probe,
			ProbeStructured: options.ProbeStructured,
			Runtime:         options.Runtime,
		})
		if err != nil {
			return nil, err
		}
		if err := doctorGateError(result, doctorGateOptions{
			FailOnWarning:        options.FailOnWarning,
			FailOnRecommendation: options.FailOnRecommendation,
		}); err != nil {
			if gateErr == nil {
				gateErr = fmt.Errorf("benchmark preflight doctor failed for profile %q: %w", profile.Name, err)
			}
		}
		fmt.Fprint(os.Stderr, renderBenchmarkDoctorPreflightSummary(result))
		results[profile.Name] = &benchmarkusecase.PreflightReport{Doctor: doctorPreflightReportFromResult(result)}
	}
	return results, gateErr
}

func resolveBenchmarkDoctorTarget(cfg config.Config, profile benchmarkusecase.Profile, requestModel, explicitServer string) (string, string, error) {
	serverName := strings.TrimSpace(explicitServer)
	routingModel := ""
	if routing, ok := cfg.Routing.Profiles[profile.RoutingProfile]; ok {
		serverName = fallbackBenchmarkString(serverName, routing.Server)
		routingModel = strings.TrimSpace(routing.Model)
	}
	model := fallbackBenchmarkString(strings.TrimSpace(requestModel), fallbackBenchmarkString(strings.TrimSpace(profile.Model), routingModel))
	if model == "" {
		if serverName == "" {
			server, err := cfg.ResolveServer()
			if err != nil {
				return "", "", err
			}
			model = server.Model
		} else {
			for _, server := range cfg.Server.Servers {
				if server.Name == serverName {
					model = server.Model
					break
				}
			}
		}
	}
	return serverName, model, nil
}

func renderBenchmarkDoctorPreflightSummary(result llmcheck.Result) string {
	status := "ok"
	if len(result.Problems) > 0 {
		status = "needs_attention"
	}
	return fmt.Sprintf(
		"Benchmark preflight doctor: %s server=%s model=%s warnings=%d recommendations=%d\n",
		status,
		fallbackBenchmarkString(result.ServerName, "-"),
		fallbackBenchmarkString(result.MatchedModel, result.Model),
		len(result.Warnings),
		len(result.Recommendations),
	)
}

func doctorPreflightReportFromResult(result llmcheck.Result) *benchmarkusecase.DoctorPreflightReport {
	report := &benchmarkusecase.DoctorPreflightReport{
		ServerName:      result.ServerName,
		URL:             result.URL,
		API:             result.API,
		Model:           result.Model,
		MatchedModel:    result.MatchedModel,
		Warnings:        len(result.Warnings),
		Problems:        len(result.Problems),
		Recommendations: len(result.Recommendations),
		ProbeRequested:  result.Probe.Requested,
		ProbeStructured: result.Probe.Structured,
		ProbeOK:         result.Probe.OK,
	}
	if result.Runtime.ModelFound {
		report.RuntimeModel = result.Runtime.MatchedModel.ID
		report.RuntimeLoaded = result.Runtime.Loaded
		report.RuntimeContextLength = result.Runtime.ContextLength
		report.RuntimeMaxContext = result.Runtime.MaxContextLength
		report.RuntimeQuantization = result.Runtime.MatchedModel.Quantization
	}
	return report
}

func newBenchmarkReportCommand(configPath *string) *cobra.Command {
	var inputs []string
	var format string
	var baselineProfile string
	var caseIDs []string
	var profileNames []string
	var minPassRate float64
	var failOnRegression bool
	var runtimeAuditServer string
	var requireRuntimeAudit bool
	var minRuntimeContext int
	var maxRuntimeWarnings int
	var maxRuntimeRecommendations int
	var requireRuntimeLoaded bool
	var requireRuntimeProbeOK bool
	var requireRuntimeStructuredProbe bool

	command := &cobra.Command{
		Use:   "report",
		Short: "保存済み benchmark record を集計して回帰を確認",
		RunE: func(cmd *cobra.Command, args []string) error {
			if minPassRate < -1 || minPassRate > 1 {
				return fmt.Errorf("--min-pass-rate は 0.0 から 1.0 の範囲で指定してください")
			}
			records, err := benchmarkusecase.LoadRecordFiles(inputs)
			if err != nil {
				return err
			}
			var minPassRatePtr *float64
			if minPassRate >= 0 {
				minPassRatePtr = &minPassRate
			}
			var runtimeAudit *benchmarkusecase.RecordRuntimeAuditSummary
			if shouldLoadBenchmarkRuntimeAudit(benchmarkRuntimeAuditOptions{
				ServerName:             runtimeAuditServer,
				RequireAudit:           requireRuntimeAudit,
				MinContext:             minRuntimeContext,
				MaxWarnings:            intPtrFromFlag(maxRuntimeWarnings),
				MaxRecommendations:     intPtrFromFlag(maxRuntimeRecommendations),
				RequireLoaded:          requireRuntimeLoaded,
				RequireProbeOK:         requireRuntimeProbeOK,
				RequireStructuredProbe: requireRuntimeStructuredProbe,
			}) {
				audit, err := loadLatestBenchmarkRuntimeAudit(cmd.Context(), *configPath, runtimeAuditServer)
				if err != nil {
					return err
				}
				if audit != nil {
					summary := runtimeAuditSummaryFromDoctorAudit(*audit)
					runtimeAudit = &summary
				}
			}
			report := benchmarkusecase.BuildRecordReport(records, benchmarkusecase.RecordReportOptions{
				Inputs:                        inputs,
				ProfileNames:                  profileNames,
				CaseIDs:                       caseIDs,
				BaselineProfile:               baselineProfile,
				MinPassRate:                   minPassRatePtr,
				FailOnRegression:              failOnRegression,
				RuntimeAudit:                  runtimeAudit,
				RequireRuntimeAudit:           requireRuntimeAudit,
				MinRuntimeContext:             minRuntimeContext,
				MaxRuntimeWarnings:            intPtrFromFlag(maxRuntimeWarnings),
				MaxRuntimeRecommendations:     intPtrFromFlag(maxRuntimeRecommendations),
				RequireRuntimeLoaded:          requireRuntimeLoaded,
				RequireRuntimeProbeOK:         requireRuntimeProbeOK,
				RequireRuntimeStructuredProbe: requireRuntimeStructuredProbe,
			})

			switch format {
			case "text", "table":
				fmt.Print(renderBenchmarkRecordReport(report))
			case "json":
				data, err := json.MarshalIndent(report, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(data))
			case "csv":
				text, err := renderBenchmarkRecordReportCSV(report)
				if err != nil {
					return err
				}
				fmt.Print(text)
			case "junit":
				return benchmarkusecase.WriteRecordReportJUnit(os.Stdout, report)
			default:
				return fmt.Errorf("unsupported format %q", format)
			}

			if report.HasGateFailures() {
				return fmt.Errorf("benchmark report gate failed: thresholds=%d runtime=%d regressions=%d", len(report.FailedThresholds), len(report.RuntimeGateFailures), len(report.Regressions))
			}
			return nil
		},
	}
	command.Flags().StringSliceVar(&inputs, "input", nil, "読み込む benchmark JSONL/CSV record file。複数指定できます")
	command.Flags().StringVar(&format, "format", "text", "出力形式: text, json, csv, junit")
	command.Flags().StringVar(&baselineProfile, "baseline", "", "差分比較の基準 profile 名")
	command.Flags().StringSliceVar(&caseIDs, "case", nil, "集計する case ID")
	command.Flags().StringSliceVar(&profileNames, "profile", nil, "集計する profile 名")
	command.Flags().Float64Var(&minPassRate, "min-pass-rate", -1, "各 group に要求する pass rate。0.0-1.0。未指定は無効")
	command.Flags().BoolVar(&failOnRegression, "fail-on-regression", false, "baseline より pass rate が下がる、または verification failure が増えたら失敗")
	command.Flags().StringVar(&runtimeAuditServer, "runtime-audit-server", "", "保存済み doctor runtime audit を server 名で読み込み、report に添付する")
	command.Flags().BoolVar(&requireRuntimeAudit, "require-runtime-audit", false, "runtime audit が見つからなければ report gate を失敗にする")
	command.Flags().IntVar(&minRuntimeContext, "min-runtime-context", 0, "runtime audit の loaded context length 下限。0 なら無効")
	maxRuntimeWarnings = -1
	maxRuntimeRecommendations = -1
	command.Flags().IntVar(&maxRuntimeWarnings, "max-runtime-warnings", -1, "runtime audit の warning 数上限。-1 なら無効")
	command.Flags().IntVar(&maxRuntimeRecommendations, "max-runtime-recommendations", -1, "runtime audit の recommendation 数上限。-1 なら無効")
	command.Flags().BoolVar(&requireRuntimeLoaded, "require-runtime-loaded", false, "runtime audit で model loaded=true を要求する")
	command.Flags().BoolVar(&requireRuntimeProbeOK, "require-runtime-probe-ok", false, "runtime audit で probe ok を要求する")
	command.Flags().BoolVar(&requireRuntimeStructuredProbe, "require-runtime-structured-probe", false, "runtime audit で structured probe ok を要求する")
	return command
}

type benchmarkRuntimeAuditOptions struct {
	ServerName             string
	RequireAudit           bool
	MinContext             int
	MaxWarnings            *int
	MaxRecommendations     *int
	RequireLoaded          bool
	RequireProbeOK         bool
	RequireStructuredProbe bool
}

func shouldLoadBenchmarkRuntimeAudit(options benchmarkRuntimeAuditOptions) bool {
	return strings.TrimSpace(options.ServerName) != "" ||
		options.RequireAudit ||
		options.MinContext > 0 ||
		options.MaxWarnings != nil ||
		options.MaxRecommendations != nil ||
		options.RequireLoaded ||
		options.RequireProbeOK ||
		options.RequireStructuredProbe
}

func loadLatestBenchmarkRuntimeAudit(ctx context.Context, configPath string, serverName string) (*llmcheck.AuditRecord, error) {
	store, err := openAuditStore(configPath)
	if err != nil {
		return nil, err
	}
	items, err := store.ListScratch(ctx, 100)
	if err != nil {
		return nil, err
	}
	records := doctorAuditRecordsFromScratch(items, serverName)
	if len(records) == 0 {
		return nil, nil
	}
	return &records[0], nil
}

func runtimeAuditSummaryFromDoctorAudit(record llmcheck.AuditRecord) benchmarkusecase.RecordRuntimeAuditSummary {
	summary := benchmarkusecase.RecordRuntimeAuditSummary{
		ID:              record.ID,
		ServerName:      record.ServerName,
		URL:             record.URL,
		API:             record.API,
		Model:           record.Model,
		MatchedModel:    record.MatchedModel,
		Warnings:        len(record.Warnings),
		Problems:        len(record.Problems),
		Recommendations: len(record.Recommendations),
		ProbeRequested:  record.Probe.Requested,
		ProbeStructured: record.Probe.Structured,
		ProbeOK:         record.Probe.OK,
		CreatedAt:       record.CreatedAt,
	}
	if record.Runtime.ModelFound {
		summary.RuntimeLoaded = record.Runtime.Loaded
		summary.RuntimeContext = record.Runtime.ContextLength
		summary.RuntimeMaxContext = record.Runtime.MaxContextLength
		summary.RuntimeQuantization = record.Runtime.MatchedModel.Quantization
	}
	return summary
}

func intPtrFromFlag(value int) *int {
	if value < 0 {
		return nil
	}
	return &value
}

func resolveBenchmarkCases(ids []string, files []string) ([]benchmarkusecase.Case, error) {
	loaded := []benchmarkusecase.Case{}
	for _, path := range files {
		items, err := benchmarkusecase.LoadCaseFile(path)
		if err != nil {
			return nil, err
		}
		loaded = append(loaded, items...)
	}
	if len(ids) == 0 {
		if len(loaded) > 0 {
			return loaded, nil
		}
		return nil, nil
	}

	available := append(benchmarkusecase.BuiltinCases(), loaded...)
	return benchmarkusecase.SelectCases(available, ids)
}

func resolveBenchmarkProfiles(base config.FeaturesConfig, names []string, routingCandidates []string) ([]benchmarkusecase.Profile, error) {
	if len(names) == 0 {
		if len(routingCandidates) == 0 {
			names = []string{"legacy", "current"}
		} else {
			names = []string{"current"}
		}
	}

	featureProfiles := make([]benchmarkusecase.Profile, 0, len(names))
	for _, name := range names {
		profile, err := resolveBenchmarkProfile(base, name)
		if err != nil {
			return nil, err
		}
		featureProfiles = append(featureProfiles, profile)
	}
	if len(routingCandidates) == 0 {
		return featureProfiles, nil
	}

	profiles := make([]benchmarkusecase.Profile, 0, len(featureProfiles)*len(routingCandidates))
	for _, featureProfile := range featureProfiles {
		for _, candidate := range routingCandidates {
			routingName, routingProfile, model, err := parseRoutingCandidate(candidate)
			if err != nil {
				return nil, err
			}
			profile := featureProfile
			profile.Name = featureProfile.Name + "@" + routingName
			profile.Description = strings.TrimSpace(featureProfile.Description + " / routing profile " + routingProfile)
			profile.RoutingProfile = routingProfile
			profile.Model = model
			profiles = append(profiles, profile)
		}
	}
	return profiles, nil
}

func resolveBenchmarkProfile(base config.FeaturesConfig, name string) (benchmarkusecase.Profile, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "current", "modern":
		return benchmarkusecase.Profile{
			Name:        "current",
			Description: "configured multi-agent harness",
			Features:    base,
		}, nil
	case "legacy", "baseline":
		return benchmarkusecase.Profile{
			Name:        "legacy",
			Description: "single-manager baseline without new features",
			Features: config.FeaturesConfig{
				PhaseHarness:       false,
				AdaptiveCompaction: false,
				RoleRouting:        false,
				RepoMemory:         false,
			},
		}, nil
	case "no-harness":
		base.PhaseHarness = false
		return benchmarkusecase.Profile{Name: "no-harness", Description: "disable phase harness", Features: base}, nil
	case "no-routing":
		base.RoleRouting = false
		return benchmarkusecase.Profile{Name: "no-routing", Description: "disable role routing", Features: base}, nil
	case "no-memory":
		base.RepoMemory = false
		return benchmarkusecase.Profile{Name: "no-memory", Description: "disable repo memory", Features: base}, nil
	case "no-compaction":
		base.AdaptiveCompaction = false
		return benchmarkusecase.Profile{Name: "no-compaction", Description: "disable adaptive compaction", Features: base}, nil
	default:
		return benchmarkusecase.Profile{}, fmt.Errorf("unknown feature profile %q", name)
	}
}

func parseRoutingCandidate(value string) (name string, routingProfile string, model string, err error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", "", fmt.Errorf("routing candidate が空です")
	}
	if strings.Contains(value, "=") {
		parts := strings.SplitN(value, "=", 2)
		name = strings.TrimSpace(parts[0])
		value = strings.TrimSpace(parts[1])
	}
	parts := strings.SplitN(value, ":", 2)
	routingProfile = strings.TrimSpace(parts[0])
	if len(parts) == 2 {
		model = strings.TrimSpace(parts[1])
	}
	if routingProfile == "" {
		return "", "", "", fmt.Errorf("routing candidate %q に routing profile がありません", value)
	}
	if name == "" {
		name = routingProfile
	}
	return name, routingProfile, model, nil
}

func renderBenchmarkTable(report benchmarkusecase.Report) string {
	var sb strings.Builder
	if len(report.Cases) == 1 && report.Cases[0].ID == "prompt" {
		sb.WriteString(fmt.Sprintf("Prompt: %s\n", report.Prompt))
	} else {
		sb.WriteString("Cases:\n")
		for _, item := range report.Cases {
			sb.WriteString(fmt.Sprintf("  - %s  %s\n", item.ID, fallbackBenchmarkString(item.Name, item.Description)))
		}
	}
	sb.WriteString(fmt.Sprintf("Runs per case/profile: %d\n\n", report.Runs))
	sb.WriteString("Profile       Success  Eval Pass  Avg Time  Avg Events  Avg Attempt  Verify Fails  Features\n")
	for _, result := range report.Results {
		sb.WriteString(fmt.Sprintf(
			"%-12s  %d/%-4d  %d/%-7d %-8s  %-10.1f %-12.1f %-13d %s\n",
			result.Profile.Name,
			result.Summary.Successes,
			result.Summary.Runs,
			result.Summary.EvaluationPasses,
			result.Summary.Runs,
			result.Summary.AvgDuration.Round(time.Millisecond),
			result.Summary.AvgEvents,
			result.Summary.AvgAttempt,
			result.Summary.VerificationFailures,
			featureSummary(result.Profile.Features),
		))
		for _, run := range result.Runs {
			sb.WriteString(fmt.Sprintf(
				"  %s run %-3d %s eval=%s  %s  phase=%s  attempt=%d  tools=%d  artifacts=%d\n",
				fallbackBenchmarkString(run.CaseID, "prompt"),
				run.Index,
				run.Status,
				formatBenchmarkBool(run.Evaluation.Passed),
				run.Duration.Round(time.Millisecond),
				run.Phase,
				run.Attempt,
				run.ToolCalls,
				run.Artifacts,
			))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func renderBenchmarkCases(cases []benchmarkusecase.Case) string {
	if len(cases) == 0 {
		cases = benchmarkusecase.BuiltinCases()
	}
	var sb strings.Builder
	sb.WriteString("Benchmark cases\n")
	for _, item := range cases {
		sb.WriteString(fmt.Sprintf("- %s", item.ID))
		if item.Name != "" {
			sb.WriteString("  " + item.Name)
		}
		if item.Description != "" {
			sb.WriteString("\n  " + item.Description)
		}
		if len(item.Tags) > 0 {
			sb.WriteString("\n  tags: " + strings.Join(item.Tags, ","))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func renderBenchmarkRecordReport(report benchmarkusecase.RecordReport) string {
	var sb strings.Builder
	sb.WriteString("Benchmark record report\n")
	sb.WriteString(fmt.Sprintf("  records: %d\n", report.Records))
	if len(report.Inputs) > 0 {
		sb.WriteString("  inputs: " + strings.Join(report.Inputs, ", ") + "\n")
	}
	if report.BaselineProfile != "" {
		sb.WriteString("  baseline: " + report.BaselineProfile + "\n")
	}
	if report.RuntimeAudit != nil {
		audit := report.RuntimeAudit
		sb.WriteString(fmt.Sprintf(
			"  runtime: server=%s model=%s context=%d/%d quant=%s warnings=%d recommendations=%d\n",
			fallbackBenchmarkString(audit.ServerName, "-"),
			fallbackBenchmarkString(audit.MatchedModel, audit.Model),
			audit.RuntimeContext,
			audit.RuntimeMaxContext,
			fallbackBenchmarkString(audit.RuntimeQuantization, "-"),
			audit.Warnings,
			audit.Recommendations,
		))
	}
	sb.WriteString("\n")
	sb.WriteString("Profile       Case             Runs  Pass Rate  Success  Avg Time  Avg Tools  Avg LLM  Avg Tx  Tx Fails  Fallbacks  Verify Fails  Preflight        Delta        Models\n")
	for _, group := range report.Groups {
		delta := "-"
		if group.BaselineDelta != nil {
			delta = fmt.Sprintf("pass %s, verify %+d", formatSignedRate(group.BaselineDelta.PassRate), group.BaselineDelta.VerificationFailures)
		}
		preflight := "-"
		if group.PreflightDoctor {
			preflight = fmt.Sprintf("ctx=%d warn=%d rec=%d", group.PreflightRuntimeContextLength, group.PreflightDoctorWarnings, group.PreflightDoctorRecommendations)
		}
		sb.WriteString(fmt.Sprintf(
			"%-12s  %-15s  %-4d  %-9s  %-7s  %-8s  %-9.1f  %-7.1f  %-7.1f %-8d  %-9d  %-12d  %-16s %-12s %s\n",
			group.ProfileName,
			fallbackBenchmarkString(group.CaseID, "-"),
			group.Runs,
			formatRate(group.PassRate),
			formatRate(group.SuccessRate),
			(time.Duration(group.AvgDurationMS) * time.Millisecond).Round(time.Millisecond),
			group.AvgToolCalls,
			group.AvgModelCalls,
			group.AvgModelTransportAttempts,
			group.ModelTransportFailures,
			group.ModelFallbacks,
			group.VerificationFailures,
			preflight,
			delta,
			recordModelSummary(group),
		))
	}
	if len(report.FailedThresholds) > 0 {
		sb.WriteString("\nThreshold failures\n")
		for _, failure := range report.FailedThresholds {
			sb.WriteString(fmt.Sprintf("  - %s/%s %s got=%s want>=%s\n", failure.ProfileName, failure.CaseID, failure.Metric, formatRate(failure.Got), formatRate(failure.Want)))
		}
	}
	if len(report.RuntimeGateFailures) > 0 {
		sb.WriteString("\nRuntime gate failures\n")
		for _, failure := range report.RuntimeGateFailures {
			sb.WriteString(fmt.Sprintf("  - %s got=%s want=%s\n", failure.Metric, fallbackBenchmarkString(failure.Got, "-"), fallbackBenchmarkString(failure.Want, "-")))
		}
	}
	if len(report.Regressions) > 0 {
		sb.WriteString("\nRegressions\n")
		for _, regression := range report.Regressions {
			sb.WriteString(fmt.Sprintf("  - %s/%s %s %s\n", regression.ProfileName, regression.CaseID, regression.Metric, regression.Detail))
		}
	}
	return sb.String()
}

func recordModelSummary(group benchmarkusecase.RecordGroupSummary) string {
	parts := []string{}
	if len(group.ModelServers) > 0 {
		parts = append(parts, "servers="+strings.Join(group.ModelServers, "|"))
	}
	if len(group.ModelNames) > 0 {
		parts = append(parts, "models="+strings.Join(group.ModelNames, "|"))
	}
	if len(group.ModelAPIs) > 0 {
		parts = append(parts, "apis="+strings.Join(group.ModelAPIs, "|"))
	}
	parts = append(parts,
		fmt.Sprintf("usage=%.0f%%", group.ModelUsageCoverage*100),
		fmt.Sprintf("tokens=%.0f", group.AvgModelTotalTokens),
		fmt.Sprintf("actions=%.1f/%.1f/%.1f/%.1f", group.AvgMutations, group.AvgPermissionRequests, group.AvgDelegations, group.AvgHandoffs),
	)
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

func renderBenchmarkRecordReportCSV(report benchmarkusecase.RecordReport) (string, error) {
	var sb strings.Builder
	writer := csv.NewWriter(&sb)
	if err := writer.Write([]string{
		"profile",
		"routing_profile",
		"model",
		"case_id",
		"case_name",
		"runs",
		"passes",
		"pass_rate",
		"success_rate",
		"avg_duration_ms",
		"avg_events",
		"avg_tool_calls",
		"avg_model_calls",
		"avg_model_duration_ms",
		"avg_model_transport_attempts",
		"model_transport_failures",
		"avg_model_transport_duration_ms",
		"model_usage_available",
		"model_usage_unavailable",
		"model_usage_coverage",
		"avg_model_input_tokens",
		"avg_model_output_tokens",
		"avg_model_total_tokens",
		"avg_model_cached_input_tokens",
		"avg_model_reasoning_tokens",
		"model_fallbacks",
		"model_servers",
		"model_names",
		"model_apis",
		"model_profiles",
		"avg_mutations",
		"avg_permission_requests",
		"avg_delegations",
		"avg_handoffs",
		"failed_events",
		"verification_failures",
		"baseline_pass_rate_delta",
		"baseline_avg_model_total_tokens_delta",
		"baseline_model_usage_coverage_delta",
		"baseline_verification_failures_delta",
		"preflight_doctor",
		"preflight_doctor_server",
		"preflight_doctor_model",
		"preflight_doctor_warnings",
		"preflight_doctor_recommendations",
		"preflight_runtime_context_length",
		"preflight_runtime_quantization",
		"runtime_audit_server",
		"runtime_audit_model",
		"runtime_audit_context",
		"runtime_audit_quantization",
		"runtime_audit_warnings",
		"runtime_audit_recommendations",
		"failed_expectations",
	}); err != nil {
		return "", err
	}
	for _, group := range report.Groups {
		passRateDelta := ""
		verifyDelta := ""
		tokenDelta := ""
		usageDelta := ""
		if group.BaselineDelta != nil {
			passRateDelta = fmt.Sprintf("%.6f", group.BaselineDelta.PassRate)
			tokenDelta = fmt.Sprintf("%.1f", group.BaselineDelta.AvgModelTotalTokens)
			usageDelta = fmt.Sprintf("%.6f", group.BaselineDelta.ModelUsageCoverage)
			verifyDelta = fmt.Sprint(group.BaselineDelta.VerificationFailures)
		}
		runtimeServer := ""
		runtimeModel := ""
		runtimeContext := ""
		runtimeQuantization := ""
		runtimeWarnings := ""
		runtimeRecommendations := ""
		if report.RuntimeAudit != nil {
			runtimeServer = report.RuntimeAudit.ServerName
			runtimeModel = fallbackBenchmarkString(report.RuntimeAudit.MatchedModel, report.RuntimeAudit.Model)
			runtimeContext = fmt.Sprint(report.RuntimeAudit.RuntimeContext)
			runtimeQuantization = report.RuntimeAudit.RuntimeQuantization
			runtimeWarnings = fmt.Sprint(report.RuntimeAudit.Warnings)
			runtimeRecommendations = fmt.Sprint(report.RuntimeAudit.Recommendations)
		}
		if err := writer.Write([]string{
			group.ProfileName,
			group.RoutingProfile,
			group.Model,
			group.CaseID,
			group.CaseName,
			fmt.Sprint(group.Runs),
			fmt.Sprint(group.Passes),
			fmt.Sprintf("%.6f", group.PassRate),
			fmt.Sprintf("%.6f", group.SuccessRate),
			fmt.Sprintf("%.1f", group.AvgDurationMS),
			fmt.Sprintf("%.1f", group.AvgEvents),
			fmt.Sprintf("%.1f", group.AvgToolCalls),
			fmt.Sprintf("%.1f", group.AvgModelCalls),
			fmt.Sprintf("%.1f", group.AvgModelDurationMS),
			fmt.Sprintf("%.1f", group.AvgModelTransportAttempts),
			fmt.Sprint(group.ModelTransportFailures),
			fmt.Sprintf("%.1f", group.AvgModelTransportDurationMS),
			fmt.Sprint(group.ModelUsageAvailable),
			fmt.Sprint(group.ModelUsageUnavailable),
			fmt.Sprintf("%.6f", group.ModelUsageCoverage),
			fmt.Sprintf("%.1f", group.AvgModelInputTokens),
			fmt.Sprintf("%.1f", group.AvgModelOutputTokens),
			fmt.Sprintf("%.1f", group.AvgModelTotalTokens),
			fmt.Sprintf("%.1f", group.AvgModelCachedInputTokens),
			fmt.Sprintf("%.1f", group.AvgModelReasoningTokens),
			fmt.Sprint(group.ModelFallbacks),
			strings.Join(group.ModelServers, "|"),
			strings.Join(group.ModelNames, "|"),
			strings.Join(group.ModelAPIs, "|"),
			strings.Join(group.ModelProfiles, "|"),
			fmt.Sprintf("%.1f", group.AvgMutations),
			fmt.Sprintf("%.1f", group.AvgPermissionRequests),
			fmt.Sprintf("%.1f", group.AvgDelegations),
			fmt.Sprintf("%.1f", group.AvgHandoffs),
			fmt.Sprint(group.FailedEvents),
			fmt.Sprint(group.VerificationFailures),
			passRateDelta,
			tokenDelta,
			usageDelta,
			verifyDelta,
			fmt.Sprint(group.PreflightDoctor),
			group.PreflightDoctorServer,
			group.PreflightDoctorModel,
			fmt.Sprint(group.PreflightDoctorWarnings),
			fmt.Sprint(group.PreflightDoctorRecommendations),
			fmt.Sprint(group.PreflightRuntimeContextLength),
			group.PreflightRuntimeQuantization,
			runtimeServer,
			runtimeModel,
			runtimeContext,
			runtimeQuantization,
			runtimeWarnings,
			runtimeRecommendations,
			strings.Join(group.FailedExpectations, "|"),
		}); err != nil {
			return "", err
		}
	}
	writer.Flush()
	return sb.String(), writer.Error()
}

func writeBenchmarkFile(path string, write func(*os.File) error) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("benchmark output file の作成に失敗しました: %w", err)
	}
	defer file.Close()
	if err := write(file); err != nil {
		return err
	}
	return nil
}

func featureSummary(features config.FeaturesConfig) string {
	flags := []string{}
	if features.PhaseHarness {
		flags = append(flags, "harness")
	}
	if features.AdaptiveCompaction {
		flags = append(flags, "compaction")
	}
	if features.RoleRouting {
		flags = append(flags, "routing")
	}
	if features.RepoMemory {
		flags = append(flags, "memory")
	}
	if len(flags) == 0 {
		return "(all off)"
	}
	return strings.Join(flags, ",")
}

func formatBenchmarkBool(value bool) string {
	if value {
		return "pass"
	}
	return "fail"
}

func formatRate(value float64) string {
	return fmt.Sprintf("%.0f%%", value*100)
}

func formatSignedRate(value float64) string {
	return fmt.Sprintf("%+.0f%%", value*100)
}

func fallbackBenchmarkString(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
