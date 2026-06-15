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

type runAuditSummary struct {
	ID                string           `json:"id"`
	RootRunID         string           `json:"root_run_id,omitempty"`
	Status            domain.RunStatus `json:"status"`
	CurrentPhase      domain.RunPhase  `json:"current_phase"`
	Attempt           int              `json:"attempt"`
	Profile           string           `json:"profile,omitempty"`
	UserGoal          string           `json:"user_goal,omitempty"`
	MessageCount      int              `json:"message_count"`
	PlanCount         int              `json:"plan_count"`
	WorkUnitCount     int              `json:"work_unit_count"`
	ArtifactCount     int              `json:"artifact_count"`
	CheckpointCount   int              `json:"checkpoint_count"`
	VerificationCount int              `json:"verification_count"`
	CreatedAt         time.Time        `json:"created_at"`
	UpdatedAt         time.Time        `json:"updated_at"`
}

type artifactAuditSummary struct {
	ID            string                     `json:"id"`
	RunID         string                     `json:"run_id,omitempty"`
	Name          string                     `json:"name"`
	Kind          string                     `json:"kind"`
	SchemaVersion string                     `json:"schema_version,omitempty"`
	Phase         domain.RunPhase            `json:"phase"`
	AgentID       string                     `json:"agent_id,omitempty"`
	Summary       string                     `json:"summary,omitempty"`
	Text          string                     `json:"text,omitempty"`
	Content       string                     `json:"content,omitempty"`
	Payload       json.RawMessage            `json:"payload,omitempty"`
	References    []domain.ArtifactReference `json:"references,omitempty"`
	CreatedAt     time.Time                  `json:"created_at"`
}

func newAuditRunsCommand(configPath *string) *cobra.Command {
	var runID string
	var format string
	var limit int
	var full bool

	command := &cobra.Command{
		Use:   "runs",
		Short: "saved run state を表示",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openAuditStore(*configPath)
			if err != nil {
				return err
			}
			if full {
				if format != "json" {
					return fmt.Errorf("--full は --format json と一緒に指定してください")
				}
				selectedRunID := runID
				if selectedRunID == "" {
					selectedRunID = "latest"
				}
				run, err := loadAuditRun(cmd.Context(), store, selectedRunID)
				if err != nil {
					return err
				}
				data, err := json.MarshalIndent(run, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(data))
				return nil
			}
			runs, err := loadAuditRuns(cmd.Context(), store, runID, limit)
			if err != nil {
				return err
			}
			switch format {
			case "text":
				fmt.Print(renderRunAuditText(runs))
			case "json":
				data, err := json.MarshalIndent(runs, "", "  ")
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
	command.Flags().StringVar(&runID, "run", "", "run id。latest も指定できます。未指定なら一覧")
	command.Flags().StringVar(&format, "format", "text", "出力形式: text または json")
	command.Flags().IntVar(&limit, "limit", 20, "読み込む run 件数")
	command.Flags().BoolVar(&full, "full", false, "選択した run state 全体を JSON で出力する")
	return command
}

func newAuditArtifactsCommand(configPath *string) *cobra.Command {
	var runID string
	var artifactID string
	var kind string
	var format string
	var includeContent bool
	var includePayload bool

	command := &cobra.Command{
		Use:   "artifacts",
		Short: "saved run artifacts を表示",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openAuditStore(*configPath)
			if err != nil {
				return err
			}
			if runID == "" {
				runID = "latest"
			}
			run, err := loadAuditRun(cmd.Context(), store, runID)
			if err != nil {
				return err
			}
			artifacts := filterArtifactSummaries(run.ID, run.Artifacts, artifactID, kind, includeContent, includePayload)
			switch format {
			case "text":
				fmt.Print(renderArtifactAuditText(artifacts))
			case "json":
				data, err := json.MarshalIndent(artifacts, "", "  ")
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
	command.Flags().StringVar(&artifactID, "artifact", "", "artifact id で絞り込む")
	command.Flags().StringVar(&kind, "kind", "", "artifact kind で絞り込む")
	command.Flags().StringVar(&format, "format", "text", "出力形式: text または json")
	command.Flags().BoolVar(&includeContent, "include-content", false, "artifact の text/content 本文を含める")
	command.Flags().BoolVar(&includePayload, "include-payload", false, "artifact の payload JSON を含める")
	return command
}

type auditRunStore interface {
	ListRuns() ([]string, error)
	LoadRun(context.Context, string) (*domain.RunState, error)
	LoadLatestRun(context.Context) (*domain.RunState, error)
}

func loadAuditRuns(ctx context.Context, store auditRunStore, runID string, limit int) ([]runAuditSummary, error) {
	if runID != "" {
		run, err := loadAuditRun(ctx, store, runID)
		if err != nil {
			return nil, err
		}
		if run == nil {
			return nil, nil
		}
		return []runAuditSummary{summarizeRun(run)}, nil
	}

	ids, err := store.ListRuns()
	if err != nil {
		return nil, err
	}
	summaries := make([]runAuditSummary, 0, len(ids))
	for _, id := range ids {
		run, err := store.LoadRun(ctx, id)
		if err != nil {
			continue
		}
		summaries = append(summaries, summarizeRun(run))
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].UpdatedAt.After(summaries[j].UpdatedAt)
	})
	if limit > 0 && len(summaries) > limit {
		summaries = summaries[:limit]
	}
	return summaries, nil
}

func loadAuditRun(ctx context.Context, store auditRunStore, runID string) (*domain.RunState, error) {
	if runID == "latest" {
		run, err := store.LoadLatestRun(ctx)
		if err != nil {
			return nil, err
		}
		if run == nil {
			return nil, fmt.Errorf("latest run がありません")
		}
		return run, nil
	}
	return store.LoadRun(ctx, runID)
}

func summarizeRun(run *domain.RunState) runAuditSummary {
	if run == nil {
		return runAuditSummary{}
	}
	return runAuditSummary{
		ID:                run.ID,
		RootRunID:         run.RootRunID,
		Status:            run.Status,
		CurrentPhase:      run.CurrentPhase,
		Attempt:           run.Attempt,
		Profile:           run.Profile,
		UserGoal:          run.UserGoal,
		MessageCount:      len(run.Messages),
		PlanCount:         len(run.Plan),
		WorkUnitCount:     len(run.WorkUnits),
		ArtifactCount:     len(run.Artifacts),
		CheckpointCount:   len(run.Checkpoints),
		VerificationCount: len(run.Verification),
		CreatedAt:         run.CreatedAt,
		UpdatedAt:         run.UpdatedAt,
	}
}

func filterArtifactSummaries(runID string, artifacts []domain.RunArtifact, artifactID string, kind string, includeContent bool, includePayload bool) []artifactAuditSummary {
	items := make([]artifactAuditSummary, 0, len(artifacts))
	for _, artifact := range artifacts {
		if artifactID != "" && artifact.ID != artifactID {
			continue
		}
		if kind != "" && artifact.Kind != kind {
			continue
		}
		item := artifactAuditSummary{
			ID:            artifact.ID,
			RunID:         runID,
			Name:          artifact.Name,
			Kind:          artifact.Kind,
			SchemaVersion: artifact.SchemaVersion,
			Phase:         artifact.Phase,
			AgentID:       artifact.AgentID,
			Summary:       artifact.Summary,
			References:    append([]domain.ArtifactReference(nil), artifact.References...),
			CreatedAt:     artifact.CreatedAt,
		}
		if includeContent {
			item.Text = artifact.Text
			item.Content = artifact.Content
		}
		if includePayload {
			item.Payload = append(json.RawMessage(nil), artifact.Payload...)
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return items
}

func renderRunAuditText(runs []runAuditSummary) string {
	var sb strings.Builder
	sb.WriteString("Runs\n")
	sb.WriteString(fmt.Sprintf("  count: %d\n", len(runs)))
	for _, run := range runs {
		sb.WriteString(fmt.Sprintf(
			"- %s %s %s phase=%s attempt=%d",
			formatAuditTime(run.UpdatedAt),
			fallbackAuditString(run.ID, "-"),
			fallbackAuditString(string(run.Status), "-"),
			fallbackAuditString(string(run.CurrentPhase), "-"),
			run.Attempt,
		))
		if run.RootRunID != "" && run.RootRunID != run.ID {
			sb.WriteString(" root=" + run.RootRunID)
		}
		if run.Profile != "" {
			sb.WriteString(" profile=" + run.Profile)
		}
		sb.WriteString(fmt.Sprintf(
			" messages=%d plan=%d work_units=%d artifacts=%d checkpoints=%d verification=%d",
			run.MessageCount,
			run.PlanCount,
			run.WorkUnitCount,
			run.ArtifactCount,
			run.CheckpointCount,
			run.VerificationCount,
		))
		if run.UserGoal != "" {
			sb.WriteString(" goal=" + truncateAuditText(run.UserGoal, 120))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func renderArtifactAuditText(artifacts []artifactAuditSummary) string {
	var sb strings.Builder
	sb.WriteString("Artifacts\n")
	sb.WriteString(fmt.Sprintf("  count: %d\n", len(artifacts)))
	for _, artifact := range artifacts {
		sb.WriteString(fmt.Sprintf(
			"- %s %s %s %s phase=%s",
			formatAuditTime(artifact.CreatedAt),
			fallbackAuditString(artifact.ID, "-"),
			fallbackAuditString(artifact.Kind, "-"),
			fallbackAuditString(artifact.Name, "-"),
			fallbackAuditString(string(artifact.Phase), "-"),
		))
		if artifact.AgentID != "" {
			sb.WriteString(" agent=" + artifact.AgentID)
		}
		if artifact.RunID != "" {
			sb.WriteString(" run=" + artifact.RunID)
		}
		if artifact.Summary != "" {
			sb.WriteString(" summary=" + truncateAuditText(artifact.Summary, 120))
		}
		if artifact.Text != "" {
			sb.WriteString(" text=" + truncateAuditText(artifact.Text, 240))
		}
		if artifact.Content != "" {
			sb.WriteString(" content=" + truncateAuditText(artifact.Content, 240))
		}
		if len(artifact.Payload) > 0 {
			sb.WriteString(" payload=" + truncateAuditText(string(artifact.Payload), 240))
		}
		if len(artifact.References) > 0 {
			sb.WriteString(" refs=" + strings.Join(artifactReferenceIDs(artifact.References), ","))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func artifactReferenceIDs(refs []domain.ArtifactReference) []string {
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.ID != "" {
			ids = append(ids, ref.ID)
		}
	}
	return ids
}

func formatAuditTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Format("2006-01-02T15:04:05Z07:00")
}

func truncateAuditText(value string, limit int) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\n", "\\n")
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}
