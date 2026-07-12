package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestClaimReadyBatchRejectsDuplicateUnitAndLease(t *testing.T) {
	snapshot := testSnapshot(t, "unit-a", "unit-b")
	cases := []struct {
		name   string
		claims []WorkUnitClaim
	}{
		{name: "duplicate unit", claims: []WorkUnitClaim{{UnitID: "unit-a", Lease: testLease("lease-a", 1)}, {UnitID: "unit-a", Lease: testLease("lease-b", 1)}}},
		{name: "duplicate lease", claims: []WorkUnitClaim{{UnitID: "unit-a", Lease: testLease("lease-a", 1)}, {UnitID: "unit-b", Lease: testLease("lease-a", 1)}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ClaimReadyBatch(snapshot, WorkflowBatchClaims{ExpectedRevision: snapshot.Workflow.Revision, Claims: tc.claims}, workflowTestTime); !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("ClaimReadyBatch() error = %v", err)
			}
		})
	}
}

func TestBlockWorkUnitsAcceptsUnorderedTransitiveClosureInOneRevision(t *testing.T) {
	snapshot := blockingClosureSnapshot(t)
	blockedClosure, err := BlockWorkUnits(snapshot, BlockWorkUnitsInput{
		ExpectedRevision: snapshot.Workflow.Revision,
		Blocks: []WorkUnitBlock{
			{UnitID: "grandchild", Reason: "blocked through declared closure"},
			{UnitID: "child", Reason: "blocked by failed dependency"},
		},
		At: workflowTestTime.Add(3 * time.Minute),
	})
	if err != nil {
		t.Fatalf("BlockWorkUnits() closure error = %v", err)
	}
	if blockedClosure.Workflow.Revision != snapshot.Workflow.Revision+1 || blockedClosure.WorkUnits[1].Status != DurableWorkUnitStatusBlocked || blockedClosure.WorkUnits[2].Status != DurableWorkUnitStatusBlocked {
		t.Fatalf("BlockWorkUnits() closure = %+v", blockedClosure)
	}
}

func TestBlockWorkUnitsPropagatesDependencyFailuresInStages(t *testing.T) {
	snapshot := blockingClosureSnapshot(t)
	blockedChild, err := BlockWorkUnits(snapshot, BlockWorkUnitsInput{
		ExpectedRevision: snapshot.Workflow.Revision,
		Blocks:           []WorkUnitBlock{{UnitID: "child", Reason: "blocked by failed dependency"}},
		At:               workflowTestTime.Add(3 * time.Minute),
	})
	if err != nil {
		t.Fatalf("BlockWorkUnits() child error = %v", err)
	}
	child := blockedChild.WorkUnits[1]
	if child.Status != DurableWorkUnitStatusBlocked || child.Lease != nil || !child.ClaimedAt.IsZero() || !child.StartedAt.IsZero() || !child.CompletedAt.Equal(workflowTestTime.Add(3*time.Minute)) || child.Outcome == nil || child.Outcome.Reason == "" {
		t.Fatalf("blocked child = %+v", child)
	}
	if _, err := ClaimReadyBatch(blockedChild, WorkflowBatchClaims{ExpectedRevision: blockedChild.Workflow.Revision, Claims: []WorkUnitClaim{{UnitID: "grandchild", Lease: testLease("lease-grandchild", 1)}}}, workflowTestTime.Add(4*time.Minute)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("ClaimReadyBatch() blocked dependency error = %v", err)
	}

	blockedGrandchild, err := BlockWorkUnits(blockedChild, BlockWorkUnitsInput{
		ExpectedRevision: blockedChild.Workflow.Revision,
		Blocks:           []WorkUnitBlock{{UnitID: "grandchild", Reason: "blocked by blocked dependency"}},
		At:               workflowTestTime.Add(4 * time.Minute),
	})
	if err != nil {
		t.Fatalf("BlockWorkUnits() grandchild error = %v", err)
	}
	if blockedGrandchild.WorkUnits[2].Status != DurableWorkUnitStatusBlocked || blockedGrandchild.Workflow.Revision != snapshot.Workflow.Revision+2 {
		t.Fatalf("blocked grandchild snapshot = %+v", blockedGrandchild)
	}
}

func blockingClosureSnapshot(t *testing.T) WorkflowSnapshot {
	t.Helper()
	snapshot := testSnapshot(t, "failed", "child", "grandchild")
	snapshot.Workflow.Status = WorkflowStatusRunning
	snapshot.WorkUnits[1].Dependencies = []DurableWorkUnitID{"failed"}
	snapshot.WorkUnits[2].Dependencies = []DurableWorkUnitID{"child"}
	setExecutedTerminalTestUnit(&snapshot.WorkUnits[0], DurableWorkUnitStatusFailed)
	snapshot.WorkUnits[0].Outcome.Reason = "dependency failed"
	return snapshot
}

func TestBlockWorkUnitsAtomicallyUsesOneRevision(t *testing.T) {
	snapshot := blockableSnapshot(t)
	blocked, err := BlockWorkUnits(snapshot, BlockWorkUnitsInput{
		ExpectedRevision: snapshot.Workflow.Revision,
		Blocks: []WorkUnitBlock{
			{UnitID: "child-a", Reason: "blocked by failed dependency"},
			{UnitID: "child-b", Reason: "blocked by unresolved dependency"},
		},
		At: workflowTestTime.Add(3 * time.Minute),
	})
	if err != nil {
		t.Fatalf("BlockWorkUnits() error = %v", err)
	}
	if blocked.Workflow.Revision != snapshot.Workflow.Revision+1 || blocked.WorkUnits[2].Status != DurableWorkUnitStatusBlocked || blocked.WorkUnits[3].Status != DurableWorkUnitStatusBlocked {
		t.Fatalf("blocked batch = %+v", blocked)
	}
}

func TestBlockWorkUnitsRejectsInvalidBatchesWithoutMutation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*WorkflowSnapshot)
		input  func(WorkflowSnapshot) BlockWorkUnitsInput
	}{
		{name: "empty", input: func(snapshot WorkflowSnapshot) BlockWorkUnitsInput {
			return BlockWorkUnitsInput{ExpectedRevision: snapshot.Workflow.Revision, At: workflowTestTime.Add(3 * time.Minute)}
		}},
		{name: "duplicate", input: func(snapshot WorkflowSnapshot) BlockWorkUnitsInput {
			return BlockWorkUnitsInput{ExpectedRevision: snapshot.Workflow.Revision, Blocks: []WorkUnitBlock{{UnitID: "child-a", Reason: "one"}, {UnitID: "child-a", Reason: "two"}}, At: workflowTestTime.Add(3 * time.Minute)}
		}},
		{name: "empty reason", input: func(snapshot WorkflowSnapshot) BlockWorkUnitsInput {
			return BlockWorkUnitsInput{ExpectedRevision: snapshot.Workflow.Revision, Blocks: []WorkUnitBlock{{UnitID: "child-a", Reason: " "}}, At: workflowTestTime.Add(3 * time.Minute)}
		}},
		{name: "time reversal", input: func(snapshot WorkflowSnapshot) BlockWorkUnitsInput {
			return BlockWorkUnitsInput{ExpectedRevision: snapshot.Workflow.Revision, Blocks: []WorkUnitBlock{{UnitID: "child-a", Reason: "blocked"}}, At: workflowTestTime.Add(-time.Minute)}
		}},
		{name: "stale revision", input: func(snapshot WorkflowSnapshot) BlockWorkUnitsInput {
			return BlockWorkUnitsInput{ExpectedRevision: snapshot.Workflow.Revision - 1, Blocks: []WorkUnitBlock{{UnitID: "child-a", Reason: "blocked"}}, At: workflowTestTime.Add(3 * time.Minute)}
		}},
		{name: "no direct failed dependency", mutate: func(snapshot *WorkflowSnapshot) {
			snapshot.WorkUnits[2].Dependencies = nil
		}, input: func(snapshot WorkflowSnapshot) BlockWorkUnitsInput {
			return BlockWorkUnitsInput{ExpectedRevision: snapshot.Workflow.Revision, Blocks: []WorkUnitBlock{{UnitID: "child-a", Reason: "blocked"}}, At: workflowTestTime.Add(3 * time.Minute)}
		}},
		{name: "unrelated pending unit mixed into closure", mutate: func(snapshot *WorkflowSnapshot) {
			snapshot.WorkUnits[3].Dependencies = nil
		}, input: func(snapshot WorkflowSnapshot) BlockWorkUnitsInput {
			return BlockWorkUnitsInput{ExpectedRevision: snapshot.Workflow.Revision, Blocks: []WorkUnitBlock{{UnitID: "child-a", Reason: "blocked"}, {UnitID: "child-b", Reason: "unrelated"}}, At: workflowTestTime.Add(3 * time.Minute)}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := blockableSnapshot(t)
			if test.mutate != nil {
				test.mutate(&snapshot)
			}
			beforeRevision := snapshot.Workflow.Revision
			_, err := BlockWorkUnits(snapshot, test.input(snapshot))
			if test.name == "stale revision" {
				if !errors.Is(err, ErrStaleWorkflowRevision) {
					t.Fatalf("BlockWorkUnits() error = %v", err)
				}
			} else if !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("BlockWorkUnits() error = %v", err)
			}
			if snapshot.Workflow.Revision != beforeRevision || snapshot.WorkUnits[2].Status != DurableWorkUnitStatusPending {
				t.Fatalf("BlockWorkUnits() mutated rejected snapshot: %+v", snapshot)
			}
		})
	}
}

func blockableSnapshot(t *testing.T) WorkflowSnapshot {
	t.Helper()
	snapshot := testSnapshot(t, "failed-a", "failed-b", "child-a", "child-b")
	snapshot.Workflow.Status = WorkflowStatusRunning
	setExecutedTerminalTestUnit(&snapshot.WorkUnits[0], DurableWorkUnitStatusFailed)
	setExecutedTerminalTestUnit(&snapshot.WorkUnits[1], DurableWorkUnitStatusNeedsAttention)
	snapshot.WorkUnits[0].Outcome.Reason = "dependency failed"
	snapshot.WorkUnits[1].Outcome.Reason = "dependency needs attention"
	snapshot.WorkUnits[2].Dependencies = []DurableWorkUnitID{"failed-a"}
	snapshot.WorkUnits[3].Dependencies = []DurableWorkUnitID{"failed-b"}
	return snapshot
}

func TestPrepareActionRejectsExpiredLease(t *testing.T) {
	snapshot := startedSnapshot(t, "unit-a", "lease-a")
	_, err := PrepareAction(snapshot, PrepareActionInput{
		ExpectedRevision: snapshot.Workflow.Revision,
		Action:           actionInput(snapshot, "action-a", "unit-a", "lease-a"),
		At:               snapshot.WorkUnits[0].Lease.ExpiresAt,
	})
	if !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("PrepareAction() error = %v, want %v", err, ErrLeaseExpired)
	}
	if len(snapshot.Actions) != 0 {
		t.Fatalf("PrepareAction() mutated input snapshot: %+v", snapshot.Actions)
	}
}

func TestPrepareActionRejectsDuplicateActionIDAndIdempotencyKeySeparately(t *testing.T) {
	snapshot := startedSnapshot(t, "unit-a", "lease-a")
	first := actionInput(snapshot, "action-a", "unit-a", "lease-a")
	prepared, err := PrepareAction(snapshot, PrepareActionInput{
		ExpectedRevision: snapshot.Workflow.Revision,
		Action:           first,
		At:               workflowTestTime.Add(90 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}

	duplicateID := first
	duplicateID.IdempotencyKey = "another-key"
	if _, err := PrepareAction(prepared, PrepareActionInput{
		ExpectedRevision: prepared.Workflow.Revision,
		Action:           duplicateID,
		At:               workflowTestTime.Add(2 * time.Minute),
	}); !errors.Is(err, ErrInvalidTransition) || !strings.Contains(err.Error(), "duplicate action id") {
		t.Fatalf("duplicate action id error = %v", err)
	}

	duplicateKey := first
	duplicateKey.ID = "action-b"
	if _, err := PrepareAction(prepared, PrepareActionInput{
		ExpectedRevision: prepared.Workflow.Revision,
		Action:           duplicateKey,
		At:               workflowTestTime.Add(2 * time.Minute),
	}); !errors.Is(err, ErrInvalidTransition) || !strings.Contains(err.Error(), "duplicate action idempotency key") {
		t.Fatalf("duplicate idempotency key error = %v", err)
	}
}

func TestFinishActionAtomicallyRecordsTerminalEvidence(t *testing.T) {
	tests := []struct {
		status ActionStatus
		reason string
	}{
		{status: ActionStatusSucceeded},
		{status: ActionStatusFailed, reason: "provider returned an error"},
		{status: ActionStatusAmbiguous, reason: "postcondition could not be proven"},
	}
	for _, test := range tests {
		t.Run(string(test.status), func(t *testing.T) {
			snapshot := executingActionSnapshot(t)
			artifactID := "evidence-" + string(test.status)
			completion := ActionCompletion{
				Status: test.status,
				Reason: test.reason,
				Result: DurableActionResult{
					ResultArtifactRefs:       []ArtifactReference{{ID: artifactID, Kind: "test_report"}},
					ExecutionRef:             "execution-" + string(test.status),
					MutationRefs:             []string{"mutation-" + string(test.status)},
					PostconditionFingerprint: "postcondition-" + string(test.status),
				},
				Artifacts: []RunArtifact{testReportArtifact(artifactID)},
			}
			completion.Artifacts[0].References = []ArtifactReference{{ID: "input-unit-a"}}
			finished, err := FinishAction(snapshot, "action-a", completion, WorkflowLeaseCredential{ExpectedRevision: snapshot.Workflow.Revision, LeaseCredential: LeaseCredential{Token: "lease-a", FencingToken: 1}}, workflowTestTime.Add(3*time.Minute))
			if err != nil {
				t.Fatalf("FinishAction() error = %v", err)
			}
			completion.Result.ResultArtifactRefs[0].ID = "changed"
			completion.Result.MutationRefs[0] = "changed"
			completion.Artifacts[0].Payload[0] = 'x'
			completion.Artifacts[0].References[0].ID = "changed"
			action := finished.Actions[0]
			artifact := finished.Artifacts[len(finished.Artifacts)-1]
			if finished.Workflow.Revision != snapshot.Workflow.Revision+1 || action.Status != test.status || action.Reason != test.reason || action.ResultArtifactRefs[0].ID != artifactID || action.ExecutionRef != "execution-"+string(test.status) || action.MutationRefs[0] != "mutation-"+string(test.status) || action.PostconditionFingerprint != "postcondition-"+string(test.status) || artifact.ID != artifactID || string(artifact.Payload) != `{"attempt":1}` || artifact.References[0].ID != "input-unit-a" {
				t.Fatalf("finished evidence = action=%+v artifact=%+v", action, artifact)
			}
			if test.status == ActionStatusAmbiguous && !errors.Is(ValidateActionRetry(action), ErrAmbiguousMutatingAction) {
				t.Fatalf("ValidateActionRetry() error = %v", ValidateActionRetry(action))
			}
		})
	}
}

func TestFinishActionRequiresSuccessEvidenceForMutatingActions(t *testing.T) {
	tests := []struct {
		name   string
		status ActionStatus
		reason string
		result DurableActionResult
		wantOK bool
	}{
		{name: "missing postcondition", status: ActionStatusSucceeded, result: DurableActionResult{ResultArtifactRefs: []ArtifactReference{{ID: "result", Kind: "test_report"}}}},
		{name: "missing result artifacts", status: ActionStatusSucceeded, result: DurableActionResult{PostconditionFingerprint: "postcondition"}},
		{name: "failed retains optional evidence", status: ActionStatusFailed, reason: "provider failed", wantOK: true},
		{name: "ambiguous retains optional evidence", status: ActionStatusAmbiguous, reason: "postcondition unknown", wantOK: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := executingActionSnapshot(t)
			_, err := FinishAction(snapshot, "action-a", ActionCompletion{Status: test.status, Reason: test.reason, Result: test.result}, WorkflowLeaseCredential{ExpectedRevision: snapshot.Workflow.Revision, LeaseCredential: LeaseCredential{Token: "lease-a", FencingToken: 1}}, workflowTestTime.Add(3*time.Minute))
			if test.wantOK {
				if err != nil {
					t.Fatalf("FinishAction() error = %v", err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("FinishAction() error = %v", err)
			}
		})
	}
}

func TestTerminalTransitionsRejectDuplicateAndInvalidArtifacts(t *testing.T) {
	tests := []struct {
		name      string
		artifacts []RunArtifact
	}{
		{name: "existing id collision", artifacts: []RunArtifact{{ID: "input-unit-a"}}},
		{name: "batch id collision", artifacts: []RunArtifact{{ID: "new-artifact"}, {ID: "new-artifact"}}},
		{name: "empty id", artifacts: []RunArtifact{{ID: " "}}},
		{name: "invalid typed payload", artifacts: []RunArtifact{{ID: "invalid", Kind: "execution", SchemaVersion: "execution.v1", Payload: []byte(`{}`)}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := executingActionSnapshot(t)
			_, err := FinishAction(snapshot, "action-a", ActionCompletion{Status: ActionStatusSucceeded, Artifacts: test.artifacts}, WorkflowLeaseCredential{ExpectedRevision: snapshot.Workflow.Revision, LeaseCredential: LeaseCredential{Token: "lease-a", FencingToken: 1}}, workflowTestTime.Add(3*time.Minute))
			if !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("FinishAction() error = %v", err)
			}
			if snapshot.Actions[0].Status != ActionStatusExecuting || len(snapshot.Artifacts) != 1 {
				t.Fatalf("FinishAction() mutated rejected snapshot: %+v", snapshot)
			}
		})
	}
}

func TestFinishUnitRequiresTerminalOwnedActionsAndDeepCopiesSnapshot(t *testing.T) {
	snapshot := startedSnapshot(t, "unit-a", "lease-a")
	prepared, err := PrepareAction(snapshot, PrepareActionInput{ExpectedRevision: snapshot.Workflow.Revision, Action: actionInput(snapshot, "action-a", "unit-a", "lease-a"), At: workflowTestTime.Add(90 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	started, err := StartAction(prepared, "action-a", WorkflowLeaseCredential{ExpectedRevision: prepared.Workflow.Revision, LeaseCredential: LeaseCredential{Token: "lease-a", FencingToken: 1}}, workflowTestTime.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	finishedAction, err := FinishAction(started, "action-a", ActionCompletion{Status: ActionStatusSucceeded, Result: DurableActionResult{ResultArtifactRefs: []ArtifactReference{{ID: "action-result", Kind: "test_report"}}, PostconditionFingerprint: "postcondition"}, Artifacts: []RunArtifact{testReportArtifact("action-result")}}, WorkflowLeaseCredential{ExpectedRevision: started.Workflow.Revision, LeaseCredential: LeaseCredential{Token: "lease-a", FencingToken: 1}}, workflowTestTime.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	finishedAction.WorkUnits[0].ReadSet[0] = "mutated-input"
	if started.WorkUnits[0].ReadSet[0] != "internal/domain" {
		t.Fatalf("FinishAction() did not deep-copy snapshot: %+v", started.WorkUnits[0])
	}
	if _, err := FinishUnit(finishedAction, FinishUnitInput{ExpectedRevision: finishedAction.Workflow.Revision, UnitID: "unit-a", Status: DurableWorkUnitStatusSucceeded, Outcome: DurableWorkUnitOutcome{ActionIDs: []ActionID{"missing"}}, Credential: LeaseCredential{Token: "lease-a", FencingToken: 1}, At: workflowTestTime.Add(4 * time.Minute)}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("FinishUnit() missing action error = %v", err)
	}
	other := testUnit(t, finishedAction.Workflow.ID, "unit-b")
	other.InputArtifactRefs = nil
	other.Status = DurableWorkUnitStatusExecuting
	other.ClaimedAt = workflowTestTime
	other.StartedAt = workflowTestTime.Add(time.Minute)
	other.Lease = cloneLease(finishedAction.WorkUnits[0].Lease)
	other.LastFencingToken = other.Lease.FencingToken
	finishedAction.WorkUnits = append(finishedAction.WorkUnits, other)
	finishedAction.Workflow.WorkUnitIDs = append(finishedAction.Workflow.WorkUnitIDs, other.ID)
	finishedAction.Actions[0].WorkUnitID = "unit-b"
	if _, err := FinishUnit(finishedAction, FinishUnitInput{ExpectedRevision: finishedAction.Workflow.Revision, UnitID: "unit-a", Status: DurableWorkUnitStatusSucceeded, Outcome: DurableWorkUnitOutcome{ActionIDs: []ActionID{"action-a"}}, Credential: LeaseCredential{Token: "lease-a", FencingToken: 1}, At: workflowTestTime.Add(4 * time.Minute)}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("FinishUnit() invalid aggregate error = %v", err)
	}
}

func executingActionSnapshot(t *testing.T) WorkflowSnapshot {
	t.Helper()
	snapshot := startedSnapshot(t, "unit-a", "lease-a")
	prepared, err := PrepareAction(snapshot, PrepareActionInput{ExpectedRevision: snapshot.Workflow.Revision, Action: actionInput(snapshot, "action-a", "unit-a", "lease-a"), At: workflowTestTime.Add(90 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	started, err := StartAction(prepared, "action-a", WorkflowLeaseCredential{ExpectedRevision: prepared.Workflow.Revision, LeaseCredential: LeaseCredential{Token: "lease-a", FencingToken: 1}}, workflowTestTime.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return started
}
