package orchestrator

import (
	"context"
	"fmt"
	"time"

	"yagent/internal/domain"
)

func (s *Service) executeToolCall(ctx context.Context, invocation domain.AgentInvocation, item executableCall) (domain.Message, []domain.ExecutionEvent, error) {
	item.call.RunID = invocation.RunID
	item.call.RootRunID = invocation.RootRunID
	item.call.Phase = invocation.Phase
	item.call.Attempt = invocation.Attempt

	spec := s.prepareToolRuntimeSpec(ctx, invocation.Agent, item)
	if s.hasDurableToolContext(invocation) {
		result, record, reused, err := s.executeDurableToolAction(ctx, invocation, spec)
		metrics := toolExecutionMetrics(spec, record)
		if err != nil {
			stateErr := fmt.Errorf("durable tool action failed: %w", err)
			return toolMessage(item.call, "エラー: "+stateErr.Error()), []domain.ExecutionEvent{
				s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "tool_action_failed", invocation.Phase, invocation.Attempt, "failed", stateErr.Error(), "", metrics, countContextItems(invocation.Messages, invocation.Context)),
			}, stateErr
		}
		eventType := "tool_called"
		status := "done"
		if reused {
			eventType = "tool_reused"
		} else if !result.Success {
			eventType = "tool_failed"
			status = "failed"
		}
		return toolMessage(item.call, result.Output), []domain.ExecutionEvent{
			s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, eventType, invocation.Phase, invocation.Attempt, status, item.call.Name, "", metrics, countContextItems(invocation.Messages, invocation.Context)),
		}, nil
	}
	if spec.semantics.SideEffectClass != domain.SideEffectNone || spec.semantics.Class == domain.ToolClassMutate {
		err := fmt.Errorf("tool %q has side effects and requires a durable work-unit lease", item.call.Name)
		return toolMessage(item.call, "エラー: "+err.Error()), []domain.ExecutionEvent{
			s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "tool_action_rejected", invocation.Phase, invocation.Attempt, "failed", err.Error(), "", toolExecutionMetrics(spec, nil), countContextItems(invocation.Messages, invocation.Context)),
		}, err
	}

	if cached, ok, err := s.lookupReusableExecution(ctx, invocation, spec); err != nil {
		stateErr := fmt.Errorf("tool state の参照に失敗しました: %w", err)
		return toolMessage(item.call, "エラー: "+stateErr.Error()), []domain.ExecutionEvent{
			s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "runtime_state_failed", invocation.Phase, invocation.Attempt, "failed", stateErr.Error(), "", map[string]any{"semantic_key": spec.semanticKey}, countContextItems(invocation.Messages, invocation.Context)),
		}, stateErr
	} else if ok {
		return toolMessage(item.call, cached.Output), []domain.ExecutionEvent{
			s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "cache_hit", invocation.Phase, invocation.Attempt, "done", item.call.Name, cached.OutputArtifactID, map[string]any{"semantic_key": cached.SemanticKey}, countContextItems(invocation.Messages, invocation.Context)),
		}, nil
	}

	s.notifyToolEvent(ctx, domain.ToolEvent{Phase: "start", Call: item.call})
	result := s.tools.Execute(ctx, invocation.Agent, item.call)
	s.notifyToolEvent(ctx, domain.ToolEvent{Phase: "finish", Call: item.call, Result: result})

	eventType := "tool_called"
	detail := item.call.Name
	status := "done"
	if !result.Success {
		eventType = "tool_failed"
		status = "failed"
		detail = item.call.Name + ": " + result.Output
	}

	record, persistErr := s.recordToolExecution(ctx, invocation, spec, result, "")
	metrics := toolExecutionMetrics(spec, record)
	events := []domain.ExecutionEvent{
		s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, eventType, invocation.Phase, invocation.Attempt, status, detail, "", metrics, countContextItems(invocation.Messages, invocation.Context)),
	}
	if persistErr != nil {
		stateErr := fmt.Errorf("tool execution state の保存に失敗しました: %w", persistErr)
		events = append(events, s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "runtime_state_failed", invocation.Phase, invocation.Attempt, "failed", stateErr.Error(), "", metrics, countContextItems(invocation.Messages, invocation.Context)))
		return toolMessage(item.call, result.Output), events, stateErr
	}
	return toolMessage(item.call, result.Output), events, nil
}

func toolExecutionMetrics(spec toolRuntimeSpec, record *domain.ToolExecutionRecord) map[string]any {
	metrics := map[string]any{"semantic_key": spec.semanticKey}
	if record == nil {
		return metrics
	}
	if record.MutationID != "" {
		metrics["mutation_id"] = record.MutationID
	}
	if record.MutationFingerprint != "" {
		metrics["mutation_fingerprint"] = record.MutationFingerprint
	}
	if record.WorkspaceRevision > 0 {
		metrics["workspace_revision"] = record.WorkspaceRevision
	}
	return metrics
}

func (s *Service) lookupReusableExecution(ctx context.Context, invocation domain.AgentInvocation, spec toolRuntimeSpec) (*domain.ToolExecutionRecord, bool, error) {
	if s.config.RuntimeStore == nil {
		return nil, false, nil
	}
	if spec.semantics.ReusePolicy != domain.ToolReuseOnSuccess {
		return nil, false, nil
	}
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	record, err := s.config.RuntimeStore.FindReusableExecution(ctx, spec.semanticKey, spec.readSet)
	if err != nil {
		return nil, false, err
	}
	if record == nil || !record.Success {
		return nil, false, nil
	}
	return record, true, nil
}

func (s *Service) recordToolExecution(ctx context.Context, invocation domain.AgentInvocation, spec toolRuntimeSpec, result domain.ToolResult, outputArtifactID string) (*domain.ToolExecutionRecord, error) {
	if s.config.RuntimeStore == nil {
		return nil, nil
	}
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()

	if outputArtifactID == "" {
		outputArtifactID = nextExecutionID("artifact", spec.semanticKey)
	}
	artifact := newToolOutputArtifact(invocation, spec, result, outputArtifactID)
	if err := s.config.RuntimeStore.SaveArtifact(ctx, artifact); err != nil {
		return nil, fmt.Errorf("tool output artifact: %w", err)
	}
	workspaceRevision, err := s.workspaceRevision(ctx)
	if err != nil {
		return nil, fmt.Errorf("workspace revision: %w", err)
	}

	execution := domain.ToolExecutionRecord{
		ID:                nextExecutionID("exec", spec.semanticKey),
		SessionID:         invocation.RootRunID,
		ToolName:          spec.call.Name,
		ToolClass:         spec.semantics.Class,
		AgentID:           invocation.Agent.ID,
		NormalizedArgs:    spec.normalizedArgs,
		SemanticKey:       spec.semanticKey,
		ReadSet:           spec.readSet,
		WriteSet:          spec.writeSet,
		PathStates:        spec.pathStates,
		OutputArtifactID:  outputArtifactID,
		Success:           result.Success,
		Output:            result.Output,
		Failure:           failureMessage(result),
		Reusable:          spec.semantics.ReusePolicy == domain.ToolReuseOnSuccess && result.Success,
		Source:            spec.semantics.Source,
		SideEffectClass:   spec.semantics.SideEffectClass,
		WorkspaceRevision: workspaceRevision,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}

	if execution.Reusable {
		observation := domain.ObservationRecord{
			ID:               nextExecutionID("obs", spec.semanticKey),
			SessionID:        invocation.RootRunID,
			ToolName:         spec.call.Name,
			SemanticKey:      spec.semanticKey,
			Summary:          truncateSummary(result.Output),
			OutputArtifactID: outputArtifactID,
			ReadSet:          spec.readSet,
			PathStates:       spec.pathStates,
			SnapshotRevision: execution.WorkspaceRevision,
			Reusable:         true,
			CreatedAt:        time.Now(),
			UpdatedAt:        time.Now(),
		}
		execution.ObservationID = observation.ID
		if err := s.config.RuntimeStore.SaveObservation(ctx, observation); err != nil {
			return nil, fmt.Errorf("observation: %w", err)
		}
	}

	if len(spec.writeSet) > 0 || spec.semantics.Class == domain.ToolClassMutate {
		mutation, err := s.applyMutationSnapshot(ctx, invocation, execution.ID, spec)
		if err != nil {
			return nil, err
		}
		execution.MutationID = mutation.ID
		execution.MutationFingerprint = mutation.MutationFingerprint
		execution.WorkspaceRevision = mutation.AfterRevision
		if err := s.config.RuntimeStore.SaveMutation(ctx, mutation); err != nil {
			return nil, fmt.Errorf("mutation: %w", err)
		}
	} else {
		if err := s.recordReadSnapshot(ctx, spec.pathStates); err != nil {
			return nil, err
		}
	}
	if err := s.config.RuntimeStore.SaveExecution(ctx, execution); err != nil {
		return nil, fmt.Errorf("execution record: %w", err)
	}
	return &execution, nil
}

func newToolOutputArtifact(invocation domain.AgentInvocation, spec toolRuntimeSpec, result domain.ToolResult, outputArtifactID string) domain.RunArtifact {
	return domain.RunArtifact{
		ID:            outputArtifactID,
		Name:          "Tool output " + spec.call.Name,
		Kind:          "tool_output",
		SchemaVersion: "tool_output.v1",
		Phase:         invocation.Phase,
		AgentID:       invocation.Agent.ID,
		Summary:       truncateSummary(result.Output),
		Text:          result.Output,
		Content:       result.Output,
		Payload: marshalArtifactPayload(domain.ToolOutputArtifactPayload{
			ToolName:       spec.call.Name,
			NormalizedArgs: spec.normalizedArgs,
			SemanticKey:    spec.semanticKey,
			Success:        result.Success,
			Output:         result.Output,
			ReadSet:        spec.readSet,
			WriteSet:       spec.writeSet,
		}),
		CreatedAt: time.Now(),
	}
}

func (s *Service) workspaceRevision(ctx context.Context) (int64, error) {
	if s.config.RuntimeStore == nil {
		return 0, nil
	}
	snapshot, err := s.config.RuntimeStore.LoadWorkspaceSnapshot(ctx)
	if err != nil {
		return 0, err
	}
	if snapshot == nil {
		return 0, nil
	}
	return snapshot.Revision, nil
}

func (s *Service) recordReadSnapshot(ctx context.Context, states []domain.WorkspacePathState) error {
	if s.config.RuntimeStore == nil || len(states) == 0 {
		return nil
	}
	snapshot, err := s.config.RuntimeStore.LoadWorkspaceSnapshot(ctx)
	if err != nil {
		return fmt.Errorf("read workspace snapshot: %w", err)
	}
	if snapshot == nil {
		snapshot = &domain.WorkspaceSnapshot{Paths: map[string]domain.WorkspacePathState{}}
	}
	if snapshot.Paths == nil {
		snapshot.Paths = map[string]domain.WorkspacePathState{}
	}
	for _, state := range states {
		snapshot.Paths[state.Path] = state
	}
	if err := s.config.RuntimeStore.SaveWorkspaceSnapshot(ctx, snapshot); err != nil {
		return fmt.Errorf("save read workspace snapshot: %w", err)
	}
	return nil
}

func (s *Service) applyMutationSnapshot(ctx context.Context, invocation domain.AgentInvocation, executionID string, spec toolRuntimeSpec) (domain.MutationRecord, error) {
	snapshot, err := s.config.RuntimeStore.LoadWorkspaceSnapshot(ctx)
	if err != nil {
		return domain.MutationRecord{}, fmt.Errorf("read workspace snapshot: %w", err)
	}
	if snapshot == nil {
		snapshot = &domain.WorkspaceSnapshot{Paths: map[string]domain.WorkspacePathState{}}
	}
	if snapshot.Paths == nil {
		snapshot.Paths = map[string]domain.WorkspacePathState{}
	}
	before := snapshot.Revision
	states := s.capturePathStates(ctx, spec.writeSet)
	for _, state := range states {
		snapshot.Paths[state.Path] = state
	}
	snapshot.Revision++
	if err := s.config.RuntimeStore.SaveWorkspaceSnapshot(ctx, snapshot); err != nil {
		return domain.MutationRecord{}, fmt.Errorf("save mutation workspace snapshot: %w", err)
	}
	return domain.MutationRecord{
		ID:                  nextExecutionID("mut", spec.semanticKey),
		SessionID:           invocation.RootRunID,
		AgentID:             invocation.Agent.ID,
		ExecutionID:         executionID,
		ToolName:            spec.call.Name,
		WriteSet:            spec.writeSet,
		MutationFingerprint: mutationFingerprint(spec.writeSet, states),
		BeforeRevision:      before,
		AfterRevision:       snapshot.Revision,
		CreatedAt:           time.Now(),
	}, nil
}

func failureMessage(result domain.ToolResult) string {
	if result.Success {
		return ""
	}
	return result.Output
}
