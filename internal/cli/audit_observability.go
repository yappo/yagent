package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"yagent/internal/domain"
)

func newAuditObservationsCommand(configPath *string) *cobra.Command {
	var runID string
	var toolName string
	var format string
	var limit int
	var includeStale bool

	command := &cobra.Command{
		Use:   "observations",
		Short: "reusable observation audit を表示",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openAuditStore(*configPath)
			if err != nil {
				return err
			}
			records, err := store.ListObservations(cmd.Context(), limit)
			if err != nil {
				return err
			}
			records = filterObservationRecords(records, runID, toolName, includeStale)
			switch format {
			case "text":
				fmt.Print(renderObservationAuditText(records))
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
	command.Flags().StringVar(&runID, "run", "", "SessionID で絞り込む")
	command.Flags().StringVar(&toolName, "tool", "", "tool name で絞り込む")
	command.Flags().StringVar(&format, "format", "text", "出力形式: text または json")
	command.Flags().IntVar(&limit, "limit", 100, "読み込む observation record 件数")
	command.Flags().BoolVar(&includeStale, "include-stale", false, "stale observation も含める")
	return command
}

func newAuditMutationsCommand(configPath *string) *cobra.Command {
	var runID string
	var toolName string
	var format string
	var limit int

	command := &cobra.Command{
		Use:   "mutations",
		Short: "workspace mutation audit を表示",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openAuditStore(*configPath)
			if err != nil {
				return err
			}
			records, err := store.ListMutations(cmd.Context(), limit)
			if err != nil {
				return err
			}
			records = filterMutationRecords(records, runID, toolName)
			switch format {
			case "text":
				fmt.Print(renderMutationAuditText(records))
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
	command.Flags().StringVar(&runID, "run", "", "SessionID で絞り込む")
	command.Flags().StringVar(&toolName, "tool", "", "tool name で絞り込む")
	command.Flags().StringVar(&format, "format", "text", "出力形式: text または json")
	command.Flags().IntVar(&limit, "limit", 100, "読み込む mutation record 件数")
	return command
}

func filterObservationRecords(records []domain.ObservationRecord, runID string, toolName string, includeStale bool) []domain.ObservationRecord {
	filtered := make([]domain.ObservationRecord, 0, len(records))
	for _, record := range records {
		if runID != "" && record.SessionID != runID {
			continue
		}
		if toolName != "" && record.ToolName != toolName {
			continue
		}
		if record.Stale && !includeStale {
			continue
		}
		filtered = append(filtered, record)
	}
	return filtered
}

func filterMutationRecords(records []domain.MutationRecord, runID string, toolName string) []domain.MutationRecord {
	filtered := make([]domain.MutationRecord, 0, len(records))
	for _, record := range records {
		if runID != "" && record.SessionID != runID {
			continue
		}
		if toolName != "" && record.ToolName != toolName {
			continue
		}
		filtered = append(filtered, record)
	}
	return filtered
}

func renderObservationAuditText(records []domain.ObservationRecord) string {
	var sb strings.Builder
	sb.WriteString("Observations\n")
	sb.WriteString(fmt.Sprintf("  count: %d\n", len(records)))
	for _, record := range records {
		sb.WriteString(fmt.Sprintf(
			"- %s %s %s %s",
			formatAuditTime(record.UpdatedAt),
			fallbackAuditString(record.ID, "-"),
			fallbackAuditString(record.ToolName, "-"),
			fallbackAuditString(record.SemanticKey, "-"),
		))
		if record.SessionID != "" {
			sb.WriteString(" session=" + record.SessionID)
		}
		if record.OutputArtifactID != "" {
			sb.WriteString(" artifact=" + record.OutputArtifactID)
		}
		if len(record.ReadSet) > 0 {
			sb.WriteString(" reads=" + strings.Join(record.ReadSet, ","))
		}
		if record.SnapshotRevision != 0 {
			sb.WriteString(fmt.Sprintf(" snapshot=%d", record.SnapshotRevision))
		}
		if record.Reusable {
			sb.WriteString(" reusable=true")
		}
		if record.Stale {
			sb.WriteString(" stale=true")
		}
		if record.Summary != "" {
			sb.WriteString(" summary=" + truncateAuditText(record.Summary, 96))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func renderMutationAuditText(records []domain.MutationRecord) string {
	var sb strings.Builder
	sb.WriteString("Mutations\n")
	sb.WriteString(fmt.Sprintf("  count: %d\n", len(records)))
	for _, record := range records {
		sb.WriteString(fmt.Sprintf(
			"- %s %s %s",
			formatAuditTime(record.CreatedAt),
			fallbackAuditString(record.ID, "-"),
			fallbackAuditString(record.ToolName, "-"),
		))
		if record.SessionID != "" {
			sb.WriteString(" session=" + record.SessionID)
		}
		if record.AgentID != "" {
			sb.WriteString(" agent=" + record.AgentID)
		}
		if record.ExecutionID != "" {
			sb.WriteString(" execution=" + record.ExecutionID)
		}
		if len(record.WriteSet) > 0 {
			sb.WriteString(" writes=" + strings.Join(record.WriteSet, ","))
		}
		if record.MutationFingerprint != "" {
			sb.WriteString(" fingerprint=" + record.MutationFingerprint)
		}
		if record.BeforeRevision != 0 || record.AfterRevision != 0 {
			sb.WriteString(fmt.Sprintf(" revision=%d->%d", record.BeforeRevision, record.AfterRevision))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
