package domain

import (
	"context"
	"time"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type AgentMode string

const (
	AgentModeManager AgentMode = "manager"
	AgentModeTool    AgentMode = "tool"
	AgentModeHandoff AgentMode = "handoff"
)

type Message struct {
	Role      Role
	Content   string
	ToolCalls []ToolCall
	AgentID   string
	Metadata  map[string]string
}

type ToolCall struct {
	ID                 string
	Name               string
	Arguments          map[string]any
	RequestedByAgentID string
	Purpose            string
}

type ToolDefinition struct {
	Name             string
	Description      string
	Parameters       map[string]any
	Metadata         map[string]any
	ReadOnly         bool
	ParallelSafe     bool
	MutatesWorkspace bool
}

type ToolResult struct {
	CallID  string
	Name    string
	Success bool
	Output  string
}

type AgentSpec struct {
	ID            string
	Name          string
	Description   string
	Instruction   string
	Mode          AgentMode
	AllowedTools  []string
	ReadOnly      bool
	InputSchema   map[string]any
	OutputSchema  map[string]any
	Model         string
	Timeout       time.Duration
	MaxTurns      int
	TokenBudget   int
	Tags          []string
	BuiltIn       bool
	Disabled      bool
	AllowOverride bool
}

type ContextPack struct {
	UserGoal           string
	TaskBrief          string
	RelevantFiles      []string
	ArtifactRefs       []string
	Constraints        []string
	RecentSummary      string
	AvailableToolNames []string
	ExpectedOutput     map[string]any
}

type AgentInvocation struct {
	RunID       string
	ParentRunID string
	Agent       AgentSpec
	Messages    []Message
	Context     ContextPack
	Model       string
	Stream      bool
}

type AgentResult struct {
	Status    string
	Message   Message
	Summary   string
	Artifacts map[string]any
	Events    []ExecutionEvent
}

type ExecutionEvent struct {
	RunID        string
	ParentRunID  string
	AgentID      string
	Type         string
	Detail       string
	Timestamp    time.Time
	ContextCount int
}

type TurnRequest struct {
	Messages []Message
	Model    string
	Stream   bool
}

type TurnResult struct {
	Message Message
	Events  []ExecutionEvent
}

type AgentCatalog interface {
	List() []AgentSpec
	Resolve(id string) (AgentSpec, bool)
	LoadUserAgents(paths []string) error
}

type Orchestrator interface {
	RunTurn(ctx context.Context, request TurnRequest) (TurnResult, error)
}

type ModelRequest struct {
	Agent        AgentSpec
	Instructions string
	Messages     []Message
	Model        string
	Stream       bool
	Tools        []ToolDefinition
}

type ModelResponse struct {
	Message      Message
	FinishReason string
}
