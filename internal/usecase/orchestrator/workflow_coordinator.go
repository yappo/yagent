package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"yagent/internal/domain"
)

const workflowCommitAttempts = 8

type workflowTransition func(domain.WorkflowSnapshot) (domain.WorkflowSnapshot, error)
type workflowCommandApplied func(domain.WorkflowSnapshot) bool

type workflowLockEntry struct {
	mu   sync.Mutex
	refs int
}

func (s *Service) createWorkflowSnapshot(ctx context.Context, snapshot domain.WorkflowSnapshot) (domain.WorkflowSnapshot, error) {
	if s.config.WorkflowStore == nil {
		return domain.WorkflowSnapshot{}, fmt.Errorf("durable workflow store is required")
	}
	if err := domain.ValidateWorkflowSnapshot(snapshot); err != nil {
		return domain.WorkflowSnapshot{}, err
	}
	unlock := s.lockWorkflow(snapshot.Workflow.ID)
	defer unlock()

	err := s.config.WorkflowStore.CommitWorkflowSnapshot(ctx, 0, snapshot)
	if err == nil {
		return snapshot, nil
	}
	committed, loadErr := s.config.WorkflowStore.LoadWorkflowSnapshot(ctx, snapshot.Workflow.ID)
	if loadErr == nil && reflect.DeepEqual(committed, snapshot) {
		return committed, nil
	}
	if loadErr != nil && !errors.Is(loadErr, domain.ErrWorkflowNotFound) {
		return domain.WorkflowSnapshot{}, errors.Join(err, loadErr)
	}
	return domain.WorkflowSnapshot{}, err
}

func (s *Service) commitWorkflowTransition(ctx context.Context, workflowID domain.WorkflowID, transition workflowTransition, applied workflowCommandApplied) (domain.WorkflowSnapshot, error) {
	if s.config.WorkflowStore == nil {
		return domain.WorkflowSnapshot{}, fmt.Errorf("durable workflow store is required")
	}
	if transition == nil || applied == nil {
		return domain.WorkflowSnapshot{}, fmt.Errorf("workflow transition and applied predicate are required")
	}
	unlock := s.lockWorkflow(workflowID)
	defer unlock()

	var lastConflict error
	for attempt := 0; attempt < workflowCommitAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return domain.WorkflowSnapshot{}, err
		}
		current, err := s.config.WorkflowStore.LoadWorkflowSnapshot(ctx, workflowID)
		if err != nil {
			return domain.WorkflowSnapshot{}, err
		}
		if applied(current) {
			return current, nil
		}
		next, err := transition(current)
		if err != nil {
			return domain.WorkflowSnapshot{}, err
		}
		if !applied(next) {
			return domain.WorkflowSnapshot{}, fmt.Errorf("workflow command did not satisfy its applied predicate")
		}
		if err := domain.ValidateWorkflowSnapshot(next); err != nil {
			return domain.WorkflowSnapshot{}, err
		}
		err = s.config.WorkflowStore.CommitWorkflowSnapshot(ctx, current.Workflow.Revision, next)
		if err == nil {
			return next, nil
		}

		committed, loadErr := s.config.WorkflowStore.LoadWorkflowSnapshot(ctx, workflowID)
		if loadErr == nil && applied(committed) {
			return committed, nil
		}
		if loadErr != nil {
			return domain.WorkflowSnapshot{}, errors.Join(err, loadErr)
		}
		if errors.Is(err, domain.ErrWorkflowRevisionConflict) {
			lastConflict = err
			continue
		}
		return domain.WorkflowSnapshot{}, err
	}
	return domain.WorkflowSnapshot{}, fmt.Errorf("workflow %q commit did not converge after %d attempts: %w", workflowID, workflowCommitAttempts, lastConflict)
}

func (s *Service) lockWorkflow(workflowID domain.WorkflowID) func() {
	s.workflowMu.Lock()
	entry := s.workflowLocks[workflowID]
	if entry == nil {
		entry = &workflowLockEntry{}
		s.workflowLocks[workflowID] = entry
	}
	entry.refs++
	s.workflowMu.Unlock()
	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		s.workflowMu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(s.workflowLocks, workflowID)
		}
		s.workflowMu.Unlock()
	}
}
