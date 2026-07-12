package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"yagent/internal/domain"
)

func (s *Service) hasDurableToolContext(invocation domain.AgentInvocation) bool {
	return s.config.WorkflowStore != nil && invocation.WorkflowID != "" && invocation.WorkUnitID != "" && invocation.Lease.Token != "" && invocation.Lease.FencingToken != 0 && invocation.Attempt > 0
}

func (s *Service) executeDurableToolAction(ctx context.Context, invocation domain.AgentInvocation, spec toolRuntimeSpec) (domain.ToolResult, *domain.ToolExecutionRecord, bool, error) {
	if cached, ok, err := s.lookupReusableExecution(ctx, invocation, spec); err != nil {
		return domain.ToolResult{}, nil, false, fmt.Errorf("lookup reusable tool execution: %w", err)
	} else if ok {
		return domain.ToolResult{CallID: spec.call.ID, Name: spec.call.Name, Success: true, Output: cached.Output}, cached, true, nil
	}
	actionID, idempotencyKey := durableToolActionIdentifiers(invocation, spec)
	prepared, err := s.prepareDurableToolAction(ctx, invocation, spec, actionID, idempotencyKey)
	if err != nil {
		return domain.ToolResult{}, nil, false, err
	}
	action, ok := durableAction(prepared, actionID)
	if !ok {
		return domain.ToolResult{}, nil, false, fmt.Errorf("prepared action %q is missing", actionID)
	}
	if action.Status != domain.ActionStatusPrepared {
		result, err := durableActionResult(prepared, action, spec.call)
		return result, nil, action.Status == domain.ActionStatusSucceeded, err
	}

	started, err := s.startDurableToolAction(ctx, invocation, actionID)
	if err != nil {
		return domain.ToolResult{}, nil, false, err
	}
	action, ok = durableAction(started, actionID)
	if !ok || action.Status != domain.ActionStatusExecuting {
		if !ok {
			return domain.ToolResult{}, nil, false, fmt.Errorf("started action %q is missing", actionID)
		}
		result, err := durableActionResult(started, action, spec.call)
		return result, nil, action.Status == domain.ActionStatusSucceeded, err
	}

	s.notifyToolEvent(ctx, domain.ToolEvent{Phase: "start", Call: spec.call})
	toolCtx := domain.WithDurableActionExecutionContext(ctx, domain.DurableActionExecutionContext{
		ActionID: actionID, WorkflowID: invocation.WorkflowID, WorkUnitID: invocation.WorkUnitID, Attempt: invocation.Attempt,
		IdempotencyKey: idempotencyKey, LeaseToken: invocation.Lease.Token, FencingToken: invocation.Lease.FencingToken,
	})
	result := s.tools.Execute(toolCtx, invocation.Agent, spec.call)
	s.notifyToolEvent(ctx, domain.ToolEvent{Phase: "finish", Call: spec.call, Result: result})

	artifactID := "tool-output-" + string(actionID)
	artifact := newToolOutputArtifact(invocation, spec, result, artifactID)
	record, persistErr := s.recordToolExecution(ctx, invocation, spec, result, artifactID)
	if persistErr != nil {
		completion := s.durableToolCompletion(ctx, invocation, spec, result, artifact, record, domain.ActionStatusFailed, persistErr.Error())
		if durableToolHasSideEffect(spec) {
			completion.Status = domain.ActionStatusAmbiguous
			completion.Reason = "tool effect completed but runtime persistence failed: " + persistErr.Error()
		}
		if _, finishErr := s.finishDurableToolAction(ctx, invocation, actionID, completion); finishErr != nil {
			return result, record, false, fmt.Errorf("persist tool result: %w; record terminal action: %v", persistErr, finishErr)
		}
		return result, record, false, fmt.Errorf("persist tool result: %w", persistErr)
	}

	status := domain.ActionStatusSucceeded
	reason := ""
	if !result.Success {
		status = domain.ActionStatusFailed
		reason = failureMessage(result)
		if durableToolHasSideEffect(spec) {
			status = domain.ActionStatusAmbiguous
			reason = "tool reported failure after a possible side effect: " + reason
		}
	}
	completion := s.durableToolCompletion(ctx, invocation, spec, result, artifact, record, status, reason)
	if _, err := s.finishDurableToolAction(ctx, invocation, actionID, completion); err != nil {
		if durableToolHasSideEffect(spec) {
			ambiguous := s.durableToolCompletion(ctx, invocation, spec, result, artifact, record, domain.ActionStatusAmbiguous, "tool effect completed but durable completion could not be persisted: "+err.Error())
			if _, ambiguousErr := s.finishDurableToolAction(ctx, invocation, actionID, ambiguous); ambiguousErr != nil {
				return result, record, false, fmt.Errorf("finish durable action: %w; mark action ambiguous: %v", err, ambiguousErr)
			}
		}
		return result, record, false, fmt.Errorf("finish durable action: %w", err)
	}
	if !result.Success {
		if durableToolHasSideEffect(spec) {
			return result, record, false, fmt.Errorf("tool %q failed with an ambiguous side effect: %s", spec.call.Name, result.Output)
		}
	}
	return result, record, false, nil
}

func (s *Service) prepareDurableToolAction(ctx context.Context, invocation domain.AgentInvocation, spec toolRuntimeSpec, actionID domain.ActionID, idempotencyKey string) (domain.WorkflowSnapshot, error) {
	paths := compactPaths(append(append([]string(nil), spec.readSet...), spec.writeSet...))
	states := s.capturePathStates(ctx, paths)
	precondition := durableToolPreconditionFingerprint(spec.normalizedArgs, paths, states)
	return s.commitWorkflowTransition(ctx, invocation.WorkflowID, func(snapshot domain.WorkflowSnapshot) (domain.WorkflowSnapshot, error) {
		return domain.PrepareAction(snapshot, domain.PrepareActionInput{
			ExpectedRevision: snapshot.Workflow.Revision,
			Action: domain.DurableActionInput{
				ID:                      actionID,
				WorkflowID:              invocation.WorkflowID,
				WorkUnitID:              invocation.WorkUnitID,
				Attempt:                 invocation.Attempt,
				Kind:                    "tool_call",
				Target:                  spec.call.Name,
				IdempotencyKey:          idempotencyKey,
				Lease:                   invocation.Lease,
				NormalizedArguments:     spec.normalizedArgs,
				ReadSet:                 spec.readSet,
				WriteSet:                spec.writeSet,
				SideEffectClass:         spec.semantics.SideEffectClass,
				PreconditionFingerprint: precondition,
			},
			At: workflowActionTransitionTime(snapshot),
		})
	}, func(snapshot domain.WorkflowSnapshot) bool {
		_, ok := durableAction(snapshot, actionID)
		return ok
	})
}

func (s *Service) startDurableToolAction(ctx context.Context, invocation domain.AgentInvocation, actionID domain.ActionID) (domain.WorkflowSnapshot, error) {
	return s.commitWorkflowTransition(ctx, invocation.WorkflowID, func(snapshot domain.WorkflowSnapshot) (domain.WorkflowSnapshot, error) {
		return domain.StartAction(snapshot, actionID, domain.WorkflowLeaseCredential{ExpectedRevision: snapshot.Workflow.Revision, LeaseCredential: invocation.Lease}, workflowActionTransitionTime(snapshot))
	}, func(snapshot domain.WorkflowSnapshot) bool {
		action, ok := durableAction(snapshot, actionID)
		return ok && action.Status == domain.ActionStatusExecuting
	})
}

func (s *Service) finishDurableToolAction(ctx context.Context, invocation domain.AgentInvocation, actionID domain.ActionID, completion domain.ActionCompletion) (domain.WorkflowSnapshot, error) {
	return s.commitWorkflowTransition(ctx, invocation.WorkflowID, func(snapshot domain.WorkflowSnapshot) (domain.WorkflowSnapshot, error) {
		return domain.FinishAction(snapshot, actionID, completion, domain.WorkflowLeaseCredential{ExpectedRevision: snapshot.Workflow.Revision, LeaseCredential: invocation.Lease}, workflowActionTransitionTime(snapshot))
	}, func(snapshot domain.WorkflowSnapshot) bool {
		action, ok := durableAction(snapshot, actionID)
		return ok && action.Status == completion.Status
	})
}

func workflowActionTransitionTime(snapshot domain.WorkflowSnapshot) time.Time {
	at := time.Now()
	if at.Before(snapshot.Workflow.UpdatedAt) {
		return snapshot.Workflow.UpdatedAt
	}
	return at
}

func durableToolActionIdentifiers(invocation domain.AgentInvocation, spec toolRuntimeSpec) (domain.ActionID, string) {
	parts := []string{string(invocation.WorkflowID), string(invocation.WorkUnitID), fmt.Sprintf("%d", invocation.Attempt), fmt.Sprintf("%d", invocation.Lease.FencingToken), spec.call.ID, spec.semanticKey}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	digest := hex.EncodeToString(sum[:])
	return domain.ActionID("tool-action-" + digest), "tool-action:" + digest
}

func durableToolPreconditionFingerprint(normalizedArgs string, paths []string, states []domain.WorkspacePathState) string {
	sum := sha256.Sum256([]byte(normalizedArgs + "\x00" + mutationFingerprint(paths, states)))
	return hex.EncodeToString(sum[:])
}

func (s *Service) durableToolCompletion(ctx context.Context, invocation domain.AgentInvocation, spec toolRuntimeSpec, toolResult domain.ToolResult, artifact domain.RunArtifact, record *domain.ToolExecutionRecord, status domain.ActionStatus, reason string) domain.ActionCompletion {
	result := domain.DurableActionResult{
		ResultArtifactRefs: []domain.ArtifactReference{{ID: artifact.ID, Kind: "tool_output", Name: artifact.Name}},
	}
	if record != nil {
		result.ExecutionRef = record.ID
		if record.MutationID != "" {
			result.MutationRefs = []string{record.MutationID}
		}
	}
	if status == domain.ActionStatusSucceeded {
		if spec.semantics.SideEffectClass == domain.SideEffectWorkspace {
			result.PostconditionFingerprint = mutationFingerprint(spec.writeSet, s.capturePathStates(ctx, spec.writeSet))
			if record != nil && record.MutationFingerprint != "" {
				result.PostconditionFingerprint = record.MutationFingerprint
			}
		} else {
			result.PostconditionFingerprint = durableToolResultFingerprint(toolResult)
		}
	}
	return domain.ActionCompletion{Status: status, Result: result, Reason: reason, Artifacts: []domain.RunArtifact{artifact}}
}

func durableToolResultFingerprint(result domain.ToolResult) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%t\x00%s\x00%s\x00%s", result.Success, result.CallID, result.Name, result.Output)))
	return hex.EncodeToString(sum[:])
}

func durableToolHasSideEffect(spec toolRuntimeSpec) bool {
	return spec.semantics.SideEffectClass != domain.SideEffectNone || spec.semantics.Class == domain.ToolClassMutate
}

func durableAction(snapshot domain.WorkflowSnapshot, actionID domain.ActionID) (domain.DurableAction, bool) {
	for _, action := range snapshot.Actions {
		if action.ID == actionID {
			return action, true
		}
	}
	return domain.DurableAction{}, false
}

func durableActionResult(snapshot domain.WorkflowSnapshot, action domain.DurableAction, call domain.ToolCall) (domain.ToolResult, error) {
	if action.Status != domain.ActionStatusSucceeded {
		return domain.ToolResult{}, fmt.Errorf("durable action %q is %s: %s", action.ID, action.Status, action.Reason)
	}
	for _, ref := range action.ResultArtifactRefs {
		if ref.Kind != "tool_output" {
			continue
		}
		for _, artifact := range snapshot.Artifacts {
			if artifact.ID == ref.ID {
				output := artifact.Text
				if output == "" {
					output = artifact.Content
				}
				return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: true, Output: output}, nil
			}
		}
	}
	return domain.ToolResult{}, fmt.Errorf("durable action %q succeeded without a stored tool output artifact", action.ID)
}
