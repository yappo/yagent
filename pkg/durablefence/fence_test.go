package durablefence

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type memoryStore struct {
	mu      sync.Mutex
	records map[string]Record
}

func (s *memoryStore) Load(_ context.Context, scope string) (Record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[scope]
	return record, ok, nil
}

func (s *memoryStore) CompareAndSwap(_ context.Context, scope string, expected uint64, next Record) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.records[scope]
	if exists && current.Version != expected {
		return Record{}, errors.New("revision conflict")
	}
	if !exists && expected != 0 {
		return Record{}, errors.New("revision conflict")
	}
	next.Version = expected + 1
	s.records[scope] = next
	return next, nil
}

func TestGateRejectsStaleFenceAndReplaysCompletedAction(t *testing.T) {
	store := &memoryStore{records: map[string]Record{}}
	gate := Gate{Store: store}
	action := Action{ActionID: "action-2", WorkflowID: "workflow", WorkUnitID: "unit", IdempotencyKey: "idem-2", LeaseToken: "lease-2", FencingToken: 2}
	decision, err := gate.Begin(context.Background(), "document:readme", action)
	if err != nil || !decision.Execute {
		t.Fatalf("Begin() = %+v, %v", decision, err)
	}
	if err := gate.Complete(context.Background(), "document:readme", action, map[string]any{"result": "written"}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	replay, err := gate.Begin(context.Background(), "document:readme", action)
	if err != nil || replay.Execute || replay.ReplayMetadata["result"] != "written" {
		t.Fatalf("replay = %+v, %v", replay, err)
	}
	stale := action
	stale.FencingToken = 1
	if _, err := gate.Begin(context.Background(), "document:readme", stale); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("stale fence error = %v", err)
	}
}

func TestGateRejectsDuplicatePreparedActionAndFenceConflict(t *testing.T) {
	store := &memoryStore{records: map[string]Record{}}
	gate := Gate{Store: store}
	action := Action{ActionID: "action-2", WorkflowID: "workflow", WorkUnitID: "unit", IdempotencyKey: "idem-2", LeaseToken: "lease-2", FencingToken: 2}
	if _, err := gate.Begin(context.Background(), "document:readme", action); err != nil {
		t.Fatal(err)
	}
	if _, err := gate.Begin(context.Background(), "document:readme", action); !errors.Is(err, ErrActionInFlight) {
		t.Fatalf("duplicate prepared error = %v", err)
	}
	conflict := action
	conflict.ActionID = "different"
	if _, err := gate.Begin(context.Background(), "document:readme", conflict); !errors.Is(err, ErrFenceConflict) {
		t.Fatalf("fence conflict error = %v", err)
	}
}

func TestParseMCPMetadataAndAcknowledgement(t *testing.T) {
	metadata := map[string]any{MCPMetadataKey: map[string]any{
		"action_id": "action", "workflow_id": "workflow", "work_unit_id": "unit", "idempotency_key": "idem", "lease_token": "lease", "fencing_token": float64(3),
	}}
	action, err := ParseMCPMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	ack := action.Acknowledgement()
	parsed, err := ParseMCPMetadata(ack)
	if err != nil || parsed != action {
		t.Fatalf("acknowledgement round trip = %+v, %v", parsed, err)
	}
}

func TestInvokeDoesNotRepeatProviderEffectAfterCompletion(t *testing.T) {
	store := &memoryStore{records: map[string]Record{}}
	gate := Gate{Store: store}
	action := Action{ActionID: "action", WorkflowID: "workflow", WorkUnitID: "unit", IdempotencyKey: "idem", LeaseToken: "lease", FencingToken: 1}
	calls := 0
	effect := func(context.Context) (map[string]any, error) {
		calls++
		return map[string]any{"output": "committed"}, nil
	}
	first, err := gate.Invoke(context.Background(), "resource", action, effect)
	if err != nil || !first.Executed || calls != 1 {
		t.Fatalf("first invoke = %+v err=%v calls=%d", first, err, calls)
	}
	second, err := gate.Invoke(context.Background(), "resource", action, effect)
	if err != nil || second.Executed || calls != 1 {
		t.Fatalf("replay invoke = %+v err=%v calls=%d", second, err, calls)
	}
}
