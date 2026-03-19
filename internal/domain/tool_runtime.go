package domain

import "context"

type ToolRuntimeHint struct {
	ReadSet         []string
	WriteSet        []string
	ReplaceAccess   bool
	SideEffectClass SideEffectClass
	Source          string
	SourceLimit     int
}

type ToolRuntimeInspector interface {
	InferRuntime(context.Context, AgentSpec, ToolCall, ToolDefinition) (ToolRuntimeHint, bool)
}
