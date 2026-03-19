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

type ToolClass string

const (
	ToolClassObserve ToolClass = "observe"
	ToolClassCompute ToolClass = "compute"
	ToolClassExecute ToolClass = "execute"
	ToolClassMutate  ToolClass = "mutate"
)

type ToolReusePolicy string

const (
	ToolReuseNever     ToolReusePolicy = "never"
	ToolReuseOnSuccess ToolReusePolicy = "on_success"
)

type ToolDuplicatePolicy string

const (
	ToolDuplicateAllow            ToolDuplicatePolicy = "allow"
	ToolDuplicateSuppressInflight ToolDuplicatePolicy = "suppress_inflight"
	ToolDuplicateSuppressSemantic ToolDuplicatePolicy = "suppress_semantic"
)

type ToolFreshnessStrategy string

const (
	ToolFreshnessNone     ToolFreshnessStrategy = "none"
	ToolFreshnessSnapshot ToolFreshnessStrategy = "snapshot"
	ToolFreshnessReadSet  ToolFreshnessStrategy = "read_set"
)

type SideEffectClass string

const (
	SideEffectNone      SideEffectClass = "none"
	SideEffectWorkspace SideEffectClass = "workspace"
	SideEffectProcess   SideEffectClass = "process"
	SideEffectNetwork   SideEffectClass = "network"
	SideEffectExternal  SideEffectClass = "external"
)

type ToolFreshnessPolicy struct {
	Strategy ToolFreshnessStrategy `json:"strategy"`
}

type ToolSemantics struct {
	Class           ToolClass           `json:"class"`
	ReusePolicy     ToolReusePolicy     `json:"reuse_policy"`
	DuplicatePolicy ToolDuplicatePolicy `json:"duplicate_policy"`
	Freshness       ToolFreshnessPolicy `json:"freshness"`
	SideEffectClass SideEffectClass     `json:"side_effect_class"`
	Source          string              `json:"source,omitempty"`
	ReadPathArgs    []string            `json:"read_path_args,omitempty"`
	WritePathArgs   []string            `json:"write_path_args,omitempty"`
	IdentityArgs    []string            `json:"identity_args,omitempty"`
	SourceLimit     int                 `json:"source_limit,omitempty"`
	Stateful        bool                `json:"stateful,omitempty"`
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
	Semantics        ToolSemantics
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
	PacketRole          string
	PacketKind          string
	Observations        []ObservationRecord
	Artifacts           []ArtifactReference
	KnownFailures       []string
	ScopedConstraints   []string
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
