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
	"yagent/internal/usecase/llmcheck"
)

type auditSearchStore interface {
	auditBundleStore
	conversationAuditStore
}

type auditSearchOptions struct {
	Query         string
	RunID         string
	Kind          string
	AgentID       string
	Phase         domain.RunPhase
	Limit         int
	IncludeOutput bool
}

type auditSearchResult struct {
	Kind          string            `json:"kind"`
	ID            string            `json:"id"`
	RunID         string            `json:"run_id,omitempty"`
	RootRunID     string            `json:"root_run_id,omitempty"`
	AgentID       string            `json:"agent_id,omitempty"`
	Phase         domain.RunPhase   `json:"phase,omitempty"`
	Status        string            `json:"status,omitempty"`
	Name          string            `json:"name,omitempty"`
	Summary       string            `json:"summary,omitempty"`
	Timestamp     time.Time         `json:"timestamp,omitempty"`
	MatchedFields []string          `json:"matched_fields,omitempty"`
	Score         int               `json:"score,omitempty"`
	Details       map[string]string `json:"details,omitempty"`
}

type auditSearchCandidate struct {
	result auditSearchResult
	fields map[string]string
}

func newAuditSearchCommand(configPath *string) *cobra.Command {
	var runID string
	var format string
	var limit int
	var kind string
	var agentID string
	var phase string
	var includeOutput bool

	command := &cobra.Command{
		Use:   "search <query>",
		Short: "保存済み audit record を横断検索",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openAuditStore(*configPath)
			if err != nil {
				return err
			}
			results, err := searchAuditRecords(cmd.Context(), store, auditSearchOptions{
				Query:         args[0],
				RunID:         runID,
				Kind:          kind,
				AgentID:       agentID,
				Phase:         domain.RunPhase(phase),
				Limit:         limit,
				IncludeOutput: includeOutput,
			})
			if err != nil {
				return err
			}
			switch format {
			case "text":
				fmt.Print(renderAuditSearchText(args[0], results))
			case "json":
				data, err := json.MarshalIndent(results, "", "  ")
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
	command.Flags().StringVar(&runID, "run", "", "run id/root run id で絞り込む。latest も指定できます")
	command.Flags().StringVar(&kind, "kind", "", "record kind で絞り込む。例: run, conversation, tool, model, permission, runtime, observation, mutation, artifact")
	command.Flags().StringVar(&agentID, "agent", "", "agent id で絞り込む")
	command.Flags().StringVar(&phase, "phase", "", "phase で絞り込む。例: plan, execute, verify")
	command.Flags().StringVar(&format, "format", "text", "出力形式: text または json")
	command.Flags().IntVar(&limit, "limit", 50, "返す検索結果数。読み込み件数にも使います")
	command.Flags().BoolVar(&includeOutput, "include-output", false, "tool output や conversation message 本文も検索対象に含める")
	return command
}

func searchAuditRecords(ctx context.Context, store auditSearchStore, options auditSearchOptions) ([]auditSearchResult, error) {
	query := strings.TrimSpace(options.Query)
	if query == "" {
		return nil, fmt.Errorf("query が必要です")
	}
	if options.Limit <= 0 {
		options.Limit = 50
	}
	if options.RunID == "latest" {
		run, err := loadAuditRun(ctx, store, "latest")
		if err != nil {
			return nil, err
		}
		options.RunID = run.ID
	}

	candidates, err := auditSearchCandidates(ctx, store, options)
	if err != nil {
		return nil, err
	}
	terms := searchTerms(query)
	results := make([]auditSearchResult, 0, len(candidates))
	for _, candidate := range candidates {
		if !auditSearchCandidateAllowed(candidate.result, options) {
			continue
		}
		matched, fields, score := matchAuditSearchFields(candidate.fields, terms)
		if !matched {
			continue
		}
		item := candidate.result
		item.MatchedFields = fields
		item.Score = score
		results = append(results, item)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		if !results[i].Timestamp.Equal(results[j].Timestamp) {
			return results[i].Timestamp.After(results[j].Timestamp)
		}
		if results[i].Kind != results[j].Kind {
			return results[i].Kind < results[j].Kind
		}
		return results[i].ID < results[j].ID
	})
	if len(results) > options.Limit {
		results = results[:options.Limit]
	}
	return results, nil
}

func auditSearchCandidates(ctx context.Context, store auditSearchStore, options auditSearchOptions) ([]auditSearchCandidate, error) {
	limit := options.Limit
	candidates := []auditSearchCandidate{}

	runs, err := loadAuditRuns(ctx, store, options.RunID, limit)
	if err != nil {
		return nil, err
	}
	for _, run := range runs {
		candidates = append(candidates, searchCandidateForRun(run))
	}

	conversations, err := loadConversationAudit(ctx, store, options.RunID, limit, options.IncludeOutput)
	if err != nil {
		return nil, err
	}
	for _, item := range conversations {
		candidates = append(candidates, searchCandidateForConversation(item, options.IncludeOutput))
	}

	executions, err := store.ListExecutions(ctx, limit)
	if err != nil {
		return nil, err
	}
	for _, item := range filterExecutionRecords(executions, options.RunID, options.IncludeOutput) {
		candidates = append(candidates, searchCandidateForExecution(item, options.IncludeOutput))
	}

	observations, err := store.ListObservations(ctx, limit)
	if err != nil {
		return nil, err
	}
	for _, item := range observations {
		if options.RunID != "" && item.SessionID != options.RunID {
			continue
		}
		candidates = append(candidates, searchCandidateForObservation(item))
	}

	mutations, err := store.ListMutations(ctx, limit)
	if err != nil {
		return nil, err
	}
	for _, item := range mutations {
		if options.RunID != "" && item.SessionID != options.RunID {
			continue
		}
		candidates = append(candidates, searchCandidateForMutation(item))
	}

	scratch, err := store.ListScratch(ctx, limit)
	if err != nil {
		return nil, err
	}
	for _, item := range permissionRecordsFromScratch(scratch, options.RunID) {
		candidates = append(candidates, searchCandidateForPermission(item))
	}
	for _, item := range modelInvocationRecordsFromScratch(scratch, modelInvocationFilter{RunID: options.RunID, AgentID: options.AgentID}) {
		candidates = append(candidates, searchCandidateForModel(item))
	}
	if options.RunID == "" {
		for _, item := range doctorAuditRecordsFromScratch(scratch, "") {
			candidates = append(candidates, searchCandidateForRuntime(item))
		}
	}

	for _, summary := range runs {
		run, err := store.LoadRun(ctx, summary.ID)
		if err != nil || run == nil {
			continue
		}
		for _, artifact := range run.Artifacts {
			candidates = append(candidates, searchCandidateForArtifact(artifact, run.ID, run.RootRunID, options.IncludeOutput))
		}
	}

	return candidates, nil
}

func auditSearchCandidateAllowed(item auditSearchResult, options auditSearchOptions) bool {
	if options.Kind != "" && item.Kind != options.Kind {
		return false
	}
	if options.AgentID != "" && item.AgentID != options.AgentID {
		return false
	}
	if options.Phase != "" && item.Phase != options.Phase {
		return false
	}
	if options.RunID != "" && item.RunID != options.RunID && item.RootRunID != options.RunID {
		return false
	}
	return true
}

func searchCandidateForRun(item runAuditSummary) auditSearchCandidate {
	result := auditSearchResult{
		Kind:      "run",
		ID:        item.ID,
		RunID:     item.ID,
		RootRunID: item.RootRunID,
		Phase:     item.CurrentPhase,
		Status:    string(item.Status),
		Name:      item.UserGoal,
		Summary:   item.UserGoal,
		Timestamp: item.UpdatedAt,
		Details: map[string]string{
			"profile": item.Profile,
		},
	}
	return auditSearchCandidate{result: result, fields: map[string]string{
		"id":       item.ID,
		"root_run": item.RootRunID,
		"status":   string(item.Status),
		"phase":    string(item.CurrentPhase),
		"profile":  item.Profile,
		"goal":     item.UserGoal,
		"counts":   fmt.Sprintf("messages=%d plan=%d work_units=%d artifacts=%d checkpoints=%d verification=%d", item.MessageCount, item.PlanCount, item.WorkUnitCount, item.ArtifactCount, item.CheckpointCount, item.VerificationCount),
	}}
}

func searchCandidateForConversation(item conversationAuditSummary, includeOutput bool) auditSearchCandidate {
	fields := map[string]string{
		"id":      item.ID,
		"run":     item.RunID,
		"root":    item.RootRunID,
		"profile": item.Profile,
		"status":  string(item.Status),
		"phase":   string(item.CurrentPhase),
		"goal":    item.UserGoal,
		"output":  item.OutputSummary,
		"error":   item.Error,
	}
	if includeOutput {
		fields["messages"] = messagesSearchText(item.RequestMessages) + " " + messagesSearchText(item.ContextMessages)
		if item.OutputMessage != nil {
			fields["output_message"] = item.OutputMessage.Content
		}
	}
	return auditSearchCandidate{
		result: auditSearchResult{
			Kind:      "conversation",
			ID:        item.ID,
			RunID:     item.RunID,
			RootRunID: item.RootRunID,
			Phase:     item.CurrentPhase,
			Status:    string(item.Status),
			Name:      item.UserGoal,
			Summary:   firstNonEmpty(item.Error, item.OutputSummary),
			Timestamp: item.CompletedAt,
			Details: map[string]string{
				"profile":       item.Profile,
				"tools":         fmt.Sprintf("%d", item.ToolCallCount),
				"tool_failures": fmt.Sprintf("%d", item.ToolFailureCount),
				"models":        fmt.Sprintf("%d", item.ModelCallCount),
			},
		},
		fields: fields,
	}
}

func searchCandidateForExecution(item domain.ToolExecutionRecord, includeOutput bool) auditSearchCandidate {
	status := "fail"
	if item.Success {
		status = "ok"
	}
	fields := map[string]string{
		"id":                   item.ID,
		"session":              item.SessionID,
		"tool":                 item.ToolName,
		"class":                string(item.ToolClass),
		"agent":                item.AgentID,
		"semantic_key":         item.SemanticKey,
		"normalized_args":      item.NormalizedArgs,
		"read_set":             strings.Join(item.ReadSet, " "),
		"write_set":            strings.Join(item.WriteSet, " "),
		"failure":              item.Failure,
		"source":               item.Source,
		"side_effect":          string(item.SideEffectClass),
		"mutation":             item.MutationID,
		"mutation_fingerprint": item.MutationFingerprint,
		"observation":          item.ObservationID,
		"status":               status,
	}
	if includeOutput {
		fields["output"] = item.Output
	}
	return auditSearchCandidate{
		result: auditSearchResult{
			Kind:      "tool",
			ID:        item.ID,
			RunID:     item.SessionID,
			RootRunID: item.SessionID,
			AgentID:   item.AgentID,
			Status:    status,
			Name:      item.ToolName,
			Summary:   firstNonEmpty(item.Failure, item.SemanticKey),
			Timestamp: item.UpdatedAt,
			Details: map[string]string{
				"class":       string(item.ToolClass),
				"source":      item.Source,
				"side_effect": string(item.SideEffectClass),
			},
		},
		fields: fields,
	}
}

func searchCandidateForObservation(item domain.ObservationRecord) auditSearchCandidate {
	status := "fresh"
	if item.Stale {
		status = "stale"
	}
	return auditSearchCandidate{
		result: auditSearchResult{
			Kind:      "observation",
			ID:        item.ID,
			RunID:     item.SessionID,
			RootRunID: item.SessionID,
			Status:    status,
			Name:      item.ToolName,
			Summary:   item.Summary,
			Timestamp: item.UpdatedAt,
		},
		fields: map[string]string{
			"id":           item.ID,
			"session":      item.SessionID,
			"tool":         item.ToolName,
			"semantic_key": item.SemanticKey,
			"summary":      item.Summary,
			"read_set":     strings.Join(item.ReadSet, " "),
			"artifact":     item.OutputArtifactID,
			"status":       status,
		},
	}
}

func searchCandidateForMutation(item domain.MutationRecord) auditSearchCandidate {
	return auditSearchCandidate{
		result: auditSearchResult{
			Kind:      "mutation",
			ID:        item.ID,
			RunID:     item.SessionID,
			RootRunID: item.SessionID,
			AgentID:   item.AgentID,
			Name:      item.ToolName,
			Summary:   strings.Join(item.WriteSet, ","),
			Timestamp: item.CreatedAt,
			Details: map[string]string{
				"fingerprint": item.MutationFingerprint,
			},
		},
		fields: map[string]string{
			"id":          item.ID,
			"session":     item.SessionID,
			"agent":       item.AgentID,
			"execution":   item.ExecutionID,
			"tool":        item.ToolName,
			"write_set":   strings.Join(item.WriteSet, " "),
			"fingerprint": item.MutationFingerprint,
		},
	}
}

func searchCandidateForPermission(item domain.PermissionDecisionRecord) auditSearchCandidate {
	return auditSearchCandidate{
		result: auditSearchResult{
			Kind:      "permission",
			ID:        item.ToolName + ":" + item.Resource,
			RunID:     item.RootRunID,
			RootRunID: item.RootRunID,
			AgentID:   item.AgentID,
			Phase:     item.Phase,
			Status:    string(item.Decision),
			Name:      item.ToolName,
			Summary:   firstNonEmpty(item.Error, item.Summary, item.Resource),
			Timestamp: item.CreatedAt,
			Details: map[string]string{
				"risk":     item.Risk,
				"scope":    item.Scope,
				"resource": item.Resource,
			},
		},
		fields: map[string]string{
			"run":          item.RunID,
			"root":         item.RootRunID,
			"agent":        item.AgentID,
			"phase":        string(item.Phase),
			"decision":     string(item.Decision),
			"tool":         item.ToolName,
			"operation":    item.Operation,
			"resource":     item.Resource,
			"action":       item.Action,
			"risk":         item.Risk,
			"scope":        item.Scope,
			"summary":      item.Summary,
			"side_effects": strings.Join(item.SideEffects, " "),
			"error":        item.Error,
		},
	}
}

func searchCandidateForModel(item domain.ModelInvocationRecord) auditSearchCandidate {
	status := "fail"
	if item.Success {
		status = "ok"
	}
	return auditSearchCandidate{
		result: auditSearchResult{
			Kind:      "model",
			ID:        item.ID,
			RunID:     item.RunID,
			RootRunID: item.RootRunID,
			AgentID:   item.AgentID,
			Phase:     item.Phase,
			Status:    status,
			Name:      item.Model,
			Summary:   firstNonEmpty(item.Error, item.FinishReason),
			Timestamp: item.CreatedAt,
			Details: map[string]string{
				"server":  item.ServerName,
				"api":     item.API,
				"profile": item.ProfileName,
			},
		},
		fields: map[string]string{
			"id":          item.ID,
			"run":         item.RunID,
			"root":        item.RootRunID,
			"agent":       item.AgentID,
			"phase":       string(item.Phase),
			"server":      item.ServerName,
			"model":       item.Model,
			"api":         item.API,
			"profile":     item.ProfileName,
			"format":      item.ResponseFormat,
			"finish":      item.FinishReason,
			"error":       item.Error,
			"fallback":    fmt.Sprintf("%t %s", item.Fallback, item.FallbackFromServer),
			"status":      status,
			"settings":    modelSettingsSearchText(item.Settings),
			"duration_ms": fmt.Sprintf("%d", item.DurationMS),
		},
	}
}

func searchCandidateForRuntime(item llmcheck.AuditRecord) auditSearchCandidate {
	status := doctorAuditStatus(item)
	return auditSearchCandidate{
		result: auditSearchResult{
			Kind:      "runtime",
			ID:        item.ID,
			Status:    status,
			Name:      item.ServerName,
			Summary:   firstNonEmpty(strings.Join(item.Problems, "; "), strings.Join(item.Warnings, "; "), item.MatchedModel),
			Timestamp: item.CreatedAt,
			Details: map[string]string{
				"model":   item.Model,
				"matched": item.MatchedModel,
				"api":     item.API,
			},
		},
		fields: map[string]string{
			"id":              item.ID,
			"server":          item.ServerName,
			"model":           item.Model,
			"matched_model":   item.MatchedModel,
			"api":             item.API,
			"endpoint":        item.Runtime.Endpoint,
			"runtime":         runtimeSearchText(item),
			"probe":           item.Probe.Output + " " + item.Probe.Error,
			"warnings":        strings.Join(item.Warnings, " "),
			"problems":        strings.Join(item.Problems, " "),
			"recommendations": recommendationsSearchText(item.Recommendations),
			"status":          status,
		},
	}
}

func searchCandidateForArtifact(item domain.RunArtifact, runID string, rootRunID string, includeOutput bool) auditSearchCandidate {
	fields := map[string]string{
		"id":      item.ID,
		"name":    item.Name,
		"kind":    item.Kind,
		"phase":   string(item.Phase),
		"agent":   item.AgentID,
		"summary": item.Summary,
		"refs":    artifactReferenceSearchText(item.References),
	}
	if includeOutput {
		fields["text"] = item.Text + " " + item.Content + " " + string(item.Payload)
	}
	return auditSearchCandidate{
		result: auditSearchResult{
			Kind:      "artifact",
			ID:        item.ID,
			RunID:     runID,
			RootRunID: rootRunID,
			AgentID:   item.AgentID,
			Phase:     item.Phase,
			Status:    "created",
			Name:      item.Name,
			Summary:   firstNonEmpty(item.Summary, item.Kind),
			Timestamp: item.CreatedAt,
		},
		fields: fields,
	}
}

func searchTerms(query string) []string {
	parts := strings.Fields(strings.ToLower(query))
	if len(parts) == 0 {
		return nil
	}
	return parts
}

func matchAuditSearchFields(fields map[string]string, terms []string) (bool, []string, int) {
	if len(terms) == 0 {
		return false, nil, 0
	}
	var haystack strings.Builder
	for name, value := range fields {
		if strings.TrimSpace(value) == "" {
			continue
		}
		haystack.WriteString(" ")
		haystack.WriteString(name)
		haystack.WriteString(" ")
		haystack.WriteString(strings.ToLower(value))
	}
	allText := haystack.String()
	for _, term := range terms {
		if !strings.Contains(allText, term) {
			return false, nil, 0
		}
	}
	matchedSet := map[string]bool{}
	score := 0
	for name, value := range fields {
		value = strings.ToLower(value)
		if strings.TrimSpace(value) == "" {
			continue
		}
		for _, term := range terms {
			if strings.Contains(value, term) || strings.Contains(strings.ToLower(name), term) {
				matchedSet[name] = true
				score += strings.Count(value, term)
				if strings.Contains(strings.ToLower(name), term) {
					score++
				}
			}
		}
	}
	matched := sortedStringSet(matchedSet)
	if score == 0 {
		score = len(matched)
	}
	return true, matched, score
}

func renderAuditSearchText(query string, results []auditSearchResult) string {
	var sb strings.Builder
	sb.WriteString("Audit search\n")
	sb.WriteString(fmt.Sprintf("  query: %s\n", query))
	sb.WriteString(fmt.Sprintf("  count: %d\n", len(results)))
	for _, item := range results {
		sb.WriteString(fmt.Sprintf(
			"- %s %s %s",
			formatAuditTime(item.Timestamp),
			item.Kind,
			fallbackAuditString(item.ID, "-"),
		))
		if item.Status != "" {
			sb.WriteString(" status=" + item.Status)
		}
		if item.Name != "" {
			sb.WriteString(" name=" + truncateAuditText(item.Name, 80))
		}
		if item.RunID != "" {
			sb.WriteString(" run=" + item.RunID)
		}
		if item.AgentID != "" {
			sb.WriteString(" agent=" + item.AgentID)
		}
		if item.Phase != "" {
			sb.WriteString(" phase=" + string(item.Phase))
		}
		if len(item.MatchedFields) > 0 {
			sb.WriteString(" matches=" + strings.Join(item.MatchedFields, ","))
		}
		if item.Summary != "" {
			sb.WriteString(" summary=" + truncateAuditText(item.Summary, 160))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func messagesSearchText(messages []domain.Message) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		parts = append(parts, string(message.Role), message.AgentID, message.Content)
		for _, call := range message.ToolCalls {
			parts = append(parts, call.Name, call.Purpose)
			if len(call.Arguments) > 0 {
				data, _ := json.Marshal(call.Arguments)
				parts = append(parts, string(data))
			}
		}
	}
	return strings.Join(parts, " ")
}

func artifactReferenceSearchText(refs []domain.ArtifactReference) string {
	parts := make([]string, 0, len(refs)*3)
	for _, ref := range refs {
		parts = append(parts, ref.ID, ref.Kind, ref.Name)
	}
	return strings.Join(parts, " ")
}

func modelSettingsSearchText(settings domain.ModelInvocationSettings) string {
	data, err := json.Marshal(settings)
	if err != nil {
		return ""
	}
	return string(data)
}

func runtimeSearchText(record llmcheck.AuditRecord) string {
	parts := []string{
		record.Runtime.Endpoint,
		record.Runtime.Error,
		record.Runtime.MatchedModel.ID,
		record.Runtime.MatchedModel.Params,
		record.Runtime.MatchedModel.Quantization,
		record.Runtime.MatchedModel.Format,
		record.Runtime.MatchedModel.ReasoningDefault,
		strings.Join(record.Runtime.MatchedModel.ReasoningAllowed, " "),
	}
	for _, model := range record.Runtime.Models {
		parts = append(parts, model.ID, model.Params, model.Quantization, model.Format)
	}
	return strings.Join(parts, " ")
}

func recommendationsSearchText(items []llmcheck.Recommendation) string {
	parts := make([]string, 0, len(items)*5)
	for _, item := range items {
		parts = append(parts, item.Area, item.Setting, item.Current, item.Recommended, item.Reason)
	}
	return strings.Join(parts, " ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
