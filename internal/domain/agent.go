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
	Role       Role              `json:"role"`
	Content    string            `json:"content,omitempty"`
	ToolCalls  []ToolCall        `json:"tool_calls,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
	AgentID    string            `json:"agent_id,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type ToolCall struct {
	ID                 string         `json:"id,omitempty"`
	Name               string         `json:"name"`
	Arguments          map[string]any `json:"arguments,omitempty"`
	RequestedByAgentID string         `json:"requested_by_agent_id,omitempty"`
	Purpose            string         `json:"purpose,omitempty"`
	RunID              string         `json:"run_id,omitempty"`
	RootRunID          string         `json:"root_run_id,omitempty"`
	Phase              RunPhase       `json:"phase,omitempty"`
	Attempt            int            `json:"attempt,omitempty"`
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
	Class            ToolClass           `json:"class"`
	ReusePolicy      ToolReusePolicy     `json:"reuse_policy"`
	DuplicatePolicy  ToolDuplicatePolicy `json:"duplicate_policy"`
	Freshness        ToolFreshnessPolicy `json:"freshness"`
	SideEffectClass  SideEffectClass     `json:"side_effect_class"`
	Source           string              `json:"source,omitempty"`
	ReadPathArgs     []string            `json:"read_path_args,omitempty"`
	WritePathArgs    []string            `json:"write_path_args,omitempty"`
	IdentityArgs     []string            `json:"identity_args,omitempty"`
	IdentityDefaults map[string]any      `json:"identity_defaults,omitempty"`
	SourceLimit      int                 `json:"source_limit,omitempty"`
	Stateful         bool                `json:"stateful,omitempty"`
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
	MaxToolCalls       int
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
	UserGoal              string
	CurrentPhase          RunPhase
	TaskBrief             string
	RecentMessages        []Message
	PacketRole            string
	PacketKind            string
	PacketBudgetTokens    int
	PacketEstimatedTokens int
	Observations          []ObservationRecord
	Artifacts             []ArtifactReference
	KnownFailures         []string
	ScopedConstraints     []string
	StableFacts           []string
	RelevantFiles         []string
	ArtifactRefs          []string
	Constraints           []string
	UnresolvedTODOs       []string
	RecentFailures        []string
	VerificationNotes     []string
	AvailableToolNames    []string
	EnabledCapabilities   []string
	ToolState             ToolState
	AgentInventory        []AgentInventoryEntry
	ExpectedOutput        map[string]any
	ResumeSource          string
}

type ContextPack = RunContext

type AgentInvocation struct {
	RunID          string
	ParentRunID    string
	WorkflowID     WorkflowID
	WorkUnitID     DurableWorkUnitID
	Lease          LeaseCredential
	Agent          AgentSpec
	Messages       []Message
	Context        RunContext
	Phase          RunPhase
	Attempt        int
	RootRunID      string
	Model          string
	Stream         bool
	ResponseFormat *ResponseFormat
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
	Display      string
	ArtifactRef  string
	Metrics      map[string]any
	Timestamp    time.Time
	ContextCount int
}

type TurnRequest struct {
	Messages   []Message
	Provenance []ProvenanceEvidence
	Model      string
	Profile    string
	Stream     bool
}

type ConversationTurnRequest struct {
	ConversationID ConversationID
	Messages       []Message
	Provenance     []ProvenanceEvidence
	Model          string
	Profile        string
	Stream         bool
}

type WorkflowRecoveryRequest struct {
	WorkflowID WorkflowID
}

type ProvenanceSource string

const (
	ProvenancePlannerReason  ProvenanceSource = "planner_reason"
	ProvenanceFileOutput     ProvenanceSource = "file_output"
	ProvenanceDelegation     ProvenanceSource = "delegation"
	ProvenanceMCPResponse    ProvenanceSource = "mcp_response"
	ProvenancePriorAssistant ProvenanceSource = "prior_assistant"
	ProvenancePriorTool      ProvenanceSource = "prior_tool"
	ProvenancePriorSystem    ProvenanceSource = "prior_system"
)

// ProvenanceEvidence carries untrusted runtime input separately from the root
// user message. Tool-backed sources retain the model tool protocol identifiers.
type ProvenanceEvidence struct {
	Source     ProvenanceSource `json:"source" toml:"source"`
	Content    string           `json:"content" toml:"content"`
	ToolCallID string           `json:"tool_call_id,omitempty" toml:"tool_call_id"`
	ToolName   string           `json:"tool_name,omitempty" toml:"tool_name"`
	AgentID    string           `json:"agent_id,omitempty" toml:"agent_id"`
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
	ContinueConversation(ctx context.Context, request ConversationTurnRequest) (TurnResult, error)
	RecoverWorkflow(ctx context.Context, request WorkflowRecoveryRequest) (TurnResult, error)
}

type ModelRequest struct {
	RunID          string
	RootRunID      string
	Attempt        int
	Agent          AgentSpec
	Instructions   string
	Messages       []Message
	Phase          RunPhase
	Model          string
	Stream         bool
	StreamHandler  ModelStreamHandler
	Tools          []ToolDefinition
	ResponseFormat *ResponseFormat
	Settings       ModelSettings
}

type ModelStreamHandler func(ModelStreamEvent)

type ModelStreamEvent struct {
	Type         string
	ContentDelta string
	RawEventType string
}

type ModelResponse struct {
	Message      Message
	FinishReason string
	Invocation   ModelInvocationMetadata
}

type ModelInvocationMetadata struct {
	ServerName         string
	Fallback           bool
	FallbackFromServer string
	API                string
	Model              string
	ProfileName        string
	DurationMS         int64
	Usage              ModelUsage
	Attempts           []ModelInvocationAttempt
}

// ModelInvocationAttempt records one transport request within a logical model call.
type ModelInvocationAttempt struct {
	ServerName         string
	Fallback           bool
	FallbackFromServer string
	API                string
	Model              string
	ProfileName        string
	DurationMS         int64
	Usage              ModelUsage
	Success            bool
	Error              string
}

type ModelUsage struct {
	Available         bool `json:"available"`
	InputTokens       int  `json:"input_tokens,omitempty"`
	OutputTokens      int  `json:"output_tokens,omitempty"`
	TotalTokens       int  `json:"total_tokens,omitempty"`
	CachedInputTokens int  `json:"cached_input_tokens,omitempty"`
	ReasoningTokens   int  `json:"reasoning_tokens,omitempty"`
}

type ModelSettings struct {
	MaxOutputTokens   int
	Temperature       *float64
	TopP              *float64
	TopK              int
	MinP              *float64
	PresencePenalty   *float64
	RepetitionPenalty *float64
	ReasoningEffort   string
	TextVerbosity     string
	ParallelToolCalls *bool
	Store             *bool
}

type ResponseFormat struct {
	Type   string
	Name   string
	Schema map[string]any
	Strict bool
}
