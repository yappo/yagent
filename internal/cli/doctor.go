package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"yagent/internal/config"
	"yagent/internal/domain"
	"yagent/internal/usecase/llmcheck"
)

func newDoctorCommand(configPath *string) *cobra.Command {
	var serverName string
	var probe bool
	var probeStructured bool
	var runtime bool
	var format string
	var failOnWarning bool
	var failOnRecommendation bool
	var recommendedConfigPath string
	var forceRecommendedConfig bool
	var saveAudit bool

	command := &cobra.Command{
		Use:   "doctor",
		Short: "local LLM 接続を診断",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(*configPath)
			if err != nil {
				return err
			}
			if recommendedConfigPath != "" || saveAudit {
				runtime = true
			}
			result, err := llmcheck.New(nil).CheckWithOptions(cmd.Context(), cfg, llmcheck.CheckOptions{
				ServerName:      serverName,
				Probe:           probe,
				ProbeStructured: probeStructured,
				Runtime:         runtime,
			})
			if err != nil {
				return err
			}
			switch format {
			case "text":
				fmt.Print(renderDoctorResult(result))
			case "json":
				data, err := json.MarshalIndent(result, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(data))
			default:
				return fmt.Errorf("unsupported format %q", format)
			}
			if saveAudit {
				store, err := openAuditStore(*configPath)
				if err != nil {
					return err
				}
				record := llmcheck.NewAuditRecord(result, time.Now())
				if err := saveDoctorAudit(cmd.Context(), store, record); err != nil {
					return err
				}
				fmt.Fprint(os.Stderr, renderDoctorAuditSavedResult(record))
			}
			if recommendedConfigPath != "" {
				if len(result.Problems) > 0 {
					return doctorGateError(result, doctorGateOptions{})
				}
				plan := llmcheck.BuildRecommendedConfig(cfg, result)
				writeResult, err := llmcheck.WriteRecommendedConfig(recommendedConfigPath, plan, forceRecommendedConfig)
				if err != nil {
					return err
				}
				fmt.Fprint(os.Stderr, renderDoctorRecommendedConfigResult(plan, writeResult))
			}
			if err := doctorGateError(result, doctorGateOptions{
				FailOnWarning:        failOnWarning,
				FailOnRecommendation: failOnRecommendation,
			}); err != nil {
				return err
			}
			return nil
		},
	}
	command.Flags().StringVar(&serverName, "server", "", "診断する server 名。未指定なら server.default")
	command.Flags().StringVar(&format, "format", "text", "出力形式: text または json")
	command.Flags().BoolVar(&probe, "probe", false, "軽い生成リクエストを送って model runtime を確認する")
	command.Flags().BoolVar(&probeStructured, "probe-structured", false, "JSON schema structured output の軽い probe を送る。--probe を兼ねます")
	command.Flags().BoolVar(&runtime, "runtime", false, "LM Studio REST API から load 状態 / context / quantization / capability を確認する")
	command.Flags().BoolVar(&failOnWarning, "fail-on-warning", false, "warning があれば exit status を失敗にする")
	command.Flags().BoolVar(&failOnRecommendation, "fail-on-recommendation", false, "recommendation があれば exit status を失敗にする")
	command.Flags().StringVar(&recommendedConfigPath, "write-recommended-config", "", "doctor recommendations を反映した complete config TOML を指定 path に書き出す。--runtime を兼ねます")
	command.Flags().BoolVar(&forceRecommendedConfig, "force-recommended-config", false, "既存の recommended config path を上書きする")
	command.Flags().BoolVar(&saveAudit, "save-audit", false, "診断結果を memory.state_dir の runtime audit に保存する。--runtime を兼ねます")
	return command
}

type doctorGateOptions struct {
	FailOnWarning        bool
	FailOnRecommendation bool
}

func doctorGateError(result llmcheck.Result, options doctorGateOptions) error {
	if len(result.Problems) > 0 {
		return fmt.Errorf("doctor found %d problem(s)", len(result.Problems))
	}
	if options.FailOnWarning && len(result.Warnings) > 0 {
		return fmt.Errorf("doctor found %d warning(s)", len(result.Warnings))
	}
	if options.FailOnRecommendation && len(result.Recommendations) > 0 {
		return fmt.Errorf("doctor found %d recommendation(s)", len(result.Recommendations))
	}
	return nil
}

func renderDoctorResult(result llmcheck.Result) string {
	var sb strings.Builder
	sb.WriteString("LLM doctor\n")
	sb.WriteString(fmt.Sprintf("  server: %s\n", result.ServerName))
	sb.WriteString(fmt.Sprintf("  url:    %s\n", result.URL))
	sb.WriteString(fmt.Sprintf("  api:    %s\n", result.API))
	sb.WriteString(fmt.Sprintf("  model:  %s\n", result.Model))
	sb.WriteString(fmt.Sprintf("  check:  GET %s\n", result.Endpoint))
	if len(result.Problems) == 0 {
		sb.WriteString("  status: ok\n")
	} else {
		sb.WriteString("  status: needs attention\n")
	}
	if len(result.Models) > 0 {
		sb.WriteString(fmt.Sprintf("  models: %d available\n", len(result.Models)))
		for _, model := range firstStrings(result.Models, 8) {
			prefix := "    - "
			if model == result.MatchedModel {
				prefix = "    * "
			}
			sb.WriteString(prefix + model + "\n")
		}
	}
	if result.ModelFound {
		matchKind := "fuzzy"
		if result.ModelExactMatch {
			matchKind = "exact"
		}
		sb.WriteString(fmt.Sprintf("  match:  %s (%s)\n", result.MatchedModel, matchKind))
	}
	if result.Runtime.Requested {
		sb.WriteString(fmt.Sprintf("  runtime_check: GET %s\n", result.Runtime.Endpoint))
		if result.Runtime.Error != "" {
			sb.WriteString("  runtime_status: unavailable\n")
		} else {
			sb.WriteString("  runtime_status: ok\n")
			sb.WriteString(fmt.Sprintf("  runtime_models: %d available\n", len(result.Runtime.Models)))
			if result.Runtime.ModelFound {
				model := result.Runtime.MatchedModel
				sb.WriteString(fmt.Sprintf("  runtime_match: %s\n", fallbackDoctorString(model.ID, "-")))
				sb.WriteString(fmt.Sprintf("  runtime_loaded: %t\n", result.Runtime.Loaded))
				if result.Runtime.LoadedInstance != "" {
					sb.WriteString(fmt.Sprintf("  runtime_instance: %s\n", result.Runtime.LoadedInstance))
				}
				if result.Runtime.ContextLength > 0 || result.Runtime.MaxContextLength > 0 {
					sb.WriteString(fmt.Sprintf("  runtime_context: %d/%d tokens\n", result.Runtime.ContextLength, result.Runtime.MaxContextLength))
				}
				if model.Quantization != "" || model.Format != "" || model.Params != "" {
					sb.WriteString(fmt.Sprintf(
						"  runtime_model: %s %s %s\n",
						fallbackDoctorString(model.Params, "-"),
						fallbackDoctorString(model.Quantization, "-"),
						fallbackDoctorString(model.Format, "-"),
					))
				}
				if model.TrainedForToolUse != nil {
					sb.WriteString(fmt.Sprintf("  runtime_tool_use: %t\n", *model.TrainedForToolUse))
				}
				if len(model.ReasoningAllowed) > 0 {
					sb.WriteString(fmt.Sprintf("  runtime_reasoning: %s default=%s\n", strings.Join(model.ReasoningAllowed, ","), fallbackDoctorString(model.ReasoningDefault, "-")))
				}
			}
		}
	}
	if result.Probe.Requested {
		sb.WriteString(fmt.Sprintf("  probe:  POST %s\n", result.Probe.Endpoint))
		sb.WriteString(fmt.Sprintf("  probe_model: %s\n", result.Probe.Model))
		if result.Probe.Structured {
			sb.WriteString("  probe_format: json_schema\n")
		} else {
			sb.WriteString("  probe_format: text\n")
		}
		if result.Probe.OK {
			sb.WriteString("  probe_status: ok\n")
		} else {
			sb.WriteString("  probe_status: failed\n")
		}
		if output := strings.TrimSpace(result.Probe.Output); output != "" {
			sb.WriteString("  probe_output: " + truncateDoctorLine(output, 160) + "\n")
		}
	}
	if len(result.Warnings) > 0 {
		sb.WriteString("\nWarnings:\n")
		for _, warning := range result.Warnings {
			sb.WriteString("  - " + warning + "\n")
		}
	}
	if len(result.Problems) > 0 {
		sb.WriteString("\nProblems:\n")
		for _, problem := range result.Problems {
			sb.WriteString("  - " + problem + "\n")
		}
	}
	if len(result.Recommendations) > 0 {
		sb.WriteString("\nRecommendations:\n")
		for _, item := range result.Recommendations {
			sb.WriteString(fmt.Sprintf("  - [%s] %s: %s -> %s\n", item.Area, item.Setting, fallbackDoctorString(item.Current, "-"), item.Recommended))
			if item.Reason != "" {
				sb.WriteString("    " + item.Reason + "\n")
			}
		}
	}
	if len(result.Suggestions) > 0 {
		sb.WriteString("\nSuggestions:\n")
		for _, suggestion := range uniqueStrings(result.Suggestions) {
			sb.WriteString("  - " + suggestion + "\n")
		}
	}
	return sb.String()
}

func renderDoctorRecommendedConfigResult(plan llmcheck.ConfigRecommendationPlan, result llmcheck.ConfigWriteResult) string {
	var sb strings.Builder
	sb.WriteString("\nRecommended config\n")
	sb.WriteString(fmt.Sprintf("  status:  %s\n", result.Status))
	sb.WriteString(fmt.Sprintf("  path:    %s\n", result.Path))
	sb.WriteString(fmt.Sprintf("  bytes:   %d\n", result.Bytes))
	sb.WriteString(fmt.Sprintf("  changes: %d\n", result.Changes))
	if len(plan.Changes) > 0 {
		sb.WriteString("  applied:\n")
		for _, change := range plan.Changes {
			sb.WriteString(fmt.Sprintf("    - %s: %s -> %s\n", change.Setting, fallbackDoctorString(change.Current, "-"), change.Recommended))
		}
	}
	if len(plan.External) > 0 {
		sb.WriteString("  external:\n")
		for _, change := range plan.External {
			sb.WriteString(fmt.Sprintf("    - %s: %s -> %s\n", change.Setting, fallbackDoctorString(change.Current, "-"), change.Recommended))
		}
	}
	return sb.String()
}

func saveDoctorAudit(ctx context.Context, store interface {
	SaveScratch(context.Context, domain.ScratchRecord) error
}, record llmcheck.AuditRecord) error {
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return store.SaveScratch(ctx, domain.ScratchRecord{
		ID:        record.ID,
		Kind:      llmcheck.AuditScratchKind,
		Summary:   llmcheck.AuditSummary(record),
		Payload:   payload,
		CreatedAt: record.CreatedAt,
	})
}

func renderDoctorAuditSavedResult(record llmcheck.AuditRecord) string {
	return fmt.Sprintf("\nRuntime audit\n  status: saved\n  id:     %s\n", record.ID)
}

func truncateDoctorLine(value string, limit int) string {
	value = strings.ReplaceAll(value, "\n", "\\n")
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit-3] + "..."
}

func fallbackDoctorString(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func firstStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return values[:limit]
}

func uniqueStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
