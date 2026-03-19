package orchestrator

import (
	"context"
	"time"

	"yagent/internal/domain"
)

func (s *Service) executeToolCall(ctx context.Context, invocation domain.AgentInvocation, item executableCall) (domain.Message, []domain.ExecutionEvent) {
	spec := s.prepareToolRuntimeSpec(ctx, invocation.Agent, item)
	if cached, ok := s.lookupReusableExecution(ctx, invocation, spec); ok {
		return toolMessage(item.call, cached.Output), []domain.ExecutionEvent{
			s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, "cache_hit", invocation.Phase, invocation.Attempt, "done", item.call.Name, cached.OutputArtifactID, map[string]any{"semantic_key": cached.SemanticKey}, countContextItems(invocation.Messages, invocation.Context)),
		}
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

	s.recordToolExecution(ctx, invocation, spec, result)
	return toolMessage(item.call, result.Output), []domain.ExecutionEvent{
		s.newEvent(invocation.RunID, invocation.ParentRunID, invocation.Agent.ID, eventType, invocation.Phase, invocation.Attempt, status, detail, "", map[string]any{"semantic_key": spec.semanticKey}, countContextItems(invocation.Messages, invocation.Context)),
	}
}

func (s *Service) lookupReusableExecution(ctx context.Context, invocation domain.AgentInvocation, spec toolRuntimeSpec) (*domain.ToolExecutionRecord, bool) {
	if s.config.RuntimeStore == nil {
		return nil, false
	}
	if spec.semantics.ReusePolicy != domain.ToolReuseOnSuccess {
		return nil, false
	}
	record, err := s.config.RuntimeStore.FindReusableExecution(ctx, spec.semanticKey, spec.readSet)
	if err != nil || record == nil || !record.Success {
		return nil, false
	}
	return record, true
}

func (s *Service) recordToolExecution(ctx context.Context, invocation domain.AgentInvocation, spec toolRuntimeSpec, result domain.ToolResult) {
	if s.config.RuntimeStore == nil {
		return
	}

	outputArtifactID := nextExecutionID("artifact", spec.semanticKey)
	artifact := domain.RunArtifact{
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
	_ = s.config.RuntimeStore.SaveArtifact(ctx, artifact)

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
		WorkspaceRevision: s.workspaceRevision(ctx),
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
		_ = s.config.RuntimeStore.SaveObservation(ctx, observation)
	}

	if len(spec.writeSet) > 0 || spec.semantics.Class == domain.ToolClassMutate {
		mutation := s.applyMutationSnapshot(ctx, invocation, execution.ID, spec)
		execution.MutationID = mutation.ID
		execution.MutationFingerprint = mutation.MutationFingerprint
		execution.WorkspaceRevision = mutation.AfterRevision
		_ = s.config.RuntimeStore.SaveMutation(ctx, mutation)
	} else {
		s.recordReadSnapshot(ctx, spec.pathStates)
	}
	_ = s.config.RuntimeStore.SaveExecution(ctx, execution)
}

func (s *Service) workspaceRevision(ctx context.Context) int64 {
	if s.config.RuntimeStore == nil {
		return 0
	}
	snapshot, err := s.config.RuntimeStore.LoadWorkspaceSnapshot(ctx)
	if err != nil || snapshot == nil {
		return 0
	}
	return snapshot.Revision
}

func (s *Service) recordReadSnapshot(ctx context.Context, states []domain.WorkspacePathState) {
	if s.config.RuntimeStore == nil || len(states) == 0 {
		return
	}
	snapshot, err := s.config.RuntimeStore.LoadWorkspaceSnapshot(ctx)
	if err != nil || snapshot == nil {
		snapshot = &domain.WorkspaceSnapshot{Paths: map[string]domain.WorkspacePathState{}}
	}
	if snapshot.Paths == nil {
		snapshot.Paths = map[string]domain.WorkspacePathState{}
	}
	for _, state := range states {
		snapshot.Paths[state.Path] = state
	}
	_ = s.config.RuntimeStore.SaveWorkspaceSnapshot(ctx, snapshot)
}

func (s *Service) applyMutationSnapshot(ctx context.Context, invocation domain.AgentInvocation, executionID string, spec toolRuntimeSpec) domain.MutationRecord {
	snapshot, err := s.config.RuntimeStore.LoadWorkspaceSnapshot(ctx)
	if err != nil || snapshot == nil {
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
	_ = s.config.RuntimeStore.SaveWorkspaceSnapshot(ctx, snapshot)
	return domain.MutationRecord{
		ID:                  nextExecutionID("mut", spec.semanticKey),
		SessionID:           invocation.RootRunID,
		AgentID:             invocation.Agent.ID,
		ExecutionID:         executionID,
		ToolName:            spec.call.Name,
		WriteSet:            spec.writeSet,
		MutationFingerprint: writeSetFingerprint(spec.writeSet),
		BeforeRevision:      before,
		AfterRevision:       snapshot.Revision,
		CreatedAt:           time.Now(),
	}
}

func failureMessage(result domain.ToolResult) string {
	if result.Success {
		return ""
	}
	return result.Output
}
