package orchestrator

import (
	"context"
	"testing"
	"time"

	"yagent/internal/domain"
)

func TestBlockingWorkUnitCommandsPropagatesDeclaredClosure(t *testing.T) {
	snapshot := durableDependencySnapshot(t)
	blocks := blockingWorkUnitCommands(snapshot)
	if len(blocks) != 2 || blocks[0].UnitID != "child" || blocks[1].UnitID != "grandchild" {
		t.Fatalf("blockingWorkUnitCommands() = %+v", blocks)
	}

	blocked, err := domain.BlockWorkUnits(snapshot, domain.BlockWorkUnitsInput{
		ExpectedRevision: snapshot.Workflow.Revision,
		Blocks:           blocks,
		At:               durableGraphTime.Add(4 * time.Minute),
	})
	if err != nil {
		t.Fatalf("BlockWorkUnits() error = %v", err)
	}
	if blocked.Workflow.Revision != snapshot.Workflow.Revision+1 || blocked.WorkUnits[1].Status != domain.DurableWorkUnitStatusBlocked || blocked.WorkUnits[2].Status != domain.DurableWorkUnitStatusBlocked {
		t.Fatalf("blocked closure = %+v", blocked.WorkUnits)
	}
}

func TestClaimAndStartDurableBatchCommitsExecutingUnits(t *testing.T) {
	snapshot := durableReadySnapshot(t)
	store := &coordinatorWorkflowStore{snapshot: snapshot, hasSnapshot: true}
	service := newTestService(nil, nil, nil, Config{
		WorkflowStore: store, WorkerID: "worker-1", WorkflowLeaseDuration: 10 * time.Minute, MaxParallelAgents: 2,
	})

	batch, err := service.claimAndStartDurableBatch(context.Background(), snapshot.Workflow.ID, durableGraphTime.Add(time.Minute))
	if err != nil {
		t.Fatalf("claimAndStartDurableBatch() error = %v", err)
	}
	if batch.Snapshot.Workflow.Revision != 3 || len(batch.Units) != 2 || len(batch.Credentials) != 2 || store.commitAttempts != 2 {
		t.Fatalf("unexpected durable batch: revision=%d units=%d credentials=%d commits=%d", batch.Snapshot.Workflow.Revision, len(batch.Units), len(batch.Credentials), store.commitAttempts)
	}
	for _, unit := range batch.Units {
		credential := batch.Credentials[unit.ID]
		if unit.Status != domain.DurableWorkUnitStatusExecuting || unit.Lease == nil || unit.Lease.Token != credential.Token || unit.Lease.FencingToken != credential.FencingToken {
			t.Fatalf("unit was not started with committed credential: %+v", unit)
		}
	}
}

func TestClaimAndStartDurableBatchTakesOverExpiredReadOnlyLease(t *testing.T) {
	snapshot := durableReadySnapshot(t)
	first := newTestService(nil, nil, nil, Config{WorkflowStore: &coordinatorWorkflowStore{snapshot: snapshot, hasSnapshot: true}, WorkerID: "worker-1", WorkflowLeaseDuration: time.Minute, MaxParallelAgents: 1})
	store := first.config.WorkflowStore.(*coordinatorWorkflowStore)
	batch, err := first.claimAndStartDurableBatch(context.Background(), snapshot.Workflow.ID, durableGraphTime.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	firstUnit := batch.Units[0]
	if firstUnit.Lease == nil {
		t.Fatal("first worker did not acquire a lease")
	}

	second := newTestService(nil, nil, nil, Config{WorkflowStore: store, WorkerID: "worker-2", WorkflowLeaseDuration: time.Minute, MaxParallelAgents: 1})
	takeover, err := second.claimAndStartDurableBatch(context.Background(), snapshot.Workflow.ID, firstUnit.Lease.ExpiresAt)
	if err != nil {
		t.Fatalf("expired lease takeover error = %v", err)
	}
	if len(takeover.Units) != 1 || takeover.Units[0].Lease == nil || takeover.Units[0].Lease.OwnerID != "worker-2" || takeover.Units[0].Lease.FencingToken != firstUnit.Lease.FencingToken+1 {
		t.Fatalf("takeover batch = %+v", takeover)
	}
}

func TestClaimAndStartDurableBatchStopsExpiredMutatingExecution(t *testing.T) {
	snapshot := durableReadySnapshot(t)
	snapshot.WorkUnits = snapshot.WorkUnits[:1]
	snapshot.Workflow.WorkUnitIDs = snapshot.Workflow.WorkUnitIDs[:1]
	lease := domain.DurableLease{OwnerID: "worker-1", Token: "lease-old", FencingToken: 1, ExpiresAt: durableGraphTime.Add(time.Minute)}
	var err error
	snapshot, err = domain.ClaimReadyBatch(snapshot, domain.WorkflowBatchClaims{ExpectedRevision: snapshot.Workflow.Revision, Claims: []domain.WorkUnitClaim{{UnitID: snapshot.WorkUnits[0].ID, Lease: lease}}}, durableGraphTime.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	credential := domain.LeaseCredential{Token: lease.Token, FencingToken: lease.FencingToken}
	snapshot, err = domain.StartClaimedBatch(snapshot, domain.WorkflowBatchCredentials{ExpectedRevision: snapshot.Workflow.Revision, Credentials: []domain.WorkUnitCredential{{UnitID: snapshot.WorkUnits[0].ID, Credential: credential}}}, durableGraphTime.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = domain.PrepareAction(snapshot, domain.PrepareActionInput{ExpectedRevision: snapshot.Workflow.Revision, Action: domain.DurableActionInput{
		ID: "action-old", WorkflowID: snapshot.Workflow.ID, WorkUnitID: snapshot.WorkUnits[0].ID, Attempt: snapshot.WorkUnits[0].Attempt,
		Kind: "tool_call", Target: "write_file", IdempotencyKey: "action-old", Lease: credential,
		SideEffectClass: domain.SideEffectWorkspace, PreconditionFingerprint: "before",
	}, At: durableGraphTime.Add(3 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = domain.StartAction(snapshot, "action-old", domain.WorkflowLeaseCredential{ExpectedRevision: snapshot.Workflow.Revision, LeaseCredential: credential}, durableGraphTime.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	store := &coordinatorWorkflowStore{snapshot: snapshot, hasSnapshot: true}
	service := newTestService(nil, nil, nil, Config{WorkflowStore: store, WorkerID: "worker-2", WorkflowLeaseDuration: time.Minute, MaxParallelAgents: 1})
	batch, err := service.claimAndStartDurableBatch(context.Background(), snapshot.Workflow.ID, lease.ExpiresAt)
	if err != nil {
		t.Fatalf("unsafe expiry reconciliation error = %v", err)
	}
	unit := batch.Snapshot.WorkUnits[0]
	if len(batch.Units) != 0 || unit.Status != domain.DurableWorkUnitStatusNeedsAttention || unit.Lease != nil || batch.Snapshot.Actions[0].Status != domain.ActionStatusAmbiguous {
		t.Fatalf("unsafe expiry was replayed: batch=%+v", batch)
	}
}

func TestRenewDurableWorkBatchExtendsCommittedLease(t *testing.T) {
	snapshot := durableReadySnapshot(t)
	store := &coordinatorWorkflowStore{snapshot: snapshot, hasSnapshot: true}
	service := newTestService(nil, nil, nil, Config{WorkflowStore: store, WorkerID: "worker-1", WorkflowLeaseDuration: time.Minute, MaxParallelAgents: 1})
	batch, err := service.claimAndStartDurableBatch(context.Background(), snapshot.Workflow.ID, durableGraphTime.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	before := batch.Units[0].Lease.ExpiresAt
	renewAt := durableGraphTime.Add(30 * time.Second)
	if err := service.renewDurableWorkBatch(context.Background(), snapshot.Workflow.ID, batch.Credentials, renewAt); err != nil {
		t.Fatalf("renewDurableWorkBatch() error = %v", err)
	}
	after, err := store.LoadWorkflowSnapshot(context.Background(), snapshot.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.WorkUnits[0].Lease == nil || !after.WorkUnits[0].Lease.ExpiresAt.After(before) || !after.WorkUnits[0].Lease.ExpiresAt.Equal(renewAt.Add(time.Minute)) {
		t.Fatalf("renewed lease = %+v", after.WorkUnits[0].Lease)
	}
}

func TestDurableLeaseHeartbeatRenewsBeforeShortLeaseExpires(t *testing.T) {
	snapshot := durableReadySnapshot(t)
	store := &coordinatorWorkflowStore{snapshot: snapshot, hasSnapshot: true}
	service := newTestService(nil, nil, nil, Config{WorkflowStore: store, WorkerID: "worker-heartbeat", WorkflowLeaseDuration: 90 * time.Millisecond, MaxParallelAgents: 1})
	batch, err := service.claimAndStartDurableBatch(context.Background(), snapshot.Workflow.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	initialExpiry := batch.Units[0].Lease.ExpiresAt
	stop := service.startDurableLeaseHeartbeat(context.Background(), snapshot.Workflow.ID, batch.Credentials)
	deadline := time.Now().Add(time.Second)
	for {
		loaded, loadErr := store.LoadWorkflowSnapshot(context.Background(), snapshot.Workflow.ID)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if loaded.WorkUnits[0].Lease != nil && loaded.WorkUnits[0].Lease.ExpiresAt.After(initialExpiry) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("heartbeat did not renew the short lease before timeout")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := stop(); err != nil {
		t.Fatalf("heartbeat error = %v", err)
	}
	loaded, err := store.LoadWorkflowSnapshot(context.Background(), snapshot.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.WorkUnits[0].Lease == nil || !loaded.WorkUnits[0].Lease.ExpiresAt.After(initialExpiry) {
		t.Fatalf("heartbeat did not renew short lease: before=%s after=%+v", initialExpiry, loaded.WorkUnits[0].Lease)
	}
}

var durableGraphTime = time.Date(2026, time.July, 11, 9, 0, 0, 0, time.UTC)

func durableReadySnapshot(t *testing.T) domain.WorkflowSnapshot {
	t.Helper()
	workflow, err := domain.NewWorkflow(domain.WorkflowInput{
		ID: "workflow-ready", Conversation: domain.ConversationReference{ConversationID: "conversation-ready", TurnID: "turn-ready"},
		RootGoal: "run ready units", WorkUnitIDs: []domain.DurableWorkUnitID{"unit-a", "unit-b"}, CreatedAt: durableGraphTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	units := make([]domain.DurableWorkUnit, 0, 2)
	for _, id := range workflow.WorkUnitIDs {
		unit, err := domain.NewDurableWorkUnit(domain.DurableWorkUnitInput{
			ID: id, WorkflowID: workflow.ID, Kind: "primary", Phase: domain.RunPhaseExecute,
			Role: "worker", Task: "execute " + string(id), SideEffectClass: domain.SideEffectNone,
		})
		if err != nil {
			t.Fatal(err)
		}
		units = append(units, unit)
	}
	return domain.WorkflowSnapshot{Workflow: workflow, WorkUnits: units}
}

func durableDependencySnapshot(t *testing.T) domain.WorkflowSnapshot {
	t.Helper()
	workflow, err := domain.NewWorkflow(domain.WorkflowInput{
		ID: "workflow-dependency", Conversation: domain.ConversationReference{ConversationID: "conversation-dependency", TurnID: "turn-dependency"},
		RootGoal: "propagate failure", WorkUnitIDs: []domain.DurableWorkUnitID{"root", "child", "grandchild"}, CreatedAt: durableGraphTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	inputs := []domain.DurableWorkUnitInput{
		{ID: "root", WorkflowID: workflow.ID, Kind: "primary", Phase: domain.RunPhaseExecute, Role: "worker", Task: "fail root", SideEffectClass: domain.SideEffectNone},
		{ID: "child", WorkflowID: workflow.ID, Kind: "verification", Phase: domain.RunPhaseVerify, Role: "worker", Task: "child", Dependencies: []domain.DurableWorkUnitID{"root"}, SideEffectClass: domain.SideEffectNone},
		{ID: "grandchild", WorkflowID: workflow.ID, Kind: "finalize", Phase: domain.RunPhaseFinalize, Role: "worker", Task: "grandchild", Dependencies: []domain.DurableWorkUnitID{"child"}, SideEffectClass: domain.SideEffectNone},
	}
	units := make([]domain.DurableWorkUnit, 0, len(inputs))
	for _, input := range inputs {
		unit, err := domain.NewDurableWorkUnit(input)
		if err != nil {
			t.Fatal(err)
		}
		units = append(units, unit)
	}
	snapshot := domain.WorkflowSnapshot{Workflow: workflow, WorkUnits: units}
	lease := domain.DurableLease{OwnerID: "worker-1", Token: "lease-root", FencingToken: 1, ExpiresAt: durableGraphTime.Add(10 * time.Minute)}
	snapshot, err = domain.ClaimReadyBatch(snapshot, domain.WorkflowBatchClaims{ExpectedRevision: 1, Claims: []domain.WorkUnitClaim{{UnitID: "root", Lease: lease}}}, durableGraphTime.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = domain.StartClaimedBatch(snapshot, domain.WorkflowBatchCredentials{ExpectedRevision: 2, Credentials: []domain.WorkUnitCredential{{UnitID: "root", Credential: domain.LeaseCredential{Token: lease.Token, FencingToken: lease.FencingToken}}}}, durableGraphTime.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = domain.FinishUnit(snapshot, domain.FinishUnitInput{
		ExpectedRevision: 3, UnitID: "root", Status: domain.DurableWorkUnitStatusFailed,
		Outcome: domain.DurableWorkUnitOutcome{Reason: "root failed"}, Credential: domain.LeaseCredential{Token: lease.Token, FencingToken: lease.FencingToken}, At: durableGraphTime.Add(3 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
