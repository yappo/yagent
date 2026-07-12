package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"yagent/internal/domain"
)

type guardWorkflowStore struct {
	snapshot domain.WorkflowSnapshot
}

func (s guardWorkflowStore) LoadWorkflowSnapshot(_ context.Context, workflowID domain.WorkflowID) (domain.WorkflowSnapshot, error) {
	if s.snapshot.Workflow.ID != workflowID {
		return domain.WorkflowSnapshot{}, &domain.WorkflowNotFoundError{WorkflowID: workflowID}
	}
	return s.snapshot, nil
}

func (guardWorkflowStore) CommitWorkflowSnapshot(context.Context, int64, domain.WorkflowSnapshot) error {
	return nil
}

func TestDurableActionGuardAcceptsActiveAction(t *testing.T) {
	at := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	snapshot, execution := guardSnapshot(at.Add(time.Minute))
	guard := NewDurableActionGuard(guardWorkflowStore{snapshot: snapshot})
	guard.now = func() time.Time { return at }

	if err := guard.ValidateDurableAction(context.Background(), execution); err != nil {
		t.Fatalf("ValidateDurableAction() error = %v", err)
	}
}

func TestDurableActionGuardRejectsExpiredOrMismatchedLease(t *testing.T) {
	at := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	for _, mutate := range []func(*domain.DurableActionExecutionContext, *domain.WorkflowSnapshot){
		func(_ *domain.DurableActionExecutionContext, snapshot *domain.WorkflowSnapshot) {
			snapshot.WorkUnits[0].Lease.ExpiresAt = at
		},
		func(execution *domain.DurableActionExecutionContext, _ *domain.WorkflowSnapshot) {
			execution.FencingToken++
		},
		func(execution *domain.DurableActionExecutionContext, _ *domain.WorkflowSnapshot) {
			execution.IdempotencyKey = "wrong"
		},
	} {
		snapshot, execution := guardSnapshot(at.Add(time.Minute))
		mutate(&execution, &snapshot)
		guard := NewDurableActionGuard(guardWorkflowStore{snapshot: snapshot})
		guard.now = func() time.Time { return at }
		err := guard.ValidateDurableAction(context.Background(), execution)
		if !errors.Is(err, domain.ErrDurableActionNotExecutable) {
			t.Fatalf("ValidateDurableAction() error = %v, want ErrDurableActionNotExecutable", err)
		}
	}
}

func guardSnapshot(expiresAt time.Time) (domain.WorkflowSnapshot, domain.DurableActionExecutionContext) {
	workflowID := domain.WorkflowID("workflow-1")
	unitID := domain.DurableWorkUnitID("unit-1")
	lease := domain.DurableLease{OwnerID: "worker-1", Token: "lease-1", FencingToken: 3, ExpiresAt: expiresAt}
	action := domain.DurableAction{
		ID: "action-1", WorkflowID: workflowID, WorkUnitID: unitID, Attempt: 1, Kind: "tool_call", Target: "fs_write", IdempotencyKey: "idem-1",
		LeaseToken: lease.Token, FencingToken: lease.FencingToken, Status: domain.ActionStatusExecuting,
	}
	snapshot := domain.WorkflowSnapshot{
		Workflow:  domain.Workflow{ID: workflowID},
		WorkUnits: []domain.DurableWorkUnit{{ID: unitID, WorkflowID: workflowID, Status: domain.DurableWorkUnitStatusExecuting, Lease: &lease}},
		Actions:   []domain.DurableAction{action},
	}
	return snapshot, domain.DurableActionExecutionContext{
		ActionID: action.ID, WorkflowID: workflowID, WorkUnitID: unitID, Attempt: action.Attempt, IdempotencyKey: action.IdempotencyKey,
		LeaseToken: lease.Token, FencingToken: lease.FencingToken,
	}
}
