package domain

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
)

// WorkflowBatchClaims identifies all units claimed by one aggregate transition.
type WorkflowBatchClaims struct {
	ExpectedRevision int64
	Claims           []WorkUnitClaim
}

type WorkUnitClaim struct {
	UnitID DurableWorkUnitID
	Lease  DurableLease
}

// WorkflowBatchCredentials identifies all units started by one aggregate transition.
type WorkflowBatchCredentials struct {
	ExpectedRevision int64
	Credentials      []WorkUnitCredential
}

type WorkUnitCredential struct {
	UnitID     DurableWorkUnitID
	Credential LeaseCredential
}

// RenewWorkUnitLeasesInput extends active leases without changing their
// credentials or fencing generation.
type RenewWorkUnitLeasesInput struct {
	ExpectedRevision int64
	Renewals         []WorkUnitLeaseRenewal
	At               time.Time
}

type WorkUnitLeaseRenewal struct {
	UnitID     DurableWorkUnitID
	Credential LeaseCredential
	ExpiresAt  time.Time
}

// ReconcileExpiredLeasesInput resolves every lease that is expired at At.
// The aggregate derives whether each execution is retryable from its actions.
type ReconcileExpiredLeasesInput struct {
	ExpectedRevision int64
	At               time.Time
}

// BlockWorkUnitsInput identifies pending work units that cannot run because a
// direct dependency has already failed, needs attention, or is blocked.
type BlockWorkUnitsInput struct {
	ExpectedRevision int64
	Blocks           []WorkUnitBlock
	At               time.Time
}

type WorkUnitBlock struct {
	UnitID DurableWorkUnitID
	Reason string
}

type WorkflowLeaseCredential struct {
	ExpectedRevision int64
	LeaseCredential  LeaseCredential
}

type PrepareActionInput struct {
	ExpectedRevision int64
	Action           DurableActionInput
	At               time.Time
}

type ActionCompletion struct {
	Status    ActionStatus
	Result    DurableActionResult
	Reason    string
	Artifacts []RunArtifact
}

type FinishUnitInput struct {
	ExpectedRevision int64
	UnitID           DurableWorkUnitID
	Status           DurableWorkUnitStatus
	Outcome          DurableWorkUnitOutcome
	NewUnits         []DurableWorkUnit
	Artifacts        []RunArtifact
	Credential       LeaseCredential
	At               time.Time
}

type SettleWorkflowInput struct {
	ExpectedRevision int64
	FinalOutcomeRefs []ArtifactReference
	Artifacts        []RunArtifact
	At               time.Time
}

// AttachWorkflowPlanInput advances a persisted workflow intent to its first
// executable graph. The graph and its units are immutable after this point.
type AttachWorkflowPlanInput struct {
	ExpectedRevision  int64
	GraphArtifactRefs []ArtifactReference
	WorkUnits         []DurableWorkUnit
	Artifacts         []RunArtifact
	At                time.Time
}

// AttachWorkflowPlan atomically attaches the execution-plan artifact and its
// derived work units to a pending workflow intent. Retrying the exact same
// attachment is a no-op; a different graph is rejected.
func AttachWorkflowPlan(snapshot WorkflowSnapshot, input AttachWorkflowPlanInput) (WorkflowSnapshot, error) {
	next, err := beginWorkflowTransition(snapshot, input.ExpectedRevision)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	if workflowPlanAttachmentMatches(next, input) {
		return next, nil
	}
	if next.Workflow.Status != WorkflowStatusPending || len(next.Workflow.GraphArtifactRefs) != 0 || len(next.Workflow.WorkUnitIDs) != 0 || len(next.WorkUnits) != 0 || len(next.Actions) != 0 {
		return WorkflowSnapshot{}, fmt.Errorf("%w: workflow plan may only be attached to an unplanned pending intent", ErrInvalidTransition)
	}
	if len(input.GraphArtifactRefs) != 1 || len(input.WorkUnits) == 0 || input.At.IsZero() {
		return WorkflowSnapshot{}, fmt.Errorf("%w: plan attachment requires one graph artifact, work units, and a time", ErrInvalidTransition)
	}
	if err := validateArtifactReferences(input.GraphArtifactRefs); err != nil {
		return WorkflowSnapshot{}, fmt.Errorf("%w: graph artifact refs: %v", ErrInvalidTransition, err)
	}
	if err := appendWorkflowArtifacts(&next, input.Artifacts); err != nil {
		return WorkflowSnapshot{}, err
	}
	placeAttachedArtifactsBeforeWorkflowInput(&next, input.Artifacts)
	artifacts := make(map[string]RunArtifact, len(next.Artifacts))
	for _, artifact := range next.Artifacts {
		artifacts[artifact.ID] = artifact
	}
	graphArtifact, ok := artifacts[input.GraphArtifactRefs[0].ID]
	if !ok || graphArtifact.Kind != "execution_plan" || input.GraphArtifactRefs[0].Kind != "execution_plan" {
		return WorkflowSnapshot{}, fmt.Errorf("%w: graph attachment must reference an execution plan artifact", ErrInvalidTransition)
	}
	if err := appendNewDurableWorkUnits(&next, input.WorkUnits); err != nil {
		return WorkflowSnapshot{}, err
	}
	next.Workflow.GraphArtifactRefs = cloneArtifactReferences(input.GraphArtifactRefs)
	return finishWorkflowTransition(snapshot, next, input.At)
}

// ClaimReadyBatch atomically leases ready pending units. Dependencies must have
// succeeded or been skipped before a unit can be claimed.
func ClaimReadyBatch(snapshot WorkflowSnapshot, claims WorkflowBatchClaims, at time.Time) (WorkflowSnapshot, error) {
	next, err := beginWorkflowTransition(snapshot, claims.ExpectedRevision)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	if workflowTerminal(next.Workflow.Status) || len(claims.Claims) == 0 || at.IsZero() {
		return WorkflowSnapshot{}, fmt.Errorf("%w: claim requires an active workflow, claims, and a time", ErrInvalidTransition)
	}

	seenUnits := make(map[DurableWorkUnitID]struct{}, len(claims.Claims))
	seenTokens := make(map[LeaseToken]struct{}, len(next.WorkUnits)+len(claims.Claims))
	for _, unit := range next.WorkUnits {
		if unit.Lease != nil {
			seenTokens[unit.Lease.Token] = struct{}{}
		}
	}
	for _, claim := range claims.Claims {
		if _, exists := seenUnits[claim.UnitID]; exists {
			return WorkflowSnapshot{}, fmt.Errorf("%w: duplicate claim for unit %q", ErrInvalidTransition, claim.UnitID)
		}
		seenUnits[claim.UnitID] = struct{}{}
		if _, exists := seenTokens[claim.Lease.Token]; exists {
			return WorkflowSnapshot{}, fmt.Errorf("%w: duplicate claim lease %q", ErrInvalidTransition, claim.Lease.Token)
		}
		seenTokens[claim.Lease.Token] = struct{}{}

		unit, index := durableWorkUnitByID(next, claim.UnitID)
		if index < 0 || unit.Status != DurableWorkUnitStatusPending || !dependenciesComplete(next, unit) {
			return WorkflowSnapshot{}, fmt.Errorf("%w: unit %q is not ready to claim", ErrInvalidTransition, claim.UnitID)
		}
		if err := validateLease(claim.Lease, at, unit.LastFencingToken, true); err != nil {
			return WorkflowSnapshot{}, err
		}
		next.WorkUnits[index].Status = DurableWorkUnitStatusLeased
		next.WorkUnits[index].ClaimedAt = at
		next.WorkUnits[index].Lease = cloneLease(&claim.Lease)
		next.WorkUnits[index].LastFencingToken = claim.Lease.FencingToken
	}
	next.Workflow.Status = WorkflowStatusRunning
	return finishWorkflowTransition(snapshot, next, at)
}

// StartClaimedBatch atomically starts all supplied leases after checking each
// credential against the active lease and fencing token.
func StartClaimedBatch(snapshot WorkflowSnapshot, credentials WorkflowBatchCredentials, at time.Time) (WorkflowSnapshot, error) {
	next, err := beginWorkflowTransition(snapshot, credentials.ExpectedRevision)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	if next.Workflow.Status != WorkflowStatusRunning || len(credentials.Credentials) == 0 || at.IsZero() {
		return WorkflowSnapshot{}, fmt.Errorf("%w: start requires a running workflow, credentials, and a time", ErrInvalidTransition)
	}
	claimedCount := 0
	for _, unit := range next.WorkUnits {
		if unit.Status == DurableWorkUnitStatusLeased {
			claimedCount++
		}
	}
	if len(credentials.Credentials) != claimedCount {
		return WorkflowSnapshot{}, fmt.Errorf("%w: start requires credentials for every claimed unit", ErrInvalidTransition)
	}
	seen := make(map[DurableWorkUnitID]struct{}, len(credentials.Credentials))
	for _, item := range credentials.Credentials {
		if _, exists := seen[item.UnitID]; exists {
			return WorkflowSnapshot{}, fmt.Errorf("%w: duplicate start credential for unit %q", ErrInvalidTransition, item.UnitID)
		}
		seen[item.UnitID] = struct{}{}
		unit, index := durableWorkUnitByID(next, item.UnitID)
		if index < 0 {
			return WorkflowSnapshot{}, fmt.Errorf("%w: missing unit %q", ErrInvalidTransition, item.UnitID)
		}
		if err := validateUnitCredential(next.Workflow, unit, item.Credential, at, DurableWorkUnitStatusLeased); err != nil {
			return WorkflowSnapshot{}, err
		}
		next.WorkUnits[index].Status = DurableWorkUnitStatusExecuting
		next.WorkUnits[index].StartedAt = at
	}
	return finishWorkflowTransition(snapshot, next, at)
}

// RenewWorkUnitLeases extends active, unexpired leases. Renewal never changes
// the token or fencing generation, so in-flight action credentials stay valid.
func RenewWorkUnitLeases(snapshot WorkflowSnapshot, input RenewWorkUnitLeasesInput) (WorkflowSnapshot, error) {
	next, err := beginWorkflowTransition(snapshot, input.ExpectedRevision)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	if next.Workflow.Status != WorkflowStatusRunning || len(input.Renewals) == 0 || input.At.IsZero() {
		return WorkflowSnapshot{}, fmt.Errorf("%w: renewal requires a running workflow, leases, and a time", ErrInvalidTransition)
	}
	seen := make(map[DurableWorkUnitID]struct{}, len(input.Renewals))
	for _, renewal := range input.Renewals {
		if _, exists := seen[renewal.UnitID]; exists {
			return WorkflowSnapshot{}, fmt.Errorf("%w: duplicate lease renewal for unit %q", ErrInvalidTransition, renewal.UnitID)
		}
		seen[renewal.UnitID] = struct{}{}
		unit, index := durableWorkUnitByID(next, renewal.UnitID)
		if index < 0 || (unit.Status != DurableWorkUnitStatusLeased && unit.Status != DurableWorkUnitStatusExecuting) || unit.Lease == nil {
			return WorkflowSnapshot{}, fmt.Errorf("%w: unit %q has no active lease", ErrInvalidTransition, renewal.UnitID)
		}
		if renewal.Credential.Token != unit.Lease.Token || renewal.Credential.FencingToken != unit.Lease.FencingToken {
			return WorkflowSnapshot{}, ErrLeaseMismatch
		}
		if !unit.Lease.ExpiresAt.After(input.At) {
			return WorkflowSnapshot{}, ErrLeaseExpired
		}
		if !renewal.ExpiresAt.After(unit.Lease.ExpiresAt) {
			return WorkflowSnapshot{}, fmt.Errorf("%w: renewed lease expiry must increase", ErrInvalidTransition)
		}
		next.WorkUnits[index].Lease.ExpiresAt = renewal.ExpiresAt
	}
	return finishWorkflowTransition(snapshot, next, input.At)
}

// ReconcileExpiredLeases atomically fences expired executions. Units with no
// uncertain mutating effect return to pending; unsafe executions stop for
// operator reconciliation instead of being replayed blindly.
func ReconcileExpiredLeases(snapshot WorkflowSnapshot, input ReconcileExpiredLeasesInput) (WorkflowSnapshot, error) {
	next, err := beginWorkflowTransition(snapshot, input.ExpectedRevision)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	if next.Workflow.Status != WorkflowStatusRunning || input.At.IsZero() {
		return WorkflowSnapshot{}, fmt.Errorf("%w: reconciliation requires a running workflow and a time", ErrInvalidTransition)
	}
	reconciled := 0
	for unitIndex := range next.WorkUnits {
		unit := &next.WorkUnits[unitIndex]
		if unit.Lease == nil || (unit.Status != DurableWorkUnitStatusLeased && unit.Status != DurableWorkUnitStatusExecuting) || unit.Lease.ExpiresAt.After(input.At) {
			continue
		}
		reconciled++
		unsafe := false
		actionIDs := make([]ActionID, 0)
		for actionIndex := range next.Actions {
			action := &next.Actions[actionIndex]
			if action.WorkUnitID != unit.ID {
				continue
			}
			actionIDs = append(actionIDs, action.ID)
			if action.SideEffectClass != SideEffectNone && (action.Status == ActionStatusExecuting || action.Status == ActionStatusSucceeded || action.Status == ActionStatusAmbiguous) {
				unsafe = true
			}
			if action.FencingToken != unit.Lease.FencingToken {
				continue
			}
			switch action.Status {
			case ActionStatusPrepared:
				action.Status = ActionStatusAbandoned
				action.Reason = "lease expired before external execution started"
				action.CompletedAt = input.At
			case ActionStatusExecuting:
				action.Status = ActionStatusAmbiguous
				action.Reason = "lease expired while external execution outcome was unknown"
				action.CompletedAt = input.At
			}
		}

		unit.Lease = nil
		if unsafe {
			unit.Status = DurableWorkUnitStatusNeedsAttention
			unit.CompletedAt = input.At
			unit.Outcome = &DurableWorkUnitOutcome{ActionIDs: actionIDs, Reason: "expired lease contains a mutating action that cannot be replayed safely"}
			continue
		}
		unit.Status = DurableWorkUnitStatusPending
		unit.ClaimedAt = time.Time{}
		unit.StartedAt = time.Time{}
		unit.CompletedAt = time.Time{}
		unit.Outcome = nil
	}
	if reconciled == 0 {
		return WorkflowSnapshot{}, fmt.Errorf("%w: no expired leases to reconcile", ErrInvalidTransition)
	}
	return finishWorkflowTransition(snapshot, next, input.At)
}

// PrepareAction records a durable action intent before the action can execute.
func PrepareAction(snapshot WorkflowSnapshot, input PrepareActionInput) (WorkflowSnapshot, error) {
	next, err := beginWorkflowTransition(snapshot, input.ExpectedRevision)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	if workflowTerminal(next.Workflow.Status) {
		return WorkflowSnapshot{}, fmt.Errorf("%w: cannot prepare an action for a terminal workflow", ErrInvalidTransition)
	}
	action, err := NewDurableAction(input.Action)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	if _, index := durableActionByID(next, action.ID); index >= 0 {
		return WorkflowSnapshot{}, fmt.Errorf("%w: duplicate action id %q", ErrInvalidTransition, action.ID)
	}
	for _, existing := range next.Actions {
		if existing.IdempotencyKey == action.IdempotencyKey {
			return WorkflowSnapshot{}, fmt.Errorf("%w: duplicate action idempotency key %q", ErrInvalidTransition, action.IdempotencyKey)
		}
	}
	unit, _ := durableWorkUnitByID(next, action.WorkUnitID)
	if err := validatePreparedActionLease(next.Workflow, unit, action, input.Action.Lease, input.At); err != nil {
		return WorkflowSnapshot{}, err
	}
	next.Actions = append(next.Actions, action)
	return finishWorkflowTransition(snapshot, next, input.At)
}

// StartAction commits an action's prepared-to-executing transition. Callers
// must not perform the external effect before this transition is committed.
func StartAction(snapshot WorkflowSnapshot, actionID ActionID, credential WorkflowLeaseCredential, at time.Time) (WorkflowSnapshot, error) {
	next, err := beginWorkflowTransition(snapshot, credential.ExpectedRevision)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	action, index := durableActionByID(next, actionID)
	if index < 0 {
		return WorkflowSnapshot{}, fmt.Errorf("%w: missing action %q", ErrInvalidTransition, actionID)
	}
	unit, _ := durableWorkUnitByID(next, action.WorkUnitID)
	if err := validateActionLease(next.Workflow, unit, action, credential.LeaseCredential, at, ActionStatusPrepared); err != nil {
		return WorkflowSnapshot{}, err
	}
	next.Actions[index].Status = ActionStatusExecuting
	next.Actions[index].StartedAt = at
	return finishWorkflowTransition(snapshot, next, at)
}

// FinishAction records the terminal result of a previously committed executing action.
func FinishAction(snapshot WorkflowSnapshot, actionID ActionID, completion ActionCompletion, credential WorkflowLeaseCredential, at time.Time) (WorkflowSnapshot, error) {
	next, err := beginWorkflowTransition(snapshot, credential.ExpectedRevision)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	action, index := durableActionByID(next, actionID)
	if index < 0 {
		return WorkflowSnapshot{}, fmt.Errorf("%w: missing action %q", ErrInvalidTransition, actionID)
	}
	unit, _ := durableWorkUnitByID(next, action.WorkUnitID)
	if err := validateActionLease(next.Workflow, unit, action, credential.LeaseCredential, at, ActionStatusExecuting); err != nil {
		return WorkflowSnapshot{}, err
	}
	if err := appendWorkflowArtifacts(&next, completion.Artifacts); err != nil {
		return WorkflowSnapshot{}, err
	}
	if err := applyActionCompletion(&next.Actions[index], completion, at); err != nil {
		return WorkflowSnapshot{}, err
	}
	return finishWorkflowTransition(snapshot, next, at)
}

// FinishUnit records a terminal work-unit outcome and can append newly planned
// pending units in the same workflow revision.
func FinishUnit(snapshot WorkflowSnapshot, input FinishUnitInput) (WorkflowSnapshot, error) {
	next, err := beginWorkflowTransition(snapshot, input.ExpectedRevision)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	unit, index := durableWorkUnitByID(next, input.UnitID)
	if index < 0 || !terminalUnitStatus(input.Status) || input.Status == DurableWorkUnitStatusBlocked {
		return WorkflowSnapshot{}, fmt.Errorf("%w: finish requires an existing terminal unit status", ErrInvalidTransition)
	}
	if input.Status == DurableWorkUnitStatusSkipped {
		if unit.Status != DurableWorkUnitStatusPending || strings.TrimSpace(input.Outcome.Reason) == "" {
			return WorkflowSnapshot{}, fmt.Errorf("%w: skipped unit requires a pending unit and reason", ErrInvalidTransition)
		}
	} else if err := validateUnitCredential(next.Workflow, unit, input.Credential, input.At, DurableWorkUnitStatusExecuting); err != nil {
		return WorkflowSnapshot{}, err
	}
	if err := appendWorkflowArtifacts(&next, input.Artifacts); err != nil {
		return WorkflowSnapshot{}, err
	}
	if err := validateTerminalUnitOutcome(input.Status, input.Outcome); err != nil {
		return WorkflowSnapshot{}, err
	}
	if err := validateOutcomeActions(next, unit.ID, input.Outcome); err != nil {
		return WorkflowSnapshot{}, err
	}
	if err := appendNewDurableWorkUnits(&next, input.NewUnits); err != nil {
		return WorkflowSnapshot{}, err
	}
	next.WorkUnits[index].Status = input.Status
	next.WorkUnits[index].Lease = nil
	next.WorkUnits[index].Outcome = cloneOutcome(&input.Outcome)
	next.WorkUnits[index].CompletedAt = input.At
	return finishWorkflowTransition(snapshot, next, input.At)
}

// BlockWorkUnits atomically records dependency-propagated no-execution
// outcomes. A blocked unit is terminal without ever acquiring a lease.
func BlockWorkUnits(snapshot WorkflowSnapshot, input BlockWorkUnitsInput) (WorkflowSnapshot, error) {
	next, err := beginWorkflowTransition(snapshot, input.ExpectedRevision)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	if next.Workflow.Status != WorkflowStatusRunning || len(input.Blocks) == 0 || input.At.IsZero() {
		return WorkflowSnapshot{}, fmt.Errorf("%w: blocking requires a running workflow, units, and a time", ErrInvalidTransition)
	}

	blockSet := make(map[DurableWorkUnitID]struct{}, len(input.Blocks))
	for _, block := range input.Blocks {
		if _, exists := blockSet[block.UnitID]; exists {
			return WorkflowSnapshot{}, fmt.Errorf("%w: duplicate block for unit %q", ErrInvalidTransition, block.UnitID)
		}
		blockSet[block.UnitID] = struct{}{}
		unit, index := durableWorkUnitByID(snapshot, block.UnitID)
		if index < 0 || unit.Status != DurableWorkUnitStatusPending {
			return WorkflowSnapshot{}, fmt.Errorf("%w: unit %q is not pending", ErrInvalidTransition, block.UnitID)
		}
		outcome := DurableWorkUnitOutcome{Reason: strings.TrimSpace(block.Reason)}
		if err := validateTerminalUnitOutcome(DurableWorkUnitStatusBlocked, outcome); err != nil {
			return WorkflowSnapshot{}, err
		}
	}

	reachable := make(map[DurableWorkUnitID]struct{}, len(input.Blocks))
	for {
		progressed := false
		for _, block := range input.Blocks {
			if _, exists := reachable[block.UnitID]; exists {
				continue
			}
			unit, _ := durableWorkUnitByID(snapshot, block.UnitID)
			for _, dependencyID := range unit.Dependencies {
				dependency, _ := durableWorkUnitByID(snapshot, dependencyID)
				_, dependencyReached := reachable[dependencyID]
				if blockingWorkUnitStatus(dependency.Status) || dependencyReached {
					reachable[block.UnitID] = struct{}{}
					progressed = true
					break
				}
			}
		}
		if !progressed {
			break
		}
	}
	if len(reachable) != len(blockSet) {
		return WorkflowSnapshot{}, fmt.Errorf("%w: block batch contains a unit outside the failed dependency closure", ErrInvalidTransition)
	}

	for _, block := range input.Blocks {
		_, index := durableWorkUnitByID(next, block.UnitID)
		outcome := DurableWorkUnitOutcome{Reason: strings.TrimSpace(block.Reason)}
		next.WorkUnits[index].Status = DurableWorkUnitStatusBlocked
		next.WorkUnits[index].Outcome = &outcome
		next.WorkUnits[index].CompletedAt = input.At
	}
	return finishWorkflowTransition(snapshot, next, input.At)
}

// SettleWorkflow assigns the aggregate terminal status after every unit is terminal.
func SettleWorkflow(snapshot WorkflowSnapshot, input SettleWorkflowInput) (WorkflowSnapshot, error) {
	next, err := beginWorkflowTransition(snapshot, input.ExpectedRevision)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	if workflowTerminal(next.Workflow.Status) {
		return WorkflowSnapshot{}, fmt.Errorf("%w: workflow is already terminal", ErrInvalidTransition)
	}
	if input.At.IsZero() {
		return WorkflowSnapshot{}, fmt.Errorf("%w: settlement time is required", ErrInvalidTransition)
	}
	status, ok := workflowTerminalStatus(next.WorkUnits)
	if !ok {
		return WorkflowSnapshot{}, fmt.Errorf("%w: workflow has nonterminal work units", ErrInvalidTransition)
	}
	if err := validateArtifactReferences(input.FinalOutcomeRefs); err != nil {
		return WorkflowSnapshot{}, fmt.Errorf("%w: final outcome refs: %v", ErrInvalidTransition, err)
	}
	if err := appendWorkflowArtifacts(&next, input.Artifacts); err != nil {
		return WorkflowSnapshot{}, err
	}
	next.Workflow.Status = status
	next.Workflow.FinalOutcomeRefs = cloneArtifactReferences(input.FinalOutcomeRefs)
	next.Workflow.CompletedAt = input.At
	return finishWorkflowTransition(snapshot, next, input.At)
}

func beginWorkflowTransition(snapshot WorkflowSnapshot, expectedRevision int64) (WorkflowSnapshot, error) {
	next, err := cloneWorkflowSnapshot(snapshot)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	if err := ValidateWorkflowSnapshot(next); err != nil {
		return WorkflowSnapshot{}, err
	}
	if expectedRevision != next.Workflow.Revision {
		return WorkflowSnapshot{}, ErrStaleWorkflowRevision
	}
	return next, nil
}

func finishWorkflowTransition(before, next WorkflowSnapshot, at time.Time) (WorkflowSnapshot, error) {
	if next.Workflow.Revision != before.Workflow.Revision {
		return WorkflowSnapshot{}, fmt.Errorf("%w: operation changed revision before commit", ErrInvalidTransition)
	}
	if at.IsZero() || at.Before(next.Workflow.UpdatedAt) {
		return WorkflowSnapshot{}, fmt.Errorf("%w: transition time is required and cannot precede the workflow update time", ErrInvalidTransition)
	}
	if err := validateFrozenWorkUnits(before, next); err != nil {
		return WorkflowSnapshot{}, err
	}
	next.Workflow.Revision++
	next.Workflow.UpdatedAt = at
	if err := ValidateWorkflowSnapshot(next); err != nil {
		return WorkflowSnapshot{}, err
	}
	return next, nil
}

func cloneWorkflowSnapshot(snapshot WorkflowSnapshot) (WorkflowSnapshot, error) {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return WorkflowSnapshot{}, fmt.Errorf("%w: clone snapshot: %v", ErrInvalidWorkflow, err)
	}
	var copy WorkflowSnapshot
	if err := json.Unmarshal(encoded, &copy); err != nil {
		return WorkflowSnapshot{}, fmt.Errorf("%w: clone snapshot: %v", ErrInvalidWorkflow, err)
	}
	return copy, nil
}

func durableWorkUnitByID(snapshot WorkflowSnapshot, id DurableWorkUnitID) (DurableWorkUnit, int) {
	for index, unit := range snapshot.WorkUnits {
		if unit.ID == id {
			return unit, index
		}
	}
	return DurableWorkUnit{}, -1
}

func durableActionByID(snapshot WorkflowSnapshot, id ActionID) (DurableAction, int) {
	for index, action := range snapshot.Actions {
		if action.ID == id {
			return action, index
		}
	}
	return DurableAction{}, -1
}

func dependenciesComplete(snapshot WorkflowSnapshot, unit DurableWorkUnit) bool {
	for _, dependencyID := range unit.Dependencies {
		dependency, index := durableWorkUnitByID(snapshot, dependencyID)
		if index < 0 || (dependency.Status != DurableWorkUnitStatusSucceeded && dependency.Status != DurableWorkUnitStatusSkipped) {
			return false
		}
	}
	return true
}

func blockingWorkUnitStatus(status DurableWorkUnitStatus) bool {
	return status == DurableWorkUnitStatusFailed || status == DurableWorkUnitStatusNeedsAttention || status == DurableWorkUnitStatusBlocked
}

func validateUnitCredential(workflow Workflow, unit DurableWorkUnit, credential LeaseCredential, at time.Time, expected DurableWorkUnitStatus) error {
	if workflow.Status != WorkflowStatusRunning || unit.Status != expected || unit.Lease == nil {
		return fmt.Errorf("%w: expected running workflow and %s unit with a lease", ErrInvalidTransition, expected)
	}
	if strings.TrimSpace(string(credential.Token)) == "" || credential.FencingToken == 0 || credential.Token != unit.Lease.Token || credential.FencingToken != unit.Lease.FencingToken {
		return ErrLeaseMismatch
	}
	if at.IsZero() {
		return fmt.Errorf("%w: transition time is required", ErrInvalidTransition)
	}
	if !unit.Lease.ExpiresAt.After(at) {
		return ErrLeaseExpired
	}
	return nil
}

func validateActionLease(workflow Workflow, unit DurableWorkUnit, action DurableAction, credential LeaseCredential, at time.Time, expected ActionStatus) error {
	if err := validateUnitCredential(workflow, unit, credential, at, DurableWorkUnitStatusExecuting); err != nil {
		return err
	}
	if action.WorkflowID != workflow.ID || action.WorkUnitID != unit.ID || action.Attempt != unit.Attempt {
		return fmt.Errorf("%w: action linkage or attempt does not match its work unit", ErrInvalidTransition)
	}
	if action.LeaseToken != credential.Token || action.FencingToken != credential.FencingToken {
		return ErrLeaseMismatch
	}
	if action.Status != expected {
		return fmt.Errorf("%w: action must be %s, got %s", ErrInvalidTransition, expected, action.Status)
	}
	if !action.StartedAt.IsZero() && at.Before(action.StartedAt) {
		return fmt.Errorf("%w: transition time precedes action start", ErrInvalidTransition)
	}
	return nil
}

func validatePreparedActionLease(workflow Workflow, unit DurableWorkUnit, action DurableAction, credential LeaseCredential, at time.Time) error {
	if err := validateUnitCredential(workflow, unit, credential, at, DurableWorkUnitStatusExecuting); err != nil {
		return err
	}
	if action.WorkflowID != workflow.ID || action.WorkUnitID != unit.ID || action.Attempt != unit.Attempt || action.LeaseToken != credential.Token || action.FencingToken != credential.FencingToken || action.Status != ActionStatusPrepared {
		return fmt.Errorf("%w: prepared action linkage, attempt, lease, or status is invalid", ErrInvalidTransition)
	}
	return nil
}

func applyActionCompletion(action *DurableAction, completion ActionCompletion, at time.Time) error {
	completion.Reason = strings.TrimSpace(completion.Reason)
	if err := validateActionResult(completion.Result); err != nil {
		return fmt.Errorf("%w: result: %v", ErrInvalidTransition, err)
	}
	switch completion.Status {
	case ActionStatusSucceeded:
		if completion.Reason != "" {
			return fmt.Errorf("%w: successful action cannot have a reason", ErrInvalidTransition)
		}
		if action.SideEffectClass != SideEffectNone && (strings.TrimSpace(completion.Result.PostconditionFingerprint) == "" || len(completion.Result.ResultArtifactRefs) == 0) {
			return fmt.Errorf("%w: successful mutating action requires a postcondition fingerprint and result artifact references", ErrInvalidTransition)
		}
	case ActionStatusFailed, ActionStatusAmbiguous:
		if completion.Reason == "" {
			return fmt.Errorf("%w: %s action requires a reason", ErrInvalidTransition, completion.Status)
		}
	default:
		return fmt.Errorf("%w: action completion must be terminal", ErrInvalidTransition)
	}
	action.Status = completion.Status
	action.Reason = completion.Reason
	action.ResultArtifactRefs = cloneArtifactReferences(completion.Result.ResultArtifactRefs)
	action.ExecutionRef = strings.TrimSpace(completion.Result.ExecutionRef)
	action.MutationRefs = cloneStrings(completion.Result.MutationRefs)
	action.PostconditionFingerprint = strings.TrimSpace(completion.Result.PostconditionFingerprint)
	action.CompletedAt = at
	return nil
}

func validateTerminalUnitOutcome(status DurableWorkUnitStatus, outcome DurableWorkUnitOutcome) error {
	if err := validateDurableWorkUnitOutcomeState(status, &outcome); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidTransition, err)
	}
	return nil
}

func validateOutcomeActions(snapshot WorkflowSnapshot, unitID DurableWorkUnitID, outcome DurableWorkUnitOutcome) error {
	seen := make(map[ActionID]struct{}, len(outcome.ActionIDs))
	for _, actionID := range outcome.ActionIDs {
		if _, duplicate := seen[actionID]; duplicate {
			return fmt.Errorf("%w: duplicate outcome action %q", ErrInvalidTransition, actionID)
		}
		seen[actionID] = struct{}{}
		action, index := durableActionByID(snapshot, actionID)
		if index < 0 || action.WorkUnitID != unitID || !terminalActionStatus(action.Status) {
			return fmt.Errorf("%w: outcome action %q is not terminal for unit %q", ErrInvalidTransition, actionID, unitID)
		}
	}
	return nil
}

func appendNewDurableWorkUnits(snapshot *WorkflowSnapshot, units []DurableWorkUnit) error {
	known := make(map[DurableWorkUnitID]struct{}, len(snapshot.WorkUnits)+len(units))
	for _, unit := range snapshot.WorkUnits {
		known[unit.ID] = struct{}{}
	}
	for _, unit := range units {
		if _, exists := known[unit.ID]; exists || unit.WorkflowID != snapshot.Workflow.ID || unit.Status != DurableWorkUnitStatusPending || unit.Attempt < 1 || unit.Lease != nil || unit.Outcome != nil {
			return fmt.Errorf("%w: appended unit %q must be a new pending unit for this workflow", ErrInvalidTransition, unit.ID)
		}
		if err := ValidateDurableWorkUnit(unit); err != nil {
			return err
		}
		known[unit.ID] = struct{}{}
		snapshot.WorkUnits = append(snapshot.WorkUnits, cloneDurableWorkUnit(unit))
		snapshot.Workflow.WorkUnitIDs = append(snapshot.Workflow.WorkUnitIDs, unit.ID)
	}
	return nil
}

func appendWorkflowArtifacts(snapshot *WorkflowSnapshot, artifacts []RunArtifact) error {
	known := make(map[string]struct{}, len(snapshot.Artifacts)+len(artifacts))
	for _, artifact := range snapshot.Artifacts {
		known[artifact.ID] = struct{}{}
	}
	for _, artifact := range artifacts {
		if strings.TrimSpace(artifact.ID) == "" {
			return fmt.Errorf("%w: appended artifact id is required", ErrInvalidTransition)
		}
		if _, exists := known[artifact.ID]; exists {
			return fmt.Errorf("%w: appended artifact %q already exists", ErrInvalidTransition, artifact.ID)
		}
		if err := ValidateArtifactPayload(artifact); err != nil {
			return fmt.Errorf("%w: appended artifact %q payload: %v", ErrInvalidTransition, artifact.ID, err)
		}
		known[artifact.ID] = struct{}{}
		snapshot.Artifacts = append(snapshot.Artifacts, cloneRunArtifact(artifact))
	}
	return nil
}

func workflowPlanAttachmentMatches(snapshot WorkflowSnapshot, input AttachWorkflowPlanInput) bool {
	if !reflect.DeepEqual(snapshot.Workflow.GraphArtifactRefs, input.GraphArtifactRefs) || len(snapshot.WorkUnits) != len(input.WorkUnits) {
		return false
	}
	for index, unit := range input.WorkUnits {
		if !reflect.DeepEqual(snapshot.WorkUnits[index], unit) {
			return false
		}
	}
	artifacts := make(map[string]RunArtifact, len(snapshot.Artifacts))
	for _, artifact := range snapshot.Artifacts {
		artifacts[artifact.ID] = artifact
	}
	for _, artifact := range input.Artifacts {
		if existing, ok := artifacts[artifact.ID]; !ok || !reflect.DeepEqual(existing, artifact) {
			return false
		}
	}
	return len(input.GraphArtifactRefs) == 1 && len(input.WorkUnits) > 0
}

func placeAttachedArtifactsBeforeWorkflowInput(snapshot *WorkflowSnapshot, attached []RunArtifact) {
	attachedIDs := make(map[string]struct{}, len(attached))
	for _, artifact := range attached {
		attachedIDs[artifact.ID] = struct{}{}
	}
	beforeInput := make([]RunArtifact, 0, len(snapshot.Artifacts))
	attachedArtifacts := make([]RunArtifact, 0, len(attached))
	workflowInputs := make([]RunArtifact, 0, 1)
	for _, artifact := range snapshot.Artifacts {
		if _, ok := attachedIDs[artifact.ID]; ok {
			attachedArtifacts = append(attachedArtifacts, artifact)
			continue
		}
		if artifact.Kind == "workflow_input" {
			workflowInputs = append(workflowInputs, artifact)
			continue
		}
		beforeInput = append(beforeInput, artifact)
	}
	snapshot.Artifacts = append(beforeInput, attachedArtifacts...)
	snapshot.Artifacts = append(snapshot.Artifacts, workflowInputs...)
}

func cloneRunArtifact(artifact RunArtifact) RunArtifact {
	encoded, err := json.Marshal(artifact)
	if err == nil {
		var cloned RunArtifact
		if json.Unmarshal(encoded, &cloned) == nil {
			return cloned
		}
	}
	artifact.Payload = append(json.RawMessage(nil), artifact.Payload...)
	artifact.References = cloneArtifactReferences(artifact.References)
	return artifact
}

func validateFrozenWorkUnits(before, after WorkflowSnapshot) error {
	afterByID := make(map[DurableWorkUnitID]DurableWorkUnit, len(after.WorkUnits))
	for _, unit := range after.WorkUnits {
		afterByID[unit.ID] = unit
	}
	for _, unit := range before.WorkUnits {
		next, exists := afterByID[unit.ID]
		if !exists || !sameFrozenWorkUnitFields(unit, next) {
			return fmt.Errorf("%w: immutable fields changed for unit %q", ErrInvalidTransition, unit.ID)
		}
	}
	return nil
}

func sameFrozenWorkUnitFields(left, right DurableWorkUnit) bool {
	return left.ID == right.ID && left.WorkflowID == right.WorkflowID && left.Kind == right.Kind && left.Phase == right.Phase && left.Role == right.Role && left.Task == right.Task && left.Source == right.Source && left.SourceLimit == right.SourceLimit && left.SideEffectClass == right.SideEffectClass && left.DuplicateKey == right.DuplicateKey && reflect.DeepEqual(left.InputArtifactRefs, right.InputArtifactRefs) && reflect.DeepEqual(left.ReadSet, right.ReadSet) && reflect.DeepEqual(left.WriteSet, right.WriteSet) && reflect.DeepEqual(left.Dependencies, right.Dependencies)
}

func terminalActionStatus(status ActionStatus) bool {
	return status == ActionStatusSucceeded || status == ActionStatusFailed || status == ActionStatusAmbiguous || status == ActionStatusAbandoned
}

func terminalUnitStatus(status DurableWorkUnitStatus) bool {
	return status == DurableWorkUnitStatusSucceeded || status == DurableWorkUnitStatusSkipped || status == DurableWorkUnitStatusBlocked || status == DurableWorkUnitStatusFailed || status == DurableWorkUnitStatusNeedsAttention
}

func workflowTerminal(status WorkflowStatus) bool {
	return status == WorkflowStatusSucceeded || status == WorkflowStatusFailed || status == WorkflowStatusNeedsAttention
}

func workflowTerminalStatus(units []DurableWorkUnit) (WorkflowStatus, bool) {
	allSucceededOrSkipped := true
	for _, unit := range units {
		switch unit.Status {
		case DurableWorkUnitStatusNeedsAttention:
			return WorkflowStatusNeedsAttention, true
		case DurableWorkUnitStatusFailed, DurableWorkUnitStatusBlocked:
			allSucceededOrSkipped = false
		case DurableWorkUnitStatusSucceeded, DurableWorkUnitStatusSkipped:
		default:
			return "", false
		}
	}
	if !allSucceededOrSkipped {
		return WorkflowStatusFailed, true
	}
	return WorkflowStatusSucceeded, true
}
