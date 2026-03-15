package domain

import "context"

type ToolDefinition struct {
	Name        string
	Description string
	Parameters  map[string]any
}

type ToolResult struct {
	CallID  string
	Name    string
	Success bool
	Output  string
}

type Tool interface {
	Definition() ToolDefinition
	Execute(context.Context, ToolCall) ToolResult
}

type ToolRegistry interface {
	Definitions() []ToolDefinition
	Execute(context.Context, ToolCall) ToolResult
}
