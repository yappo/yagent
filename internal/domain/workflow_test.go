package domain

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

var workflowTestTime = time.Date(2026, time.July, 10, 12, 0, 0, 0, time.UTC)

func TestDurableWorkUnitCarriesFrozenSchedulerFields(t *testing.T) {
	input := DurableWorkUnitInput{
		ID: "unit-1", WorkflowID: "workflow-1", Kind: "execute", Phase: RunPhaseExecute, Role: "worker",
		Task: "write the implementation", Source: "coder", SourceLimit: 2, SideEffectClass: SideEffectWorkspace, DuplicateKey: "write:workflow",
		InputArtifactRefs: []ArtifactReference{{ID: "input-1", Kind: "repo_map"}}, ReadSet: []string{"internal/domain"}, WriteSet: []string{"internal/domain/workflow.go"}, Dependencies: []DurableWorkUnitID{"unit-0"},
	}
	unit, err := NewDurableWorkUnit(input)
	if err != nil {
		t.Fatalf("NewDurableWorkUnit() error = %v", err)
	}
	input.Task = "changed"
	input.Source = "other"
	input.InputArtifactRefs[0].ID = "changed"
	input.ReadSet[0] = "changed"
	input.WriteSet[0] = "changed"
	input.Dependencies[0] = "changed"
	if unit.Task != "write the implementation" || unit.Source != "coder" || unit.SourceLimit != 2 || unit.SideEffectClass != SideEffectWorkspace || unit.DuplicateKey != "write:workflow" || unit.InputArtifactRefs[0].ID != "input-1" || unit.ReadSet[0] != "internal/domain" || unit.WriteSet[0] != "internal/domain/workflow.go" || unit.Dependencies[0] != "unit-0" {
		t.Fatalf("durable unit retained mutable input: %+v", unit)
	}
	input.SourceLimit = -1
	if _, err := NewDurableWorkUnit(input); !errors.Is(err, ErrInvalidDurableWorkUnit) {
		t.Fatalf("negative source limit error = %v", err)
	}
}

func TestNewWorkflowRequiresCreationTime(t *testing.T) {
	_, err := NewWorkflow(WorkflowInput{
		ID:           "workflow-without-time",
		Conversation: ConversationReference{ConversationID: "conversation-1", TurnID: "turn-1"},
		RootGoal:     "require a durable lifecycle origin",
	})
	if !errors.Is(err, ErrInvalidWorkflow) {
		t.Fatalf("NewWorkflow() error = %v", err)
	}
}

func TestValidateDurableWorkUnitEnforcesTimestampLifecycle(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*DurableWorkUnit)
	}{
		{name: "pending with claim", mutate: func(unit *DurableWorkUnit) { unit.ClaimedAt = workflowTestTime }},
		{name: "leased without claim", mutate: func(unit *DurableWorkUnit) {
			unit.Status = DurableWorkUnitStatusLeased
			unit.Lease = cloneLeasePtr(testLease("lease-a", 1))
			unit.LastFencingToken = 1
		}},
		{name: "executing before claim", mutate: func(unit *DurableWorkUnit) {
			unit.Status = DurableWorkUnitStatusExecuting
			unit.ClaimedAt = workflowTestTime.Add(time.Minute)
			unit.StartedAt = workflowTestTime
			unit.Lease = cloneLeasePtr(testLease("lease-a", 1))
			unit.LastFencingToken = 1
		}},
		{name: "terminal completion before start", mutate: func(unit *DurableWorkUnit) {
			setExecutedTerminalTestUnit(unit, DurableWorkUnitStatusSucceeded)
			unit.CompletedAt = unit.StartedAt.Add(-time.Nanosecond)
		}},
		{name: "skipped with claim", mutate: func(unit *DurableWorkUnit) {
			setTerminalTestUnit(unit, DurableWorkUnitStatusSkipped)
			unit.ClaimedAt = workflowTestTime
		}},
		{name: "blocked with lease", mutate: func(unit *DurableWorkUnit) {
			setTerminalTestUnit(unit, DurableWorkUnitStatusBlocked)
			unit.Lease = cloneLeasePtr(testLease("lease-a", 1))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			unit := testUnit(t, "workflow-1", "unit-a")
			test.mutate(&unit)
			if err := ValidateDurableWorkUnit(unit); !errors.Is(err, ErrInvalidDurableWorkUnit) {
				t.Fatalf("ValidateDurableWorkUnit() error = %v", err)
			}
		})
	}
}

func TestDurableActionRequiresPreconditionForEffects(t *testing.T) {
	base := DurableActionInput{ID: "action-1", WorkflowID: "workflow-1", WorkUnitID: "unit-1", Attempt: 1, Kind: "tool", Target: "write", IdempotencyKey: "key-1", Lease: testCredential(), SideEffectClass: SideEffectWorkspace}
	if _, err := NewDurableAction(base); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("mutating action without precondition error = %v", err)
	}
	base.SideEffectClass = SideEffectNone
	if _, err := NewDurableAction(base); err != nil {
		t.Fatalf("read-only action without precondition error = %v", err)
	}
}

func TestValidateDurableWorkUnitEnforcesStatusOutcomeLifecycle(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*DurableWorkUnit)
		wantErr bool
	}{
		{name: "pending without outcome"},
		{name: "pending with outcome", mutate: func(unit *DurableWorkUnit) { unit.Outcome = &DurableWorkUnitOutcome{} }, wantErr: true},
		{name: "leased with outcome", mutate: func(unit *DurableWorkUnit) {
			unit.Status = DurableWorkUnitStatusLeased
			unit.ClaimedAt = workflowTestTime
			unit.Lease = cloneLeasePtr(testLease("lease-a", 1))
			unit.LastFencingToken = 1
			unit.Outcome = &DurableWorkUnitOutcome{}
		}, wantErr: true},
		{name: "executing with outcome", mutate: func(unit *DurableWorkUnit) {
			unit.Status = DurableWorkUnitStatusExecuting
			unit.ClaimedAt = workflowTestTime
			unit.StartedAt = workflowTestTime.Add(time.Minute)
			unit.Lease = cloneLeasePtr(testLease("lease-a", 1))
			unit.LastFencingToken = 1
			unit.Outcome = &DurableWorkUnitOutcome{}
		}, wantErr: true},
		{name: "succeeded without outcome", mutate: func(unit *DurableWorkUnit) { unit.Status = DurableWorkUnitStatusSucceeded }, wantErr: true},
		{name: "succeeded with reason", mutate: func(unit *DurableWorkUnit) {
			unit.Status = DurableWorkUnitStatusSucceeded
			unit.Outcome = &DurableWorkUnitOutcome{Reason: "unexpected"}
		}, wantErr: true},
		{name: "succeeded", mutate: func(unit *DurableWorkUnit) {
			setExecutedTerminalTestUnit(unit, DurableWorkUnitStatusSucceeded)
		}},
		{name: "skipped without outcome", mutate: func(unit *DurableWorkUnit) { unit.Status = DurableWorkUnitStatusSkipped }, wantErr: true},
		{name: "skipped without reason", mutate: func(unit *DurableWorkUnit) {
			unit.Status = DurableWorkUnitStatusSkipped
			unit.Outcome = &DurableWorkUnitOutcome{}
		}, wantErr: true},
		{name: "skipped", mutate: func(unit *DurableWorkUnit) {
			unit.Status = DurableWorkUnitStatusSkipped
			unit.Outcome = &DurableWorkUnitOutcome{Reason: "optional work unavailable"}
			unit.CompletedAt = workflowTestTime.Add(time.Minute)
		}},
		{name: "blocked without reason", mutate: func(unit *DurableWorkUnit) {
			unit.Status = DurableWorkUnitStatusBlocked
			unit.Outcome = &DurableWorkUnitOutcome{}
			unit.CompletedAt = workflowTestTime.Add(time.Minute)
		}, wantErr: true},
		{name: "blocked", mutate: func(unit *DurableWorkUnit) {
			setTerminalTestUnit(unit, DurableWorkUnitStatusBlocked)
		}},
		{name: "failed without reason", mutate: func(unit *DurableWorkUnit) {
			unit.Status = DurableWorkUnitStatusFailed
			unit.Outcome = &DurableWorkUnitOutcome{}
		}, wantErr: true},
		{name: "failed", mutate: func(unit *DurableWorkUnit) {
			setExecutedTerminalTestUnit(unit, DurableWorkUnitStatusFailed)
			unit.Outcome.Reason = "execution failed"
		}},
		{name: "needs attention without reason", mutate: func(unit *DurableWorkUnit) {
			unit.Status = DurableWorkUnitStatusNeedsAttention
			unit.Outcome = &DurableWorkUnitOutcome{}
		}, wantErr: true},
		{name: "needs attention", mutate: func(unit *DurableWorkUnit) {
			setExecutedTerminalTestUnit(unit, DurableWorkUnitStatusNeedsAttention)
			unit.Outcome.Reason = "manual reconciliation required"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			unit := testUnit(t, "workflow-1", "unit-a")
			if test.mutate != nil {
				test.mutate(&unit)
			}
			err := ValidateDurableWorkUnit(unit)
			if test.wantErr && !errors.Is(err, ErrInvalidDurableWorkUnit) {
				t.Fatalf("ValidateDurableWorkUnit() error = %v", err)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("ValidateDurableWorkUnit() unexpected error = %v", err)
			}
		})
	}
}

func TestClaimAndStartBatchUseOneRevision(t *testing.T) {
	snapshot := testSnapshot(t, "unit-a", "unit-b")
	claimed, err := ClaimReadyBatch(snapshot, WorkflowBatchClaims{ExpectedRevision: snapshot.Workflow.Revision, Claims: []WorkUnitClaim{
		{UnitID: "unit-a", Lease: testLease("lease-a", 1)}, {UnitID: "unit-b", Lease: testLease("lease-b", 1)},
	}}, workflowTestTime)
	if err != nil {
		t.Fatalf("ClaimReadyBatch() error = %v", err)
	}
	if claimed.Workflow.Revision != snapshot.Workflow.Revision+1 || claimed.Workflow.Status != WorkflowStatusRunning || !claimed.Workflow.UpdatedAt.Equal(workflowTestTime) || claimed.WorkUnits[0].Status != DurableWorkUnitStatusLeased || !claimed.WorkUnits[0].ClaimedAt.Equal(workflowTestTime) || claimed.WorkUnits[1].Status != DurableWorkUnitStatusLeased || !claimed.WorkUnits[1].ClaimedAt.Equal(workflowTestTime) {
		t.Fatalf("claimed snapshot = %+v", claimed)
	}
	snapshot.WorkUnits[0].ReadSet[0] = "mutated-input"
	snapshot.Artifacts[0].ID = "mutated-input"
	if claimed.WorkUnits[0].ReadSet[0] != "internal/domain" || claimed.Artifacts[0].ID != "input-unit-a" {
		t.Fatalf("ClaimReadyBatch() did not deep-copy the complete snapshot: %+v", claimed)
	}
	started, err := StartClaimedBatch(claimed, WorkflowBatchCredentials{ExpectedRevision: claimed.Workflow.Revision, Credentials: []WorkUnitCredential{
		{UnitID: "unit-a", Credential: LeaseCredential{Token: "lease-a", FencingToken: 1}}, {UnitID: "unit-b", Credential: LeaseCredential{Token: "lease-b", FencingToken: 1}},
	}}, workflowTestTime.Add(time.Minute))
	if err != nil {
		t.Fatalf("StartClaimedBatch() error = %v", err)
	}
	if started.Workflow.Revision != claimed.Workflow.Revision+1 || !started.Workflow.UpdatedAt.Equal(workflowTestTime.Add(time.Minute)) || started.WorkUnits[0].Status != DurableWorkUnitStatusExecuting || !started.WorkUnits[0].StartedAt.Equal(workflowTestTime.Add(time.Minute)) || started.WorkUnits[1].Status != DurableWorkUnitStatusExecuting || !started.WorkUnits[1].StartedAt.Equal(workflowTestTime.Add(time.Minute)) {
		t.Fatalf("started snapshot = %+v", started)
	}
	if _, err := StartClaimedBatch(claimed, WorkflowBatchCredentials{ExpectedRevision: claimed.Workflow.Revision, Credentials: []WorkUnitCredential{{UnitID: "unit-a", Credential: LeaseCredential{Token: "lease-a", FencingToken: 1}}}}, workflowTestTime.Add(time.Minute)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("partial batch start error = %v", err)
	}
}

func TestClaimReadyBatchRejectsUnmetDependencyAndAcceptsSkipped(t *testing.T) {
	snapshot := testSnapshot(t, "prepare", "execute")
	snapshot.WorkUnits[1].Dependencies = []DurableWorkUnitID{"prepare"}
	if _, err := ClaimReadyBatch(snapshot, WorkflowBatchClaims{ExpectedRevision: 1, Claims: []WorkUnitClaim{{UnitID: "execute", Lease: testLease("lease-b", 1)}}}, workflowTestTime); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("unmet dependency error = %v", err)
	}
	snapshot.WorkUnits[0].Status = DurableWorkUnitStatusSkipped
	snapshot.WorkUnits[0].Outcome = &DurableWorkUnitOutcome{Reason: "optional preparation unavailable"}
	snapshot.WorkUnits[0].CompletedAt = workflowTestTime
	snapshot.Workflow.Status = WorkflowStatusRunning
	claimed, err := ClaimReadyBatch(snapshot, WorkflowBatchClaims{ExpectedRevision: 1, Claims: []WorkUnitClaim{{UnitID: "execute", Lease: testLease("lease-b", 1)}}}, workflowTestTime)
	if err != nil {
		t.Fatalf("ClaimReadyBatch() with skipped dependency error = %v", err)
	}
	if claimed.WorkUnits[1].Status != DurableWorkUnitStatusLeased || claimed.Workflow.Revision != 2 {
		t.Fatalf("skipped dependency was not ready: %+v", claimed)
	}
}

func TestActionLifecycleEnforcesCommitOwnershipAndCopiesResult(t *testing.T) {
	snapshot := startedSnapshot(t, "unit-a", "lease-a")
	prepared, err := PrepareAction(snapshot, PrepareActionInput{ExpectedRevision: snapshot.Workflow.Revision, Action: actionInput(snapshot, "action-a", "unit-a", "lease-a"), At: workflowTestTime.Add(90 * time.Second)})
	if err != nil {
		t.Fatalf("PrepareAction() error = %v", err)
	}
	if prepared.Workflow.Revision != snapshot.Workflow.Revision+1 || prepared.Actions[0].Status != ActionStatusPrepared {
		t.Fatalf("prepared snapshot = %+v", prepared)
	}
	if _, err := FinishAction(prepared, "action-a", ActionCompletion{Status: ActionStatusSucceeded}, WorkflowLeaseCredential{ExpectedRevision: prepared.Workflow.Revision, LeaseCredential: LeaseCredential{Token: "lease-a", FencingToken: 1}}, workflowTestTime.Add(2*time.Minute)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("finish before committed start error = %v", err)
	}
	if _, err := StartAction(prepared, "action-a", WorkflowLeaseCredential{ExpectedRevision: prepared.Workflow.Revision, LeaseCredential: LeaseCredential{Token: "wrong", FencingToken: 1}}, workflowTestTime.Add(2*time.Minute)); !errors.Is(err, ErrLeaseMismatch) {
		t.Fatalf("wrong owner error = %v", err)
	}
	started, err := StartAction(prepared, "action-a", WorkflowLeaseCredential{ExpectedRevision: prepared.Workflow.Revision, LeaseCredential: LeaseCredential{Token: "lease-a", FencingToken: 1}}, workflowTestTime.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("StartAction() error = %v", err)
	}
	result := DurableActionResult{ResultArtifactRefs: []ArtifactReference{{ID: "result-1", Kind: "test_report"}}, MutationRefs: []string{"mutation-1"}, ExecutionRef: "execution-1", PostconditionFingerprint: "post"}
	artifacts := []RunArtifact{testReportArtifact("result-1")}
	finished, err := FinishAction(started, "action-a", ActionCompletion{Status: ActionStatusSucceeded, Result: result, Artifacts: artifacts}, WorkflowLeaseCredential{ExpectedRevision: started.Workflow.Revision, LeaseCredential: LeaseCredential{Token: "lease-a", FencingToken: 1}}, workflowTestTime.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("FinishAction() error = %v", err)
	}
	result.ResultArtifactRefs[0].ID = "changed"
	result.MutationRefs[0] = "changed"
	artifacts[0].ID = "changed"
	if finished.Workflow.Revision != started.Workflow.Revision+1 || finished.Actions[0].Status != ActionStatusSucceeded || finished.Actions[0].ResultArtifactRefs[0].ID != "result-1" || finished.Actions[0].MutationRefs[0] != "mutation-1" || finished.Artifacts[len(finished.Artifacts)-1].ID != "result-1" {
		t.Fatalf("finished snapshot = %+v", finished)
	}
}

func TestAggregateTransitionsRejectStaleRevisionAndFencing(t *testing.T) {
	snapshot := testSnapshot(t, "unit-a")
	if _, err := ClaimReadyBatch(snapshot, WorkflowBatchClaims{ExpectedRevision: 0, Claims: []WorkUnitClaim{{UnitID: "unit-a", Lease: testLease("lease-a", 1)}}}, workflowTestTime); !errors.Is(err, ErrStaleWorkflowRevision) {
		t.Fatalf("stale claim error = %v", err)
	}
	claimed, err := ClaimReadyBatch(snapshot, WorkflowBatchClaims{ExpectedRevision: 1, Claims: []WorkUnitClaim{{UnitID: "unit-a", Lease: testLease("lease-a", 1)}}}, workflowTestTime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := StartClaimedBatch(claimed, WorkflowBatchCredentials{ExpectedRevision: claimed.Workflow.Revision, Credentials: []WorkUnitCredential{{UnitID: "unit-a", Credential: LeaseCredential{Token: "lease-a", FencingToken: 2}}}}, workflowTestTime.Add(time.Minute)); !errors.Is(err, ErrLeaseMismatch) {
		t.Fatalf("stale fencing error = %v", err)
	}
}

func TestRenewWorkUnitLeasesPreservesCredentialAndExtendsExpiry(t *testing.T) {
	snapshot := startedSnapshot(t, "unit-a", "lease-a")
	renewedExpiry := workflowTestTime.Add(20 * time.Minute)
	renewed, err := RenewWorkUnitLeases(snapshot, RenewWorkUnitLeasesInput{
		ExpectedRevision: snapshot.Workflow.Revision,
		Renewals: []WorkUnitLeaseRenewal{{
			UnitID: "unit-a", Credential: LeaseCredential{Token: "lease-a", FencingToken: 1}, ExpiresAt: renewedExpiry,
		}},
		At: workflowTestTime.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("RenewWorkUnitLeases() error = %v", err)
	}
	unit := renewed.WorkUnits[0]
	if renewed.Workflow.Revision != snapshot.Workflow.Revision+1 || unit.Lease == nil || unit.Lease.Token != "lease-a" || unit.Lease.FencingToken != 1 || !unit.Lease.ExpiresAt.Equal(renewedExpiry) {
		t.Fatalf("renewed snapshot = %+v", renewed)
	}
	if _, err := RenewWorkUnitLeases(snapshot, RenewWorkUnitLeasesInput{ExpectedRevision: snapshot.Workflow.Revision, Renewals: []WorkUnitLeaseRenewal{{UnitID: "unit-a", Credential: LeaseCredential{Token: "wrong", FencingToken: 1}, ExpiresAt: renewedExpiry}}, At: workflowTestTime.Add(2 * time.Minute)}); !errors.Is(err, ErrLeaseMismatch) {
		t.Fatalf("wrong renewal credential error = %v", err)
	}
}

func TestReconcileExpiredLeaseRetriesReadOnlyExecutionWithHistoricalAction(t *testing.T) {
	snapshot := startedSnapshot(t, "unit-a", "lease-a")
	input := actionInput(snapshot, "action-a", "unit-a", "lease-a")
	input.SideEffectClass = SideEffectNone
	input.PreconditionFingerprint = ""
	prepared, err := PrepareAction(snapshot, PrepareActionInput{ExpectedRevision: snapshot.Workflow.Revision, Action: input, At: workflowTestTime.Add(2 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	started, err := StartAction(prepared, "action-a", WorkflowLeaseCredential{ExpectedRevision: prepared.Workflow.Revision, LeaseCredential: LeaseCredential{Token: "lease-a", FencingToken: 1}}, workflowTestTime.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	reconciledAt := workflowTestTime.Add(11 * time.Minute)
	reconciled, err := ReconcileExpiredLeases(started, ReconcileExpiredLeasesInput{ExpectedRevision: started.Workflow.Revision, At: reconciledAt})
	if err != nil {
		t.Fatalf("ReconcileExpiredLeases() error = %v", err)
	}
	unit, action := reconciled.WorkUnits[0], reconciled.Actions[0]
	if unit.Status != DurableWorkUnitStatusPending || unit.Lease != nil || !unit.ClaimedAt.IsZero() || !unit.StartedAt.IsZero() || action.Status != ActionStatusAmbiguous || !action.CompletedAt.Equal(reconciledAt) {
		t.Fatalf("reconciled read-only execution = unit=%+v action=%+v", unit, action)
	}
	reclaimed, err := ClaimReadyBatch(reconciled, WorkflowBatchClaims{ExpectedRevision: reconciled.Workflow.Revision, Claims: []WorkUnitClaim{{UnitID: "unit-a", Lease: DurableLease{OwnerID: "worker-2", Token: "lease-b", FencingToken: 2, ExpiresAt: reconciledAt.Add(time.Minute)}}}}, reconciledAt)
	if err != nil || reclaimed.WorkUnits[0].LastFencingToken != 2 {
		t.Fatalf("reclaim after reconciliation = snapshot=%+v error=%v", reclaimed, err)
	}
}

func TestReconcileExpiredLeaseStopsAmbiguousMutatingExecution(t *testing.T) {
	snapshot := startedSnapshot(t, "unit-a", "lease-a")
	prepared, err := PrepareAction(snapshot, PrepareActionInput{ExpectedRevision: snapshot.Workflow.Revision, Action: actionInput(snapshot, "action-a", "unit-a", "lease-a"), At: workflowTestTime.Add(2 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	started, err := StartAction(prepared, "action-a", WorkflowLeaseCredential{ExpectedRevision: prepared.Workflow.Revision, LeaseCredential: LeaseCredential{Token: "lease-a", FencingToken: 1}}, workflowTestTime.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	reconciledAt := workflowTestTime.Add(11 * time.Minute)
	reconciled, err := ReconcileExpiredLeases(started, ReconcileExpiredLeasesInput{ExpectedRevision: started.Workflow.Revision, At: reconciledAt})
	if err != nil {
		t.Fatalf("ReconcileExpiredLeases() error = %v", err)
	}
	unit, action := reconciled.WorkUnits[0], reconciled.Actions[0]
	if unit.Status != DurableWorkUnitStatusNeedsAttention || unit.Lease != nil || unit.Outcome == nil || len(unit.Outcome.ActionIDs) != 1 || action.Status != ActionStatusAmbiguous {
		t.Fatalf("reconciled mutating execution = unit=%+v action=%+v", unit, action)
	}
}

func TestReconcileExpiredLeaseAbandonsPreparedActionBeforeRetry(t *testing.T) {
	snapshot := startedSnapshot(t, "unit-a", "lease-a")
	prepared, err := PrepareAction(snapshot, PrepareActionInput{ExpectedRevision: snapshot.Workflow.Revision, Action: actionInput(snapshot, "action-a", "unit-a", "lease-a"), At: workflowTestTime.Add(2 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	reconciledAt := workflowTestTime.Add(11 * time.Minute)
	reconciled, err := ReconcileExpiredLeases(prepared, ReconcileExpiredLeasesInput{ExpectedRevision: prepared.Workflow.Revision, At: reconciledAt})
	if err != nil {
		t.Fatalf("ReconcileExpiredLeases() error = %v", err)
	}
	if reconciled.WorkUnits[0].Status != DurableWorkUnitStatusPending || reconciled.Actions[0].Status != ActionStatusAbandoned || !reconciled.Actions[0].StartedAt.IsZero() {
		t.Fatalf("prepared action reconciliation = unit=%+v action=%+v", reconciled.WorkUnits[0], reconciled.Actions[0])
	}
}

func TestFinishUnitAppendsUnitsAndPreservesFrozenFields(t *testing.T) {
	snapshot := startedSnapshot(t, "unit-a", "lease-a")
	newUnit := testUnit(t, snapshot.Workflow.ID, "unit-b")
	newUnit.Task = "verify the work"
	newUnit.InputArtifactRefs = nil
	finished, err := FinishUnit(snapshot, FinishUnitInput{ExpectedRevision: snapshot.Workflow.Revision, UnitID: "unit-a", Status: DurableWorkUnitStatusSucceeded, Outcome: DurableWorkUnitOutcome{ArtifactRefs: []ArtifactReference{{ID: "unit-result", Kind: "test_report"}}}, NewUnits: []DurableWorkUnit{newUnit}, Artifacts: []RunArtifact{testReportArtifact("unit-result")}, Credential: LeaseCredential{Token: "lease-a", FencingToken: 1}, At: workflowTestTime.Add(2 * time.Minute)})
	if err != nil {
		t.Fatalf("FinishUnit() error = %v", err)
	}
	newUnit.Task = "changed"
	if finished.Workflow.Revision != snapshot.Workflow.Revision+1 || !finished.Workflow.UpdatedAt.Equal(workflowTestTime.Add(2*time.Minute)) || len(finished.WorkUnits) != 2 || len(finished.Workflow.WorkUnitIDs) != 2 || finished.WorkUnits[0].Task != snapshot.WorkUnits[0].Task || finished.WorkUnits[1].Task != "verify the work" || finished.WorkUnits[0].Status != DurableWorkUnitStatusSucceeded || !finished.WorkUnits[0].CompletedAt.Equal(workflowTestTime.Add(2*time.Minute)) || finished.WorkUnits[0].Outcome.ArtifactRefs[0].ID != "unit-result" || finished.Artifacts[len(finished.Artifacts)-1].ID != "unit-result" {
		t.Fatalf("finished snapshot = %+v", finished)
	}
}

func TestSettleWorkflowRollsUpTerminalStatuses(t *testing.T) {
	cases := []struct {
		name  string
		left  DurableWorkUnitStatus
		right DurableWorkUnitStatus
		want  WorkflowStatus
	}{
		{name: "needs attention wins", left: DurableWorkUnitStatusFailed, right: DurableWorkUnitStatusNeedsAttention, want: WorkflowStatusNeedsAttention},
		{name: "failure wins", left: DurableWorkUnitStatusFailed, right: DurableWorkUnitStatusSkipped, want: WorkflowStatusFailed},
		{name: "success permits skipped", left: DurableWorkUnitStatusSucceeded, right: DurableWorkUnitStatusSkipped, want: WorkflowStatusSucceeded},
		{name: "blocked rolls up as failed", left: DurableWorkUnitStatusSucceeded, right: DurableWorkUnitStatusBlocked, want: WorkflowStatusFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := testSnapshot(t, "unit-a", "unit-b")
			snapshot.Workflow.Status = WorkflowStatusRunning
			setTerminalTestUnit(&snapshot.WorkUnits[0], tc.left)
			setTerminalTestUnit(&snapshot.WorkUnits[1], tc.right)
			if tc.right == DurableWorkUnitStatusBlocked {
				snapshot.WorkUnits[1].Dependencies = []DurableWorkUnitID{snapshot.WorkUnits[0].ID}
				setExecutedTerminalTestUnit(&snapshot.WorkUnits[0], DurableWorkUnitStatusFailed)
				snapshot.WorkUnits[0].Outcome.Reason = "dependency failed"
			}
			settled, err := SettleWorkflow(snapshot, SettleWorkflowInput{ExpectedRevision: 1, FinalOutcomeRefs: []ArtifactReference{{ID: "final-1", Kind: "final_response"}}, Artifacts: []RunArtifact{{ID: "final-1"}}, At: workflowTestTime.Add(4 * time.Minute)})
			if err != nil {
				t.Fatalf("SettleWorkflow() error = %v", err)
			}
			if settled.Workflow.Revision != 2 || settled.Workflow.Status != tc.want || !settled.Workflow.UpdatedAt.Equal(workflowTestTime.Add(4*time.Minute)) || !settled.Workflow.CompletedAt.Equal(workflowTestTime.Add(4*time.Minute)) || settled.Workflow.FinalOutcomeRefs[0].ID != "final-1" || settled.Artifacts[len(settled.Artifacts)-1].ID != "final-1" {
				t.Fatalf("settled snapshot = %+v", settled)
			}
		})
	}
	snapshot := testSnapshot(t, "unit-a")
	if _, err := SettleWorkflow(snapshot, SettleWorkflowInput{ExpectedRevision: 1}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("nonterminal settle error = %v", err)
	}
}

func TestFinishSkippedUnitRequiresCompletionTime(t *testing.T) {
	snapshot := testSnapshot(t, "unit-a")
	snapshot.Workflow.Status = WorkflowStatusRunning
	input := FinishUnitInput{
		ExpectedRevision: snapshot.Workflow.Revision,
		UnitID:           "unit-a",
		Status:           DurableWorkUnitStatusSkipped,
		Outcome:          DurableWorkUnitOutcome{Reason: "optional work is not applicable"},
	}
	if _, err := FinishUnit(snapshot, input); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("FinishUnit() without time error = %v", err)
	}
	input.At = workflowTestTime.Add(time.Minute)
	skipped, err := FinishUnit(snapshot, input)
	if err != nil {
		t.Fatalf("FinishUnit() error = %v", err)
	}
	unit := skipped.WorkUnits[0]
	if !unit.ClaimedAt.IsZero() || !unit.StartedAt.IsZero() || !unit.CompletedAt.Equal(input.At) || !skipped.Workflow.UpdatedAt.Equal(input.At) {
		t.Fatalf("skipped lifecycle timestamps = unit=%+v workflow=%+v", unit, skipped.Workflow)
	}
}

func testSnapshot(t *testing.T, ids ...DurableWorkUnitID) WorkflowSnapshot {
	t.Helper()
	workflow, err := NewWorkflow(WorkflowInput{ID: "workflow-1", Conversation: ConversationReference{ConversationID: "conversation-1", TurnID: "turn-1"}, RootGoal: "durable runtime", CreatedAt: workflowTestTime, WorkUnitIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := WorkflowSnapshot{Workflow: workflow}
	for _, id := range ids {
		snapshot.WorkUnits = append(snapshot.WorkUnits, testUnit(t, workflow.ID, id))
		snapshot.Artifacts = append(snapshot.Artifacts, RunArtifact{ID: "input-" + string(id)})
	}
	return snapshot
}

func testUnit(t *testing.T, workflowID WorkflowID, id DurableWorkUnitID) DurableWorkUnit {
	t.Helper()
	unit, err := NewDurableWorkUnit(DurableWorkUnitInput{ID: id, WorkflowID: workflowID, Kind: "execute", Phase: RunPhaseExecute, Role: "worker", Task: "perform durable work", Source: "worker", SourceLimit: 1, SideEffectClass: SideEffectWorkspace, DuplicateKey: "unique:" + string(id), InputArtifactRefs: []ArtifactReference{{ID: "input-" + string(id), Kind: "repo_map"}}, ReadSet: []string{"internal/domain"}, WriteSet: []string{"internal/domain/workflow.go"}})
	if err != nil {
		t.Fatal(err)
	}
	return unit
}

func startedSnapshot(t *testing.T, unitID DurableWorkUnitID, token LeaseToken) WorkflowSnapshot {
	t.Helper()
	snapshot := testSnapshot(t, unitID)
	claimed, err := ClaimReadyBatch(snapshot, WorkflowBatchClaims{ExpectedRevision: snapshot.Workflow.Revision, Claims: []WorkUnitClaim{{UnitID: unitID, Lease: testLease(token, 1)}}}, workflowTestTime)
	if err != nil {
		t.Fatal(err)
	}
	started, err := StartClaimedBatch(claimed, WorkflowBatchCredentials{ExpectedRevision: claimed.Workflow.Revision, Credentials: []WorkUnitCredential{{UnitID: unitID, Credential: LeaseCredential{Token: token, FencingToken: 1}}}}, workflowTestTime.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return started
}

func actionInput(snapshot WorkflowSnapshot, actionID ActionID, unitID DurableWorkUnitID, token LeaseToken) DurableActionInput {
	return DurableActionInput{ID: actionID, WorkflowID: snapshot.Workflow.ID, WorkUnitID: unitID, Attempt: 1, Kind: "tool", Target: "write", IdempotencyKey: "key-" + string(actionID), Lease: LeaseCredential{Token: token, FencingToken: 1}, SideEffectClass: SideEffectWorkspace, PreconditionFingerprint: "precondition"}
}

func testLease(token LeaseToken, fencing uint64) DurableLease {
	return DurableLease{OwnerID: "worker-1", Token: token, FencingToken: fencing, ExpiresAt: workflowTestTime.Add(10 * time.Minute)}
}

func cloneLeasePtr(lease DurableLease) *DurableLease {
	return &lease
}

func testCredential() LeaseCredential {
	return LeaseCredential{Token: "lease-1", FencingToken: 1}
}

func testReportArtifact(id string) RunArtifact {
	return RunArtifact{ID: id, Kind: "test_report", SchemaVersion: "test_report.v1", Payload: json.RawMessage(`{"attempt":1}`)}
}

func terminalTestOutcome(status DurableWorkUnitStatus) *DurableWorkUnitOutcome {
	outcome := &DurableWorkUnitOutcome{}
	if status != DurableWorkUnitStatusSucceeded {
		outcome.Reason = "terminal reason"
	}
	return outcome
}

func setTerminalTestUnit(unit *DurableWorkUnit, status DurableWorkUnitStatus) {
	if status == DurableWorkUnitStatusSkipped || status == DurableWorkUnitStatusBlocked {
		unit.Status = status
		unit.ClaimedAt = time.Time{}
		unit.StartedAt = time.Time{}
		unit.CompletedAt = workflowTestTime.Add(3 * time.Minute)
		unit.Outcome = terminalTestOutcome(status)
		return
	}
	setExecutedTerminalTestUnit(unit, status)
}

func setExecutedTerminalTestUnit(unit *DurableWorkUnit, status DurableWorkUnitStatus) {
	unit.Status = status
	unit.ClaimedAt = workflowTestTime
	unit.StartedAt = workflowTestTime.Add(time.Minute)
	unit.CompletedAt = workflowTestTime.Add(2 * time.Minute)
	unit.Outcome = terminalTestOutcome(status)
}
