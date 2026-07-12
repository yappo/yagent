package state

import (
	"context"
	"fmt"
	"time"

	"yagent/internal/domain"
)

type DurableActionGuard struct {
	store domain.DurableWorkflowStore
	now   func() time.Time
}

func NewDurableActionGuard(store domain.DurableWorkflowStore) *DurableActionGuard {
	return &DurableActionGuard{store: store, now: time.Now}
}

func (g *DurableActionGuard) ValidateDurableAction(ctx context.Context, execution domain.DurableActionExecutionContext) error {
	if g == nil || g.store == nil {
		return nil
	}
	snapshot, err := g.store.LoadWorkflowSnapshot(ctx, execution.WorkflowID)
	if err != nil {
		return fmt.Errorf("%w: load workflow %q: %v", domain.ErrDurableActionNotExecutable, execution.WorkflowID, err)
	}
	at := g.now()
	unit, ok := guardWorkUnit(snapshot, execution.WorkUnitID)
	if !ok || unit.Status != domain.DurableWorkUnitStatusExecuting || unit.Lease == nil {
		return fmt.Errorf("%w: work unit %q is not executing", domain.ErrDurableActionNotExecutable, execution.WorkUnitID)
	}
	if unit.Lease.Token != execution.LeaseToken || unit.Lease.FencingToken != execution.FencingToken || !unit.Lease.ExpiresAt.After(at) {
		return fmt.Errorf("%w: work unit %q lease is stale", domain.ErrDurableActionNotExecutable, execution.WorkUnitID)
	}
	action, ok := guardAction(snapshot, execution.ActionID)
	if !ok || action.Status != domain.ActionStatusExecuting || action.WorkflowID != execution.WorkflowID || action.WorkUnitID != execution.WorkUnitID {
		return fmt.Errorf("%w: action %q is not executing", domain.ErrDurableActionNotExecutable, execution.ActionID)
	}
	if action.IdempotencyKey != execution.IdempotencyKey || action.LeaseToken != execution.LeaseToken || action.FencingToken != execution.FencingToken {
		return fmt.Errorf("%w: action %q credential mismatch", domain.ErrDurableActionNotExecutable, execution.ActionID)
	}
	return nil
}

func guardWorkUnit(snapshot domain.WorkflowSnapshot, id domain.DurableWorkUnitID) (domain.DurableWorkUnit, bool) {
	for _, unit := range snapshot.WorkUnits {
		if unit.ID == id {
			return unit, true
		}
	}
	return domain.DurableWorkUnit{}, false
}

func guardAction(snapshot domain.WorkflowSnapshot, id domain.ActionID) (domain.DurableAction, bool) {
	for _, action := range snapshot.Actions {
		if action.ID == id {
			return action, true
		}
	}
	return domain.DurableAction{}, false
}
