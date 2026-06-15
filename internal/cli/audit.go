package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"yagent/internal/config"
	"yagent/internal/domain"
	"yagent/internal/infra/state"
	"yagent/internal/usecase/llmcheck"
)

func newAuditCommand(configPath *string) *cobra.Command {
	command := &cobra.Command{
		Use:   "audit",
		Short: "保存済み audit record を表示",
	}
	command.AddCommand(newAuditPermissionsCommand(configPath))
	command.AddCommand(newAuditExecutionsCommand(configPath))
	command.AddCommand(newAuditConversationsCommand(configPath))
	command.AddCommand(newAuditRunsCommand(configPath))
	command.AddCommand(newAuditArtifactsCommand(configPath))
	command.AddCommand(newAuditObservationsCommand(configPath))
	command.AddCommand(newAuditMutationsCommand(configPath))
	command.AddCommand(newAuditBundleCommand(configPath))
	command.AddCommand(newAuditTraceCommand(configPath))
	command.AddCommand(newAuditRuntimeCommand(configPath))
	command.AddCommand(newAuditModelsCommand(configPath))
	command.AddCommand(newAuditSearchCommand(configPath))
	return command
}

func newAuditModelsCommand(configPath *string) *cobra.Command {
	var runID string
	var serverName string
	var agentID string
	var format string
	var limit int
	var summary bool

	command := &cobra.Command{
		Use:   "models",
		Short: "LLM model invocation audit を表示",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openAuditStore(*configPath)
			if err != nil {
				return err
			}
			items, err := store.ListScratch(cmd.Context(), limit)
			if err != nil {
				return err
			}
			records := modelInvocationRecordsFromScratch(items, modelInvocationFilter{
				RunID:      runID,
				ServerName: serverName,
				AgentID:    agentID,
			})
			if summary {
				report := summarizeModelInvocations(records)
				switch format {
				case "text":
					fmt.Print(renderModelInvocationSummaryText(report))
				case "json":
					data, err := json.MarshalIndent(report, "", "  ")
					if err != nil {
						return err
					}
					fmt.Println(string(data))
				default:
					return fmt.Errorf("unsupported format: %s", format)
				}
				return nil
			}
			switch format {
			case "text":
				fmt.Print(renderModelInvocationAuditText(records))
			case "json":
				data, err := json.MarshalIndent(records, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(data))
			default:
				return fmt.Errorf("unsupported format: %s", format)
			}
			return nil
		},
	}
	command.Flags().StringVar(&runID, "run", "", "SessionID/RootRunID で絞り込む")
	command.Flags().StringVar(&serverName, "server", "", "server 名で絞り込む")
	command.Flags().StringVar(&agentID, "agent", "", "agent id で絞り込む")
	command.Flags().StringVar(&format, "format", "text", "出力形式: text または json")
	command.Flags().IntVar(&limit, "limit", 100, "読み込む model invocation record 件数")
	command.Flags().BoolVar(&summary, "summary", false, "server/model/profile ごとに model invocation を集計する")
	return command
}

func newAuditRuntimeCommand(configPath *string) *cobra.Command {
	var serverName string
	var format string
	var limit int

	command := &cobra.Command{
		Use:   "runtime",
		Short: "LLM runtime doctor audit を表示",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openAuditStore(*configPath)
			if err != nil {
				return err
			}
			items, err := store.ListScratch(cmd.Context(), limit)
			if err != nil {
				return err
			}
			records := doctorAuditRecordsFromScratch(items, serverName)
			switch format {
			case "text":
				fmt.Print(renderDoctorRuntimeAuditText(records))
			case "json":
				data, err := json.MarshalIndent(records, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(data))
			default:
				return fmt.Errorf("unsupported format: %s", format)
			}
			return nil
		},
	}
	command.Flags().StringVar(&serverName, "server", "", "server 名で絞り込む")
	command.Flags().StringVar(&format, "format", "text", "出力形式: text または json")
	command.Flags().IntVar(&limit, "limit", 100, "読み込む runtime audit record 件数")
	return command
}

func newAuditPermissionsCommand(configPath *string) *cobra.Command {
	var runID string
	var format string
	var limit int

	command := &cobra.Command{
		Use:   "permissions",
		Short: "permission decision audit を表示",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openAuditStore(*configPath)
			if err != nil {
				return err
			}
			items, err := store.ListScratch(cmd.Context(), limit)
			if err != nil {
				return err
			}
			records := permissionRecordsFromScratch(items, runID)
			switch format {
			case "text":
				fmt.Print(renderPermissionAuditText(records))
			case "json":
				data, err := json.MarshalIndent(records, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(data))
			default:
				return fmt.Errorf("unsupported format: %s", format)
			}
			return nil
		},
	}
	command.Flags().StringVar(&runID, "run", "", "RootRunID/SessionID で絞り込む")
	command.Flags().StringVar(&format, "format", "text", "出力形式: text または json")
	command.Flags().IntVar(&limit, "limit", 100, "読み込む scratch record 件数")
	return command
}

func newAuditExecutionsCommand(configPath *string) *cobra.Command {
	var runID string
	var format string
	var limit int
	var includeOutput bool

	command := &cobra.Command{
		Use:   "executions",
		Short: "tool execution audit を表示",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openAuditStore(*configPath)
			if err != nil {
				return err
			}
			records, err := store.ListExecutions(cmd.Context(), limit)
			if err != nil {
				return err
			}
			records = filterExecutionRecords(records, runID, includeOutput)
			switch format {
			case "text":
				fmt.Print(renderExecutionAuditText(records))
			case "json":
				data, err := json.MarshalIndent(records, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(data))
			default:
				return fmt.Errorf("unsupported format: %s", format)
			}
			return nil
		},
	}
	command.Flags().StringVar(&runID, "run", "", "SessionID/RootRunID で絞り込む")
	command.Flags().StringVar(&format, "format", "text", "出力形式: text または json")
	command.Flags().IntVar(&limit, "limit", 100, "読み込む execution record 件数")
	command.Flags().BoolVar(&includeOutput, "include-output", false, "JSON/text 出力に tool output 本文を含める")
	return command
}

func openAuditStore(configPath string) (*state.FileStore, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	if !cfg.Memory.Enabled {
		return nil, fmt.Errorf("memory.enabled=false のため audit state を利用できません")
	}
	stateDir := cfg.Memory.StateDir
	if !filepath.IsAbs(stateDir) {
		pwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		stateDir = filepath.Join(pwd, stateDir)
	}
	return state.NewFileStore(stateDir)
}

func permissionRecordsFromScratch(items []domain.ScratchRecord, runID string) []domain.PermissionDecisionRecord {
	records := make([]domain.PermissionDecisionRecord, 0, len(items))
	for _, item := range items {
		if item.Kind != "permission_decision" {
			continue
		}
		if runID != "" && item.SessionID != runID {
			continue
		}
		var record domain.PermissionDecisionRecord
		if err := json.Unmarshal(item.Payload, &record); err != nil {
			continue
		}
		records = append(records, record)
	}
	return records
}

func doctorAuditRecordsFromScratch(items []domain.ScratchRecord, serverName string) []llmcheck.AuditRecord {
	records := make([]llmcheck.AuditRecord, 0, len(items))
	for _, item := range items {
		if item.Kind != llmcheck.AuditScratchKind {
			continue
		}
		var record llmcheck.AuditRecord
		if err := json.Unmarshal(item.Payload, &record); err != nil {
			continue
		}
		if record.ID == "" {
			record.ID = item.ID
		}
		if record.CreatedAt.IsZero() {
			record.CreatedAt = item.CreatedAt
		}
		if serverName != "" && record.ServerName != serverName {
			continue
		}
		records = append(records, record)
	}
	return records
}

type modelInvocationFilter struct {
	RunID      string
	ServerName string
	AgentID    string
}

func modelInvocationRecordsFromScratch(items []domain.ScratchRecord, filter modelInvocationFilter) []domain.ModelInvocationRecord {
	records := make([]domain.ModelInvocationRecord, 0, len(items))
	for _, item := range items {
		if item.Kind != domain.ScratchKindModelInvocation {
			continue
		}
		var record domain.ModelInvocationRecord
		if err := json.Unmarshal(item.Payload, &record); err != nil {
			continue
		}
		if record.ID == "" {
			record.ID = item.ID
		}
		if record.CreatedAt.IsZero() {
			record.CreatedAt = item.CreatedAt
		}
		if filter.RunID != "" && record.RootRunID != filter.RunID && record.RunID != filter.RunID {
			continue
		}
		if filter.ServerName != "" && record.ServerName != filter.ServerName {
			continue
		}
		if filter.AgentID != "" && record.AgentID != filter.AgentID {
			continue
		}
		records = append(records, record)
	}
	return records
}

type modelInvocationSummaryReport struct {
	Records       int                           `json:"records"`
	Successes     int                           `json:"successes"`
	Failures      int                           `json:"failures"`
	Fallbacks     int                           `json:"fallbacks"`
	TotalDuration int64                         `json:"total_duration_ms"`
	AvgDuration   float64                       `json:"avg_duration_ms"`
	Groups        []modelInvocationGroupSummary `json:"groups,omitempty"`
}

type modelInvocationGroupSummary struct {
	ServerName    string   `json:"server_name,omitempty"`
	Model         string   `json:"model,omitempty"`
	API           string   `json:"api,omitempty"`
	ProfileName   string   `json:"profile_name,omitempty"`
	Records       int      `json:"records"`
	Successes     int      `json:"successes"`
	Failures      int      `json:"failures"`
	Fallbacks     int      `json:"fallbacks"`
	TotalDuration int64    `json:"total_duration_ms"`
	AvgDuration   float64  `json:"avg_duration_ms"`
	MaxDuration   int64    `json:"max_duration_ms"`
	Agents        []string `json:"agents,omitempty"`
	Phases        []string `json:"phases,omitempty"`
}

type modelInvocationGroupKey struct {
	serverName  string
	model       string
	api         string
	profileName string
}

func summarizeModelInvocations(records []domain.ModelInvocationRecord) modelInvocationSummaryReport {
	report := modelInvocationSummaryReport{Records: len(records)}
	groups := map[modelInvocationGroupKey]*modelInvocationGroupSummary{}
	agentsByGroup := map[modelInvocationGroupKey]map[string]bool{}
	phasesByGroup := map[modelInvocationGroupKey]map[string]bool{}
	for _, record := range records {
		if record.Success {
			report.Successes++
		} else {
			report.Failures++
		}
		if record.Fallback {
			report.Fallbacks++
		}
		report.TotalDuration += record.DurationMS

		key := modelInvocationGroupKey{
			serverName:  record.ServerName,
			model:       record.Model,
			api:         record.API,
			profileName: record.ProfileName,
		}
		group, ok := groups[key]
		if !ok {
			group = &modelInvocationGroupSummary{
				ServerName:  record.ServerName,
				Model:       record.Model,
				API:         record.API,
				ProfileName: record.ProfileName,
			}
			groups[key] = group
			agentsByGroup[key] = map[string]bool{}
			phasesByGroup[key] = map[string]bool{}
		}
		group.Records++
		if record.Success {
			group.Successes++
		} else {
			group.Failures++
		}
		if record.Fallback {
			group.Fallbacks++
		}
		group.TotalDuration += record.DurationMS
		if record.DurationMS > group.MaxDuration {
			group.MaxDuration = record.DurationMS
		}
		if record.AgentID != "" {
			agentsByGroup[key][record.AgentID] = true
		}
		if record.Phase != "" {
			phasesByGroup[key][string(record.Phase)] = true
		}
	}
	if report.Records > 0 {
		report.AvgDuration = float64(report.TotalDuration) / float64(report.Records)
	}
	for key, group := range groups {
		if group.Records > 0 {
			group.AvgDuration = float64(group.TotalDuration) / float64(group.Records)
		}
		group.Agents = sortedStringSet(agentsByGroup[key])
		group.Phases = sortedStringSet(phasesByGroup[key])
		report.Groups = append(report.Groups, *group)
	}
	sort.Slice(report.Groups, func(i, j int) bool {
		if report.Groups[i].ServerName != report.Groups[j].ServerName {
			return report.Groups[i].ServerName < report.Groups[j].ServerName
		}
		if report.Groups[i].Model != report.Groups[j].Model {
			return report.Groups[i].Model < report.Groups[j].Model
		}
		if report.Groups[i].API != report.Groups[j].API {
			return report.Groups[i].API < report.Groups[j].API
		}
		return report.Groups[i].ProfileName < report.Groups[j].ProfileName
	})
	return report
}

func sortedStringSet(items map[string]bool) []string {
	out := make([]string, 0, len(items))
	for item := range items {
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func filterExecutionRecords(records []domain.ToolExecutionRecord, runID string, includeOutput bool) []domain.ToolExecutionRecord {
	filtered := make([]domain.ToolExecutionRecord, 0, len(records))
	for _, record := range records {
		if runID != "" && record.SessionID != runID {
			continue
		}
		if !includeOutput {
			record.Output = ""
		}
		filtered = append(filtered, record)
	}
	return filtered
}

func renderPermissionAuditText(records []domain.PermissionDecisionRecord) string {
	var sb strings.Builder
	sb.WriteString("Permission decisions\n")
	sb.WriteString(fmt.Sprintf("  count: %d\n", len(records)))
	for _, record := range records {
		sb.WriteString(fmt.Sprintf(
			"- %s %s %s %s",
			record.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			fallbackAuditString(string(record.Decision), "-"),
			fallbackAuditString(record.ToolName, "-"),
			fallbackAuditString(record.Resource, "-"),
		))
		if record.AgentID != "" {
			sb.WriteString(" agent=" + record.AgentID)
		}
		if record.Phase != "" {
			sb.WriteString(" phase=" + string(record.Phase))
		}
		if record.Risk != "" {
			sb.WriteString(" risk=" + record.Risk)
		}
		if record.Scope != "" {
			sb.WriteString(" scope=" + record.Scope)
		}
		if len(record.SideEffects) > 0 {
			sb.WriteString(" effects=" + strings.Join(record.SideEffects, ","))
		}
		if record.PreviewKind != "" {
			sb.WriteString(fmt.Sprintf(" preview=%s:%d", record.PreviewKind, record.PreviewLines))
		}
		if changes := auditPermissionChangeSummary(record); changes != "" {
			sb.WriteString(" changes=" + changes)
		}
		if record.Error != "" {
			sb.WriteString(" error=" + record.Error)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func auditPermissionChangeSummary(record domain.PermissionDecisionRecord) string {
	parts := make([]string, 0, 3)
	if record.ChangeFiles > 0 {
		parts = append(parts, fmt.Sprintf("files=%d", record.ChangeFiles))
	}
	if record.Additions > 0 || record.Deletions > 0 {
		parts = append(parts, fmt.Sprintf("+%d", record.Additions), fmt.Sprintf("-%d", record.Deletions))
	}
	return strings.Join(parts, " ")
}

func renderExecutionAuditText(records []domain.ToolExecutionRecord) string {
	var sb strings.Builder
	sb.WriteString("Tool executions\n")
	sb.WriteString(fmt.Sprintf("  count: %d\n", len(records)))
	for _, record := range records {
		status := "fail"
		if record.Success {
			status = "ok"
		}
		sb.WriteString(fmt.Sprintf(
			"- %s %s %s %s",
			record.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
			status,
			fallbackAuditString(record.ToolName, "-"),
			fallbackAuditString(record.SemanticKey, "-"),
		))
		if record.AgentID != "" {
			sb.WriteString(" agent=" + record.AgentID)
		}
		if record.SessionID != "" {
			sb.WriteString(" session=" + record.SessionID)
		}
		if record.ToolClass != "" {
			sb.WriteString(" class=" + string(record.ToolClass))
		}
		if record.Source != "" {
			sb.WriteString(" source=" + record.Source)
		}
		if record.SideEffectClass != "" {
			sb.WriteString(" side_effect=" + string(record.SideEffectClass))
		}
		if len(record.ReadSet) > 0 {
			sb.WriteString(" reads=" + strings.Join(record.ReadSet, ","))
		}
		if len(record.WriteSet) > 0 {
			sb.WriteString(" writes=" + strings.Join(record.WriteSet, ","))
		}
		if record.MutationID != "" {
			sb.WriteString(" mutation=" + record.MutationID)
		}
		if record.MutationFingerprint != "" {
			sb.WriteString(" mutation_fingerprint=" + record.MutationFingerprint)
		}
		if record.ObservationID != "" {
			sb.WriteString(" observation=" + record.ObservationID)
		}
		if record.Reusable {
			sb.WriteString(" reusable=true")
		}
		if record.Stale {
			sb.WriteString(" stale=true")
		}
		if record.Failure != "" {
			sb.WriteString(" failure=" + record.Failure)
		}
		if record.Output != "" {
			sb.WriteString(" output=" + strings.ReplaceAll(record.Output, "\n", "\\n"))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func renderModelInvocationAuditText(records []domain.ModelInvocationRecord) string {
	var sb strings.Builder
	sb.WriteString("Model invocations\n")
	sb.WriteString(fmt.Sprintf("  count: %d\n", len(records)))
	for _, record := range records {
		status := "fail"
		if record.Success {
			status = "ok"
		}
		sb.WriteString(fmt.Sprintf(
			"- %s %s %s %s",
			record.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			status,
			fallbackAuditString(record.ServerName, "-"),
			fallbackAuditString(record.Model, "-"),
		))
		if record.ProfileName != "" {
			sb.WriteString(" profile=" + record.ProfileName)
		}
		if record.AgentID != "" {
			sb.WriteString(" agent=" + record.AgentID)
		}
		if record.Phase != "" {
			sb.WriteString(" phase=" + string(record.Phase))
		}
		if record.API != "" {
			sb.WriteString(" api=" + record.API)
		}
		if record.ResponseFormat != "" {
			sb.WriteString(" format=" + record.ResponseFormat)
		}
		if record.Fallback {
			sb.WriteString(" fallback=true")
			if record.FallbackFromServer != "" {
				sb.WriteString(" from=" + record.FallbackFromServer)
			}
		}
		sb.WriteString(fmt.Sprintf(" messages=%d tools=%d duration=%dms", record.Messages, record.Tools, record.DurationMS))
		if record.FinishReason != "" {
			sb.WriteString(" finish=" + record.FinishReason)
		}
		if record.Error != "" {
			sb.WriteString(" error=" + record.Error)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func renderModelInvocationSummaryText(report modelInvocationSummaryReport) string {
	var sb strings.Builder
	sb.WriteString("Model invocation summary\n")
	sb.WriteString(fmt.Sprintf("  records: %d\n", report.Records))
	sb.WriteString(fmt.Sprintf("  success: %d\n", report.Successes))
	sb.WriteString(fmt.Sprintf("  failure: %d\n", report.Failures))
	sb.WriteString(fmt.Sprintf("  fallback: %d\n", report.Fallbacks))
	sb.WriteString(fmt.Sprintf("  avg_duration: %.1fms\n", report.AvgDuration))
	for _, group := range report.Groups {
		sb.WriteString(fmt.Sprintf(
			"- %s %s api=%s profile=%s calls=%d success=%d failure=%d fallback=%d avg=%.1fms max=%dms",
			fallbackAuditString(group.ServerName, "-"),
			fallbackAuditString(group.Model, "-"),
			fallbackAuditString(group.API, "-"),
			fallbackAuditString(group.ProfileName, "-"),
			group.Records,
			group.Successes,
			group.Failures,
			group.Fallbacks,
			group.AvgDuration,
			group.MaxDuration,
		))
		if len(group.Agents) > 0 {
			sb.WriteString(" agents=" + strings.Join(group.Agents, ","))
		}
		if len(group.Phases) > 0 {
			sb.WriteString(" phases=" + strings.Join(group.Phases, ","))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func renderDoctorRuntimeAuditText(records []llmcheck.AuditRecord) string {
	var sb strings.Builder
	sb.WriteString("LLM runtime audits\n")
	sb.WriteString(fmt.Sprintf("  count: %d\n", len(records)))
	for _, record := range records {
		sb.WriteString(fmt.Sprintf(
			"- %s %s server=%s model=%s",
			record.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			doctorAuditStatus(record),
			fallbackAuditString(record.ServerName, "-"),
			fallbackAuditString(record.Model, "-"),
		))
		if record.MatchedModel != "" {
			sb.WriteString(" matched=" + record.MatchedModel)
		}
		if record.API != "" {
			sb.WriteString(" api=" + record.API)
		}
		if record.Runtime.Requested {
			if record.Runtime.Error != "" {
				sb.WriteString(" runtime=unavailable")
			} else {
				sb.WriteString(fmt.Sprintf(" runtime_loaded=%t", record.Runtime.Loaded))
				if record.Runtime.ContextLength > 0 || record.Runtime.MaxContextLength > 0 {
					sb.WriteString(fmt.Sprintf(" context=%d/%d", record.Runtime.ContextLength, record.Runtime.MaxContextLength))
				}
				if record.Runtime.MatchedModel.Quantization != "" {
					sb.WriteString(" quant=" + record.Runtime.MatchedModel.Quantization)
				}
			}
		}
		if record.Probe.Requested {
			probeStatus := "failed"
			if record.Probe.OK {
				probeStatus = "ok"
			}
			sb.WriteString(" probe=" + probeStatus)
			if record.Probe.Structured {
				sb.WriteString(" probe_format=json_schema")
			}
		}
		if len(record.Warnings) > 0 {
			sb.WriteString(fmt.Sprintf(" warnings=%d", len(record.Warnings)))
		}
		if len(record.Problems) > 0 {
			sb.WriteString(fmt.Sprintf(" problems=%d", len(record.Problems)))
		}
		if len(record.Recommendations) > 0 {
			sb.WriteString(fmt.Sprintf(" recommendations=%d", len(record.Recommendations)))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func doctorAuditStatus(record llmcheck.AuditRecord) string {
	if len(record.Problems) > 0 {
		return "problem"
	}
	if len(record.Warnings) > 0 {
		return "warning"
	}
	return "ok"
}

func fallbackAuditString(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
