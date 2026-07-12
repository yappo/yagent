package durablefence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
)

const MCPMetadataKey = "dev.yagent/durable-action"

var (
	ErrStaleFence      = errors.New("durable fence is stale")
	ErrFenceConflict   = errors.New("durable fence conflicts with active action")
	ErrActionInFlight  = errors.New("durable action is already in flight")
	ErrActionNotActive = errors.New("durable action is not active")
)

type Action struct {
	ActionID       string
	WorkflowID     string
	WorkUnitID     string
	IdempotencyKey string
	LeaseToken     string
	FencingToken   uint64
}

type Status string

const (
	StatusPrepared  Status = "prepared"
	StatusCompleted Status = "completed"
)

type Record struct {
	Version        uint64
	Action         Action
	Status         Status
	ResultMetadata map[string]any
}

type Store interface {
	Load(context.Context, string) (Record, bool, error)
	CompareAndSwap(context.Context, string, uint64, Record) (Record, error)
}

type Gate struct {
	Store       Store
	MaxAttempts int
}

type Decision struct {
	Execute        bool
	ReplayMetadata map[string]any
}

type InvocationResult struct {
	Executed bool
	Metadata map[string]any
}

// Invoke wraps a provider-owned effect. The provider still owns the resource
// transaction and must make that effect idempotent or atomic with its fence
// record; this package cannot make an arbitrary external effect atomic. An
// error from effect deliberately leaves the action prepared and non-retryable.
func (g Gate) Invoke(ctx context.Context, scope string, action Action, effect func(context.Context) (map[string]any, error)) (InvocationResult, error) {
	decision, err := g.Begin(ctx, scope, action)
	if err != nil {
		return InvocationResult{}, err
	}
	if !decision.Execute {
		return InvocationResult{Metadata: cloneMetadata(decision.ReplayMetadata)}, nil
	}
	result, err := effect(ctx)
	if err != nil {
		return InvocationResult{}, err
	}
	if err := g.Complete(ctx, scope, action, result); err != nil {
		return InvocationResult{}, err
	}
	return InvocationResult{Executed: true, Metadata: action.Acknowledgement()}, nil
}

func ParseMCPMetadata(metadata map[string]any) (Action, error) {
	raw, ok := metadata[MCPMetadataKey]
	if !ok {
		return Action{}, fmt.Errorf("%s metadata is required", MCPMetadataKey)
	}
	value, ok := raw.(map[string]any)
	if !ok {
		return Action{}, fmt.Errorf("%s metadata must be an object", MCPMetadataKey)
	}
	action := Action{
		ActionID:       stringField(value, "action_id"),
		WorkflowID:     stringField(value, "workflow_id"),
		WorkUnitID:     stringField(value, "work_unit_id"),
		IdempotencyKey: stringField(value, "idempotency_key"),
		LeaseToken:     stringField(value, "lease_token"),
		FencingToken:   uint64Field(value["fencing_token"]),
	}
	if action.ActionID == "" || action.WorkflowID == "" || action.WorkUnitID == "" || action.IdempotencyKey == "" || action.LeaseToken == "" || action.FencingToken == 0 {
		return Action{}, fmt.Errorf("%s metadata has missing action identity or fence", MCPMetadataKey)
	}
	return action, nil
}

func (a Action) Acknowledgement() map[string]any {
	return map[string]any{
		MCPMetadataKey: map[string]any{
			"action_id": a.ActionID, "workflow_id": a.WorkflowID, "work_unit_id": a.WorkUnitID,
			"idempotency_key": a.IdempotencyKey, "lease_token": a.LeaseToken, "fencing_token": a.FencingToken,
		},
	}
}

func (g Gate) Begin(ctx context.Context, scope string, action Action) (Decision, error) {
	if g.Store == nil || strings.TrimSpace(scope) == "" {
		return Decision{}, fmt.Errorf("durable fence store and scope are required")
	}
	if err := validateAction(action); err != nil {
		return Decision{}, err
	}
	attempts := g.MaxAttempts
	if attempts < 1 {
		attempts = 8
	}
	for range attempts {
		current, exists, err := g.Store.Load(ctx, scope)
		if err != nil {
			return Decision{}, err
		}
		if exists {
			switch {
			case action.FencingToken < current.Action.FencingToken:
				return Decision{}, fmt.Errorf("%w: token=%d latest=%d", ErrStaleFence, action.FencingToken, current.Action.FencingToken)
			case action.FencingToken == current.Action.FencingToken && !sameAction(action, current.Action):
				return Decision{}, fmt.Errorf("%w: token=%d", ErrFenceConflict, action.FencingToken)
			case action.FencingToken == current.Action.FencingToken && current.Status == StatusCompleted:
				return Decision{ReplayMetadata: cloneMetadata(current.ResultMetadata)}, nil
			case action.FencingToken == current.Action.FencingToken:
				return Decision{}, fmt.Errorf("%w: %s", ErrActionInFlight, action.ActionID)
			}
		}
		expected := uint64(0)
		if exists {
			expected = current.Version
		}
		if _, err := g.Store.CompareAndSwap(ctx, scope, expected, Record{Action: action, Status: StatusPrepared}); err == nil {
			return Decision{Execute: true}, nil
		}
	}
	return Decision{}, fmt.Errorf("durable fence compare-and-swap did not converge")
}

func (g Gate) Complete(ctx context.Context, scope string, action Action, resultMetadata map[string]any) error {
	if g.Store == nil || strings.TrimSpace(scope) == "" {
		return fmt.Errorf("durable fence store and scope are required")
	}
	if err := validateAction(action); err != nil {
		return err
	}
	current, exists, err := g.Store.Load(ctx, scope)
	if err != nil {
		return err
	}
	if !exists || current.Status != StatusPrepared || !sameAction(action, current.Action) {
		return fmt.Errorf("%w: %s", ErrActionNotActive, action.ActionID)
	}
	_, err = g.Store.CompareAndSwap(ctx, scope, current.Version, Record{Action: action, Status: StatusCompleted, ResultMetadata: cloneMetadata(resultMetadata)})
	return err
}

func validateAction(action Action) error {
	if action.ActionID == "" || action.WorkflowID == "" || action.WorkUnitID == "" || action.IdempotencyKey == "" || action.LeaseToken == "" || action.FencingToken == 0 {
		return errors.New("durable action identity and fence are required")
	}
	return nil
}

func sameAction(left, right Action) bool {
	return left.ActionID == right.ActionID && left.WorkflowID == right.WorkflowID && left.WorkUnitID == right.WorkUnitID && left.IdempotencyKey == right.IdempotencyKey && left.LeaseToken == right.LeaseToken && left.FencingToken == right.FencingToken
}

func cloneMetadata(metadata map[string]any) map[string]any {
	if metadata == nil {
		return nil
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return nil
	}
	var copy map[string]any
	if json.Unmarshal(data, &copy) != nil {
		return nil
	}
	return copy
}

func stringField(value map[string]any, key string) string {
	item, _ := value[key].(string)
	return strings.TrimSpace(item)
}

func uint64Field(value any) uint64 {
	switch item := value.(type) {
	case uint64:
		return item
	case int:
		if item > 0 {
			return uint64(item)
		}
	case int64:
		if item > 0 {
			return uint64(item)
		}
	case float64:
		if item > 0 && item <= math.MaxUint64 && math.Trunc(item) == item {
			return uint64(item)
		}
	}
	return 0
}
