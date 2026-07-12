package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"yagent/internal/domain"
)

func (s *Service) conversationStore() domain.ConversationStore {
	if s.config.ConversationStore != nil {
		return s.config.ConversationStore
	}
	if store, ok := s.config.RuntimeStore.(domain.ConversationStore); ok {
		return store
	}
	if store, ok := s.config.RunStore.(domain.ConversationStore); ok {
		return store
	}
	return nil
}

func (s *Service) loadConversationHistory(ctx context.Context, conversationID domain.ConversationID) ([]domain.Message, string, error) {
	store := s.conversationStore()
	if store == nil {
		return nil, "", fmt.Errorf("conversation continuation requires a conversation store")
	}
	turns, err := store.ListConversationTurns(ctx, 0)
	if err != nil {
		return nil, "", fmt.Errorf("load conversation %q: %w", conversationID, err)
	}
	matched := make([]domain.ConversationTurnRecord, 0)
	for _, turn := range turns {
		if turn.ConversationID == conversationID {
			matched = append(matched, turn)
		}
	}
	if len(matched) == 0 {
		return nil, "", fmt.Errorf("conversation %q was not found", conversationID)
	}
	history := make([]domain.Message, 0, len(matched)*2)
	for index := len(matched) - 1; index >= 0; index-- {
		turn := matched[index]
		history = append(history, normalizeConversationMessages(turn.RequestMessages)...)
		output := turn.OutputMessage
		if (output.Role == "" || strings.TrimSpace(output.Content) == "") && turn.WorkflowID != "" {
			if s.config.WorkflowStore == nil {
				return nil, "", fmt.Errorf("conversation %q turn %q has no output and no durable workflow store", conversationID, turn.ID)
			}
			snapshot, loadErr := s.config.WorkflowStore.LoadWorkflowSnapshot(ctx, turn.WorkflowID)
			if loadErr != nil {
				return nil, "", fmt.Errorf("load conversation %q workflow %q: %w", conversationID, turn.WorkflowID, loadErr)
			}
			if !durableWorkflowTerminal(snapshot) {
				return nil, "", fmt.Errorf("conversation %q workflow %q is not terminal; recover it before continuing", conversationID, turn.WorkflowID)
			}
			output = durableFinalResult(snapshot).Message
		}
		if output.Role != "" && strings.TrimSpace(output.Content) != "" {
			history = append(history, normalizeConversationMessages([]domain.Message{output})...)
		}
	}
	return history, matched[0].Profile, nil
}

func (s *Service) recordConversationTurn(ctx context.Context, run *domain.RunState, request domain.TurnRequest, output domain.Message, events []domain.ExecutionEvent, turnErr error, startedAt time.Time) error {
	store := s.conversationStore()
	if store == nil || run == nil {
		return nil
	}
	record := domain.ConversationTurnRecord{
		ID:              string(run.ConversationTurnID),
		ConversationID:  run.ConversationID,
		WorkflowID:      run.WorkflowID,
		RunID:           run.ID,
		RootRunID:       run.RootRunID,
		Profile:         run.Profile,
		Status:          run.Status,
		CurrentPhase:    run.CurrentPhase,
		Attempt:         run.Attempt,
		UserGoal:        run.UserGoal,
		RequestMessages: cloneMessages(request.Messages),
		ContextMessages: cloneMessages(run.Messages),
		OutputMessage:   cloneMessages([]domain.Message{output})[0],
		EventCount:      len(events),
		ArtifactRefs:    artifactRefsForConversation(run.Artifacts),
		StartedAt:       startedAt,
	}
	if turnErr != nil || run.Status == domain.RunStatusCompleted || run.Status == domain.RunStatusNeedsAttention || run.Status == domain.RunStatusFailed {
		record.CompletedAt = time.Now()
	}
	if turnErr != nil {
		record.Error = turnErr.Error()
		if record.Status == "" || record.Status == domain.RunStatusRunning {
			record.Status = domain.RunStatusFailed
		}
	}
	for _, event := range events {
		switch event.Type {
		case "tool_called":
			record.ToolCallCount++
		case "tool_failed":
			record.ToolCallCount++
			record.ToolFailureCount++
		case "llm_called":
			record.ModelCallCount++
		case "cache_hit":
			record.CacheHitCount++
		}
		if event.Phase == domain.RunPhaseVerify && (event.Status == "failed" || strings.Contains(event.Type, "failed")) {
			record.VerificationFailureCount++
		}
	}
	return store.SaveConversationTurn(ctx, record)
}

func artifactRefsForConversation(artifacts []domain.RunArtifact) []domain.ArtifactReference {
	refs := make([]domain.ArtifactReference, 0, len(artifacts))
	for _, artifact := range artifacts {
		if artifact.ID == "" {
			continue
		}
		refs = append(refs, domain.ArtifactReference{
			ID:   artifact.ID,
			Kind: artifact.Kind,
			Name: artifact.Name,
		})
	}
	return refs
}
