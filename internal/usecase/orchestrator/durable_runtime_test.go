package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"yagent/internal/domain"
	"yagent/internal/infra/state"
)

func TestDurableRuntimeSimplePrimarySuccessAdvancesProjection(t *testing.T) {
	plan := durableRuntimePlan()
	service, store, run := newDurableRuntimeService(t, plan, &fakeModelClient{responses: map[string][]domain.ModelResponse{
		"coder": {{Message: domain.Message{Role: domain.RoleAssistant, Content: "primary complete"}}},
	}}, &fakeToolExecutor{}, fakeCatalog{agents: map[string]domain.AgentSpec{"coder": {ID: "coder", Name: "Coder"}}})

	result, _, err := service.runWorkGraph(context.Background(), run, plan, domain.TurnRequest{})
	if err != nil {
		t.Fatalf("runWorkGraph() error = %v", err)
	}
	snapshot := durableRuntimeSnapshot(t, store, run.WorkflowID)
	if snapshot.Workflow.Status != domain.WorkflowStatusSucceeded || snapshot.Workflow.Revision <= 1 {
		t.Fatalf("workflow status=%s revision=%d, want succeeded with revision advance", snapshot.Workflow.Status, snapshot.Workflow.Revision)
	}
	if run.WorkflowRevision != snapshot.Workflow.Revision || run.Status != domain.RunStatusCompleted {
		t.Fatalf("projected run revision=%d status=%s, snapshot=%d/%s", run.WorkflowRevision, run.Status, snapshot.Workflow.Revision, snapshot.Workflow.Status)
	}
	if result.Message.Content != "primary complete" || len(snapshot.Workflow.FinalOutcomeRefs) != 1 {
		t.Fatalf("primary result was not durably finalized: result=%q refs=%+v", result.Message.Content, snapshot.Workflow.FinalOutcomeRefs)
	}
}

func TestDurableOutcomeRequiresAttentionForAmbiguousMutation(t *testing.T) {
	snapshot := domain.WorkflowSnapshot{Actions: []domain.DurableAction{{
		ID: "action-ambiguous", WorkUnitID: "unit-a", Status: domain.ActionStatusAmbiguous,
		SideEffectClass: domain.SideEffectWorkspace, Reason: "write may have completed",
	}}}
	status, outcome := durableOutcome(snapshot, "unit-a", workUnitOutcome{err: fmt.Errorf("tool failed")})
	if status != domain.DurableWorkUnitStatusNeedsAttention || outcome.Reason != "write may have completed" {
		t.Fatalf("durableOutcome() = status=%s outcome=%+v", status, outcome)
	}
}

func TestRecoverLostDurableBatchReconcilesExpiredReadOnlyExecution(t *testing.T) {
	snapshot := durableReadySnapshot(t)
	store := &coordinatorWorkflowStore{snapshot: snapshot, hasSnapshot: true}
	service := newTestService(nil, nil, nil, Config{WorkflowStore: store, WorkerID: "worker-sleep", WorkflowLeaseDuration: time.Minute, MaxParallelAgents: 1})
	claimedAt := time.Now().Add(-2 * time.Minute)
	batch, err := service.claimAndStartDurableBatch(context.Background(), snapshot.Workflow.ID, claimedAt)
	if err != nil {
		t.Fatal(err)
	}

	recovered, err := service.recoverLostDurableBatch(context.Background(), snapshot.Workflow.ID, fmt.Errorf("renew durable work leases: %w", domain.ErrLeaseExpired), time.Now())
	if err != nil {
		t.Fatalf("recoverLostDurableBatch() error = %v", err)
	}
	unit, ok := durableSnapshotUnit(recovered, batch.Units[0].ID)
	if !ok || unit.Status != domain.DurableWorkUnitStatusPending || unit.Lease != nil {
		t.Fatalf("expired read-only unit was not made retryable: %+v", unit)
	}
	if unit.LastFencingToken != batch.Credentials[unit.ID].FencingToken {
		t.Fatalf("fencing generation = %d, want %d", unit.LastFencingToken, batch.Credentials[unit.ID].FencingToken)
	}
}

func TestRecoverLostDurableBatchRejectsUnrelatedHeartbeatFailure(t *testing.T) {
	service := newTestService(nil, nil, nil, Config{WorkflowStore: &coordinatorWorkflowStore{}})
	want := fmt.Errorf("storage unavailable")
	if _, err := service.recoverLostDurableBatch(context.Background(), "workflow", want, time.Now()); !errors.Is(err, want) {
		t.Fatalf("recoverLostDurableBatch() error = %v, want %v", err, want)
	}
}

func TestDurableRuntimeReconcilesLeaseExpiryDetectedAtFinish(t *testing.T) {
	plan := durableRuntimePlan()
	model := &finishLeaseExpiringModelClient{}
	service, store, run := newDurableRuntimeService(t, plan, model, &fakeToolExecutor{}, fakeCatalog{agents: map[string]domain.AgentSpec{"coder": {ID: "coder", Name: "Coder"}}})
	model.store = store

	result, _, err := service.runWorkGraph(context.Background(), run, plan, domain.TurnRequest{})
	if err != nil {
		t.Fatalf("runWorkGraph() error = %v", err)
	}
	snapshot := durableRuntimeSnapshot(t, store, run.WorkflowID)
	unit, ok := durableSnapshotUnit(snapshot, "execute:primary:coder")
	if !ok || unit.Status != domain.DurableWorkUnitStatusSucceeded || unit.LastFencingToken != 2 {
		t.Fatalf("finish-time lease expiry was not retried under new fencing: %+v", unit)
	}
	if model.calls != 2 || result.Message.Content != "retry complete" {
		t.Fatalf("model calls=%d result=%q", model.calls, result.Message.Content)
	}
}

func TestRunTurnUsesDurableWorkflowAuthority(t *testing.T) {
	store := &coordinatorWorkflowStore{}
	model := &fakeModelClient{responses: map[string][]domain.ModelResponse{
		"manager": {{Message: domain.Message{Role: domain.RoleAssistant, Content: "durable answer"}}},
	}}
	service := newTestService(
		model,
		&fakeToolExecutor{},
		fakeCatalog{agents: map[string]domain.AgentSpec{"manager": {ID: "manager", Name: "Manager"}}},
		Config{WorkflowStore: store, WorkerID: "worker-run-turn", WorkflowLeaseDuration: time.Minute, DisablePhaseHarness: true},
	)

	result, err := service.RunTurn(context.Background(), domain.TurnRequest{Messages: []domain.Message{{Role: domain.RoleUser, Content: "answer durably"}}})
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if result.Message.Content != "durable answer" || result.Run == nil || result.Run.WorkflowRevision < 1 || result.Run.Status != domain.RunStatusCompleted {
		t.Fatalf("RunTurn() did not return durable projection: %+v", result)
	}
	snapshot := durableRuntimeSnapshot(t, store, result.Run.WorkflowID)
	if snapshot.Workflow.Status != domain.WorkflowStatusSucceeded || len(snapshot.Workflow.FinalOutcomeRefs) != 1 {
		t.Fatalf("RunTurn() durable snapshot = %+v", snapshot.Workflow)
	}
}

func TestContinueConversationStartsNewWorkflowWithPriorTurnContext(t *testing.T) {
	store, err := state.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var managerRequests []domain.ModelRequest
	model := &fakeModelClient{responses: map[string][]domain.ModelResponse{
		"manager": {
			{Message: domain.Message{Role: domain.RoleAssistant, Content: "first answer"}},
			{Message: domain.Message{Role: domain.RoleAssistant, Content: "second answer"}},
		},
	}, inspect: func(request domain.ModelRequest) {
		if request.Agent.ID == "manager" {
			managerRequests = append(managerRequests, request)
		}
	}}
	service := newTestService(model, &fakeToolExecutor{}, fakeCatalog{agents: map[string]domain.AgentSpec{"manager": {ID: "manager", Name: "Manager"}}}, Config{
		WorkflowStore: store, RunStore: store, RuntimeStore: store, ConversationStore: store,
		WorkerID: "conversation-worker", WorkflowLeaseDuration: time.Minute, DisablePhaseHarness: true,
	})

	first, err := service.RunTurn(context.Background(), domain.TurnRequest{Messages: []domain.Message{{Role: domain.RoleUser, Content: "first question"}}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ContinueConversation(context.Background(), domain.ConversationTurnRequest{
		ConversationID: first.Run.ConversationID, Messages: []domain.Message{{Role: domain.RoleUser, Content: "follow up"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Run.ConversationID != first.Run.ConversationID || second.Run.WorkflowID == first.Run.WorkflowID || second.Run.ConversationTurnID == first.Run.ConversationTurnID {
		t.Fatalf("continuation identity mismatch: first=%+v second=%+v", first.Run, second.Run)
	}
	if len(managerRequests) != 2 || !messagesContain(managerRequests[1].Messages, "first answer") || second.Message.Content != "second answer" {
		t.Fatalf("continuation context/result mismatch: requests=%+v result=%q", managerRequests, second.Message.Content)
	}
	firstSnapshot, err := store.LoadWorkflowSnapshot(context.Background(), first.Run.WorkflowID)
	if err != nil || firstSnapshot.Workflow.Status != domain.WorkflowStatusSucceeded {
		t.Fatalf("first workflow was mutated by continuation: snapshot=%+v err=%v", firstSnapshot.Workflow, err)
	}
}

func TestConversationContinuationRecoversTerminalOutputAfterFinalIndexWriteFailure(t *testing.T) {
	store, err := state.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	conversations := &failNthConversationStore{FileStore: store, failAt: 2}
	trace := &fakeTraceSink{}
	var managerRequests []domain.ModelRequest
	model := &fakeModelClient{responses: map[string][]domain.ModelResponse{
		"manager": {
			{Message: domain.Message{Role: domain.RoleAssistant, Content: "first durable answer"}},
			{Message: domain.Message{Role: domain.RoleAssistant, Content: "continued answer"}},
		},
	}, inspect: func(request domain.ModelRequest) {
		if request.Agent.ID == "manager" {
			managerRequests = append(managerRequests, request)
		}
	}}
	service := newTestService(model, &fakeToolExecutor{}, fakeCatalog{agents: map[string]domain.AgentSpec{
		"manager": {ID: "manager", Name: "Manager"},
	}}, Config{
		WorkflowStore: store, RunStore: store, RuntimeStore: store, ConversationStore: conversations,
		TraceSink: trace, WorkerID: "conversation-recovery-worker", WorkflowLeaseDuration: time.Minute, DisablePhaseHarness: true,
	})

	first, err := service.RunTurn(context.Background(), domain.TurnRequest{Messages: []domain.Message{{Role: domain.RoleUser, Content: "first question"}}})
	if err != nil {
		t.Fatalf("durable success must survive final conversation projection failure: %v", err)
	}
	if len(first.Run.KnownFailures) == 0 || !strings.Contains(first.Run.KnownFailures[len(first.Run.KnownFailures)-1], "conversation turn") {
		t.Fatalf("conversation projection failure was not exposed: %+v", first.Run.KnownFailures)
	}
	second, err := service.ContinueConversation(context.Background(), domain.ConversationTurnRequest{
		ConversationID: first.Run.ConversationID,
		Messages:       []domain.Message{{Role: domain.RoleUser, Content: "continue"}},
	})
	if err != nil {
		t.Fatalf("continuation could not recover output from workflow snapshot: %v", err)
	}
	if len(managerRequests) != 2 || !messagesContain(managerRequests[1].Messages, "first durable answer") || second.Message.Content != "continued answer" {
		t.Fatalf("snapshot-backed continuation mismatch: requests=%+v second=%+v", managerRequests, second)
	}
	found := false
	for _, event := range trace.events {
		if event.Type == "projection_degraded" && event.Metrics["component"] == "conversation turn" {
			found = true
		}
	}
	if !found {
		t.Fatalf("conversation degradation event missing: %+v", trace.events)
	}
}

func TestRunTurnStopsBeforeModelWhenConversationIntentCannotBeSaved(t *testing.T) {
	store, err := state.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	conversations := &failNthConversationStore{FileStore: store, failAt: 1}
	model := &fakeModelClient{inspect: func(domain.ModelRequest) {
		t.Fatal("model must not run before conversation intent is durable")
	}}
	service := newTestService(model, &fakeToolExecutor{}, fakeCatalog{}, Config{ConversationStore: conversations})

	_, err = service.RunTurn(context.Background(), domain.TurnRequest{Messages: []domain.Message{{Role: domain.RoleUser, Content: "hello"}}})
	if err == nil || !strings.Contains(err.Error(), "conversation turn intent") {
		t.Fatalf("expected conversation intent persistence error, got %v", err)
	}
}

func TestArtifactIDsAreUniqueUnderConcurrency(t *testing.T) {
	const count = 256
	ids := make(chan string, count)
	var group sync.WaitGroup
	for range count {
		group.Add(1)
		go func() {
			defer group.Done()
			ids <- newArtifactID()
		}()
	}
	group.Wait()
	close(ids)
	seen := make(map[string]struct{}, count)
	for id := range ids {
		if _, exists := seen[id]; exists {
			t.Fatalf("duplicate concurrent artifact id %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestRecoverWorkflowResumesExistingPendingSnapshot(t *testing.T) {
	plan := durableRuntimePlan()
	_, store, run := newDurableRuntimeService(t, plan, &fakeModelClient{}, &fakeToolExecutor{}, fakeCatalog{})
	seed := durableRunSeed{messages: cloneMessages(run.Messages), model: "stored-model", profile: "stored-profile", capabilities: []string{"repo-read"}, stream: true}
	initial, err := buildInitialWorkflowSnapshot(run, seed, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	store.snapshot, store.hasSnapshot = initial, true
	var gotModel string
	model := &fakeModelClient{responses: map[string][]domain.ModelResponse{
		"coder": {{Message: domain.Message{Role: domain.RoleAssistant, Content: "resumed result"}}},
	}, inspect: func(request domain.ModelRequest) { gotModel = request.Model }}
	restarted := newTestService(model, &fakeToolExecutor{}, fakeCatalog{agents: map[string]domain.AgentSpec{"coder": {ID: "coder", Name: "Coder"}}}, Config{WorkflowStore: store, WorkerID: "restarted-worker", WorkflowLeaseDuration: time.Minute, MaxParallelAgents: 1})

	result, err := restarted.RecoverWorkflow(context.Background(), domain.WorkflowRecoveryRequest{WorkflowID: run.WorkflowID})
	if err != nil {
		t.Fatalf("RecoverWorkflow() error = %v", err)
	}
	if result.Message.Content != "resumed result" || store.commitAttempts == 0 || gotModel != "stored-model" {
		t.Fatalf("existing workflow was not resumed: result=%q commits=%d", result.Message.Content, store.commitAttempts)
	}
	if result.Run.Profile != "stored-profile" || len(result.Run.Messages) == 0 || len(result.Run.EnabledCapabilities) != 1 {
		t.Fatalf("workflow input was not reconstructed from snapshot: %+v", result.Run)
	}
}

func TestWorkflowIntentSurvivesBeforePlanAttachment(t *testing.T) {
	_, store, run := newDurableRuntimeService(t, durableRuntimePlan(), &fakeModelClient{inspect: func(domain.ModelRequest) {
		t.Fatal("planner must not run while persisting workflow intent")
	}}, &fakeToolExecutor{}, fakeCatalog{})
	seed := durableRunSeed{
		messages: cloneMessages(run.Messages), model: "intent-model", profile: "intent-profile", capabilities: []string{"repo-read"}, stream: true,
	}
	intent, err := buildWorkflowIntentSnapshot(run, seed, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CommitWorkflowSnapshot(context.Background(), 0, intent); err != nil {
		t.Fatal(err)
	}

	snapshot := durableRuntimeSnapshot(t, store, run.WorkflowID)
	if snapshot.Workflow.Status != domain.WorkflowStatusPending || len(snapshot.Workflow.GraphArtifactRefs) != 0 || len(snapshot.WorkUnits) != 0 {
		t.Fatalf("intent must remain unplanned: %+v", snapshot.Workflow)
	}
	stored, err := durableRunSeedFromSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if stored.model != seed.model || stored.profile != seed.profile || len(stored.messages) != 1 || stored.messages[0].Content != run.Messages[0].Content || len(stored.capabilities) != 1 || !stored.stream {
		t.Fatalf("intent did not preserve recoverable input: %+v", stored)
	}
}

func TestRunTurnCommitsWorkflowIntentBeforePlannerExecution(t *testing.T) {
	store := &coordinatorWorkflowStore{}
	model := &fakeModelClient{responses: map[string][]domain.ModelResponse{
		"planner": {{Message: domain.Message{Role: domain.RoleAssistant, Content: `{"task_kind":"question","primary_agent_id":"coder","preparation_agent_ids":[]}`}}},
		"coder":   {{Message: domain.Message{Role: domain.RoleAssistant, Content: "planned after intent"}}},
	}, inspect: func(request domain.ModelRequest) {
		if request.Agent.ID != "planner" {
			return
		}
		store.mu.Lock()
		defer store.mu.Unlock()
		if !store.hasSnapshot || store.snapshot.Workflow.Status != domain.WorkflowStatusPending || len(store.snapshot.Workflow.GraphArtifactRefs) != 0 || len(store.snapshot.WorkUnits) != 0 {
			t.Fatalf("planner ran before durable intent commit: %+v", store.snapshot)
		}
		if _, err := durableRunSeedFromSnapshot(store.snapshot); err != nil {
			t.Fatalf("planner ran without recoverable workflow input: %v", err)
		}
	}}
	service := newTestService(model, &fakeToolExecutor{}, fakeCatalog{agents: map[string]domain.AgentSpec{
		"planner": {ID: "planner", Mode: domain.AgentModeTool, ReadOnly: true, PreferredPhases: []domain.RunPhase{domain.RunPhasePlan}},
		"coder":   {ID: "coder", Name: "Coder", TaskKinds: []domain.TaskKind{domain.TaskKindQuestion}, PreferredPhases: []domain.RunPhase{domain.RunPhaseExecute}},
	}}, Config{WorkflowStore: store, WorkerID: "intent-worker", WorkflowLeaseDuration: time.Minute, MaxParallelAgents: 1})

	result, err := service.RunTurn(context.Background(), domain.TurnRequest{Messages: []domain.Message{{Role: domain.RoleUser, Content: "persist intent first"}}})
	if err != nil || result.Message.Content != "planned after intent" {
		t.Fatalf("RunTurn() result=%+v error=%v", result, err)
	}
}

func TestRecoverWorkflowPlansIntentThenCompletes(t *testing.T) {
	_, store, run := newDurableRuntimeService(t, durableRuntimePlan(), &fakeModelClient{}, &fakeToolExecutor{}, fakeCatalog{})
	seed := durableRunSeed{messages: cloneMessages(run.Messages), model: "recovered-model", profile: "recovered-profile"}
	intent, err := buildWorkflowIntentSnapshot(run, seed, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	store.snapshot, store.hasSnapshot = intent, true

	model := &fakeModelClient{responses: map[string][]domain.ModelResponse{
		"planner": {{Message: domain.Message{Role: domain.RoleAssistant, Content: `{"task_kind":"question","primary_agent_id":"coder","preparation_agent_ids":[]}`}}},
		"coder":   {{Message: domain.Message{Role: domain.RoleAssistant, Content: "recovered complete"}}},
	}}
	restarted := newTestService(model, &fakeToolExecutor{}, fakeCatalog{agents: map[string]domain.AgentSpec{
		"planner": {ID: "planner", Mode: domain.AgentModeTool, ReadOnly: true, PreferredPhases: []domain.RunPhase{domain.RunPhasePlan}},
		"coder":   {ID: "coder", Name: "Coder", TaskKinds: []domain.TaskKind{domain.TaskKindQuestion}, PreferredPhases: []domain.RunPhase{domain.RunPhaseExecute}},
	}}, Config{WorkflowStore: store, WorkerID: "restarted-worker", WorkflowLeaseDuration: time.Minute, MaxParallelAgents: 1})

	result, err := restarted.RecoverWorkflow(context.Background(), domain.WorkflowRecoveryRequest{WorkflowID: run.WorkflowID})
	if err != nil {
		t.Fatalf("RecoverWorkflow() error = %v", err)
	}
	snapshot := durableRuntimeSnapshot(t, store, run.WorkflowID)
	if result.Message.Content != "recovered complete" || model.indexes["planner"] != 1 || model.indexes["coder"] != 1 || snapshot.Workflow.Status != domain.WorkflowStatusSucceeded || len(snapshot.Workflow.GraphArtifactRefs) != 1 || len(snapshot.WorkUnits) == 0 {
		t.Fatalf("intent recovery did not plan and complete: result=%q calls=%+v workflow=%+v units=%+v", result.Message.Content, model.indexes, snapshot.Workflow, snapshot.WorkUnits)
	}
}

func TestAttachWorkflowPlanConvergesOnRetryAndDuplicate(t *testing.T) {
	service, store, run := newDurableRuntimeService(t, durableRuntimePlan(), &fakeModelClient{}, &fakeToolExecutor{}, fakeCatalog{})
	intent, err := buildWorkflowIntentSnapshot(run, durableRunSeed{messages: cloneMessages(run.Messages)}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	store.snapshot, store.hasSnapshot, store.conflictOnce = intent, true, true

	attached, err := service.attachWorkflowPlan(context.Background(), intent, run, time.Now())
	if err != nil {
		t.Fatalf("attachWorkflowPlan() error = %v", err)
	}
	commitsAfterRetry := store.commitAttempts
	duplicate, err := service.attachWorkflowPlan(context.Background(), attached, run, time.Now())
	if err != nil {
		t.Fatalf("duplicate attachWorkflowPlan() error = %v", err)
	}
	if attached.Workflow.Revision != 2 || duplicate.Workflow.Revision != attached.Workflow.Revision || commitsAfterRetry != 2 || store.commitAttempts != commitsAfterRetry || len(attached.Workflow.GraphArtifactRefs) != 1 || len(attached.WorkUnits) == 0 {
		t.Fatalf("plan attachment did not converge: first=%+v duplicate=%+v commits=%d", attached.Workflow, duplicate.Workflow, store.commitAttempts)
	}
}

func TestDurableRuntimeRunsIndependentPreparationInParallel(t *testing.T) {
	plan := durableRuntimePlan()
	plan.Preparation = []domain.PlannedAgentAssignment{{AgentID: "research-a", Reason: "a"}, {AgentID: "research-b", Reason: "b"}}
	model := &concurrentModelClient{delay: 30 * time.Millisecond, responses: map[string]domain.ModelResponse{
		"research-a": {Message: domain.Message{Role: domain.RoleAssistant, Content: "a"}},
		"research-b": {Message: domain.Message{Role: domain.RoleAssistant, Content: "b"}},
		"coder":      {Message: domain.Message{Role: domain.RoleAssistant, Content: "done"}},
	}}
	service, store, run := newDurableRuntimeService(t, plan, model, &fakeToolExecutor{}, fakeCatalog{agents: map[string]domain.AgentSpec{
		"research-a": {ID: "research-a", Name: "Research A"}, "research-b": {ID: "research-b", Name: "Research B"}, "coder": {ID: "coder", Name: "Coder"},
	}})

	_, _, err := service.runWorkGraph(context.Background(), run, plan, domain.TurnRequest{})
	if err != nil {
		t.Fatalf("runWorkGraph() error = %v", err)
	}
	if model.maxConcurrent.Load() < 2 {
		t.Fatalf("max concurrent model calls = %d, want parallel preparation", model.maxConcurrent.Load())
	}
	if snapshot := durableRuntimeSnapshot(t, store, run.WorkflowID); snapshot.Workflow.Status != domain.WorkflowStatusSucceeded {
		t.Fatalf("workflow status = %s, want succeeded", snapshot.Workflow.Status)
	}
}

func TestDurableRuntimePersistsToolActionWithUnitLease(t *testing.T) {
	plan := durableRuntimePlan()
	tools := &fakeToolExecutor{defs: map[string][]domain.ToolDefinition{
		"coder": {{
			Name: "fs_stat", ReadOnly: true,
			Semantics: domain.ToolSemantics{Class: domain.ToolClassObserve, ReusePolicy: domain.ToolReuseNever, DuplicatePolicy: domain.ToolDuplicateAllow, Freshness: domain.ToolFreshnessPolicy{Strategy: domain.ToolFreshnessNone}, SideEffectClass: domain.SideEffectNone},
		}},
	}}
	model := &fakeModelClient{responses: map[string][]domain.ModelResponse{
		"coder": {
			{Message: domain.Message{Role: domain.RoleAssistant, ToolCalls: []domain.ToolCall{{ID: "tool-1", Name: "fs_stat", Arguments: map[string]any{"path": "README.md"}}}}},
			{Message: domain.Message{Role: domain.RoleAssistant, Content: "done"}},
		},
	}}
	catalog := fakeCatalog{agents: map[string]domain.AgentSpec{
		"coder": {ID: "coder", Name: "Coder", AllowedTools: []string{"fs_stat"}},
	}}
	service, store, run := newDurableRuntimeService(t, plan, model, tools, catalog)

	_, _, err := service.runWorkGraph(context.Background(), run, plan, domain.TurnRequest{})
	if err != nil {
		t.Fatalf("runWorkGraph() error = %v", err)
	}
	snapshot := durableRuntimeSnapshot(t, store, run.WorkflowID)
	if len(snapshot.Actions) != 1 {
		t.Fatalf("actions = %+v, want one persisted tool action", snapshot.Actions)
	}
	action := snapshot.Actions[0]
	if action.WorkflowID != run.WorkflowID || action.WorkUnitID == "" || action.LeaseToken == "" || action.FencingToken == 0 || action.Status != domain.ActionStatusSucceeded {
		t.Fatalf("persisted action lost durable invocation context: %+v", action)
	}
	unit, ok := durableSnapshotUnit(snapshot, action.WorkUnitID)
	if !ok || unit.Outcome == nil || len(unit.Outcome.ActionIDs) != 1 || unit.Outcome.ActionIDs[0] != action.ID {
		t.Fatalf("work unit outcome did not include action: unit=%+v action=%+v", unit, action)
	}
}

func TestDurableRuntimeFailureBlocksDescendants(t *testing.T) {
	plan := durableRuntimePlan()
	plan.Verify = []domain.PlannedAgentAssignment{{AgentID: "verifier", Reason: "verify"}}
	service, store, run := newDurableRuntimeService(t, plan, &fakeModelClient{}, &fakeToolExecutor{}, fakeCatalog{})

	_, _, err := service.runWorkGraph(context.Background(), run, plan, domain.TurnRequest{})
	if err == nil {
		t.Fatal("runWorkGraph() error = nil, want durable workflow failure")
	}
	snapshot := durableRuntimeSnapshot(t, store, run.WorkflowID)
	primary, primaryOK := durableSnapshotUnit(snapshot, "execute:primary:coder")
	verify, verifyOK := durableSnapshotUnit(snapshot, "verify:verifier")
	if !primaryOK || primary.Status != domain.DurableWorkUnitStatusFailed || !verifyOK || verify.Status != domain.DurableWorkUnitStatusBlocked || snapshot.Workflow.Status != domain.WorkflowStatusFailed {
		t.Fatalf("failure closure = workflow=%s primary=%+v verify=%+v", snapshot.Workflow.Status, primary, verify)
	}
}

func TestDurableRuntimeMapsRecoveryAttemptsAndSettlesFinalArtifact(t *testing.T) {
	plan := durableRuntimePlan()
	plan.Verify = []domain.PlannedAgentAssignment{{AgentID: "verifier", Reason: "verify"}}
	plan.Recovery = &domain.PlannedAgentAssignment{AgentID: "coder", Reason: "repair"}
	plan.Finalize = &domain.PlannedAgentAssignment{AgentID: "manager", Reason: "finalize"}
	service, store, run := newDurableRuntimeService(t, plan, &fakeModelClient{responses: map[string][]domain.ModelResponse{
		"coder": {
			{Message: domain.Message{Role: domain.RoleAssistant, Content: "initial execution"}},
			{Message: domain.Message{Role: domain.RoleAssistant, Content: "repaired execution"}},
		},
		"verifier": {
			{Message: domain.Message{Role: domain.RoleAssistant, Content: `{"status":"fail","summary":"needs repair","repair_brief":"repair it"}`}},
			{Message: domain.Message{Role: domain.RoleAssistant, Content: `{"status":"pass","summary":"verified"}`}},
		},
		"manager": {{Message: domain.Message{Role: domain.RoleAssistant, Content: `{"response":"final answer","summary":"final answer","claims":[{"claim":"the execution completed","evidence_refs":["execution"]}]}`}}},
	}}, &fakeToolExecutor{}, fakeCatalog{agents: map[string]domain.AgentSpec{
		"coder": {ID: "coder", Name: "Coder"}, "verifier": {ID: "verifier", Name: "Verifier"}, "manager": {ID: "manager", Name: "Manager"},
	}})

	result, _, err := service.runWorkGraph(context.Background(), run, plan, domain.TurnRequest{})
	if err != nil {
		t.Fatalf("runWorkGraph() error = %v", err)
	}
	snapshot := durableRuntimeSnapshot(t, store, run.WorkflowID)
	if result.Message.Content != "final answer" {
		t.Fatalf("final result = %q, want persisted final response; workflow=%+v units=%+v artifacts=%+v", result.Message.Content, snapshot.Workflow, snapshot.WorkUnits, snapshot.Artifacts)
	}
	recovery, recoveryOK := durableSnapshotUnit(snapshot, "recover:2:coder")
	verification, verificationOK := durableSnapshotUnit(snapshot, "verify:2:verifier")
	if !recoveryOK || recovery.Attempt != 2 || !verificationOK || verification.Attempt != 2 {
		t.Fatalf("recovery/verification attempt mapping lost: recovery=%+v verification=%+v", recovery, verification)
	}
	if len(snapshot.Workflow.FinalOutcomeRefs) == 0 {
		t.Fatal("workflow settlement did not retain final response artifact refs")
	}
	assertUniqueDurableArtifactIDs(t, snapshot)
}

func TestCompleteRunDoesNotCreateProjectionOnlyFinalArtifact(t *testing.T) {
	plan := durableRuntimePlan()
	plan.Finalize = &domain.PlannedAgentAssignment{AgentID: "manager", Reason: "finalize"}
	service, store, run := newDurableRuntimeService(t, plan, &fakeModelClient{responses: map[string][]domain.ModelResponse{
		"coder":   {{Message: domain.Message{Role: domain.RoleAssistant, Content: "primary complete"}}},
		"manager": {{Message: domain.Message{Role: domain.RoleAssistant, Content: `{"response":"final answer","claims":[{"claim":"the execution completed","evidence_refs":["execution"]}]}`}}},
	}}, &fakeToolExecutor{}, fakeCatalog{agents: map[string]domain.AgentSpec{
		"coder": {ID: "coder", Name: "Coder"}, "manager": {ID: "manager", Name: "Manager"},
	}})

	result, _, err := service.runWorkGraph(context.Background(), run, plan, domain.TurnRequest{})
	if err != nil {
		t.Fatalf("runWorkGraph() error = %v", err)
	}
	before := durableRuntimeSnapshot(t, store, run.WorkflowID)
	if err := service.completeRun(context.Background(), run, result.Message); err != nil {
		t.Fatalf("completeRun() error = %v", err)
	}
	after := durableRuntimeSnapshot(t, store, run.WorkflowID)
	if len(after.Artifacts) != len(before.Artifacts) || after.Workflow.Revision != before.Workflow.Revision || len(run.Artifacts) != len(before.Artifacts) {
		t.Fatalf("completeRun created projection-only state: before=%d/%d after=%d/%d projection=%d", before.Workflow.Revision, len(before.Artifacts), after.Workflow.Revision, len(after.Artifacts), len(run.Artifacts))
	}
}

func TestCompleteRunReportsProjectionFailureWithoutOverridingDurableSuccess(t *testing.T) {
	plan := durableRuntimePlan()
	service, store, run := newDurableRuntimeService(t, plan, &fakeModelClient{responses: map[string][]domain.ModelResponse{
		"coder": {{Message: domain.Message{Role: domain.RoleAssistant, Content: "durable answer"}}},
	}}, &fakeToolExecutor{}, fakeCatalog{agents: map[string]domain.AgentSpec{"coder": {ID: "coder", Name: "Coder"}}})
	trace := &fakeTraceSink{}
	service.config.RunStore = &failingRunStore{failAt: 1}
	service.config.TraceSink = trace

	result, _, err := service.runWorkGraph(context.Background(), run, plan, domain.TurnRequest{})
	if err != nil {
		t.Fatalf("runWorkGraph() error = %v", err)
	}
	before := durableRuntimeSnapshot(t, store, run.WorkflowID)
	if err := service.completeRun(context.Background(), run, result.Message); err != nil {
		t.Fatalf("durable completion must survive projection failure: %v", err)
	}
	after := durableRuntimeSnapshot(t, store, run.WorkflowID)
	if after.Workflow.Status != domain.WorkflowStatusSucceeded || after.Workflow.Revision != before.Workflow.Revision {
		t.Fatalf("projection failure changed authoritative workflow: before=%+v after=%+v", before.Workflow, after.Workflow)
	}
	if len(run.KnownFailures) == 0 || !strings.Contains(run.KnownFailures[len(run.KnownFailures)-1], "projection") {
		t.Fatalf("projection degradation was not exposed on the run: %+v", run.KnownFailures)
	}
	found := false
	for _, event := range trace.events {
		if event.Type == "projection_degraded" && event.Status == "warning" {
			found = true
		}
	}
	if !found {
		t.Fatalf("projection degradation event was not emitted: %+v", trace.events)
	}
}

func TestDurableRuntimeMergesParallelVerificationBeforeRecovery(t *testing.T) {
	plan := durableRuntimePlan()
	plan.Verify = []domain.PlannedAgentAssignment{{AgentID: "verifier-a", Reason: "verify a"}, {AgentID: "verifier-b", Reason: "verify b"}}
	plan.Recovery = &domain.PlannedAgentAssignment{AgentID: "coder", Reason: "repair"}
	model := &lockedSequenceModelClient{responses: map[string][]domain.ModelResponse{
		"coder": {
			{Message: domain.Message{Role: domain.RoleAssistant, Content: "initial"}},
			{Message: domain.Message{Role: domain.RoleAssistant, Content: "repaired"}},
		},
		"verifier-a": {
			{Message: domain.Message{Role: domain.RoleAssistant, Content: `{"status":"pass","summary":"a pass"}`}},
			{Message: domain.Message{Role: domain.RoleAssistant, Content: `{"status":"pass","summary":"a pass again"}`}},
		},
		"verifier-b": {
			{Message: domain.Message{Role: domain.RoleAssistant, Content: `{"status":"fail","summary":"b fail","repair_brief":"repair b"}`}},
			{Message: domain.Message{Role: domain.RoleAssistant, Content: `{"status":"pass","summary":"b pass"}`}},
		},
	}}
	service, store, run := newDurableRuntimeService(t, plan, model, &fakeToolExecutor{}, fakeCatalog{agents: map[string]domain.AgentSpec{
		"coder": {ID: "coder", Name: "Coder"}, "verifier-a": {ID: "verifier-a", Name: "Verifier A"}, "verifier-b": {ID: "verifier-b", Name: "Verifier B"},
	}})

	result, _, err := service.runWorkGraph(context.Background(), run, plan, domain.TurnRequest{})
	if err != nil {
		t.Fatalf("runWorkGraph() error = %v", err)
	}
	snapshot := durableRuntimeSnapshot(t, store, run.WorkflowID)
	recovery, ok := durableSnapshotUnit(snapshot, "recover:2:coder")
	if !ok || recovery.Status != domain.DurableWorkUnitStatusSucceeded || result.Message.Content != "repaired" {
		t.Fatalf("parallel verification did not drive recovery: result=%q recovery=%+v", result.Message.Content, recovery)
	}
}

func durableRuntimePlan() *domain.ExecutionPlan {
	return &domain.ExecutionPlan{Version: "v1", TaskKind: domain.TaskKindQuestion, Summary: "durable runtime test", Primary: domain.PlannedAgentAssignment{AgentID: "coder", Reason: "do work"}}
}

func newDurableRuntimeService(t *testing.T, plan *domain.ExecutionPlan, model domain.ModelClient, tools domain.ToolExecutor, catalog domain.AgentCatalog) (*Service, *coordinatorWorkflowStore, *domain.RunState) {
	t.Helper()
	now := time.Now().Add(-time.Second)
	run := &domain.RunState{ID: "run-durable", RootRunID: "run-durable", ConversationID: "conversation-durable", ConversationTurnID: "turn-durable", WorkflowID: "workflow-durable", Status: domain.RunStatusRunning, CurrentPhase: domain.RunPhaseExecute, Attempt: 1, UserGoal: "test durable runtime", Messages: []domain.Message{{Role: domain.RoleUser, Content: "test durable runtime"}}, ExecutionPlan: plan, CreatedAt: now, UpdatedAt: now}
	run.WorkUnits = workUnitsFromExecutionPlan(run, plan)
	run.Artifacts = append(run.Artifacts, newExecutionPlanArtifact(run, domain.RunPhasePlan, "planner", plan))
	store := &coordinatorWorkflowStore{}
	service := newTestService(model, tools, catalog, Config{WorkflowStore: store, WorkerID: "durable-test-worker", WorkflowLeaseDuration: time.Minute, MaxParallelAgents: 4})
	return service, store, run
}

func durableRuntimeSnapshot(t *testing.T, store *coordinatorWorkflowStore, workflowID domain.WorkflowID) domain.WorkflowSnapshot {
	t.Helper()
	snapshot, err := store.LoadWorkflowSnapshot(context.Background(), workflowID)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func assertUniqueDurableArtifactIDs(t *testing.T, snapshot domain.WorkflowSnapshot) {
	t.Helper()
	seen := map[string]struct{}{}
	for _, artifact := range snapshot.Artifacts {
		if _, exists := seen[artifact.ID]; exists {
			t.Fatalf("duplicate durable artifact ID %q in %+v", artifact.ID, snapshot.Artifacts)
		}
		seen[artifact.ID] = struct{}{}
	}
}

type lockedSequenceModelClient struct {
	mu        sync.Mutex
	responses map[string][]domain.ModelResponse
	indexes   map[string]int
}

type finishLeaseExpiringModelClient struct {
	store *coordinatorWorkflowStore
	calls int
}

func (c *finishLeaseExpiringModelClient) Generate(_ context.Context, _ domain.ModelRequest) (domain.ModelResponse, error) {
	c.calls++
	if c.calls == 1 {
		c.store.mu.Lock()
		for index := range c.store.snapshot.WorkUnits {
			if c.store.snapshot.WorkUnits[index].Lease != nil {
				c.store.snapshot.WorkUnits[index].Lease.ExpiresAt = time.Now().Add(-time.Second)
			}
		}
		c.store.mu.Unlock()
		return domain.ModelResponse{Message: domain.Message{Role: domain.RoleAssistant, Content: "stale result"}}, nil
	}
	return domain.ModelResponse{Message: domain.Message{Role: domain.RoleAssistant, Content: "retry complete"}}, nil
}

func (c *lockedSequenceModelClient) Generate(_ context.Context, request domain.ModelRequest) (domain.ModelResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.indexes == nil {
		c.indexes = map[string]int{}
	}
	index := c.indexes[request.Agent.ID]
	items := c.responses[request.Agent.ID]
	if index >= len(items) {
		return domain.ModelResponse{}, fmt.Errorf("missing response for %s at %d", request.Agent.ID, index)
	}
	c.indexes[request.Agent.ID] = index + 1
	return items[index], nil
}
