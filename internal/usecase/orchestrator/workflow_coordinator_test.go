package orchestrator

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"yagent/internal/domain"
)

type coordinatorWorkflowStore struct {
	mu             sync.Mutex
	snapshot       domain.WorkflowSnapshot
	hasSnapshot    bool
	conflictOnce   bool
	ambiguousOnce  bool
	commitAttempts int
}

func (s *coordinatorWorkflowStore) LoadWorkflowSnapshot(_ context.Context, workflowID domain.WorkflowID) (domain.WorkflowSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasSnapshot || s.snapshot.Workflow.ID != workflowID {
		return domain.WorkflowSnapshot{}, &domain.WorkflowNotFoundError{WorkflowID: workflowID}
	}
	return s.snapshot, nil
}

func (s *coordinatorWorkflowStore) CommitWorkflowSnapshot(_ context.Context, expected int64, snapshot domain.WorkflowSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commitAttempts++
	actual := int64(0)
	if s.hasSnapshot && s.snapshot.Workflow.ID == snapshot.Workflow.ID {
		actual = s.snapshot.Workflow.Revision
	}
	if s.conflictOnce {
		s.conflictOnce = false
		return &domain.WorkflowRevisionConflictError{WorkflowID: snapshot.Workflow.ID, Expected: expected, Actual: actual}
	}
	if expected != actual {
		return &domain.WorkflowRevisionConflictError{WorkflowID: snapshot.Workflow.ID, Expected: expected, Actual: actual}
	}
	s.snapshot = snapshot
	s.hasSnapshot = true
	if s.ambiguousOnce {
		s.ambiguousOnce = false
		return errors.New("publication acknowledgement failed")
	}
	return nil
}

func TestCreateWorkflowSnapshotAcceptsVisibleAmbiguousCommit(t *testing.T) {
	store := &coordinatorWorkflowStore{ambiguousOnce: true}
	service := newTestService(nil, nil, nil, Config{WorkflowStore: store})
	want := coordinatorPendingSnapshot(t)

	got, err := service.createWorkflowSnapshot(context.Background(), want)
	if err != nil {
		t.Fatalf("createWorkflowSnapshot() error = %v", err)
	}
	if got.Workflow.ID != want.Workflow.ID || got.Workflow.Revision != 1 || store.commitAttempts != 1 {
		t.Fatalf("createWorkflowSnapshot() = %+v, attempts=%d", got.Workflow, store.commitAttempts)
	}
}

func TestCommitWorkflowTransitionRetriesRevisionConflict(t *testing.T) {
	store := &coordinatorWorkflowStore{snapshot: coordinatorPendingSnapshot(t), hasSnapshot: true, conflictOnce: true}
	service := newTestService(nil, nil, nil, Config{WorkflowStore: store})
	at := time.Date(2026, time.July, 10, 16, 0, 0, 0, time.UTC)

	got, err := service.commitWorkflowTransition(context.Background(), store.snapshot.Workflow.ID, func(snapshot domain.WorkflowSnapshot) (domain.WorkflowSnapshot, error) {
		return domain.ClaimReadyBatch(snapshot, domain.WorkflowBatchClaims{
			ExpectedRevision: snapshot.Workflow.Revision,
			Claims: []domain.WorkUnitClaim{{
				UnitID: snapshot.WorkUnits[0].ID,
				Lease:  domain.DurableLease{OwnerID: "worker-1", Token: "lease-1", FencingToken: 1, ExpiresAt: at.Add(time.Minute)},
			}},
		}, at)
	}, coordinatorUnitHasStatus("unit-1", domain.DurableWorkUnitStatusLeased))
	if err != nil {
		t.Fatalf("commitWorkflowTransition() error = %v", err)
	}
	if got.Workflow.Revision != 2 || got.WorkUnits[0].Status != domain.DurableWorkUnitStatusLeased || store.commitAttempts != 2 {
		t.Fatalf("commitWorkflowTransition() revision=%d status=%s attempts=%d", got.Workflow.Revision, got.WorkUnits[0].Status, store.commitAttempts)
	}
}

func TestCommitWorkflowTransitionAcceptsVisibleAmbiguousCommit(t *testing.T) {
	store := &coordinatorWorkflowStore{snapshot: coordinatorPendingSnapshot(t), hasSnapshot: true, ambiguousOnce: true}
	service := newTestService(nil, nil, nil, Config{WorkflowStore: store})
	at := time.Date(2026, time.July, 10, 16, 0, 0, 0, time.UTC)

	got, err := service.commitWorkflowTransition(context.Background(), store.snapshot.Workflow.ID, func(snapshot domain.WorkflowSnapshot) (domain.WorkflowSnapshot, error) {
		return domain.ClaimReadyBatch(snapshot, domain.WorkflowBatchClaims{
			ExpectedRevision: snapshot.Workflow.Revision,
			Claims: []domain.WorkUnitClaim{{
				UnitID: snapshot.WorkUnits[0].ID,
				Lease:  domain.DurableLease{OwnerID: "worker-1", Token: "lease-1", FencingToken: 1, ExpiresAt: at.Add(time.Minute)},
			}},
		}, at)
	}, coordinatorUnitHasStatus("unit-1", domain.DurableWorkUnitStatusLeased))
	if err != nil {
		t.Fatalf("commitWorkflowTransition() error = %v", err)
	}
	if got.Workflow.Revision != 2 || store.commitAttempts != 1 {
		t.Fatalf("commitWorkflowTransition() revision=%d attempts=%d", got.Workflow.Revision, store.commitAttempts)
	}
	if len(service.workflowLocks) != 0 {
		t.Fatalf("workflow lock was not released: %+v", service.workflowLocks)
	}
}

func TestCommitWorkflowTransitionRejectsUnsatisfiedCommand(t *testing.T) {
	store := &coordinatorWorkflowStore{snapshot: coordinatorPendingSnapshot(t), hasSnapshot: true}
	service := newTestService(nil, nil, nil, Config{WorkflowStore: store})

	_, err := service.commitWorkflowTransition(context.Background(), store.snapshot.Workflow.ID, func(snapshot domain.WorkflowSnapshot) (domain.WorkflowSnapshot, error) {
		return snapshot, nil
	}, coordinatorUnitHasStatus("unit-1", domain.DurableWorkUnitStatusLeased))
	if err == nil {
		t.Fatal("commitWorkflowTransition() error = nil, want unsatisfied command rejection")
	}
	if store.commitAttempts != 0 {
		t.Fatalf("unsatisfied command was committed %d times", store.commitAttempts)
	}
	if len(service.workflowLocks) != 0 {
		t.Fatalf("workflow lock was not released: %+v", service.workflowLocks)
	}
}

func coordinatorPendingSnapshot(t *testing.T) domain.WorkflowSnapshot {
	t.Helper()
	workflow, err := domain.NewWorkflow(domain.WorkflowInput{
		ID:           "workflow-1",
		Conversation: domain.ConversationReference{ConversationID: "conversation-1", TurnID: "turn-1"},
		RootGoal:     "exercise coordinator",
		CreatedAt:    time.Date(2026, time.July, 10, 15, 0, 0, 0, time.UTC),
		WorkUnitIDs:  []domain.DurableWorkUnitID{"unit-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	unit, err := domain.NewDurableWorkUnit(domain.DurableWorkUnitInput{
		ID: "unit-1", WorkflowID: workflow.ID, Kind: "primary", Phase: domain.RunPhaseExecute,
		Role: "coder", Task: "perform work", SideEffectClass: domain.SideEffectNone,
	})
	if err != nil {
		t.Fatal(err)
	}
	return domain.WorkflowSnapshot{Workflow: workflow, WorkUnits: []domain.DurableWorkUnit{unit}}
}

func coordinatorUnitHasStatus(unitID domain.DurableWorkUnitID, status domain.DurableWorkUnitStatus) workflowCommandApplied {
	return func(snapshot domain.WorkflowSnapshot) bool {
		for _, unit := range snapshot.WorkUnits {
			if unit.ID == unitID {
				return unit.Status == status
			}
		}
		return false
	}
}
