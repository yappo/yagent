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

func (s *Service) recordConversationTurn(ctx context.Context, run *domain.RunState, request domain.TurnRequest, output domain.Message, events []domain.ExecutionEvent, turnErr error, startedAt time.Time) error {
	store := s.conversationStore()
	if store == nil || run == nil {
		return nil
	}
	completedAt := time.Now()
	record := domain.ConversationTurnRecord{
		ID:              fmt.Sprintf("turn-%s-%d", fallbackString(run.ID, "unknown"), startedAt.UnixNano()),
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
		CompletedAt:     completedAt,
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
