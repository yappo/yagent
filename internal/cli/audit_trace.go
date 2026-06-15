package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"yagent/internal/domain"
)

type auditTrace struct {
	Run     runAuditSummary   `json:"run"`
	Summary auditTraceSummary `json:"summary"`
	Spans   []auditTraceSpan  `json:"spans"`
}

type auditTraceSummary struct {
	Spans       int   `json:"spans"`
	Models      int   `json:"models"`
	Tools       int   `json:"tools"`
	Permissions int   `json:"permissions"`
	Mutations   int   `json:"mutations"`
	Artifacts   int   `json:"artifacts"`
	Failures    int   `json:"failures"`
	Fallbacks   int   `json:"fallbacks"`
	DurationMS  int64 `json:"duration_ms,omitempty"`
}

type auditTraceSpan struct {
	ID         string          `json:"id"`
	ParentID   string          `json:"parent_id,omitempty"`
	Kind       string          `json:"kind"`
	Name       string          `json:"name,omitempty"`
	Status     string          `json:"status,omitempty"`
	AgentID    string          `json:"agent_id,omitempty"`
	Phase      domain.RunPhase `json:"phase,omitempty"`
	Attempt    int             `json:"attempt,omitempty"`
	StartedAt  *time.Time      `json:"started_at,omitempty"`
	EndedAt    *time.Time      `json:"ended_at,omitempty"`
	DurationMS int64           `json:"duration_ms,omitempty"`
	Details    map[string]any  `json:"details,omitempty"`
}

type auditTraceOptions struct {
	RunID        string
	Limit        int
	IncludeStale bool
	Kind         string
	AgentID      string
	Phase        domain.RunPhase
}

func newAuditTraceCommand(configPath *string) *cobra.Command {
	var runID string
	var format string
	var limit int
	var includeStale bool
	var kind string
	var agentID string
	var phase string

	command := &cobra.Command{
		Use:   "trace",
		Short: "run の agent/tool/model/permission trace を span 形式で表示",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openAuditStore(*configPath)
			if err != nil {
				return err
			}
			trace, err := buildAuditTrace(cmd.Context(), store, auditTraceOptions{
				RunID:        runID,
				Limit:        limit,
				IncludeStale: includeStale,
				Kind:         kind,
				AgentID:      agentID,
				Phase:        domain.RunPhase(phase),
			})
			if err != nil {
				return err
			}
			switch format {
			case "text":
				fmt.Print(renderAuditTraceText(trace))
			case "json":
				data, err := json.MarshalIndent(trace, "", "  ")
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
	command.Flags().StringVar(&runID, "run", "latest", "run id。latest も指定できます")
	command.Flags().StringVar(&format, "format", "text", "出力形式: text または json")
	command.Flags().IntVar(&limit, "limit", 1000, "読み込む関連 audit record 件数")
	command.Flags().BoolVar(&includeStale, "include-stale", false, "stale observation も trace に含める")
	command.Flags().StringVar(&kind, "kind", "", "span kind で絞り込む。例: model, tool, permission, mutation, artifact")
	command.Flags().StringVar(&agentID, "agent", "", "agent id で絞り込む")
	command.Flags().StringVar(&phase, "phase", "", "phase で絞り込む。例: plan, execute, verify")
	return command
}

func buildAuditTrace(ctx context.Context, store auditBundleStore, options auditTraceOptions) (auditTrace, error) {
	runID := options.RunID
	if runID == "" {
		runID = "latest"
	}
	bundle, err := buildAuditBundle(ctx, store, auditBundleOptions{
		RunID:        runID,
		Limit:        options.Limit,
		IncludeStale: options.IncludeStale,
		FullRun:      true,
	})
	if err != nil {
		return auditTrace{}, err
	}
	if bundle.FullRun == nil {
		return auditTrace{}, fmt.Errorf("run state がありません")
	}
	spans := auditTraceSpans(bundle)
	spans = filterAuditTraceSpans(spans, options)
	sortAuditTraceSpans(spans)
	return auditTrace{
		Run:     bundle.Run,
		Summary: summarizeAuditTrace(spans),
		Spans:   spans,
	}, nil
}

func auditTraceSpans(bundle auditBundle) []auditTraceSpan {
	run := bundle.FullRun
	if run == nil {
		return nil
	}
	runSpanID := "run:" + run.ID
	spans := []auditTraceSpan{{
		ID:         runSpanID,
		Kind:       "run",
		Name:       run.UserGoal,
		Status:     string(run.Status),
		Phase:      run.CurrentPhase,
		Attempt:    run.Attempt,
		StartedAt:  traceTimePtr(run.CreatedAt),
		EndedAt:    traceTimePtr(run.UpdatedAt),
		DurationMS: traceDurationMS(run.CreatedAt, run.UpdatedAt),
		Details: map[string]any{
			"profile":      run.Profile,
			"messages":     len(run.Messages),
			"plan_nodes":   len(run.Plan),
			"work_units":   len(run.WorkUnits),
			"artifacts":    len(run.Artifacts),
			"checkpoints":  len(run.Checkpoints),
			"verification": len(run.Verification),
		},
	}}
	for _, node := range run.Plan {
		spans = append(spans, auditTraceSpan{
			ID:        "plan:" + fallbackAuditString(node.ID, node.Title),
			ParentID:  runSpanID,
			Kind:      "plan_node",
			Name:      node.Title,
			Status:    node.Status,
			StartedAt: traceTimePtr(node.CreatedAt),
			Details: map[string]any{
				"description": node.Description,
			},
		})
	}
	for _, unit := range run.WorkUnits {
		spans = append(spans, auditTraceSpan{
			ID:         "work_unit:" + unit.ID,
			ParentID:   runSpanID,
			Kind:       "work_unit",
			Name:       unit.Task,
			Status:     unit.Status,
			Phase:      unit.Phase,
			Attempt:    unit.Attempt,
			StartedAt:  traceTimePtr(unit.StartedAt),
			EndedAt:    traceTimePtr(unit.CompletedAt),
			DurationMS: traceDurationMS(unit.StartedAt, unit.CompletedAt),
			Details: map[string]any{
				"kind":               unit.Kind,
				"role":               unit.Role,
				"source":             unit.Source,
				"side_effect":        string(unit.SideEffectClass),
				"depends_on":         unit.DependsOn,
				"read_set":           unit.ReadSet,
				"write_set":          unit.WriteSet,
				"artifact_refs":      artifactReferenceIDs(unit.ArtifactRefs),
				"known_failure_refs": unit.KnownFailureRefs,
			},
		})
	}
	for _, checkpoint := range run.Checkpoints {
		spans = append(spans, auditTraceSpan{
			ID:        "checkpoint:" + checkpoint.ID,
			ParentID:  runSpanID,
			Kind:      "checkpoint",
			Name:      string(checkpoint.Phase),
			Status:    string(checkpoint.Status),
			Phase:     checkpoint.Phase,
			Attempt:   checkpoint.Attempt,
			StartedAt: traceTimePtr(checkpoint.CreatedAt),
			Details: map[string]any{
				"summary": checkpoint.Summary,
			},
		})
	}
	for idx, verification := range run.Verification {
		spans = append(spans, auditTraceSpan{
			ID:        fmt.Sprintf("verification:%d:%s", idx+1, fallbackAuditString(verification.ArtifactID, verification.SourceAgent)),
			ParentID:  runSpanID,
			Kind:      "verification",
			Name:      verification.Summary,
			Status:    verification.Status,
			AgentID:   verification.SourceAgent,
			Phase:     domain.RunPhaseVerify,
			Attempt:   verification.Attempt,
			StartedAt: traceTimePtr(verification.CreatedAt),
			Details: map[string]any{
				"repair_brief": verification.RepairBrief,
				"artifact_id":  verification.ArtifactID,
			},
		})
	}
	for _, artifact := range bundle.Artifacts {
		spans = append(spans, auditTraceSpan{
			ID:        "artifact:" + artifact.ID,
			ParentID:  runSpanID,
			Kind:      "artifact",
			Name:      artifact.Name,
			Status:    "created",
			AgentID:   artifact.AgentID,
			Phase:     artifact.Phase,
			StartedAt: traceTimePtr(artifact.CreatedAt),
			Details: map[string]any{
				"artifact_kind":  artifact.Kind,
				"schema_version": artifact.SchemaVersion,
				"summary":        artifact.Summary,
				"refs":           artifactReferenceIDs(artifact.References),
			},
		})
	}
	for _, execution := range bundle.Executions {
		status := "failed"
		if execution.Success {
			status = "ok"
		}
		spans = append(spans, auditTraceSpan{
			ID:         "tool:" + execution.ID,
			ParentID:   runSpanID,
			Kind:       "tool",
			Name:       execution.ToolName,
			Status:     status,
			AgentID:    execution.AgentID,
			StartedAt:  traceTimePtr(execution.CreatedAt),
			EndedAt:    traceTimePtr(execution.UpdatedAt),
			DurationMS: traceDurationMS(execution.CreatedAt, execution.UpdatedAt),
			Details: map[string]any{
				"tool_class":           string(execution.ToolClass),
				"source":               execution.Source,
				"side_effect":          string(execution.SideEffectClass),
				"read_set":             execution.ReadSet,
				"write_set":            execution.WriteSet,
				"output_artifact_id":   execution.OutputArtifactID,
				"observation_id":       execution.ObservationID,
				"mutation_id":          execution.MutationID,
				"reusable":             execution.Reusable,
				"stale":                execution.Stale,
				"failure":              execution.Failure,
				"mutation_fingerprint": execution.MutationFingerprint,
			},
		})
	}
	for _, model := range bundle.Models {
		status := "failed"
		if model.Success {
			status = "ok"
		}
		spans = append(spans, auditTraceSpan{
			ID:         "model:" + model.ID,
			ParentID:   runSpanID,
			Kind:       "model",
			Name:       traceModelName(model),
			Status:     status,
			AgentID:    model.AgentID,
			Phase:      model.Phase,
			Attempt:    model.Attempt,
			StartedAt:  traceTimePtr(model.CreatedAt),
			EndedAt:    traceEndPtr(model.CreatedAt, model.DurationMS),
			DurationMS: model.DurationMS,
			Details: map[string]any{
				"server":               model.ServerName,
				"model":                model.Model,
				"api":                  model.API,
				"profile":              model.ProfileName,
				"fallback":             model.Fallback,
				"fallback_from_server": model.FallbackFromServer,
				"response_format":      model.ResponseFormat,
				"messages":             model.Messages,
				"tools":                model.Tools,
				"finish_reason":        model.FinishReason,
				"error":                model.Error,
			},
		})
	}
	for _, observation := range bundle.Observations {
		status := "fresh"
		if observation.Stale {
			status = "stale"
		}
		spans = append(spans, auditTraceSpan{
			ID:        "observation:" + observation.ID,
			ParentID:  runSpanID,
			Kind:      "observation",
			Name:      observation.ToolName,
			Status:    status,
			StartedAt: traceTimePtr(observation.UpdatedAt),
			Details: map[string]any{
				"semantic_key":       observation.SemanticKey,
				"summary":            observation.Summary,
				"read_set":           observation.ReadSet,
				"snapshot_revision":  observation.SnapshotRevision,
				"output_artifact_id": observation.OutputArtifactID,
				"reusable":           observation.Reusable,
			},
		})
	}
	for _, mutation := range bundle.Mutations {
		spans = append(spans, auditTraceSpan{
			ID:        "mutation:" + mutation.ID,
			ParentID:  runSpanID,
			Kind:      "mutation",
			Name:      mutation.ToolName,
			Status:    "recorded",
			AgentID:   mutation.AgentID,
			StartedAt: traceTimePtr(mutation.CreatedAt),
			Details: map[string]any{
				"execution_id":         mutation.ExecutionID,
				"write_set":            mutation.WriteSet,
				"before_revision":      mutation.BeforeRevision,
				"after_revision":       mutation.AfterRevision,
				"mutation_fingerprint": mutation.MutationFingerprint,
			},
		})
	}
	for idx, permission := range bundle.Permissions {
		spans = append(spans, auditTraceSpan{
			ID:        fmt.Sprintf("permission:%d:%s", idx+1, fallbackAuditString(permission.ToolName, "tool")),
			ParentID:  runSpanID,
			Kind:      "permission",
			Name:      permission.ToolName,
			Status:    string(permission.Decision),
			AgentID:   permission.AgentID,
			Phase:     permission.Phase,
			Attempt:   permission.Attempt,
			StartedAt: traceTimePtr(permission.CreatedAt),
			Details: map[string]any{
				"operation":     permission.Operation,
				"resource":      permission.Resource,
				"action":        permission.Action,
				"resource_kind": permission.ResourceKind,
				"risk":          permission.Risk,
				"scope":         permission.Scope,
				"side_effects":  permission.SideEffects,
				"preview":       permission.PreviewKind,
				"preview_lines": permission.PreviewLines,
				"change_files":  permission.ChangeFiles,
				"additions":     permission.Additions,
				"deletions":     permission.Deletions,
				"error":         permission.Error,
			},
		})
	}
	return spans
}

func filterAuditTraceSpans(spans []auditTraceSpan, options auditTraceOptions) []auditTraceSpan {
	if options.Kind == "" && options.AgentID == "" && options.Phase == "" {
		return spans
	}
	filtered := make([]auditTraceSpan, 0, len(spans))
	for _, span := range spans {
		if options.Kind != "" && span.Kind != options.Kind {
			continue
		}
		if options.AgentID != "" && span.AgentID != options.AgentID {
			continue
		}
		if options.Phase != "" && span.Phase != options.Phase {
			continue
		}
		filtered = append(filtered, span)
	}
	return filtered
}

func sortAuditTraceSpans(spans []auditTraceSpan) {
	sort.SliceStable(spans, func(i, j int) bool {
		left := traceSpanTime(spans[i])
		right := traceSpanTime(spans[j])
		if !left.Equal(right) {
			if left.IsZero() {
				return false
			}
			if right.IsZero() {
				return true
			}
			return left.Before(right)
		}
		if spans[i].Kind != spans[j].Kind {
			return spans[i].Kind < spans[j].Kind
		}
		return spans[i].ID < spans[j].ID
	})
}

func summarizeAuditTrace(spans []auditTraceSpan) auditTraceSummary {
	summary := auditTraceSummary{Spans: len(spans)}
	for _, span := range spans {
		if span.DurationMS > summary.DurationMS {
			summary.DurationMS = span.DurationMS
		}
		if isFailureTraceStatus(span.Status) {
			summary.Failures++
		}
		switch span.Kind {
		case "model":
			summary.Models++
			if traceDetailBool(span.Details, "fallback") {
				summary.Fallbacks++
			}
		case "tool":
			summary.Tools++
		case "permission":
			summary.Permissions++
		case "mutation":
			summary.Mutations++
		case "artifact":
			summary.Artifacts++
		}
	}
	return summary
}

func renderAuditTraceText(trace auditTrace) string {
	var sb strings.Builder
	sb.WriteString("Trace\n")
	sb.WriteString(fmt.Sprintf(
		"  run: %s status=%s phase=%s attempt=%d\n",
		fallbackAuditString(trace.Run.ID, "-"),
		fallbackAuditString(string(trace.Run.Status), "-"),
		fallbackAuditString(string(trace.Run.CurrentPhase), "-"),
		trace.Run.Attempt,
	))
	if trace.Run.UserGoal != "" {
		sb.WriteString("  goal: " + truncateAuditText(trace.Run.UserGoal, 160) + "\n")
	}
	sb.WriteString(fmt.Sprintf(
		"  spans: %d models=%d tools=%d permissions=%d mutations=%d artifacts=%d failures=%d fallbacks=%d duration=%dms\n",
		trace.Summary.Spans,
		trace.Summary.Models,
		trace.Summary.Tools,
		trace.Summary.Permissions,
		trace.Summary.Mutations,
		trace.Summary.Artifacts,
		trace.Summary.Failures,
		trace.Summary.Fallbacks,
		trace.Summary.DurationMS,
	))
	for _, span := range trace.Spans {
		sb.WriteString(fmt.Sprintf(
			"- %s %s %s",
			formatAuditTime(traceSpanTime(span)),
			fallbackAuditString(span.Kind, "-"),
			fallbackAuditString(span.ID, "-"),
		))
		if span.Status != "" {
			sb.WriteString(" status=" + span.Status)
		}
		if span.Name != "" {
			sb.WriteString(" name=" + truncateAuditText(span.Name, 80))
		}
		if span.AgentID != "" {
			sb.WriteString(" agent=" + span.AgentID)
		}
		if span.Phase != "" {
			sb.WriteString(" phase=" + string(span.Phase))
		}
		if span.Attempt != 0 {
			sb.WriteString(fmt.Sprintf(" attempt=%d", span.Attempt))
		}
		if span.DurationMS != 0 {
			sb.WriteString(fmt.Sprintf(" duration=%dms", span.DurationMS))
		}
		appendTraceDetails(&sb, span.Details)
		sb.WriteString("\n")
	}
	return sb.String()
}

func appendTraceDetails(sb *strings.Builder, details map[string]any) {
	if len(details) == 0 {
		return
	}
	keys := make([]string, 0, len(details))
	for key := range details {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value, ok := formatTraceDetailValue(details[key])
		if !ok {
			continue
		}
		sb.WriteString(" " + key + "=" + value)
	}
}

func formatTraceDetailValue(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		typed = strings.TrimSpace(typed)
		if typed == "" {
			return "", false
		}
		return truncateAuditText(typed, 96), true
	case bool:
		if !typed {
			return "", false
		}
		return "true", true
	case int:
		if typed == 0 {
			return "", false
		}
		return fmt.Sprint(typed), true
	case int64:
		if typed == 0 {
			return "", false
		}
		return fmt.Sprint(typed), true
	case []string:
		if len(typed) == 0 {
			return "", false
		}
		return strings.Join(typed, ","), true
	default:
		return "", false
	}
}

func traceDetailBool(details map[string]any, key string) bool {
	value, _ := details[key].(bool)
	return value
}

func traceTimePtr(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copied := value
	return &copied
}

func traceEndPtr(start time.Time, durationMS int64) *time.Time {
	if start.IsZero() || durationMS <= 0 {
		return nil
	}
	end := start.Add(time.Duration(durationMS) * time.Millisecond)
	return &end
}

func traceDurationMS(start time.Time, end time.Time) int64 {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return 0
	}
	return end.Sub(start).Milliseconds()
}

func traceSpanTime(span auditTraceSpan) time.Time {
	if span.StartedAt != nil {
		return *span.StartedAt
	}
	if span.EndedAt != nil {
		return *span.EndedAt
	}
	return time.Time{}
}

func traceModelName(record domain.ModelInvocationRecord) string {
	if record.ServerName == "" {
		return record.Model
	}
	if record.Model == "" {
		return record.ServerName
	}
	return record.ServerName + "/" + record.Model
}

func isFailureTraceStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "fail", "error", "denied", "deny":
		return true
	default:
		return false
	}
}
