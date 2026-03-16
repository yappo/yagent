package domain

import "context"

type Tool interface {
	Definition() ToolDefinition
	Execute(context.Context, ToolCall) ToolResult
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

type ExecutionEventStream interface {
	SubscribeEvents() (<-chan ExecutionEvent, func())
}
