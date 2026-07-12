package state

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"yagent/internal/domain"
)

func TestWorkflowStoreCreateLoadRoundTrip(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	want := workflowSnapshot(t, "workflow-round-trip", 1, 2, 2)
	if err := store.CommitWorkflowSnapshot(context.Background(), 0, want); err != nil {
		t.Fatalf("CommitWorkflowSnapshot() error = %v", err)
	}

	got, err := store.LoadWorkflowSnapshot(context.Background(), want.Workflow.ID)
	if err != nil {
		t.Fatalf("LoadWorkflowSnapshot() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded snapshot shape = %+v, want %+v", got, want)
	}
	for _, unit := range got.WorkUnits {
		if unit.WorkflowID != got.Workflow.ID {
			t.Fatalf("loaded unit belongs to %q, want %q", unit.WorkflowID, got.Workflow.ID)
		}
	}
	for _, action := range got.Actions {
		if action.WorkflowID != got.Workflow.ID {
			t.Fatalf("loaded action belongs to %q, want %q", action.WorkflowID, got.Workflow.ID)
		}
	}
}

func TestWorkflowStoreCASAllowsExactlyOneConcurrentClaim(t *testing.T) {
	root := t.TempDir()
	first, err := NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore(first) error = %v", err)
	}
	second, err := NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore(second) error = %v", err)
	}
	initial := workflowSnapshot(t, "workflow-cas", 1, 1, 0)
	if err := first.CommitWorkflowSnapshot(context.Background(), 0, initial); err != nil {
		t.Fatalf("initial CommitWorkflowSnapshot() error = %v", err)
	}

	left, err := first.LoadWorkflowSnapshot(context.Background(), initial.Workflow.ID)
	if err != nil {
		t.Fatalf("first LoadWorkflowSnapshot() error = %v", err)
	}
	right, err := second.LoadWorkflowSnapshot(context.Background(), initial.Workflow.ID)
	if err != nil {
		t.Fatalf("second LoadWorkflowSnapshot() error = %v", err)
	}
	left = claimedWorkflowSnapshot(t, left, "owner-a", "lease-a")
	right = claimedWorkflowSnapshot(t, right, "owner-b", "lease-b")

	start := make(chan struct{})
	errorsByCommit := make(chan error, 2)
	var group sync.WaitGroup
	for _, candidate := range []struct {
		store    *FileStore
		snapshot domain.WorkflowSnapshot
	}{{first, left}, {second, right}} {
		group.Add(1)
		go func(candidate struct {
			store    *FileStore
			snapshot domain.WorkflowSnapshot
		}) {
			defer group.Done()
			<-start
			errorsByCommit <- candidate.store.CommitWorkflowSnapshot(context.Background(), 1, candidate.snapshot)
		}(candidate)
	}
	close(start)
	group.Wait()
	close(errorsByCommit)

	successes := 0
	conflicts := 0
	for err := range errorsByCommit {
		if err == nil {
			successes++
			continue
		}
		if errors.Is(err, domain.ErrWorkflowRevisionConflict) {
			conflicts++
			continue
		}
		t.Fatalf("concurrent CommitWorkflowSnapshot() error = %v", err)
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent commit results: successes=%d conflicts=%d", successes, conflicts)
	}
	loaded, err := first.LoadWorkflowSnapshot(context.Background(), initial.Workflow.ID)
	if err != nil {
		t.Fatalf("LoadWorkflowSnapshot() after concurrent commits error = %v", err)
	}
	if loaded.Workflow.Revision != 2 || loaded.WorkUnits[0].Status != domain.DurableWorkUnitStatusLeased {
		t.Fatalf("committed claim = workflow=%+v unit=%+v", loaded.Workflow, loaded.WorkUnits[0])
	}
}

func TestWorkflowStoreCASAllowsExactlyOneExpiredLeaseReconciliation(t *testing.T) {
	root := t.TempDir()
	first, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	initial := workflowSnapshot(t, "workflow-reconcile-cas", 1, 1, 0)
	if err := first.CommitWorkflowSnapshot(context.Background(), 0, initial); err != nil {
		t.Fatal(err)
	}
	claimedAt := initial.Workflow.CreatedAt.Add(time.Minute)
	lease := domain.DurableLease{OwnerID: "expired-worker", Token: "expired-lease", FencingToken: 1, ExpiresAt: claimedAt.Add(time.Minute)}
	claimed, err := domain.ClaimReadyBatch(initial, domain.WorkflowBatchClaims{ExpectedRevision: initial.Workflow.Revision, Claims: []domain.WorkUnitClaim{{UnitID: initial.WorkUnits[0].ID, Lease: lease}}}, claimedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.CommitWorkflowSnapshot(context.Background(), initial.Workflow.Revision, claimed); err != nil {
		t.Fatal(err)
	}
	credential := domain.LeaseCredential{Token: lease.Token, FencingToken: lease.FencingToken}
	initial, err = domain.StartClaimedBatch(claimed, domain.WorkflowBatchCredentials{ExpectedRevision: claimed.Workflow.Revision, Credentials: []domain.WorkUnitCredential{{UnitID: claimed.WorkUnits[0].ID, Credential: credential}}}, claimedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := first.CommitWorkflowSnapshot(context.Background(), claimed.Workflow.Revision, initial); err != nil {
		t.Fatal(err)
	}

	left, err := first.LoadWorkflowSnapshot(context.Background(), initial.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	right, err := second.LoadWorkflowSnapshot(context.Background(), initial.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	reconciledAt := lease.ExpiresAt
	left, err = domain.ReconcileExpiredLeases(left, domain.ReconcileExpiredLeasesInput{ExpectedRevision: left.Workflow.Revision, At: reconciledAt})
	if err != nil {
		t.Fatal(err)
	}
	right, err = domain.ReconcileExpiredLeases(right, domain.ReconcileExpiredLeasesInput{ExpectedRevision: right.Workflow.Revision, At: reconciledAt})
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errorsByCommit := make(chan error, 2)
	var group sync.WaitGroup
	for _, candidate := range []struct {
		store    *FileStore
		snapshot domain.WorkflowSnapshot
	}{{first, left}, {second, right}} {
		group.Add(1)
		go func(candidate struct {
			store    *FileStore
			snapshot domain.WorkflowSnapshot
		}) {
			defer group.Done()
			<-start
			errorsByCommit <- candidate.store.CommitWorkflowSnapshot(context.Background(), initial.Workflow.Revision, candidate.snapshot)
		}(candidate)
	}
	close(start)
	group.Wait()
	close(errorsByCommit)

	successes, conflicts := 0, 0
	for err := range errorsByCommit {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, domain.ErrWorkflowRevisionConflict):
			conflicts++
		default:
			t.Fatalf("concurrent reconciliation commit error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent reconciliation results: successes=%d conflicts=%d", successes, conflicts)
	}
	loaded, err := first.LoadWorkflowSnapshot(context.Background(), initial.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Workflow.Revision != initial.Workflow.Revision+1 || loaded.WorkUnits[0].Status != domain.DurableWorkUnitStatusPending || loaded.WorkUnits[0].Lease != nil || loaded.WorkUnits[0].LastFencingToken != 1 {
		t.Fatalf("published reconciliation = workflow=%+v unit=%+v", loaded.Workflow, loaded.WorkUnits[0])
	}
}

func TestWorkflowStoreUnpublishedGenerationIsInvisible(t *testing.T) {
	errInjected := errors.New("injected failure before HEAD")
	root := t.TempDir()
	store, err := NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	initial := workflowSnapshot(t, "workflow-unpublished", 1, 1, 1)
	if err := store.CommitWorkflowSnapshot(context.Background(), 0, initial); err != nil {
		t.Fatalf("initial CommitWorkflowSnapshot() error = %v", err)
	}
	updated := workflowSnapshot(t, "workflow-unpublished", 2, 2, 2)
	if err := store.commitWorkflowSnapshot(context.Background(), 1, updated, workflowStoreOptions{
		beforeHeadPublication: func() error { return errInjected },
	}); !errors.Is(err, errInjected) {
		t.Fatalf("CommitWorkflowSnapshot() error = %v, want injected error", err)
	}

	reopened, err := NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore(reopened) error = %v", err)
	}
	loaded, err := reopened.LoadWorkflowSnapshot(context.Background(), initial.Workflow.ID)
	if err != nil {
		t.Fatalf("LoadWorkflowSnapshot() error = %v", err)
	}
	if !reflect.DeepEqual(loaded, initial) {
		t.Fatalf("reopened snapshot = %+v, want complete revision 1", loaded)
	}
}

func TestWorkflowStoreFirstPublicationFailureAllowsRetry(t *testing.T) {
	errInjected := errors.New("injected failure before first HEAD")
	root := t.TempDir()
	store, err := NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	snapshot := workflowSnapshot(t, "workflow-first-publication", 1, 1, 1)
	if err := store.commitWorkflowSnapshot(context.Background(), 0, snapshot, workflowStoreOptions{
		beforeHeadPublication: func() error { return errInjected },
	}); !errors.Is(err, errInjected) {
		t.Fatalf("commitWorkflowSnapshot() error = %v, want injected error", err)
	}

	reopened, err := NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore(reopened) error = %v", err)
	}
	_, err = reopened.LoadWorkflowSnapshot(context.Background(), snapshot.Workflow.ID)
	if !errors.Is(err, domain.ErrWorkflowNotFound) {
		t.Fatalf("LoadWorkflowSnapshot() error = %v, want not found while HEAD is absent", err)
	}
	if err := reopened.CommitWorkflowSnapshot(context.Background(), 0, snapshot); err != nil {
		t.Fatalf("retry CommitWorkflowSnapshot() error = %v", err)
	}
	loaded, err := reopened.LoadWorkflowSnapshot(context.Background(), snapshot.Workflow.ID)
	if err != nil {
		t.Fatalf("LoadWorkflowSnapshot() after retry error = %v", err)
	}
	if !reflect.DeepEqual(loaded, snapshot) {
		t.Fatalf("retry loaded snapshot = %+v, want %+v", loaded, snapshot)
	}
}

func TestWorkflowStoreHeadRemainsAuthoritativeWithoutMarkerAndRepairsIt(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	initial := workflowSnapshot(t, "workflow-marker-repair", 1, 1, 0)
	if err := store.CommitWorkflowSnapshot(context.Background(), 0, initial); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(store.workflowInitializedPath()); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadWorkflowSnapshot(context.Background(), initial.Workflow.ID)
	if err != nil {
		t.Fatalf("LoadWorkflowSnapshot() with HEAD and no marker error = %v", err)
	}
	if !reflect.DeepEqual(loaded, initial) {
		t.Fatalf("loaded snapshot = %+v, want %+v", loaded, initial)
	}
	updated := initial
	updated.Workflow.Revision = 2
	updated.Workflow.RootGoal = "marker repair"
	if err := store.CommitWorkflowSnapshot(context.Background(), 1, updated); err != nil {
		t.Fatalf("CommitWorkflowSnapshot() repair error = %v", err)
	}
	marker, err := os.ReadFile(store.workflowInitializedPath())
	if err != nil {
		t.Fatalf("read repaired marker: %v", err)
	}
	if string(marker) != workflowInitializedName+"\n" {
		t.Fatalf("initialization marker = %q", marker)
	}
}

func TestWorkflowStoreCorruptionFailsClosed(t *testing.T) {
	cases := []struct {
		name    string
		corrupt func(t *testing.T, store *FileStore)
	}{
		{
			name: "HEAD",
			corrupt: func(t *testing.T, store *FileStore) {
				t.Helper()
				if err := os.WriteFile(store.workflowHeadPath(), []byte("not-a-generation\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "missing HEAD",
			corrupt: func(t *testing.T, store *FileStore) {
				t.Helper()
				if err := os.Remove(store.workflowHeadPath()); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "manifest",
			corrupt: func(t *testing.T, store *FileStore) {
				t.Helper()
				generation := workflowHeadGeneration(t, store)
				if err := os.WriteFile(store.workflowManifestPath(generation), []byte("{"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "object",
			corrupt: func(t *testing.T, store *FileStore) {
				t.Helper()
				manifest, err := store.loadWorkflowManifest()
				if err != nil {
					t.Fatal(err)
				}
				for _, index := range manifest.Workflows {
					if err := os.WriteFile(store.workflowObjectPath(index.Workflow), []byte("corrupt"), 0o644); err != nil {
						t.Fatal(err)
					}
					return
				}
				t.Fatal("manifest had no workflow")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, err := NewFileStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			snapshot := workflowSnapshot(t, "workflow-corrupt-"+tc.name, 1, 1, 0)
			if err := store.CommitWorkflowSnapshot(context.Background(), 0, snapshot); err != nil {
				t.Fatal(err)
			}
			tc.corrupt(t, store)
			_, err = store.LoadWorkflowSnapshot(context.Background(), snapshot.Workflow.ID)
			if err == nil || errors.Is(err, domain.ErrWorkflowNotFound) {
				t.Fatalf("LoadWorkflowSnapshot() error = %v, want closed failure", err)
			}
		})
	}
}

func TestWorkflowStoreExtraWorkUnitManifestEntryFailsClosed(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := workflowSnapshot(t, "workflow-extra-unit-index", 1, 1, 0)
	if err := store.CommitWorkflowSnapshot(context.Background(), 0, snapshot); err != nil {
		t.Fatal(err)
	}

	extra, err := domain.NewDurableWorkUnit(domain.DurableWorkUnitInput{
		ID:              "extra-unit",
		WorkflowID:      snapshot.Workflow.ID,
		Kind:            "execute",
		Phase:           domain.RunPhaseExecute,
		Role:            "worker",
		Task:            "silently injected unit",
		SideEffectClass: domain.SideEffectNone,
	})
	if err != nil {
		t.Fatal(err)
	}
	hash, data, err := marshalWorkflowObject(extra)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeWorkflowFile(store.workflowObjectPath(hash), data); err != nil {
		t.Fatal(err)
	}

	manifest, err := store.loadWorkflowManifest()
	if err != nil {
		t.Fatal(err)
	}
	index := manifest.Workflows[string(snapshot.Workflow.ID)]
	index.WorkUnits[string(extra.ID)] = hash
	manifest.Workflows[string(snapshot.Workflow.ID)] = index
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.workflowManifestPath(manifest.Generation), manifestData, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := store.LoadWorkflowSnapshot(context.Background(), snapshot.Workflow.ID); !errors.Is(err, errWorkflowStoreCorrupt) {
		t.Fatalf("LoadWorkflowSnapshot() error = %v, want corruption", err)
	}
}

func TestWorkflowStoreContextCancelledWhileWaitingForLock(t *testing.T) {
	root := t.TempDir()
	entered := make(chan struct{})
	release := make(chan struct{})
	first, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := workflowSnapshot(t, "workflow-lock", 1, 1, 0)
	firstResult := make(chan error, 1)
	go func() {
		firstResult <- first.commitWorkflowSnapshot(context.Background(), 0, snapshot, workflowStoreOptions{
			beforeHeadPublication: func() error {
				close(entered)
				<-release
				return nil
			},
		})
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first commit did not reach publication hook")
	}

	ctx, cancel := context.WithCancel(context.Background())
	secondResult := make(chan error, 1)
	go func() { secondResult <- second.CommitWorkflowSnapshot(ctx, 0, snapshot) }()
	select {
	case err := <-secondResult:
		t.Fatalf("second commit returned before cancellation: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	if err := <-secondResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("second commit error = %v, want context.Canceled", err)
	}
	close(release)
	if err := <-firstResult; err != nil {
		t.Fatalf("first commit error = %v", err)
	}
}

func TestWorkflowStoreRejectsInvalidSnapshotLinkageAndRevision(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	valid := workflowSnapshot(t, "workflow-invalid", 1, 1, 1)

	badRevision := valid
	badRevision.Workflow.Revision = 2
	if err := store.CommitWorkflowSnapshot(context.Background(), 0, badRevision); !errors.Is(err, domain.ErrInvalidWorkflow) {
		t.Fatalf("revision commit error = %v, want invalid workflow", err)
	}
	badLinkage := valid
	badLinkage.WorkUnits = append([]domain.DurableWorkUnit(nil), valid.WorkUnits...)
	badLinkage.WorkUnits[0].WorkflowID = "other-workflow"
	if err := store.CommitWorkflowSnapshot(context.Background(), 0, badLinkage); !errors.Is(err, domain.ErrInvalidWorkflow) {
		t.Fatalf("linkage commit error = %v, want invalid workflow", err)
	}
	_, err = store.LoadWorkflowSnapshot(context.Background(), valid.Workflow.ID)
	if !errors.Is(err, domain.ErrWorkflowNotFound) {
		t.Fatalf("invalid commits became visible: %v", err)
	}
}

func TestWorkflowStoreReadersSeeOnlyCompleteReferencedRecords(t *testing.T) {
	root := t.TempDir()
	initialStore, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	initial := workflowSnapshot(t, "workflow-visibility", 1, 1, 1)
	if err := initialStore.CommitWorkflowSnapshot(context.Background(), 0, initial); err != nil {
		t.Fatal(err)
	}
	updated := initial
	updated.Workflow.Revision = 2
	updated.Workflow.RootGoal = "updated goal"
	updated.Artifacts = []domain.RunArtifact{testArtifact("visible-artifact")}
	entered := make(chan struct{})
	release := make(chan struct{})
	writer, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	writerResult := make(chan error, 1)
	go func() {
		writerResult <- writer.commitWorkflowSnapshot(context.Background(), 1, updated, workflowStoreOptions{
			beforeHeadPublication: func() error {
				close(entered)
				<-release
				return nil
			},
		})
	}()
	<-entered

	reader, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	during, err := reader.LoadWorkflowSnapshot(context.Background(), initial.Workflow.ID)
	if err != nil {
		t.Fatalf("LoadWorkflowSnapshot() during commit error = %v", err)
	}
	if during.Workflow.Revision != 1 || during.Workflow.RootGoal != initial.Workflow.RootGoal || len(during.WorkUnits) != 1 || len(during.Actions) != 1 || len(during.Artifacts) != 0 {
		t.Fatalf("reader observed partial generation: %+v", during)
	}
	close(release)
	if err := <-writerResult; err != nil {
		t.Fatalf("updated commit error = %v", err)
	}
	after, err := reader.LoadWorkflowSnapshot(context.Background(), initial.Workflow.ID)
	if err != nil {
		t.Fatalf("LoadWorkflowSnapshot() after commit error = %v", err)
	}
	if after.Workflow.Revision != 2 || after.Workflow.RootGoal != updated.Workflow.RootGoal || len(after.WorkUnits) != 1 || len(after.Actions) != 1 || !reflect.DeepEqual(after.Artifacts, updated.Artifacts) {
		t.Fatalf("reader did not observe complete published generation: %+v", after)
	}
}

func TestWorkflowStoreArtifactRoundTripAndReferenceCompleteness(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := workflowSnapshot(t, "workflow-artifacts", 1, 1, 1)
	snapshot.Workflow.GraphArtifactRefs = []domain.ArtifactReference{{ID: "graph", Kind: "repo_map"}}
	snapshot.Workflow.FinalOutcomeRefs = []domain.ArtifactReference{{ID: "final", Kind: "final_response"}}
	snapshot.WorkUnits[0].InputArtifactRefs = []domain.ArtifactReference{{ID: "input", Kind: "execution"}}
	snapshot.WorkUnits[0].Outcome = &domain.DurableWorkUnitOutcome{
		ArtifactRefs: []domain.ArtifactReference{{ID: "outcome", Kind: "tool_output"}},
		ActionIDs:    []domain.ActionID{snapshot.Actions[0].ID},
	}
	snapshot.Actions[0].Status = domain.ActionStatusSucceeded
	snapshot.Actions[0].StartedAt = time.Unix(1, 0).UTC()
	snapshot.Actions[0].CompletedAt = time.Unix(2, 0).UTC()
	snapshot.Actions[0].ResultArtifactRefs = []domain.ArtifactReference{{ID: "action-result", Kind: "tool_output"}}
	snapshot.Artifacts = []domain.RunArtifact{
		{ID: "graph", Name: "graph", Kind: "repo_map", SchemaVersion: "repo_map.v1", Payload: []byte(`{"entries":[]}`)},
		{ID: "input", Name: "input", Kind: "execution", SchemaVersion: "execution.v1", Payload: []byte(`{"message":"input"}`)},
		{ID: "outcome", Name: "outcome", Kind: "tool_output", SchemaVersion: "tool_output.v1", Payload: []byte(`{"tool_name":"tool","success":true}`)},
		{ID: "action-result", Name: "action-result", Kind: "tool_output", SchemaVersion: "tool_output.v1", Payload: []byte(`{"tool_name":"tool","success":true}`)},
		{ID: "final", Name: "final", Kind: "final_response", SchemaVersion: "final_response.v1", Payload: []byte(`{"response":"done"}`)},
	}
	if err := store.CommitWorkflowSnapshot(context.Background(), 0, snapshot); err != nil {
		t.Fatalf("CommitWorkflowSnapshot() error = %v", err)
	}
	got, err := store.LoadWorkflowSnapshot(context.Background(), snapshot.Workflow.ID)
	if err != nil {
		t.Fatalf("LoadWorkflowSnapshot() error = %v", err)
	}
	if !reflect.DeepEqual(got, snapshot) {
		t.Fatalf("loaded snapshot = %+v, want %+v", got, snapshot)
	}

	snapshot.Actions[0].ResultArtifactRefs = []domain.ArtifactReference{{ID: "missing"}}
	snapshot.Workflow.Revision = 2
	if err := store.CommitWorkflowSnapshot(context.Background(), 1, snapshot); !errors.Is(err, domain.ErrInvalidWorkflow) {
		t.Fatalf("missing artifact reference commit error = %v, want invalid workflow", err)
	}
	invalid := workflowSnapshot(t, "workflow-invalid-artifacts", 1, 1, 0)
	invalid.Artifacts = []domain.RunArtifact{testArtifact("duplicate"), testArtifact("duplicate")}
	if err := domain.ValidateWorkflowSnapshot(invalid); !errors.Is(err, domain.ErrInvalidWorkflow) {
		t.Fatalf("duplicate artifacts validation error = %v, want invalid workflow", err)
	}
	invalid.Artifacts = []domain.RunArtifact{{ID: "invalid-payload", Kind: "final_response", SchemaVersion: "final_response.v1", Payload: []byte(`{"summary":"missing response"}`)}}
	if err := domain.ValidateWorkflowSnapshot(invalid); !errors.Is(err, domain.ErrInvalidWorkflow) {
		t.Fatalf("invalid artifact payload validation error = %v, want invalid workflow", err)
	}
}

func TestWorkflowStoreRejectsCyclesAndInvalidOutcomeActions(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(domain.WorkflowSnapshot) domain.WorkflowSnapshot
	}{
		{
			name: "cycle",
			mutate: func(snapshot domain.WorkflowSnapshot) domain.WorkflowSnapshot {
				snapshot.WorkUnits[0].Dependencies = []domain.DurableWorkUnitID{snapshot.WorkUnits[1].ID}
				return snapshot
			},
		},
		{
			name: "cross unit action",
			mutate: func(snapshot domain.WorkflowSnapshot) domain.WorkflowSnapshot {
				snapshot.WorkUnits[0].Outcome = &domain.DurableWorkUnitOutcome{ActionIDs: []domain.ActionID{snapshot.Actions[1].ID}}
				return snapshot
			},
		},
		{
			name: "nonterminal action",
			mutate: func(snapshot domain.WorkflowSnapshot) domain.WorkflowSnapshot {
				snapshot.Actions[0].Status = domain.ActionStatusExecuting
				snapshot.Actions[0].CompletedAt = time.Time{}
				return snapshot
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, err := NewFileStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			units, actions := 2, 2
			if tc.name == "cycle" {
				actions = 0
			}
			snapshot := tc.mutate(workflowSnapshot(t, "workflow-invalid-"+tc.name, 1, units, actions))
			if err := store.CommitWorkflowSnapshot(context.Background(), 0, snapshot); !errors.Is(err, domain.ErrInvalidWorkflow) {
				t.Fatalf("CommitWorkflowSnapshot() error = %v, want invalid workflow", err)
			}
		})
	}
}

func TestWorkflowStorePreservesDeclaredAndManifestOrdering(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := workflowSnapshot(t, "workflow-order", 1, 3, 2)
	snapshot.WorkUnits = []domain.DurableWorkUnit{
		snapshot.WorkUnits[2],
		snapshot.WorkUnits[0],
		snapshot.WorkUnits[1],
	}
	snapshot.Workflow.WorkUnitIDs = []domain.DurableWorkUnitID{
		snapshot.WorkUnits[0].ID,
		snapshot.WorkUnits[1].ID,
		snapshot.WorkUnits[2].ID,
	}
	snapshot.Actions[0], snapshot.Actions[1] = snapshot.Actions[1], snapshot.Actions[0]
	snapshot.Artifacts = []domain.RunArtifact{testArtifact("artifact-z"), testArtifact("artifact-a")}
	if err := store.CommitWorkflowSnapshot(context.Background(), 0, snapshot); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadWorkflowSnapshot(context.Background(), snapshot.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.Workflow.WorkUnitIDs, []domain.DurableWorkUnitID{loaded.WorkUnits[0].ID, loaded.WorkUnits[1].ID, loaded.WorkUnits[2].ID}) {
		t.Fatalf("loaded work unit order = %+v, want workflow declaration %+v", loaded.WorkUnits, loaded.Workflow.WorkUnitIDs)
	}
	if !reflect.DeepEqual(loaded.Actions, snapshot.Actions) || !reflect.DeepEqual(loaded.Artifacts, snapshot.Artifacts) {
		t.Fatalf("loaded action/artifact order = actions=%+v artifacts=%+v, want actions=%+v artifacts=%+v", loaded.Actions, loaded.Artifacts, snapshot.Actions, snapshot.Artifacts)
	}
}

func TestWorkflowStoreCommittedArtifactCorruptionFailsClosed(t *testing.T) {
	cases := []struct {
		name    string
		corrupt func(t *testing.T, store *FileStore)
	}{
		{
			name: "artifact object",
			corrupt: func(t *testing.T, store *FileStore) {
				manifest, err := store.loadWorkflowManifest()
				if err != nil {
					t.Fatal(err)
				}
				for _, index := range manifest.Workflows {
					for _, hash := range index.Artifacts {
						if err := os.WriteFile(store.workflowObjectPath(hash), []byte("corrupt"), 0o644); err != nil {
							t.Fatal(err)
						}
						return
					}
				}
				t.Fatal("manifest had no artifact")
			},
		},
		{
			name: "artifact index",
			corrupt: func(t *testing.T, store *FileStore) {
				generation := workflowHeadGeneration(t, store)
				if err := os.WriteFile(store.workflowManifestPath(generation), []byte(`{"version":2,"generation":"`+generation+`","workflows":{"workflow-artifact-corrupt":{"workflow":"`+strings.Repeat("0", 64)+`","work_units":{},"actions":{},"action_order":[],"artifacts":{"artifact":"not-a-hash"},"artifact_order":["artifact"]}}}`), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, err := NewFileStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			snapshot := workflowSnapshot(t, "workflow-artifact-corrupt", 1, 1, 0)
			snapshot.Artifacts = []domain.RunArtifact{testArtifact("artifact")}
			if err := store.CommitWorkflowSnapshot(context.Background(), 0, snapshot); err != nil {
				t.Fatal(err)
			}
			tc.corrupt(t, store)
			if _, err := store.LoadWorkflowSnapshot(context.Background(), snapshot.Workflow.ID); !errors.Is(err, errWorkflowStoreCorrupt) {
				t.Fatalf("LoadWorkflowSnapshot() error = %v, want corruption", err)
			}
		})
	}
}

func workflowSnapshot(t *testing.T, workflowID string, revision, units, actions int) domain.WorkflowSnapshot {
	t.Helper()
	unitIDs := make([]domain.DurableWorkUnitID, units)
	for index := range unitIDs {
		unitIDs[index] = domain.DurableWorkUnitID(workflowID + "-unit-" + string(rune('a'+index)))
	}
	workflow, err := domain.NewWorkflow(domain.WorkflowInput{
		ID:           domain.WorkflowID(workflowID),
		Conversation: domain.ConversationReference{ConversationID: domain.ConversationID("conversation-" + workflowID), TurnID: domain.ConversationTurnID("turn-" + workflowID)},
		RootGoal:     "durable workflow test",
		CreatedAt:    time.Date(2026, 7, 10, 11, 0, 0, 0, time.UTC),
		WorkUnitIDs:  unitIDs,
	})
	if err != nil {
		t.Fatal(err)
	}
	workflow.Revision = int64(revision)
	snapshot := domain.WorkflowSnapshot{Workflow: workflow}
	for index, unitID := range unitIDs {
		unit, err := domain.NewDurableWorkUnit(domain.DurableWorkUnitInput{
			ID: unitID, WorkflowID: workflow.ID, Kind: "execute", Phase: domain.RunPhaseExecute, Role: "worker", Task: "durable workflow test", SideEffectClass: domain.SideEffectNone,
		})
		if err != nil {
			t.Fatal(err)
		}
		if index > 0 {
			unit.Dependencies = []domain.DurableWorkUnitID{unitIDs[index-1]}
		}
		snapshot.WorkUnits = append(snapshot.WorkUnits, unit)
	}
	for index := 0; index < actions; index++ {
		unitIndex := index % len(snapshot.WorkUnits)
		unit := snapshot.WorkUnits[unitIndex]
		action, err := domain.NewDurableAction(domain.DurableActionInput{
			ID:                      domain.ActionID(workflowID + "-action-" + string(rune('a'+index))),
			WorkflowID:              workflow.ID,
			WorkUnitID:              unit.ID,
			Attempt:                 1,
			Kind:                    "tool",
			Target:                  "read",
			IdempotencyKey:          workflowID + "-key-" + string(rune('a'+index)),
			Lease:                   domain.LeaseCredential{Token: "lease-token", FencingToken: 1},
			SideEffectClass:         domain.SideEffectNone,
			PreconditionFingerprint: "fingerprint",
		})
		if err != nil {
			t.Fatal(err)
		}
		action.Status = domain.ActionStatusSucceeded
		action.StartedAt = time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
		action.CompletedAt = action.StartedAt.Add(time.Second)
		snapshot.Actions = append(snapshot.Actions, action)
		snapshot.WorkUnits[unitIndex].Status = domain.DurableWorkUnitStatusSucceeded
		snapshot.WorkUnits[unitIndex].ClaimedAt = action.StartedAt.Add(-time.Minute)
		snapshot.WorkUnits[unitIndex].StartedAt = action.StartedAt
		snapshot.WorkUnits[unitIndex].CompletedAt = action.CompletedAt
		snapshot.WorkUnits[unitIndex].LastFencingToken = action.FencingToken
		if snapshot.WorkUnits[unitIndex].Outcome == nil {
			snapshot.WorkUnits[unitIndex].Outcome = &domain.DurableWorkUnitOutcome{}
		}
		snapshot.WorkUnits[unitIndex].Outcome.ActionIDs = append(snapshot.WorkUnits[unitIndex].Outcome.ActionIDs, action.ID)
	}
	if actions > 0 {
		snapshot.Workflow.Status = domain.WorkflowStatusRunning
	}
	if err := domain.ValidateWorkflowSnapshot(snapshot); err != nil {
		t.Fatalf("workflowSnapshot() made invalid snapshot: %v", err)
	}
	return snapshot
}

func testArtifact(id string) domain.RunArtifact {
	return domain.RunArtifact{
		ID:            id,
		Name:          id,
		Kind:          "final_response",
		SchemaVersion: "final_response.v1",
		Payload:       []byte(`{"response":"done"}`),
	}
}

func claimedWorkflowSnapshot(t *testing.T, snapshot domain.WorkflowSnapshot, owner, token string) domain.WorkflowSnapshot {
	t.Helper()
	claimed, err := domain.ClaimReadyBatch(snapshot, domain.WorkflowBatchClaims{
		ExpectedRevision: snapshot.Workflow.Revision,
		Claims: []domain.WorkUnitClaim{{
			UnitID: snapshot.WorkUnits[0].ID,
			Lease:  domain.DurableLease{OwnerID: owner, Token: domain.LeaseToken(token), FencingToken: 1, ExpiresAt: time.Now().Add(time.Minute)},
		}},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return claimed
}

func workflowHeadGeneration(t *testing.T, store *FileStore) string {
	t.Helper()
	head, err := os.ReadFile(store.workflowHeadPath())
	if err != nil {
		t.Fatal(err)
	}
	return string(head[:len(head)-1])
}
