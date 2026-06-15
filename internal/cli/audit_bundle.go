package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"yagent/internal/domain"
)

type auditBundle struct {
	Run          runAuditSummary                   `json:"run"`
	FullRun      *domain.RunState                  `json:"full_run,omitempty"`
	Artifacts    []artifactAuditSummary            `json:"artifacts,omitempty"`
	Executions   []domain.ToolExecutionRecord      `json:"executions,omitempty"`
	Models       []domain.ModelInvocationRecord    `json:"models,omitempty"`
	Observations []domain.ObservationRecord        `json:"observations,omitempty"`
	Mutations    []domain.MutationRecord           `json:"mutations,omitempty"`
	Permissions  []domain.PermissionDecisionRecord `json:"permissions,omitempty"`
}

func newAuditBundleCommand(configPath *string) *cobra.Command {
	var runID string
	var format string
	var limit int
	var includeOutput bool
	var includeArtifactContent bool
	var includeArtifactPayload bool
	var includeStale bool
	var fullRun bool

	command := &cobra.Command{
		Use:   "bundle",
		Short: "run に紐づく audit record をまとめて export",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openAuditStore(*configPath)
			if err != nil {
				return err
			}
			bundle, err := buildAuditBundle(cmd.Context(), store, auditBundleOptions{
				RunID:                  runID,
				Limit:                  limit,
				IncludeOutput:          includeOutput,
				IncludeArtifactContent: includeArtifactContent,
				IncludeArtifactPayload: includeArtifactPayload,
				IncludeStale:           includeStale,
				FullRun:                fullRun,
			})
			if err != nil {
				return err
			}
			switch format {
			case "text":
				fmt.Print(renderAuditBundleText(bundle))
			case "json":
				data, err := json.MarshalIndent(bundle, "", "  ")
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
	command.Flags().StringVar(&format, "format", "json", "出力形式: json または text")
	command.Flags().IntVar(&limit, "limit", 1000, "読み込む関連 record 件数")
	command.Flags().BoolVar(&includeOutput, "include-output", false, "tool execution output 本文を含める")
	command.Flags().BoolVar(&includeArtifactContent, "include-artifact-content", false, "artifact の text/content 本文を含める")
	command.Flags().BoolVar(&includeArtifactPayload, "include-artifact-payload", false, "artifact の payload JSON を含める")
	command.Flags().BoolVar(&includeStale, "include-stale", false, "stale observation も含める")
	command.Flags().BoolVar(&fullRun, "full-run", false, "run state 全体を含める")
	return command
}

type auditBundleOptions struct {
	RunID                  string
	Limit                  int
	IncludeOutput          bool
	IncludeArtifactContent bool
	IncludeArtifactPayload bool
	IncludeStale           bool
	FullRun                bool
}

type auditBundleStore interface {
	auditRunStore
	ListExecutions(context.Context, int) ([]domain.ToolExecutionRecord, error)
	ListObservations(context.Context, int) ([]domain.ObservationRecord, error)
	ListMutations(context.Context, int) ([]domain.MutationRecord, error)
	ListScratch(context.Context, int) ([]domain.ScratchRecord, error)
}

func buildAuditBundle(ctx context.Context, store auditBundleStore, options auditBundleOptions) (auditBundle, error) {
	runID := options.RunID
	if runID == "" {
		runID = "latest"
	}
	run, err := loadAuditRun(ctx, store, runID)
	if err != nil {
		return auditBundle{}, err
	}
	resolvedRunID := run.ID

	executions, err := store.ListExecutions(ctx, options.Limit)
	if err != nil {
		return auditBundle{}, err
	}
	observations, err := store.ListObservations(ctx, options.Limit)
	if err != nil {
		return auditBundle{}, err
	}
	mutations, err := store.ListMutations(ctx, options.Limit)
	if err != nil {
		return auditBundle{}, err
	}
	scratch, err := store.ListScratch(ctx, options.Limit)
	if err != nil {
		return auditBundle{}, err
	}

	bundle := auditBundle{
		Run:          summarizeRun(run),
		Artifacts:    filterArtifactSummaries(resolvedRunID, run.Artifacts, "", "", options.IncludeArtifactContent, options.IncludeArtifactPayload),
		Executions:   filterExecutionRecords(executions, resolvedRunID, options.IncludeOutput),
		Models:       modelInvocationRecordsFromScratch(scratch, modelInvocationFilter{RunID: resolvedRunID}),
		Observations: filterObservationRecords(observations, resolvedRunID, "", options.IncludeStale),
		Mutations:    filterMutationRecords(mutations, resolvedRunID, ""),
		Permissions:  permissionRecordsFromScratch(scratch, resolvedRunID),
	}
	if options.FullRun {
		copied := *run
		bundle.FullRun = &copied
	}
	return bundle, nil
}

func renderAuditBundleText(bundle auditBundle) string {
	var sb strings.Builder
	sb.WriteString("Audit bundle\n")
	sb.WriteString(fmt.Sprintf(
		"  run: %s status=%s phase=%s attempt=%d\n",
		fallbackAuditString(bundle.Run.ID, "-"),
		fallbackAuditString(string(bundle.Run.Status), "-"),
		fallbackAuditString(string(bundle.Run.CurrentPhase), "-"),
		bundle.Run.Attempt,
	))
	if bundle.Run.UserGoal != "" {
		sb.WriteString("  goal: " + truncateAuditText(bundle.Run.UserGoal, 160) + "\n")
	}
	sb.WriteString(fmt.Sprintf("  artifacts: %d\n", len(bundle.Artifacts)))
	sb.WriteString(fmt.Sprintf("  executions: %d\n", len(bundle.Executions)))
	sb.WriteString(fmt.Sprintf("  models: %d\n", len(bundle.Models)))
	sb.WriteString(fmt.Sprintf("  observations: %d\n", len(bundle.Observations)))
	sb.WriteString(fmt.Sprintf("  mutations: %d\n", len(bundle.Mutations)))
	sb.WriteString(fmt.Sprintf("  permissions: %d\n", len(bundle.Permissions)))
	return sb.String()
}
