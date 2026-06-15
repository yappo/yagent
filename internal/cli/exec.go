package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"yagent/internal/app"
	"yagent/internal/domain"
)

func newExecCommand(configPath *string, logPath *string) *cobra.Command {
	var prompt string
	var model string
	var profile string
	var resumeID string
	var stream bool
	var format string
	var showEvents bool

	command := &cobra.Command{
		Use:   "exec",
		Short: "単発でプロンプトを実行",
		RunE: func(cmd *cobra.Command, args []string) error {
			container, err := app.Build(*configPath, NewStdinApprover(), app.BuildOptions{LogPath: *logPath})
			if err != nil {
				return err
			}
			if container.Closer != nil {
				defer container.Closer.Close()
			}

			result, err := container.Orchestrator.RunTurn(cmd.Context(), domain.TurnRequest{
				Messages: []domain.Message{{Role: domain.RoleUser, Content: prompt}},
				Model:    model,
				Profile:  profile,
				ResumeID: resumeID,
				Stream:   stream,
			})
			if err != nil {
				return err
			}

			switch format {
			case "text":
				fmt.Print(renderExecText(result, showEvents))
			case "json":
				output := execOutputFromResult(result)
				data, err := json.MarshalIndent(output, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(data))
			default:
				return fmt.Errorf("unsupported format %q", format)
			}
			return nil
		},
	}

	command.Flags().StringVar(&prompt, "prompt", "", "AI に送信するプロンプト")
	command.Flags().StringVar(&model, "model", "", "使用するモデル名")
	command.Flags().StringVar(&profile, "profile", "", "routing profile 名")
	command.Flags().StringVar(&resumeID, "resume", "", "復元する run id。latest も指定できます")
	command.Flags().BoolVar(&stream, "stream", false, "ストリーミング応答を有効にする")
	command.Flags().StringVar(&format, "format", "text", "出力形式: text または json")
	command.Flags().BoolVar(&showEvents, "show-events", false, "text 出力で実行イベント要約を表示する")
	_ = command.MarkFlagRequired("prompt")

	return command
}

type execOutput struct {
	Message string            `json:"message"`
	Run     *execRunSummary   `json:"run,omitempty"`
	Summary execEventSummary  `json:"summary"`
	Events  []execEventRecord `json:"events,omitempty"`
}

type execRunSummary struct {
	ID                string           `json:"id,omitempty"`
	RootRunID         string           `json:"root_run_id,omitempty"`
	Status            domain.RunStatus `json:"status,omitempty"`
	CurrentPhase      domain.RunPhase  `json:"current_phase,omitempty"`
	Attempt           int              `json:"attempt,omitempty"`
	Profile           string           `json:"profile,omitempty"`
	Artifacts         int              `json:"artifacts"`
	WorkUnits         int              `json:"work_units"`
	PlanNodes         int              `json:"plan_nodes"`
	Checkpoints       int              `json:"checkpoints"`
	VerificationItems int              `json:"verification_items"`
}

type execEventSummary struct {
	Events         int           `json:"events"`
	Agents         []string      `json:"agents,omitempty"`
	Phases         []string      `json:"phases,omitempty"`
	ToolCalls      int           `json:"tool_calls"`
	ToolFailures   int           `json:"tool_failures"`
	CacheHits      int           `json:"cache_hits"`
	ModelCalls     int           `json:"model_calls"`
	ModelFallbacks int           `json:"model_fallbacks"`
	FailedEvents   int           `json:"failed_events"`
	Mutations      int           `json:"mutations"`
	DurationMS     int64         `json:"duration_ms,omitempty"`
	Tools          []execCount   `json:"tools,omitempty"`
	Models         []execCount   `json:"models,omitempty"`
	Failures       []execFailure `json:"failures,omitempty"`
}

type execCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type execFailure struct {
	AgentID string          `json:"agent_id,omitempty"`
	Type    string          `json:"type"`
	Phase   domain.RunPhase `json:"phase,omitempty"`
	Detail  string          `json:"detail,omitempty"`
	Display string          `json:"display,omitempty"`
}

type execEventRecord struct {
	RunID        string          `json:"run_id,omitempty"`
	ParentRunID  string          `json:"parent_run_id,omitempty"`
	AgentID      string          `json:"agent_id,omitempty"`
	Type         string          `json:"type"`
	Phase        domain.RunPhase `json:"phase,omitempty"`
	Attempt      int             `json:"attempt,omitempty"`
	Status       string          `json:"status,omitempty"`
	Detail       string          `json:"detail,omitempty"`
	Display      string          `json:"display,omitempty"`
	ArtifactRef  string          `json:"artifact_ref,omitempty"`
	Metrics      map[string]any  `json:"metrics,omitempty"`
	Timestamp    time.Time       `json:"timestamp,omitempty"`
	ContextCount int             `json:"context_count,omitempty"`
}

func renderExecText(result domain.TurnResult, showEvents bool) string {
	var sb strings.Builder
	sb.WriteString(strings.TrimRight(result.Message.Content, "\n"))
	sb.WriteString("\n")
	if showEvents {
		sb.WriteString("\n")
		sb.WriteString(renderExecEventSummary(result))
	}
	return sb.String()
}

func execOutputFromResult(result domain.TurnResult) execOutput {
	return execOutput{
		Message: strings.TrimSpace(result.Message.Content),
		Run:     execRunSummaryFromRun(result.Run),
		Summary: execEventSummaryFromResult(result),
		Events:  execEventRecords(result.Events),
	}
}

func execRunSummaryFromRun(run *domain.RunState) *execRunSummary {
	if run == nil {
		return nil
	}
	return &execRunSummary{
		ID:                run.ID,
		RootRunID:         run.RootRunID,
		Status:            run.Status,
		CurrentPhase:      run.CurrentPhase,
		Attempt:           run.Attempt,
		Profile:           run.Profile,
		Artifacts:         len(run.Artifacts),
		WorkUnits:         len(run.WorkUnits),
		PlanNodes:         len(run.Plan),
		Checkpoints:       len(run.Checkpoints),
		VerificationItems: len(run.Verification),
	}
}

func renderExecEventSummary(result domain.TurnResult) string {
	summary := execEventSummaryFromResult(result)
	var sb strings.Builder
	sb.WriteString("Execution summary\n")
	if result.Run != nil {
		sb.WriteString(fmt.Sprintf(
			"  run: %s status=%s phase=%s attempt=%d\n",
			fallbackCLIString(result.Run.ID),
			fallbackCLIString(string(result.Run.Status)),
			fallbackCLIString(string(result.Run.CurrentPhase)),
			result.Run.Attempt,
		))
	}
	sb.WriteString(fmt.Sprintf(
		"  events: %d agents=%s phases=%s failed=%d duration=%dms\n",
		summary.Events,
		fallbackCLIString(strings.Join(summary.Agents, ",")),
		fallbackCLIString(strings.Join(summary.Phases, ",")),
		summary.FailedEvents,
		summary.DurationMS,
	))
	sb.WriteString(fmt.Sprintf(
		"  tools: calls=%d failures=%d cache_hits=%d",
		summary.ToolCalls,
		summary.ToolFailures,
		summary.CacheHits,
	))
	if len(summary.Tools) > 0 {
		sb.WriteString(" names=" + execCountList(summary.Tools))
	}
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf(
		"  models: calls=%d fallbacks=%d",
		summary.ModelCalls,
		summary.ModelFallbacks,
	))
	if len(summary.Models) > 0 {
		sb.WriteString(" names=" + execCountList(summary.Models))
	}
	sb.WriteString("\n")
	if result.Run != nil {
		sb.WriteString(fmt.Sprintf(
			"  run_state: artifacts=%d work_units=%d plan_nodes=%d checkpoints=%d verification=%d mutations=%d\n",
			len(result.Run.Artifacts),
			len(result.Run.WorkUnits),
			len(result.Run.Plan),
			len(result.Run.Checkpoints),
			len(result.Run.Verification),
			summary.Mutations,
		))
	}
	if len(summary.Failures) > 0 {
		sb.WriteString("  failures:\n")
		for _, failure := range summary.Failures {
			sb.WriteString(fmt.Sprintf(
				"    - %s %s %s\n",
				fallbackCLIString(failure.AgentID),
				fallbackCLIString(failure.Type),
				truncateAuditText(fallbackCLIString(failure.Display), 160),
			))
		}
	}
	return sb.String()
}

func execEventSummaryFromResult(result domain.TurnResult) execEventSummary {
	summary := execEventSummary{Events: len(result.Events)}
	agents := map[string]bool{}
	phases := map[string]bool{}
	tools := map[string]int{}
	models := map[string]int{}
	mutations := map[string]bool{}
	var firstEvent time.Time
	var lastEvent time.Time
	for _, event := range result.Events {
		if event.AgentID != "" {
			agents[event.AgentID] = true
		}
		if event.Phase != "" {
			phases[string(event.Phase)] = true
		}
		if !event.Timestamp.IsZero() {
			if firstEvent.IsZero() || event.Timestamp.Before(firstEvent) {
				firstEvent = event.Timestamp
			}
			if lastEvent.IsZero() || event.Timestamp.After(lastEvent) {
				lastEvent = event.Timestamp
			}
		}
		switch event.Type {
		case "tool_called", "tool_failed":
			summary.ToolCalls++
			tools[execEventToolName(event)]++
		case "cache_hit":
			summary.CacheHits++
			tools[execEventToolName(event)]++
		case "llm_called":
			summary.ModelCalls++
			models[execEventModelName(event)]++
			if execMetricBool(event.Metrics, "fallback") {
				summary.ModelFallbacks++
			}
		}
		if event.Type == "tool_failed" {
			summary.ToolFailures++
		}
		if event.Status == "failed" || strings.HasSuffix(event.Type, "_failed") {
			summary.FailedEvents++
			summary.Failures = append(summary.Failures, execFailure{
				AgentID: event.AgentID,
				Type:    event.Type,
				Phase:   event.Phase,
				Detail:  event.Detail,
				Display: execEventDisplay(event),
			})
		}
		if mutationID := execMetricString(event.Metrics, "mutation_id"); mutationID != "" {
			mutations[mutationID] = true
		} else if fingerprint := execMetricString(event.Metrics, "mutation_fingerprint"); fingerprint != "" {
			mutations[fingerprint] = true
		}
	}
	if !firstEvent.IsZero() && !lastEvent.IsZero() && !lastEvent.Before(firstEvent) {
		summary.DurationMS = lastEvent.Sub(firstEvent).Milliseconds()
	}
	summary.Agents = sortedExecStrings(agents)
	summary.Phases = sortedExecStrings(phases)
	summary.Tools = execCounts(tools)
	summary.Models = execCounts(models)
	summary.Mutations = len(mutations)
	return summary
}

func execEventRecords(events []domain.ExecutionEvent) []execEventRecord {
	out := make([]execEventRecord, 0, len(events))
	for _, event := range events {
		out = append(out, execEventRecord{
			RunID:        event.RunID,
			ParentRunID:  event.ParentRunID,
			AgentID:      event.AgentID,
			Type:         event.Type,
			Phase:        event.Phase,
			Attempt:      event.Attempt,
			Status:       event.Status,
			Detail:       truncateAuditText(event.Detail, 240),
			Display:      truncateAuditText(execEventDisplay(event), 240),
			ArtifactRef:  event.ArtifactRef,
			Metrics:      event.Metrics,
			Timestamp:    event.Timestamp,
			ContextCount: event.ContextCount,
		})
	}
	return out
}

func execEventToolName(event domain.ExecutionEvent) string {
	detail := strings.TrimSpace(execEventDisplay(event))
	if idx := strings.Index(detail, ":"); idx >= 0 {
		detail = strings.TrimSpace(detail[:idx])
	}
	return fallbackCLIString(detail)
}

func execEventDisplay(event domain.ExecutionEvent) string {
	if display := strings.TrimSpace(event.Display); display != "" {
		return display
	}
	return strings.TrimSpace(event.Detail)
}

func execEventModelName(event domain.ExecutionEvent) string {
	model := execMetricString(event.Metrics, "model")
	server := execMetricString(event.Metrics, "server_name")
	switch {
	case server != "" && model != "":
		return server + "/" + model
	case model != "":
		return model
	case server != "":
		return server
	default:
		return "-"
	}
}

func execMetricString(metrics map[string]any, name string) string {
	if metrics == nil {
		return ""
	}
	if value, ok := metrics[name].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func execMetricBool(metrics map[string]any, name string) bool {
	if metrics == nil {
		return false
	}
	switch value := metrics[name].(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true")
	default:
		return false
	}
}

func sortedExecStrings(items map[string]bool) []string {
	out := make([]string, 0, len(items))
	for item := range items {
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func execCounts(items map[string]int) []execCount {
	out := make([]execCount, 0, len(items))
	for name, count := range items {
		if name == "" {
			name = "-"
		}
		out = append(out, execCount{Name: name, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func execCountList(items []execCount) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, fmt.Sprintf("%s=%d", item.Name, item.Count))
	}
	return strings.Join(parts, ",")
}

type stdinPatternApproval struct {
	toolName     string
	action       string
	resourceKind string
	risk         string
	pattern      string
}

type StdinApprover struct {
	sessionApprovals map[string]bool
	patternApprovals []stdinPatternApproval
}

func NewStdinApprover() *StdinApprover {
	return &StdinApprover{
		sessionApprovals: map[string]bool{},
	}
}

func (a *StdinApprover) Approve(ctx context.Context, request domain.PermissionRequest) (domain.PermissionDecision, error) {
	key := cliApprovalKey(request)
	if a.sessionApprovals[key] || a.hasPatternApproval(request) {
		return domain.PermissionAllowSession, nil
	}

	fmt.Printf("%sを実行しますか？ファイル：%s\n", request.Operation, request.Resource)
	fmt.Printf("requester: %s (%s)\n", cliPermissionRequesterLabel(request), cliPermissionRequesterType(request))
	if request.Summary != "" {
		fmt.Printf("summary: %s\n", request.Summary)
	}
	if request.Purpose != "" {
		fmt.Printf("purpose: %s\n", request.Purpose)
	}
	if request.Risk != "" || request.Scope != "" {
		fmt.Printf("risk: %s  scope: %s\n", fallbackCLIString(request.Risk), fallbackCLIString(request.Scope))
	}
	if changes := cliPermissionChangeSummary(request); changes != "" {
		fmt.Printf("changes: %s\n", changes)
	}
	if len(request.SideEffects) > 0 {
		fmt.Printf("effects: %s\n", strings.Join(request.SideEffects, ", "))
	}
	if preview := strings.TrimSpace(request.Preview); preview != "" {
		label := strings.TrimSpace(request.PreviewKind)
		if label == "" {
			label = "preview"
		}
		fmt.Printf("%s:\n%s\n", label, preview)
	}
	if domain.PermissionRequestSupportsPatternApproval(request) {
		fmt.Print("[1] 今回だけ許可  [2] 同じ操作を以後許可  [3] ファイルパターン指定で以後許可  [4] 拒否: ")
	} else {
		fmt.Print("[1] 今回だけ許可  [2] 同じ操作を以後許可  [3] 拒否: ")
	}
	var input string
	fmt.Scanln(&input)
	switch input {
	case "1":
		return domain.PermissionAllowOnce, nil
	case "2":
		a.sessionApprovals[key] = true
		return domain.PermissionAllowSession, nil
	case "3":
		if domain.PermissionRequestSupportsPatternApproval(request) {
			fmt.Print("許可するパターン (例: *.go / internal/*): ")
			var patternValue string
			fmt.Scanln(&patternValue)
			patternValue = strings.TrimSpace(patternValue)
			if patternValue == "" {
				return domain.PermissionDeny, nil
			}
			a.patternApprovals = append(a.patternApprovals, stdinPatternApproval{
				toolName:     request.ToolName,
				action:       request.Action,
				resourceKind: request.ResourceKind,
				risk:         request.Risk,
				pattern:      patternValue,
			})
			return domain.PermissionAllowSession, nil
		}
		return domain.PermissionDeny, nil
	case "4":
		if domain.PermissionRequestSupportsPatternApproval(request) {
			return domain.PermissionDeny, nil
		}
		return domain.PermissionDeny, nil
	default:
		return domain.PermissionDeny, nil
	}
}

func cliApprovalKey(request domain.PermissionRequest) string {
	return strings.Join([]string{
		request.ToolName,
		request.Action,
		request.ResourceKind,
		request.Scope,
		request.Risk,
	}, "\x00")
}

func (a *StdinApprover) hasPatternApproval(request domain.PermissionRequest) bool {
	for _, approval := range a.patternApprovals {
		if approval.toolName != request.ToolName || approval.action != request.Action || approval.resourceKind != request.ResourceKind || approval.risk != request.Risk {
			continue
		}
		if domain.PermissionRequestMatchesPattern(request, approval.pattern) {
			return true
		}
	}
	return false
}

func cliPermissionChangeSummary(request domain.PermissionRequest) string {
	parts := make([]string, 0, 3)
	if request.ChangeFiles > 0 {
		parts = append(parts, fmt.Sprintf("files=%d", request.ChangeFiles))
	}
	if request.Additions > 0 || request.Deletions > 0 {
		parts = append(parts, fmt.Sprintf("+%d", request.Additions), fmt.Sprintf("-%d", request.Deletions))
	}
	return strings.Join(parts, " ")
}

func fallbackCLIString(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func cliPermissionRequesterLabel(request domain.PermissionRequest) string {
	if request.AgentID == "" || request.AgentID == "manager" {
		return "manager"
	}
	return request.AgentID
}

func cliPermissionRequesterType(request domain.PermissionRequest) string {
	if request.AgentID == "" || request.AgentID == "manager" {
		return "main"
	}
	return "subagent"
}
