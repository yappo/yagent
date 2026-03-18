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
	Role       Role
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
	AgentID    string
	Metadata   map[string]string
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
	CapabilityGroup  string
	Risk             string
	CostHint         string
	RequiresApproval bool
	DiscoveryOnly    bool
}

type ToolResult struct {
	CallID  string
	Name    string
	Success bool
	Output  string
}

type AgentSpec struct {
	ID                 string
	Name               string
	Description        string
	Instruction        string
	Mode               AgentMode
	AllowedTools       []string
	ReadOnly           bool
	InputSchema        map[string]any
	OutputSchema       map[string]any
	Model              string
	RoutingProfile     string
	Timeout            time.Duration
	MaxTurns           int
	TokenBudget        int
	Tags               []string
	TaskKinds          []TaskKind
	Capabilities       []string
	PreferredPhases    []RunPhase
	ScopeHints         []string
	PhasePolicies      []PhasePolicy
	VerificationPolicy VerificationPolicy
	BuiltIn            bool
	Disabled           bool
	AllowOverride      bool
}

type ToolState struct {
	CurrentAgentID           string   `json:"current_agent_id"`
	ReadOnly                 bool     `json:"read_only"`
	FileWriteAllowed         bool     `json:"file_write_allowed"`
	WriteCapabilityAvailable bool     `json:"write_capability_available"`
	HiddenWriteCapabilities  []string `json:"hidden_write_capabilities,omitempty"`
	VisibleWriteTools        []string `json:"visible_write_tools,omitempty"`
	VisibleMCPTools          []string `json:"visible_mcp_tools,omitempty"`
	TaskDiscoveryAvailable   bool     `json:"task_discovery_available"`
	MCPBindingAvailable      bool     `json:"mcp_binding_available"`
	MCPToolsLazyBind         bool     `json:"mcp_tools_lazy_bind"`
}

type RunContext struct {
	UserGoal            string
	CurrentPhase        RunPhase
	TaskBrief           string
	RecentMessages      []Message
	StableFacts         []string
	RelevantFiles       []string
	ArtifactRefs        []string
	Constraints         []string
	UnresolvedTODOs     []string
	RecentFailures      []string
	VerificationNotes   []string
	RecentSummary       string
	AvailableToolNames  []string
	EnabledCapabilities []string
	ToolState           ToolState
	AgentInventory      []AgentInventoryEntry
	ExpectedOutput      map[string]any
	ResumeSource        string
}

type ContextPack = RunContext

type AgentInvocation struct {
	RunID       string
	ParentRunID string
	Agent       AgentSpec
	Messages    []Message
	Context     RunContext
	Phase       RunPhase
	Attempt     int
	RootRunID   string
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
	Phase        RunPhase
	Attempt      int
	Status       string
	Detail       string
	ArtifactRef  string
	Metrics      map[string]any
	Timestamp    time.Time
	ContextCount int
}

type TurnRequest struct {
	Messages []Message
	Model    string
	Profile  string
	ResumeID string
	Stream   bool
}

type TurnResult struct {
	Message Message
	Events  []ExecutionEvent
	Run     *RunState
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
	Phase        RunPhase
	Model        string
	Stream       bool
	Tools        []ToolDefinition
}

type ModelResponse struct {
	Message      Message
	FinishReason string
}
