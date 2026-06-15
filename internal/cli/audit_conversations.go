package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"yagent/internal/domain"
)

type conversationAuditStore interface {
	ListConversationTurns(context.Context, int) ([]domain.ConversationTurnRecord, error)
}

type conversationAuditSummary struct {
	ID                       string                     `json:"id"`
	RunID                    string                     `json:"run_id,omitempty"`
	RootRunID                string                     `json:"root_run_id,omitempty"`
	Profile                  string                     `json:"profile,omitempty"`
	Status                   domain.RunStatus           `json:"status"`
	CurrentPhase             domain.RunPhase            `json:"current_phase,omitempty"`
	Attempt                  int                        `json:"attempt,omitempty"`
	UserGoal                 string                     `json:"user_goal,omitempty"`
	RequestMessageCount      int                        `json:"request_message_count"`
	ContextMessageCount      int                        `json:"context_message_count"`
	OutputSummary            string                     `json:"output_summary,omitempty"`
	Error                    string                     `json:"error,omitempty"`
	EventCount               int                        `json:"event_count"`
	ToolCallCount            int                        `json:"tool_call_count"`
	ToolFailureCount         int                        `json:"tool_failure_count"`
	ModelCallCount           int                        `json:"model_call_count"`
	CacheHitCount            int                        `json:"cache_hit_count"`
	VerificationFailureCount int                        `json:"verification_failure_count"`
	ArtifactCount            int                        `json:"artifact_count"`
	RequestMessages          []domain.Message           `json:"request_messages,omitempty"`
	ContextMessages          []domain.Message           `json:"context_messages,omitempty"`
	OutputMessage            *domain.Message            `json:"output_message,omitempty"`
	ArtifactRefs             []domain.ArtifactReference `json:"artifact_refs,omitempty"`
	StartedAt                time.Time                  `json:"started_at"`
	CompletedAt              time.Time                  `json:"completed_at"`
}

func newAuditConversationsCommand(configPath *string) *cobra.Command {
	var runID string
	var format string
	var limit int
	var includeMessages bool

	command := &cobra.Command{
		Use:   "conversations",
		Short: "saved conversation turn log を表示",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openAuditStore(*configPath)
			if err != nil {
				return err
			}
			records, err := loadConversationAudit(cmd.Context(), store, runID, limit, includeMessages)
			if err != nil {
				return err
			}
			switch format {
			case "text":
				fmt.Print(renderConversationAuditText(records))
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
	command.Flags().StringVar(&runID, "run", "", "run id/root run id で絞り込む")
	command.Flags().StringVar(&format, "format", "text", "出力形式: text または json")
	command.Flags().IntVar(&limit, "limit", 50, "読み込む conversation turn 件数")
	command.Flags().BoolVar(&includeMessages, "include-messages", false, "JSON 出力に request/context/output message 本文を含める")
	return command
}

func loadConversationAudit(ctx context.Context, store conversationAuditStore, runID string, limit int, includeMessages bool) ([]conversationAuditSummary, error) {
	items, err := store.ListConversationTurns(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]conversationAuditSummary, 0, len(items))
	for _, item := range items {
		if runID != "" && item.RunID != runID && item.RootRunID != runID {
			continue
		}
		out = append(out, summarizeConversationTurn(item, includeMessages))
	}
	return out, nil
}

func summarizeConversationTurn(item domain.ConversationTurnRecord, includeMessages bool) conversationAuditSummary {
	summary := conversationAuditSummary{
		ID:                       item.ID,
		RunID:                    item.RunID,
		RootRunID:                item.RootRunID,
		Profile:                  item.Profile,
		Status:                   item.Status,
		CurrentPhase:             item.CurrentPhase,
		Attempt:                  item.Attempt,
		UserGoal:                 item.UserGoal,
		RequestMessageCount:      len(item.RequestMessages),
		ContextMessageCount:      len(item.ContextMessages),
		OutputSummary:            strings.TrimSpace(item.OutputMessage.Content),
		Error:                    item.Error,
		EventCount:               item.EventCount,
		ToolCallCount:            item.ToolCallCount,
		ToolFailureCount:         item.ToolFailureCount,
		ModelCallCount:           item.ModelCallCount,
		CacheHitCount:            item.CacheHitCount,
		VerificationFailureCount: item.VerificationFailureCount,
		ArtifactCount:            len(item.ArtifactRefs),
		StartedAt:                item.StartedAt,
		CompletedAt:              item.CompletedAt,
	}
	if includeMessages {
		summary.RequestMessages = cloneAuditMessages(item.RequestMessages)
		summary.ContextMessages = cloneAuditMessages(item.ContextMessages)
		message := item.OutputMessage
		summary.OutputMessage = &message
		summary.ArtifactRefs = append([]domain.ArtifactReference(nil), item.ArtifactRefs...)
	}
	return summary
}

func renderConversationAuditText(records []conversationAuditSummary) string {
	var sb strings.Builder
	sb.WriteString("Conversations\n")
	sb.WriteString(fmt.Sprintf("  count: %d\n", len(records)))
	for _, record := range records {
		sb.WriteString(fmt.Sprintf(
			"- %s %s %s phase=%s attempt=%d",
			formatAuditTime(record.CompletedAt),
			fallbackAuditString(record.ID, "-"),
			fallbackAuditString(string(record.Status), "-"),
			fallbackAuditString(string(record.CurrentPhase), "-"),
			record.Attempt,
		))
		if record.RunID != "" {
			sb.WriteString(" run=" + record.RunID)
		}
		if record.Profile != "" {
			sb.WriteString(" profile=" + record.Profile)
		}
		sb.WriteString(fmt.Sprintf(
			" request_messages=%d context_messages=%d events=%d tools=%d tool_failures=%d models=%d cache_hits=%d artifacts=%d",
			record.RequestMessageCount,
			record.ContextMessageCount,
			record.EventCount,
			record.ToolCallCount,
			record.ToolFailureCount,
			record.ModelCallCount,
			record.CacheHitCount,
			record.ArtifactCount,
		))
		if record.VerificationFailureCount > 0 {
			sb.WriteString(fmt.Sprintf(" verification_failures=%d", record.VerificationFailureCount))
		}
		if record.Error != "" {
			sb.WriteString(" error=" + truncateAuditText(record.Error, 120))
		}
		if record.OutputSummary != "" {
			sb.WriteString(" output=" + truncateAuditText(record.OutputSummary, 160))
		}
		if record.UserGoal != "" {
			sb.WriteString(" goal=" + truncateAuditText(record.UserGoal, 120))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func cloneAuditMessages(messages []domain.Message) []domain.Message {
	out := make([]domain.Message, 0, len(messages))
	for _, message := range messages {
		item := message
		item.ToolCalls = append([]domain.ToolCall(nil), message.ToolCalls...)
		if message.Metadata != nil {
			item.Metadata = map[string]string{}
			for key, value := range message.Metadata {
				item.Metadata[key] = value
			}
		}
		out = append(out, item)
	}
	return out
}
