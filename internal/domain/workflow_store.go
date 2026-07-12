package domain

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrWorkflowNotFound identifies a workflow that has no committed snapshot.
	ErrWorkflowNotFound = errors.New("durable workflow not found")
	// ErrWorkflowRevisionConflict identifies a failed compare-and-swap commit.
	ErrWorkflowRevisionConflict = errors.New("durable workflow revision conflict")
)

// WorkflowSnapshot is the complete durable view of one workflow. A commit
// replaces all of these records together at one workflow revision.
type WorkflowSnapshot struct {
	Workflow  Workflow          `json:"workflow"`
	WorkUnits []DurableWorkUnit `json:"work_units"`
	Actions   []DurableAction   `json:"actions"`
	Artifacts []RunArtifact     `json:"artifacts"`
}

// DurableWorkflowStore persists complete workflow snapshots with optimistic
// concurrency control. expectedWorkflowRevision is zero when creating a
// workflow and otherwise must match the committed workflow revision.
type DurableWorkflowStore interface {
	CommitWorkflowSnapshot(ctx context.Context, expectedWorkflowRevision int64, snapshot WorkflowSnapshot) error
	LoadWorkflowSnapshot(ctx context.Context, workflowID WorkflowID) (WorkflowSnapshot, error)
}

// WorkflowNotFoundError carries the missing workflow identity while preserving
// errors.Is(err, ErrWorkflowNotFound).
type WorkflowNotFoundError struct {
	WorkflowID WorkflowID
}

func (e *WorkflowNotFoundError) Error() string {
	return fmt.Sprintf("%s: %s", ErrWorkflowNotFound, e.WorkflowID)
}

func (e *WorkflowNotFoundError) Is(target error) bool {
	if target == ErrWorkflowNotFound {
		return true
	}
	_, ok := target.(*WorkflowNotFoundError)
	return ok
}

// WorkflowRevisionConflictError describes the committed revision that prevented
// a compare-and-swap update.
type WorkflowRevisionConflictError struct {
	WorkflowID WorkflowID
	Expected   int64
	Actual     int64
}

func (e *WorkflowRevisionConflictError) Error() string {
	return fmt.Sprintf("%s: workflow=%s expected=%d actual=%d", ErrWorkflowRevisionConflict, e.WorkflowID, e.Expected, e.Actual)
}

func (e *WorkflowRevisionConflictError) Is(target error) bool {
	if target == ErrWorkflowRevisionConflict {
		return true
	}
	_, ok := target.(*WorkflowRevisionConflictError)
	return ok
}

// ValidateWorkflowSnapshot verifies both record validity and aggregate linkage.
// The workflow's unit index is authoritative: every indexed unit appears once,
// and no unrelated unit or action can be committed with the workflow.
func ValidateWorkflowSnapshot(snapshot WorkflowSnapshot) error {
	if err := ValidateWorkflow(snapshot.Workflow); err != nil {
		return err
	}

	workflowID := snapshot.Workflow.ID
	declaredUnits := make(map[DurableWorkUnitID]struct{}, len(snapshot.Workflow.WorkUnitIDs))
	for _, id := range snapshot.Workflow.WorkUnitIDs {
		if _, exists := declaredUnits[id]; exists {
			return fmt.Errorf("%w: duplicate work unit id %q", ErrInvalidWorkflow, id)
		}
		declaredUnits[id] = struct{}{}
	}
	if len(declaredUnits) != len(snapshot.WorkUnits) {
		return fmt.Errorf("%w: workflow work unit index does not match snapshot", ErrInvalidWorkflow)
	}

	units := make(map[DurableWorkUnitID]DurableWorkUnit, len(snapshot.WorkUnits))
	for index, unit := range snapshot.WorkUnits {
		if err := ValidateDurableWorkUnit(unit); err != nil {
			return err
		}
		if snapshot.Workflow.WorkUnitIDs[index] != unit.ID {
			return fmt.Errorf("%w: work unit order at index %d is %q, want %q", ErrInvalidWorkflow, index, unit.ID, snapshot.Workflow.WorkUnitIDs[index])
		}
		if unit.WorkflowID != workflowID {
			return fmt.Errorf("%w: work unit %q belongs to workflow %q, not %q", ErrInvalidWorkflow, unit.ID, unit.WorkflowID, workflowID)
		}
		if _, declared := declaredUnits[unit.ID]; !declared {
			return fmt.Errorf("%w: work unit %q is not indexed by workflow", ErrInvalidWorkflow, unit.ID)
		}
		if _, exists := units[unit.ID]; exists {
			return fmt.Errorf("%w: duplicate work unit %q", ErrInvalidWorkflow, unit.ID)
		}
		units[unit.ID] = unit
	}
	for _, unit := range snapshot.WorkUnits {
		for _, dependency := range unit.Dependencies {
			if _, exists := units[dependency]; !exists {
				return fmt.Errorf("%w: work unit %q has missing dependency %q", ErrInvalidWorkflow, unit.ID, dependency)
			}
		}
	}
	if err := validateWorkflowDependencyGraph(units); err != nil {
		return err
	}
	if err := validateWorkflowDependencyStatuses(snapshot.WorkUnits, units); err != nil {
		return err
	}

	actions := make(map[ActionID]DurableAction, len(snapshot.Actions))
	actionsByUnit := make(map[DurableWorkUnitID][]DurableAction, len(snapshot.WorkUnits))
	idempotencyKeys := make(map[string]ActionID, len(snapshot.Actions))
	for _, action := range snapshot.Actions {
		if err := ValidateDurableAction(action); err != nil {
			return err
		}
		if action.WorkflowID != workflowID {
			return fmt.Errorf("%w: action %q belongs to workflow %q, not %q", ErrInvalidWorkflow, action.ID, action.WorkflowID, workflowID)
		}
		unit, exists := units[action.WorkUnitID]
		if !exists {
			return fmt.Errorf("%w: action %q references missing work unit %q", ErrInvalidWorkflow, action.ID, action.WorkUnitID)
		}
		if _, exists := actions[action.ID]; exists {
			return fmt.Errorf("%w: duplicate action %q", ErrInvalidWorkflow, action.ID)
		}
		if existingID, exists := idempotencyKeys[action.IdempotencyKey]; exists {
			return fmt.Errorf("%w: actions %q and %q reuse idempotency key %q", ErrInvalidWorkflow, existingID, action.ID, action.IdempotencyKey)
		}
		if err := validateWorkflowActionOwnership(unit, action); err != nil {
			return err
		}
		actions[action.ID] = action
		idempotencyKeys[action.IdempotencyKey] = action.ID
		actionsByUnit[action.WorkUnitID] = append(actionsByUnit[action.WorkUnitID], action)
	}
	artifacts := make(map[string]RunArtifact, len(snapshot.Artifacts))
	for _, artifact := range snapshot.Artifacts {
		if strings.TrimSpace(artifact.ID) == "" {
			return fmt.Errorf("%w: artifact id is required", ErrInvalidWorkflow)
		}
		if _, exists := artifacts[artifact.ID]; exists {
			return fmt.Errorf("%w: duplicate artifact %q", ErrInvalidWorkflow, artifact.ID)
		}
		if err := ValidateArtifactPayload(artifact); err != nil {
			return fmt.Errorf("%w: artifact %q payload: %v", ErrInvalidWorkflow, artifact.ID, err)
		}
		artifacts[artifact.ID] = artifact
	}
	if err := validateSnapshotArtifactReferences(artifacts, "workflow graph", snapshot.Workflow.GraphArtifactRefs); err != nil {
		return err
	}
	if err := validateSnapshotArtifactReferences(artifacts, "workflow input", snapshot.Workflow.InputArtifactRefs); err != nil {
		return err
	}
	if err := validateSnapshotArtifactReferences(artifacts, "workflow final outcome", snapshot.Workflow.FinalOutcomeRefs); err != nil {
		return err
	}
	for _, unit := range snapshot.WorkUnits {
		if err := validateSnapshotArtifactReferences(artifacts, fmt.Sprintf("work unit %q input", unit.ID), unit.InputArtifactRefs); err != nil {
			return err
		}
		if unit.Outcome == nil {
			continue
		}
		if err := validateSnapshotArtifactReferences(artifacts, fmt.Sprintf("work unit %q outcome", unit.ID), unit.Outcome.ArtifactRefs); err != nil {
			return err
		}
		outcomeActions := make(map[ActionID]struct{}, len(unit.Outcome.ActionIDs))
		for _, actionID := range unit.Outcome.ActionIDs {
			if _, duplicate := outcomeActions[actionID]; duplicate {
				return fmt.Errorf("%w: work unit %q outcome duplicates action %q", ErrInvalidWorkflow, unit.ID, actionID)
			}
			outcomeActions[actionID] = struct{}{}
			action, exists := actions[actionID]
			if !exists {
				return fmt.Errorf("%w: work unit %q outcome references missing action %q", ErrInvalidWorkflow, unit.ID, actionID)
			}
			if action.WorkUnitID != unit.ID {
				return fmt.Errorf("%w: work unit %q outcome references action %q from work unit %q", ErrInvalidWorkflow, unit.ID, actionID, action.WorkUnitID)
			}
			if !workflowSnapshotTerminalActionStatus(action.Status) {
				return fmt.Errorf("%w: work unit %q outcome references nonterminal action %q", ErrInvalidWorkflow, unit.ID, actionID)
			}
		}
	}
	for _, action := range snapshot.Actions {
		if err := validateSnapshotArtifactReferences(artifacts, fmt.Sprintf("action %q result", action.ID), action.ResultArtifactRefs); err != nil {
			return err
		}
	}
	if err := validateWorkflowAggregateStatus(snapshot.Workflow.Status, snapshot.WorkUnits, len(snapshot.Actions)); err != nil {
		return err
	}
	if err := validateTerminalWorkUnitActions(snapshot.WorkUnits, actions, actionsByUnit); err != nil {
		return err
	}
	return nil
}

func validateWorkflowDependencyGraph(units map[DurableWorkUnitID]DurableWorkUnit) error {
	visiting := make(map[DurableWorkUnitID]bool, len(units))
	visited := make(map[DurableWorkUnitID]bool, len(units))
	var visit func(DurableWorkUnitID) error
	visit = func(id DurableWorkUnitID) error {
		if visiting[id] {
			return fmt.Errorf("%w: work unit dependency graph contains a cycle at %q", ErrInvalidWorkflow, id)
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, dependency := range units[id].Dependencies {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		visiting[id] = false
		visited[id] = true
		return nil
	}
	for id := range units {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

func validateWorkflowDependencyStatuses(ordered []DurableWorkUnit, units map[DurableWorkUnitID]DurableWorkUnit) error {
	for _, unit := range ordered {
		if unit.Status == DurableWorkUnitStatusPending || unit.Status == DurableWorkUnitStatusSkipped {
			continue
		}
		if unit.Status == DurableWorkUnitStatusBlocked {
			if !hasBlockingDependencyInUnits(unit, units) {
				return fmt.Errorf("%w: blocked work unit %q has no failed dependency", ErrInvalidWorkflow, unit.ID)
			}
			continue
		}
		for _, dependencyID := range unit.Dependencies {
			dependency := units[dependencyID]
			if dependency.Status != DurableWorkUnitStatusSucceeded && dependency.Status != DurableWorkUnitStatusSkipped {
				return fmt.Errorf("%w: work unit %q in status %q has incomplete dependency %q in status %q", ErrInvalidWorkflow, unit.ID, unit.Status, dependencyID, dependency.Status)
			}
		}
	}
	return nil
}

func hasBlockingDependencyInUnits(unit DurableWorkUnit, units map[DurableWorkUnitID]DurableWorkUnit) bool {
	for _, dependencyID := range unit.Dependencies {
		dependency := units[dependencyID]
		if dependency.Status == DurableWorkUnitStatusFailed || dependency.Status == DurableWorkUnitStatusNeedsAttention || dependency.Status == DurableWorkUnitStatusBlocked {
			return true
		}
	}
	return false
}

func validateSnapshotArtifactReferences(artifacts map[string]RunArtifact, owner string, refs []ArtifactReference) error {
	for _, ref := range refs {
		if _, exists := artifacts[ref.ID]; !exists {
			return fmt.Errorf("%w: %s references missing artifact %q", ErrInvalidWorkflow, owner, ref.ID)
		}
	}
	return nil
}

func workflowSnapshotTerminalActionStatus(status ActionStatus) bool {
	return status == ActionStatusSucceeded || status == ActionStatusFailed || status == ActionStatusAmbiguous || status == ActionStatusAbandoned
}

func validateWorkflowActionOwnership(unit DurableWorkUnit, action DurableAction) error {
	if action.Attempt != unit.Attempt {
		return fmt.Errorf("%w: action %q attempt=%d does not match work unit %q attempt=%d", ErrInvalidWorkflow, action.ID, action.Attempt, unit.ID, unit.Attempt)
	}
	if action.FencingToken > unit.LastFencingToken {
		return fmt.Errorf("%w: action %q fencing token=%d exceeds work unit %q last fencing token=%d", ErrInvalidWorkflow, action.ID, action.FencingToken, unit.ID, unit.LastFencingToken)
	}

	if action.Status == ActionStatusPrepared || action.Status == ActionStatusExecuting {
		if unit.Status != DurableWorkUnitStatusExecuting || unit.Lease == nil {
			return fmt.Errorf("%w: active action %q requires an executing work unit with an active lease", ErrInvalidWorkflow, action.ID)
		}
		if action.LeaseToken != unit.Lease.Token || action.FencingToken != unit.Lease.FencingToken {
			return fmt.Errorf("%w: active action %q lease does not match work unit %q", ErrInvalidWorkflow, action.ID, unit.ID)
		}
		return nil
	}

	if !workflowSnapshotTerminalActionStatus(action.Status) {
		return fmt.Errorf("%w: action %q has unsupported aggregate status %q", ErrInvalidWorkflow, action.ID, action.Status)
	}
	switch unit.Status {
	case DurableWorkUnitStatusPending, DurableWorkUnitStatusLeased, DurableWorkUnitStatusExecuting:
		if unit.Lease != nil && action.FencingToken == unit.Lease.FencingToken && action.LeaseToken != unit.Lease.Token {
			return fmt.Errorf("%w: terminal action %q does not match the executing work unit lease", ErrInvalidWorkflow, action.ID)
		}
	case DurableWorkUnitStatusSucceeded, DurableWorkUnitStatusFailed, DurableWorkUnitStatusNeedsAttention:
		// Attempt and last fencing token above are the durable execution history
		// retained after a terminal unit releases its lease.
	default:
		return fmt.Errorf("%w: terminal action %q cannot belong to work unit %q in status %q", ErrInvalidWorkflow, action.ID, unit.ID, unit.Status)
	}
	return nil
}

func validateWorkflowAggregateStatus(status WorkflowStatus, units []DurableWorkUnit, actionCount int) error {
	if status == WorkflowStatusPending {
		if actionCount != 0 {
			return fmt.Errorf("%w: pending workflow cannot contain actions", ErrInvalidWorkflow)
		}
		for _, unit := range units {
			if unit.Status != DurableWorkUnitStatusPending {
				return fmt.Errorf("%w: pending workflow contains work unit %q in status %q", ErrInvalidWorkflow, unit.ID, unit.Status)
			}
		}
		return nil
	}
	if status == WorkflowStatusRunning {
		return nil
	}

	expected, terminal := workflowSnapshotRollupStatus(units)
	if !terminal {
		return fmt.Errorf("%w: terminal workflow %q contains a nonterminal work unit", ErrInvalidWorkflow, status)
	}
	if status != expected {
		return fmt.Errorf("%w: terminal workflow status %q does not match work unit roll-up %q", ErrInvalidWorkflow, status, expected)
	}
	return nil
}

func workflowSnapshotRollupStatus(units []DurableWorkUnit) (WorkflowStatus, bool) {
	hasNeedsAttention := false
	hasFailed := false
	for _, unit := range units {
		switch unit.Status {
		case DurableWorkUnitStatusNeedsAttention:
			hasNeedsAttention = true
		case DurableWorkUnitStatusFailed, DurableWorkUnitStatusBlocked:
			hasFailed = true
		case DurableWorkUnitStatusSucceeded, DurableWorkUnitStatusSkipped:
		default:
			return "", false
		}
	}
	if hasNeedsAttention {
		return WorkflowStatusNeedsAttention, true
	}
	if hasFailed {
		return WorkflowStatusFailed, true
	}
	return WorkflowStatusSucceeded, true
}

func validateTerminalWorkUnitActions(units []DurableWorkUnit, actions map[ActionID]DurableAction, actionsByUnit map[DurableWorkUnitID][]DurableAction) error {
	for _, unit := range units {
		if !workflowSnapshotTerminalWorkUnitStatus(unit.Status) {
			continue
		}
		owned := actionsByUnit[unit.ID]
		if (unit.Status == DurableWorkUnitStatusSkipped || unit.Status == DurableWorkUnitStatusBlocked) && len(owned) != 0 {
			return fmt.Errorf("%w: %s work unit %q cannot own actions", ErrInvalidWorkflow, unit.Status, unit.ID)
		}
		if unit.Outcome == nil {
			return fmt.Errorf("%w: terminal work unit %q requires an outcome", ErrInvalidWorkflow, unit.ID)
		}

		covered := make(map[ActionID]struct{}, len(unit.Outcome.ActionIDs))
		for _, actionID := range unit.Outcome.ActionIDs {
			if _, duplicate := covered[actionID]; duplicate {
				return fmt.Errorf("%w: terminal work unit %q outcome duplicates action %q", ErrInvalidWorkflow, unit.ID, actionID)
			}
			covered[actionID] = struct{}{}
			action, exists := actions[actionID]
			if !exists {
				return fmt.Errorf("%w: terminal work unit %q outcome references missing action %q", ErrInvalidWorkflow, unit.ID, actionID)
			}
			if action.WorkUnitID != unit.ID {
				return fmt.Errorf("%w: terminal work unit %q outcome references action %q from work unit %q", ErrInvalidWorkflow, unit.ID, actionID, action.WorkUnitID)
			}
			if !workflowSnapshotTerminalActionStatus(action.Status) {
				return fmt.Errorf("%w: terminal work unit %q outcome references nonterminal action %q", ErrInvalidWorkflow, unit.ID, actionID)
			}
		}
		if len(covered) != len(owned) {
			return fmt.Errorf("%w: terminal work unit %q outcome action set does not cover its owned actions", ErrInvalidWorkflow, unit.ID)
		}
		for _, action := range owned {
			if !workflowSnapshotTerminalActionStatus(action.Status) {
				return fmt.Errorf("%w: terminal work unit %q owns nonterminal action %q", ErrInvalidWorkflow, unit.ID, action.ID)
			}
			if _, exists := covered[action.ID]; !exists {
				return fmt.Errorf("%w: terminal work unit %q outcome omits owned action %q", ErrInvalidWorkflow, unit.ID, action.ID)
			}
		}
	}
	return nil
}

func workflowSnapshotTerminalWorkUnitStatus(status DurableWorkUnitStatus) bool {
	return status == DurableWorkUnitStatusSucceeded || status == DurableWorkUnitStatusSkipped || status == DurableWorkUnitStatusBlocked || status == DurableWorkUnitStatusFailed || status == DurableWorkUnitStatusNeedsAttention
}
