package domain

import (
	"context"
	"errors"
)

var ErrDurableActionNotExecutable = errors.New("durable action is not executable")

type durableActionExecutionContextKey struct{}

type DurableActionExecutionContext struct {
	ActionID       ActionID
	WorkflowID     WorkflowID
	WorkUnitID     DurableWorkUnitID
	Attempt        int
	IdempotencyKey string
	LeaseToken     LeaseToken
	FencingToken   uint64
}

// DurableActionGuard confirms that a durable action still owns an active lease
// immediately before a provider begins work.
type DurableActionGuard interface {
	ValidateDurableAction(context.Context, DurableActionExecutionContext) error
}

func WithDurableActionExecutionContext(ctx context.Context, execution DurableActionExecutionContext) context.Context {
	return context.WithValue(ctx, durableActionExecutionContextKey{}, execution)
}

func DurableActionExecutionContextFrom(ctx context.Context) (DurableActionExecutionContext, bool) {
	execution, ok := ctx.Value(durableActionExecutionContextKey{}).(DurableActionExecutionContext)
	return execution, ok
}

type Tool interface {
	Definition() ToolDefinition
	Execute(context.Context, ToolCall) ToolResult
}

type DynamicToolProvider interface {
	Definitions(AgentSpec) []ToolDefinition
	Execute(context.Context, AgentSpec, ToolCall) (ToolResult, bool)
}

type ToolExecutor interface {
	Definitions(agent AgentSpec) []ToolDefinition
	Execute(context.Context, AgentSpec, ToolCall) ToolResult
}

type ModelClient interface {
	Generate(context.Context, ModelRequest) (ModelResponse, error)
}

type AgentRunner interface {
	Run(context.Context, AgentInvocation) (AgentResult, error)
}

type TraceSink interface {
	Append(context.Context, ExecutionEvent) error
}

type StructuredLogSink interface {
	WriteRecord(context.Context, string, map[string]any) error
}

type ExecutionEventStream interface {
	SubscribeEvents() (<-chan ExecutionEvent, func())
}

type ToolEvent struct {
	Phase  string
	Call   ToolCall
	Result ToolResult
}

type ToolObserver interface {
	OnToolEvent(context.Context, ToolEvent)
}

type ObservableOrchestrator interface {
	Orchestrator
	SetObserver(ToolObserver)
}
