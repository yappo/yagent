package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"yagent/internal/app"
	"yagent/internal/config"
	"yagent/internal/domain"
	benchmarkusecase "yagent/internal/usecase/benchmark"
)

func newBenchmarkCommand(configPath *string, logPath *string) *cobra.Command {
	var prompt string
	var model string
	var routingProfile string
	var resumeID string
	var output string
	var runs int
	var featureProfiles []string

	command := &cobra.Command{
		Use:   "benchmark",
		Short: "feature profile ごとの差分をベンチマーク",
	}

	command.Flags().StringVar(&prompt, "prompt", "", "benchmark に使うプロンプト")
	command.Flags().StringVar(&model, "model", "", "使用するモデル名")
	command.Flags().StringVar(&routingProfile, "profile", "", "routing profile 名")
	command.Flags().StringVar(&resumeID, "resume", "", "復元する run id。latest も指定できます")
	command.Flags().StringVar(&output, "output", "table", "出力形式: table または json")
	command.Flags().IntVar(&runs, "runs", 0, "各 feature profile を何回実行するか。0 なら config の既定値")
	command.Flags().StringSliceVar(&featureProfiles, "feature-profile", nil, "比較する feature profile 名")
	_ = command.MarkFlagRequired("prompt")

	command.RunE = func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(*configPath)
		if err != nil {
			return err
		}
		if runs <= 0 {
			runs = cfg.Benchmark.DefaultRuns
		}

		profiles, err := resolveBenchmarkProfiles(cfg.Features, featureProfiles)
		if err != nil {
			return err
		}

		report, err := benchmarkusecase.Execute(cmd.Context(), cfg, benchmarkusecase.Request{
			Prompt:         prompt,
			Model:          model,
			ResumeID:       resumeID,
			Runs:           runs,
			RoutingProfile: routingProfile,
			Profiles:       profiles,
		}, func(runCfg config.Config) (domain.Orchestrator, func(), error) {
			container, err := app.BuildFromConfig(runCfg, NewStdinApprover(), app.BuildOptions{LogPath: *logPath})
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
		default:
			return fmt.Errorf("unsupported output %q", output)
		}
	}

	return command
}

func resolveBenchmarkProfiles(base config.FeaturesConfig, names []string) ([]benchmarkusecase.Profile, error) {
	if len(names) == 0 {
		names = []string{"legacy", "current"}
	}

	profiles := make([]benchmarkusecase.Profile, 0, len(names))
	for _, name := range names {
		profile, err := resolveBenchmarkProfile(base, name)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
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

func renderBenchmarkTable(report benchmarkusecase.Report) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Prompt: %s\n", report.Prompt))
	sb.WriteString(fmt.Sprintf("Runs per profile: %d\n\n", report.Runs))
	sb.WriteString("Profile       Success  Avg Time  Avg Events  Avg Attempt  Verify Fails  Features\n")
	for _, result := range report.Results {
		sb.WriteString(fmt.Sprintf(
			"%-12s  %d/%-4d  %-8s  %-10.1f %-12.1f %-13d %s\n",
			result.Profile.Name,
			result.Summary.Successes,
			result.Summary.Runs,
			result.Summary.AvgDuration.Round(time.Millisecond),
			result.Summary.AvgEvents,
			result.Summary.AvgAttempt,
			result.Summary.VerificationFailures,
			featureSummary(result.Profile.Features),
		))
		for _, run := range result.Runs {
			sb.WriteString(fmt.Sprintf(
				"  run %-6d %s  %s  phase=%s  attempt=%d  tools=%d  artifacts=%d\n",
				run.Index,
				run.Status,
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
