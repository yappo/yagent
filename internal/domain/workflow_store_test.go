package domain

import (
	"fmt"
	"testing"
	"time"
)

var workflowStoreTestTime = time.Date(2026, time.July, 10, 15, 0, 0, 0, time.UTC)

func TestValidateWorkflowSnapshotAcceptsAggregateStates(t *testing.T) {
	cases := []struct {
		name     string
		snapshot func(t *testing.T) WorkflowSnapshot
	}{
		{
			name: "pending units without actions",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				return workflowStoreSnapshot(t, WorkflowStatusPending, DurableWorkUnitStatusPending, DurableWorkUnitStatusPending)
			},
		},
		{
			name: "running permits mixed states while awaiting settlement",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				snapshot := workflowStoreSnapshot(t, WorkflowStatusRunning,
					DurableWorkUnitStatusPending,
					DurableWorkUnitStatusLeased,
					DurableWorkUnitStatusExecuting,
					DurableWorkUnitStatusSucceeded,
					DurableWorkUnitStatusSkipped,
					DurableWorkUnitStatusBlocked,
					DurableWorkUnitStatusFailed,
					DurableWorkUnitStatusNeedsAttention,
				)
				snapshot.WorkUnits[5].Dependencies = []DurableWorkUnitID{snapshot.WorkUnits[6].ID}
				return snapshot
			},
		},
		{
			name: "running executing unit permits active and terminal actions",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				snapshot := workflowStoreSnapshot(t, WorkflowStatusRunning, DurableWorkUnitStatusExecuting)
				snapshot.Actions = []DurableAction{
					workflowStoreAction(t, &snapshot, 0, "prepared", ActionStatusPrepared),
					workflowStoreAction(t, &snapshot, 0, "executing", ActionStatusExecuting),
					workflowStoreAction(t, &snapshot, 0, "succeeded", ActionStatusSucceeded),
				}
				return snapshot
			},
		},
		{
			name: "running terminal unit covers terminal actions",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				snapshot := workflowStoreSnapshot(t, WorkflowStatusRunning, DurableWorkUnitStatusSucceeded)
				action := workflowStoreAction(t, &snapshot, 0, "succeeded", ActionStatusSucceeded)
				snapshot.Actions = []DurableAction{action}
				snapshot.WorkUnits[0].Outcome.ActionIDs = []ActionID{action.ID}
				return snapshot
			},
		},
		{
			name: "running unit permits skipped dependency",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				snapshot := workflowStoreSnapshot(t, WorkflowStatusRunning, DurableWorkUnitStatusSkipped, DurableWorkUnitStatusExecuting)
				snapshot.WorkUnits[1].Dependencies = []DurableWorkUnitID{snapshot.WorkUnits[0].ID}
				return snapshot
			},
		},
		{
			name: "needs attention has terminal precedence",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				return workflowStoreSnapshot(t, WorkflowStatusNeedsAttention, DurableWorkUnitStatusFailed, DurableWorkUnitStatusNeedsAttention)
			},
		},
		{
			name: "failed follows needs attention precedence",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				return workflowStoreSnapshot(t, WorkflowStatusFailed, DurableWorkUnitStatusFailed, DurableWorkUnitStatusSkipped)
			},
		},
		{
			name: "failed permits blocked dependency chain",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				snapshot := workflowStoreSnapshot(t, WorkflowStatusFailed, DurableWorkUnitStatusFailed, DurableWorkUnitStatusBlocked)
				snapshot.WorkUnits[1].Dependencies = []DurableWorkUnitID{snapshot.WorkUnits[0].ID}
				return snapshot
			},
		},
		{
			name: "succeeded permits succeeded and skipped units",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				return workflowStoreSnapshot(t, WorkflowStatusSucceeded, DurableWorkUnitStatusSucceeded, DurableWorkUnitStatusSkipped)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateWorkflowSnapshot(tc.snapshot(t)); err != nil {
				t.Fatalf("ValidateWorkflowSnapshot() error = %v", err)
			}
		})
	}
}

func TestValidateWorkflowSnapshotRejectsMalformedAggregateStates(t *testing.T) {
	cases := []struct {
		name     string
		snapshot func(t *testing.T) WorkflowSnapshot
	}{
		{
			name: "workflow input references missing artifact",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				snapshot := workflowStoreSnapshot(t, WorkflowStatusPending, DurableWorkUnitStatusPending)
				snapshot.Workflow.InputArtifactRefs = []ArtifactReference{{ID: "missing-input", Kind: "workflow_input"}}
				return snapshot
			},
		},
		{
			name: "pending workflow with nonpending unit",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				return workflowStoreSnapshot(t, WorkflowStatusPending, DurableWorkUnitStatusLeased)
			},
		},
		{
			name: "pending workflow with action",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				snapshot := workflowStoreSnapshot(t, WorkflowStatusPending, DurableWorkUnitStatusExecuting)
				snapshot.Actions = []DurableAction{workflowStoreAction(t, &snapshot, 0, "prepared", ActionStatusPrepared)}
				return snapshot
			},
		},
		{
			name: "work units differ from workflow index order",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				snapshot := workflowStoreSnapshot(t, WorkflowStatusPending, DurableWorkUnitStatusPending, DurableWorkUnitStatusPending)
				snapshot.WorkUnits[0], snapshot.WorkUnits[1] = snapshot.WorkUnits[1], snapshot.WorkUnits[0]
				return snapshot
			},
		},
		{
			name: "terminal workflow with nonterminal unit",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				return workflowStoreSnapshot(t, WorkflowStatusSucceeded, DurableWorkUnitStatusPending)
			},
		},
		{
			name: "needs attention terminal workflow still rejects nonterminal unit",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				return workflowStoreSnapshot(t, WorkflowStatusNeedsAttention, DurableWorkUnitStatusNeedsAttention, DurableWorkUnitStatusPending)
			},
		},
		{
			name: "failed status loses to needs attention",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				return workflowStoreSnapshot(t, WorkflowStatusFailed, DurableWorkUnitStatusFailed, DurableWorkUnitStatusNeedsAttention)
			},
		},
		{
			name: "failed status without failed unit",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				return workflowStoreSnapshot(t, WorkflowStatusFailed, DurableWorkUnitStatusSucceeded, DurableWorkUnitStatusSkipped)
			},
		},
		{
			name: "succeeded status with failed unit",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				return workflowStoreSnapshot(t, WorkflowStatusSucceeded, DurableWorkUnitStatusSucceeded, DurableWorkUnitStatusFailed)
			},
		},
		{
			name: "executing unit has pending dependency",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				snapshot := workflowStoreSnapshot(t, WorkflowStatusRunning, DurableWorkUnitStatusPending, DurableWorkUnitStatusExecuting)
				snapshot.WorkUnits[1].Dependencies = []DurableWorkUnitID{snapshot.WorkUnits[0].ID}
				return snapshot
			},
		},
		{
			name: "succeeded unit has failed dependency",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				snapshot := workflowStoreSnapshot(t, WorkflowStatusRunning, DurableWorkUnitStatusFailed, DurableWorkUnitStatusSucceeded)
				snapshot.WorkUnits[1].Dependencies = []DurableWorkUnitID{snapshot.WorkUnits[0].ID}
				return snapshot
			},
		},
		{
			name: "executing unit has blocked dependency",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				snapshot := workflowStoreSnapshot(t, WorkflowStatusRunning, DurableWorkUnitStatusFailed, DurableWorkUnitStatusBlocked, DurableWorkUnitStatusExecuting)
				snapshot.WorkUnits[1].Dependencies = []DurableWorkUnitID{snapshot.WorkUnits[0].ID}
				snapshot.WorkUnits[2].Dependencies = []DurableWorkUnitID{snapshot.WorkUnits[1].ID}
				return snapshot
			},
		},
		{
			name: "action attempt differs from unit attempt",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				snapshot := workflowStoreSnapshot(t, WorkflowStatusRunning, DurableWorkUnitStatusExecuting)
				action := workflowStoreAction(t, &snapshot, 0, "action", ActionStatusPrepared)
				action.Attempt++
				snapshot.Actions = []DurableAction{action}
				return snapshot
			},
		},
		{
			name: "action fencing differs from unit history",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				snapshot := workflowStoreSnapshot(t, WorkflowStatusRunning, DurableWorkUnitStatusExecuting)
				action := workflowStoreAction(t, &snapshot, 0, "action", ActionStatusPrepared)
				action.FencingToken++
				snapshot.Actions = []DurableAction{action}
				return snapshot
			},
		},
		{
			name: "actions reuse idempotency key",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				snapshot := workflowStoreSnapshot(t, WorkflowStatusRunning, DurableWorkUnitStatusExecuting)
				first := workflowStoreAction(t, &snapshot, 0, "action-a", ActionStatusPrepared)
				second := workflowStoreAction(t, &snapshot, 0, "action-b", ActionStatusPrepared)
				second.IdempotencyKey = first.IdempotencyKey
				snapshot.Actions = []DurableAction{first, second}
				return snapshot
			},
		},
		{
			name: "prepared action belongs to leased unit",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				snapshot := workflowStoreSnapshot(t, WorkflowStatusRunning, DurableWorkUnitStatusLeased)
				snapshot.Actions = []DurableAction{workflowStoreAction(t, &snapshot, 0, "action", ActionStatusPrepared)}
				return snapshot
			},
		},
		{
			name: "active action lease token differs",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				snapshot := workflowStoreSnapshot(t, WorkflowStatusRunning, DurableWorkUnitStatusExecuting)
				action := workflowStoreAction(t, &snapshot, 0, "action", ActionStatusExecuting)
				action.LeaseToken = "other-lease"
				snapshot.Actions = []DurableAction{action}
				return snapshot
			},
		},
		{
			name: "terminal action mismatches executing lease",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				snapshot := workflowStoreSnapshot(t, WorkflowStatusRunning, DurableWorkUnitStatusExecuting)
				action := workflowStoreAction(t, &snapshot, 0, "action", ActionStatusSucceeded)
				action.LeaseToken = "other-lease"
				snapshot.Actions = []DurableAction{action}
				return snapshot
			},
		},
		{
			name: "terminal unit owns nonterminal action",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				snapshot := workflowStoreSnapshot(t, WorkflowStatusRunning, DurableWorkUnitStatusSucceeded)
				snapshot.Actions = []DurableAction{workflowStoreAction(t, &snapshot, 0, "action", ActionStatusPrepared)}
				snapshot.WorkUnits[0].Outcome.ActionIDs = []ActionID{"action"}
				return snapshot
			},
		},
		{
			name: "terminal unit outcome omits owned action",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				snapshot := workflowStoreSnapshot(t, WorkflowStatusRunning, DurableWorkUnitStatusSucceeded)
				snapshot.Actions = []DurableAction{workflowStoreAction(t, &snapshot, 0, "action", ActionStatusSucceeded)}
				return snapshot
			},
		},
		{
			name: "terminal unit outcome duplicates action",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				snapshot := workflowStoreSnapshot(t, WorkflowStatusRunning, DurableWorkUnitStatusSucceeded)
				snapshot.Actions = []DurableAction{workflowStoreAction(t, &snapshot, 0, "action", ActionStatusSucceeded)}
				snapshot.WorkUnits[0].Outcome.ActionIDs = []ActionID{"action", "action"}
				return snapshot
			},
		},
		{
			name: "terminal unit outcome references foreign action",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				snapshot := workflowStoreSnapshot(t, WorkflowStatusRunning, DurableWorkUnitStatusSucceeded, DurableWorkUnitStatusSucceeded)
				action := workflowStoreAction(t, &snapshot, 0, "action", ActionStatusSucceeded)
				snapshot.Actions = []DurableAction{action}
				snapshot.WorkUnits[0].Outcome.ActionIDs = []ActionID{action.ID}
				snapshot.WorkUnits[1].Outcome.ActionIDs = []ActionID{action.ID}
				return snapshot
			},
		},
		{
			name: "skipped unit owns action",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				snapshot := workflowStoreSnapshot(t, WorkflowStatusRunning, DurableWorkUnitStatusSkipped)
				action := workflowStoreAction(t, &snapshot, 0, "action", ActionStatusSucceeded)
				snapshot.Actions = []DurableAction{action}
				snapshot.WorkUnits[0].Outcome.ActionIDs = []ActionID{action.ID}
				return snapshot
			},
		},
		{
			name: "blocked unit owns action",
			snapshot: func(t *testing.T) WorkflowSnapshot {
				snapshot := workflowStoreSnapshot(t, WorkflowStatusRunning, DurableWorkUnitStatusFailed, DurableWorkUnitStatusBlocked)
				snapshot.WorkUnits[1].Dependencies = []DurableWorkUnitID{snapshot.WorkUnits[0].ID}
				action := workflowStoreAction(t, &snapshot, 1, "action", ActionStatusSucceeded)
				snapshot.Actions = []DurableAction{action}
				snapshot.WorkUnits[1].Outcome.ActionIDs = []ActionID{action.ID}
				return snapshot
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateWorkflowSnapshot(tc.snapshot(t)); err == nil {
				t.Fatal("ValidateWorkflowSnapshot() error = nil, want malformed snapshot rejection")
			}
		})
	}
}

func workflowStoreSnapshot(t *testing.T, workflowStatus WorkflowStatus, unitStatuses ...DurableWorkUnitStatus) WorkflowSnapshot {
	t.Helper()
	ids := make([]DurableWorkUnitID, len(unitStatuses))
	for index := range unitStatuses {
		ids[index] = DurableWorkUnitID(fmt.Sprintf("unit-%d", index+1))
	}
	workflow, err := NewWorkflow(WorkflowInput{
		ID:           "workflow-store-test",
		Conversation: ConversationReference{ConversationID: "conversation-store-test", TurnID: "turn-store-test"},
		RootGoal:     "validate aggregate workflow state",
		CreatedAt:    workflowStoreTestTime,
		WorkUnitIDs:  ids,
	})
	if err != nil {
		t.Fatal(err)
	}
	workflow.Status = workflowStatus
	if workflowTerminal(workflowStatus) {
		workflow.UpdatedAt = workflowStoreTestTime.Add(4 * time.Minute)
		workflow.CompletedAt = workflow.UpdatedAt
	}
	snapshot := WorkflowSnapshot{Workflow: workflow}
	for index, status := range unitStatuses {
		unit, err := NewDurableWorkUnit(DurableWorkUnitInput{
			ID:              ids[index],
			WorkflowID:      workflow.ID,
			Kind:            "execute",
			Phase:           RunPhaseExecute,
			Role:            "worker",
			Task:            "validate aggregate consistency",
			SideEffectClass: SideEffectNone,
		})
		if err != nil {
			t.Fatal(err)
		}
		workflowStoreSetUnitStatus(&unit, status)
		snapshot.WorkUnits = append(snapshot.WorkUnits, unit)
	}
	return snapshot
}

func workflowStoreSetUnitStatus(unit *DurableWorkUnit, status DurableWorkUnitStatus) {
	unit.Status = status
	switch status {
	case DurableWorkUnitStatusLeased, DurableWorkUnitStatusExecuting:
		unit.ClaimedAt = workflowStoreTestTime
		unit.LastFencingToken = 1
		unit.Lease = &DurableLease{
			OwnerID:      "worker-" + string(unit.ID),
			Token:        LeaseToken("lease-" + string(unit.ID)),
			FencingToken: 1,
			ExpiresAt:    workflowStoreTestTime.Add(time.Hour),
		}
		if status == DurableWorkUnitStatusExecuting {
			unit.StartedAt = workflowStoreTestTime.Add(time.Minute)
		}
	case DurableWorkUnitStatusSucceeded:
		unit.ClaimedAt = workflowStoreTestTime
		unit.StartedAt = workflowStoreTestTime.Add(time.Minute)
		unit.CompletedAt = workflowStoreTestTime.Add(2 * time.Minute)
		unit.Outcome = &DurableWorkUnitOutcome{}
	case DurableWorkUnitStatusSkipped:
		unit.CompletedAt = workflowStoreTestTime.Add(2 * time.Minute)
		unit.Outcome = &DurableWorkUnitOutcome{Reason: "unit skipped"}
	case DurableWorkUnitStatusBlocked:
		unit.CompletedAt = workflowStoreTestTime.Add(2 * time.Minute)
		unit.Outcome = &DurableWorkUnitOutcome{Reason: "unit blocked"}
	case DurableWorkUnitStatusFailed:
		unit.ClaimedAt = workflowStoreTestTime
		unit.StartedAt = workflowStoreTestTime.Add(time.Minute)
		unit.CompletedAt = workflowStoreTestTime.Add(2 * time.Minute)
		unit.Outcome = &DurableWorkUnitOutcome{Reason: "unit failed"}
	case DurableWorkUnitStatusNeedsAttention:
		unit.ClaimedAt = workflowStoreTestTime
		unit.StartedAt = workflowStoreTestTime.Add(time.Minute)
		unit.CompletedAt = workflowStoreTestTime.Add(2 * time.Minute)
		unit.Outcome = &DurableWorkUnitOutcome{Reason: "unit needs attention"}
	}
}

func workflowStoreAction(t *testing.T, snapshot *WorkflowSnapshot, unitIndex int, id ActionID, status ActionStatus) DurableAction {
	t.Helper()
	unit := &snapshot.WorkUnits[unitIndex]
	if unit.LastFencingToken == 0 {
		unit.LastFencingToken = 1
	}
	token := LeaseToken("lease-" + string(unit.ID))
	if unit.Lease != nil {
		token = unit.Lease.Token
	}
	action, err := NewDurableAction(DurableActionInput{
		ID:              id,
		WorkflowID:      snapshot.Workflow.ID,
		WorkUnitID:      unit.ID,
		Attempt:         unit.Attempt,
		Kind:            "tool",
		Target:          "read",
		IdempotencyKey:  "key-" + string(id),
		Lease:           LeaseCredential{Token: token, FencingToken: unit.LastFencingToken},
		SideEffectClass: SideEffectNone,
	})
	if err != nil {
		t.Fatal(err)
	}
	action.Status = status
	switch status {
	case ActionStatusExecuting:
		action.StartedAt = workflowStoreTestTime
	case ActionStatusSucceeded:
		action.StartedAt = workflowStoreTestTime
		action.CompletedAt = workflowStoreTestTime.Add(time.Minute)
	case ActionStatusFailed, ActionStatusAmbiguous:
		action.StartedAt = workflowStoreTestTime
		action.CompletedAt = workflowStoreTestTime.Add(time.Minute)
		action.Reason = "action did not complete cleanly"
	}
	return action
}
