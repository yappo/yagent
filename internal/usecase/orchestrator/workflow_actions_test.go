package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"yagent/internal/domain"
)

type actionWorkflowStore struct {
	mu         sync.Mutex
	snapshot   domain.WorkflowSnapshot
	commits    []domain.WorkflowSnapshot
	failCommit int
	failErr    error
}

func (s *actionWorkflowStore) LoadWorkflowSnapshot(_ context.Context, workflowID domain.WorkflowID) (domain.WorkflowSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshot.Workflow.ID != workflowID {
		return domain.WorkflowSnapshot{}, &domain.WorkflowNotFoundError{WorkflowID: workflowID}
	}
	return s.snapshot, nil
}

func (s *actionWorkflowStore) CommitWorkflowSnapshot(_ context.Context, expected int64, snapshot domain.WorkflowSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if expected != s.snapshot.Workflow.Revision {
		return &domain.WorkflowRevisionConflictError{WorkflowID: snapshot.Workflow.ID, Expected: expected, Actual: s.snapshot.Workflow.Revision}
	}
	if s.failCommit > 0 && len(s.commits)+1 == s.failCommit {
		return s.failErr
	}
	s.snapshot = snapshot
	s.commits = append(s.commits, snapshot)
	return nil
}

type artifactFailingRuntimeStore struct {
	domain.RuntimeStateStore
}

func (artifactFailingRuntimeStore) SaveArtifact(context.Context, domain.RunArtifact) error {
	return errors.New("artifact persistence unavailable")
}

func TestDurableToolActionCommitsIntentBeforeEffect(t *testing.T) {
	store, invocation := newActionWorkflowStore(t)
	var executions int
	var executionContext domain.DurableActionExecutionContext
	service := newTestService(nil, &fakeToolExecutor{exec: func(ctx context.Context, _ domain.AgentSpec, call domain.ToolCall) domain.ToolResult {
		executions++
		executionContext, _ = domain.DurableActionExecutionContextFrom(ctx)
		return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: true, Output: "observed"}
	}}, nil, Config{WorkflowStore: store})

	result, _, reused, err := service.executeDurableToolAction(context.Background(), invocation, observeToolSpec())
	if err != nil {
		t.Fatalf("executeDurableToolAction() error = %v", err)
	}
	if reused || result.Output != "observed" || executions != 1 {
		t.Fatalf("result=%+v reused=%t executions=%d", result, reused, executions)
	}
	if executionContext.ActionID == "" || executionContext.WorkflowID != invocation.WorkflowID || executionContext.WorkUnitID != invocation.WorkUnitID || executionContext.IdempotencyKey == "" || executionContext.LeaseToken != invocation.Lease.Token || executionContext.FencingToken != invocation.Lease.FencingToken {
		t.Fatalf("durable action context was not propagated: %+v", executionContext)
	}
	if len(store.commits) != 3 {
		t.Fatalf("commit count = %d, want prepare/start/finish", len(store.commits))
	}
	for index, want := range []domain.ActionStatus{domain.ActionStatusPrepared, domain.ActionStatusExecuting, domain.ActionStatusSucceeded} {
		if got := store.commits[index].Actions[0].Status; got != want {
			t.Fatalf("commit %d action status = %q, want %q", index+1, got, want)
		}
	}
	if got := store.commits[2].Actions[0].PostconditionFingerprint; got != durableToolResultFingerprint(result) {
		t.Fatalf("read-only postcondition = %q, want tool result fingerprint", got)
	}
}

func TestDurableToolActionDoesNotExecuteWhenStartCommitFails(t *testing.T) {
	store, invocation := newActionWorkflowStore(t)
	store.failCommit = 2
	store.failErr = errors.New("start commit unavailable")
	var executions int
	service := newTestService(nil, &fakeToolExecutor{exec: func(_ context.Context, _ domain.AgentSpec, call domain.ToolCall) domain.ToolResult {
		executions++
		return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: true, Output: "must not run"}
	}}, nil, Config{WorkflowStore: store})

	_, _, _, err := service.executeDurableToolAction(context.Background(), invocation, observeToolSpec())
	if err == nil {
		t.Fatal("executeDurableToolAction() error = nil, want start commit error")
	}
	if executions != 0 {
		t.Fatalf("executor calls = %d, want 0 when start commit fails", executions)
	}
	if len(store.commits) != 1 || store.commits[0].Actions[0].Status != domain.ActionStatusPrepared {
		t.Fatalf("commits after failed start = %+v", store.commits)
	}
}

func TestDurableToolActionAcceptsFutureWorkflowUpdateTime(t *testing.T) {
	store, invocation := newActionWorkflowStore(t)
	store.snapshot.Workflow.UpdatedAt = time.Now().Add(time.Minute)
	service := newTestService(nil, &fakeToolExecutor{exec: func(_ context.Context, _ domain.AgentSpec, call domain.ToolCall) domain.ToolResult {
		return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: true, Output: "observed"}
	}}, nil, Config{WorkflowStore: store})

	if _, _, _, err := service.executeDurableToolAction(context.Background(), invocation, observeToolSpec()); err != nil {
		t.Fatalf("executeDurableToolAction() error = %v", err)
	}
	if got := requireSingleDurableToolAction(t, store.snapshot).Status; got != domain.ActionStatusSucceeded {
		t.Fatalf("action status = %q, want %q", got, domain.ActionStatusSucceeded)
	}
}

func TestDurableToolActionTerminalRetryDoesNotReexecute(t *testing.T) {
	store, invocation := newActionWorkflowStore(t)
	var executions int
	service := newTestService(nil, &fakeToolExecutor{exec: func(_ context.Context, _ domain.AgentSpec, call domain.ToolCall) domain.ToolResult {
		executions++
		return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: true, Output: "stored output"}
	}}, nil, Config{WorkflowStore: store})

	if _, _, _, err := service.executeDurableToolAction(context.Background(), invocation, observeToolSpec()); err != nil {
		t.Fatalf("first executeDurableToolAction() error = %v", err)
	}
	commits := len(store.commits)
	result, _, reused, err := service.executeDurableToolAction(context.Background(), invocation, observeToolSpec())
	if err != nil {
		t.Fatalf("retry executeDurableToolAction() error = %v", err)
	}
	if !reused || result.Output != "stored output" || executions != 1 {
		t.Fatalf("result=%+v reused=%t executions=%d", result, reused, executions)
	}
	if len(store.commits) != commits {
		t.Fatalf("retry committed %d transitions, want no new commits", len(store.commits)-commits)
	}
}

func TestDurableToolActionIdentifiersIncludeFencingGeneration(t *testing.T) {
	_, invocation := newActionWorkflowStore(t)
	firstID, firstKey := durableToolActionIdentifiers(invocation, observeToolSpec())
	invocation.Lease.FencingToken++
	secondID, secondKey := durableToolActionIdentifiers(invocation, observeToolSpec())
	if firstID == secondID || firstKey == secondKey {
		t.Fatalf("action identity was reused across fencing generations: first=%q second=%q", firstID, secondID)
	}
}

func TestDurableToolActionRejectsStaleInvocationAfterTakeover(t *testing.T) {
	store, staleInvocation := newActionWorkflowStore(t)
	at := time.Now().UTC()
	store.snapshot.WorkUnits[0].Lease.ExpiresAt = at.Add(-time.Second)
	reconciled, err := domain.ReconcileExpiredLeases(store.snapshot, domain.ReconcileExpiredLeasesInput{ExpectedRevision: store.snapshot.Workflow.Revision, At: at})
	if err != nil {
		t.Fatal(err)
	}
	newLease := domain.DurableLease{OwnerID: "worker-new", Token: "lease-new", FencingToken: 2, ExpiresAt: at.Add(time.Hour)}
	claimed, err := domain.ClaimReadyBatch(reconciled, domain.WorkflowBatchClaims{ExpectedRevision: reconciled.Workflow.Revision, Claims: []domain.WorkUnitClaim{{UnitID: reconciled.WorkUnits[0].ID, Lease: newLease}}}, at)
	if err != nil {
		t.Fatal(err)
	}
	store.snapshot, err = domain.StartClaimedBatch(claimed, domain.WorkflowBatchCredentials{ExpectedRevision: claimed.Workflow.Revision, Credentials: []domain.WorkUnitCredential{{UnitID: claimed.WorkUnits[0].ID, Credential: domain.LeaseCredential{Token: newLease.Token, FencingToken: newLease.FencingToken}}}}, at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	var executions int
	service := newTestService(nil, &fakeToolExecutor{exec: func(_ context.Context, _ domain.AgentSpec, call domain.ToolCall) domain.ToolResult {
		executions++
		return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: true, Output: "must not execute"}
	}}, nil, Config{WorkflowStore: store})
	if _, _, _, err := service.executeDurableToolAction(context.Background(), staleInvocation, observeToolSpec()); !errors.Is(err, domain.ErrLeaseMismatch) {
		t.Fatalf("stale invocation error = %v, want lease mismatch", err)
	}
	if executions != 0 {
		t.Fatalf("stale invocation reached tool executor %d times", executions)
	}
}

func TestDurableToolActionMarksPersistenceFailureAmbiguous(t *testing.T) {
	store, invocation := newActionWorkflowStore(t)
	var executions int
	service := newTestService(nil, &fakeToolExecutor{exec: func(_ context.Context, _ domain.AgentSpec, call domain.ToolCall) domain.ToolResult {
		executions++
		return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: true, Output: "workspace changed"}
	}}, nil, Config{WorkflowStore: store, RuntimeStore: artifactFailingRuntimeStore{}})
	spec := workspaceToolSpec(t)

	_, _, _, err := service.executeDurableToolAction(context.Background(), invocation, spec)
	if err == nil {
		t.Fatal("executeDurableToolAction() error = nil, want persistence failure")
	}
	action := requireSingleDurableToolAction(t, store.snapshot)
	if action.Status != domain.ActionStatusAmbiguous {
		t.Fatalf("action status = %q, want %q", action.Status, domain.ActionStatusAmbiguous)
	}
	if executions != 1 {
		t.Fatalf("executor calls = %d, want 1", executions)
	}
	if _, _, _, retryErr := service.executeDurableToolAction(context.Background(), invocation, spec); retryErr == nil {
		t.Fatal("retry error = nil, want explicit ambiguous action error")
	}
	if executions != 1 {
		t.Fatalf("executor reran ambiguous action: calls = %d", executions)
	}
}

func TestDurableToolActionMarksMutatingFailureAmbiguous(t *testing.T) {
	store, invocation := newActionWorkflowStore(t)
	var executions int
	service := newTestService(nil, &fakeToolExecutor{exec: func(_ context.Context, _ domain.AgentSpec, call domain.ToolCall) domain.ToolResult {
		executions++
		return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: false, Output: "write may have partially completed"}
	}}, nil, Config{WorkflowStore: store})
	spec := workspaceToolSpec(t)

	_, _, _, err := service.executeDurableToolAction(context.Background(), invocation, spec)
	if err == nil {
		t.Fatal("executeDurableToolAction() error = nil, want tool failure")
	}
	if action := requireSingleDurableToolAction(t, store.snapshot); action.Status != domain.ActionStatusAmbiguous {
		t.Fatalf("action status = %q, want %q", action.Status, domain.ActionStatusAmbiguous)
	}
	if _, _, _, retryErr := service.executeDurableToolAction(context.Background(), invocation, spec); retryErr == nil {
		t.Fatal("retry error = nil, want explicit ambiguous action error")
	}
	if executions != 1 {
		t.Fatalf("executor reran ambiguous action: calls = %d", executions)
	}
}

func TestDurableToolActionReturnsReadOnlyFailureForModelCorrection(t *testing.T) {
	store, invocation := newActionWorkflowStore(t)
	service := newTestService(nil, &fakeToolExecutor{exec: func(_ context.Context, _ domain.AgentSpec, call domain.ToolCall) domain.ToolResult {
		return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: false, Output: "file not found"}
	}}, nil, Config{WorkflowStore: store})

	result, _, _, err := service.executeDurableToolAction(context.Background(), invocation, observeToolSpec())
	if err != nil || result.Success || result.Output != "file not found" {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if action := requireSingleDurableToolAction(t, store.snapshot); action.Status != domain.ActionStatusFailed {
		t.Fatalf("action status = %q, want %q", action.Status, domain.ActionStatusFailed)
	}
}

func TestDurableToolActionStoresActionScopedArtifactAndWorkspacePostcondition(t *testing.T) {
	store, invocation := newActionWorkflowStore(t)
	path := filepath.Join(t.TempDir(), "output.txt")
	spec := workspaceToolSpecAt(path)
	service := newTestService(nil, &fakeToolExecutor{exec: func(_ context.Context, _ domain.AgentSpec, call domain.ToolCall) domain.ToolResult {
		if err := os.WriteFile(path, []byte("after"), 0o644); err != nil {
			return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: false, Output: err.Error()}
		}
		return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: true, Output: "written"}
	}}, nil, Config{WorkflowStore: store})

	result, _, _, err := service.executeDurableToolAction(context.Background(), invocation, spec)
	if err != nil {
		t.Fatalf("executeDurableToolAction() error = %v", err)
	}
	action := requireSingleDurableToolAction(t, store.snapshot)
	if len(action.ResultArtifactRefs) != 1 {
		t.Fatalf("artifact refs = %+v", action.ResultArtifactRefs)
	}
	ref := action.ResultArtifactRefs[0]
	if ref.ID != "tool-output-"+string(action.ID) || ref.Kind != "tool_output" {
		t.Fatalf("artifact ref = %+v, want action-scoped tool output", ref)
	}
	if len(store.snapshot.Artifacts) != 1 || store.snapshot.Artifacts[0].ID != ref.ID || store.snapshot.Artifacts[0].Text != result.Output {
		t.Fatalf("stored workflow artifacts = %+v", store.snapshot.Artifacts)
	}
	wantPostcondition := mutationFingerprint(spec.writeSet, service.capturePathStates(context.Background(), spec.writeSet))
	if action.PostconditionFingerprint != wantPostcondition {
		t.Fatalf("workspace postcondition = %q, want %q", action.PostconditionFingerprint, wantPostcondition)
	}
}

func newActionWorkflowStore(t *testing.T) (*actionWorkflowStore, domain.AgentInvocation) {
	t.Helper()
	at := time.Now().UTC().Add(-time.Minute)
	workflow, err := domain.NewWorkflow(domain.WorkflowInput{
		ID:           "workflow-tool-action",
		Conversation: domain.ConversationReference{ConversationID: "conversation-tool-action", TurnID: "turn-tool-action"},
		RootGoal:     "exercise durable tool actions",
		CreatedAt:    at,
		WorkUnitIDs:  []domain.DurableWorkUnitID{"unit-tool-action"},
	})
	if err != nil {
		t.Fatal(err)
	}
	unit, err := domain.NewDurableWorkUnit(domain.DurableWorkUnitInput{
		ID: "unit-tool-action", WorkflowID: workflow.ID, Kind: "primary", Phase: domain.RunPhaseExecute,
		Role: "coder", Task: "execute a durable tool action", SideEffectClass: domain.SideEffectNone,
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := domain.WorkflowSnapshot{Workflow: workflow, WorkUnits: []domain.DurableWorkUnit{unit}}
	lease := domain.DurableLease{OwnerID: "worker-tool-action", Token: "lease-tool-action", FencingToken: 1, ExpiresAt: time.Now().Add(time.Hour)}
	claimed, err := domain.ClaimReadyBatch(snapshot, domain.WorkflowBatchClaims{ExpectedRevision: snapshot.Workflow.Revision, Claims: []domain.WorkUnitClaim{{UnitID: unit.ID, Lease: lease}}}, at)
	if err != nil {
		t.Fatal(err)
	}
	started, err := domain.StartClaimedBatch(claimed, domain.WorkflowBatchCredentials{ExpectedRevision: claimed.Workflow.Revision, Credentials: []domain.WorkUnitCredential{{UnitID: unit.ID, Credential: domain.LeaseCredential{Token: lease.Token, FencingToken: lease.FencingToken}}}}, at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return &actionWorkflowStore{snapshot: started}, domain.AgentInvocation{
		RunID: "run-tool-action", RootRunID: "root-tool-action", WorkflowID: workflow.ID, WorkUnitID: unit.ID,
		Lease: domain.LeaseCredential{Token: lease.Token, FencingToken: lease.FencingToken}, Agent: domain.AgentSpec{ID: "coder"},
		Phase: domain.RunPhaseExecute, Attempt: 1,
	}
}

func observeToolSpec() toolRuntimeSpec {
	return toolRuntimeSpec{
		call:           domain.ToolCall{ID: "call-observe", Name: "fs_read", Arguments: map[string]any{"path": "/tmp/observe"}},
		normalizedArgs: `{"path":"/tmp/observe"}`,
		semanticKey:    "tool:observe",
		readSet:        []string{"/tmp/observe"},
		semantics: domain.ToolSemantics{
			Class: domain.ToolClassObserve, SideEffectClass: domain.SideEffectNone,
		},
	}
}

func workspaceToolSpec(t *testing.T) toolRuntimeSpec {
	t.Helper()
	return workspaceToolSpecAt(filepath.Join(t.TempDir(), "workspace-output.txt"))
}

func workspaceToolSpecAt(path string) toolRuntimeSpec {
	return toolRuntimeSpec{
		call:           domain.ToolCall{ID: "call-workspace", Name: "fs_write", Arguments: map[string]any{"path": path, "content": "after"}},
		normalizedArgs: `{"content":"after","path":"` + path + `"}`,
		semanticKey:    "tool:workspace:" + path,
		writeSet:       []string{path},
		semantics: domain.ToolSemantics{
			Class: domain.ToolClassMutate, SideEffectClass: domain.SideEffectWorkspace,
		},
	}
}

func requireSingleDurableToolAction(t *testing.T, snapshot domain.WorkflowSnapshot) domain.DurableAction {
	t.Helper()
	if len(snapshot.Actions) != 1 {
		t.Fatalf("workflow actions = %+v, want exactly one", snapshot.Actions)
	}
	return snapshot.Actions[0]
}
