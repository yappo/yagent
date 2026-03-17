package domain

type TaskKind string

const (
	TaskKindUnknown  TaskKind = "unknown"
	TaskKindCasual   TaskKind = "casual"
	TaskKindQuestion TaskKind = "question"
	TaskKindResearch TaskKind = "research"
	TaskKindDocs     TaskKind = "docs"
	TaskKindReview   TaskKind = "review"
	TaskKindTest     TaskKind = "test"
	TaskKindMutate   TaskKind = "mutate"
)

type AgentInventoryEntry struct {
	AgentID            string             `json:"agent_id"`
	Name               string             `json:"name"`
	Description        string             `json:"description,omitempty"`
	Mode               AgentMode          `json:"mode"`
	ReadOnly           bool               `json:"read_only"`
	TaskKinds          []TaskKind         `json:"task_kinds,omitempty"`
	Capabilities       []string           `json:"capabilities,omitempty"`
	PreferredPhases    []RunPhase         `json:"preferred_phases,omitempty"`
	ScopeHints         []string           `json:"scope_hints,omitempty"`
	AllowedToolGroups  []string           `json:"allowed_tool_groups,omitempty"`
	RoutingProfile     string             `json:"routing_profile,omitempty"`
	VerificationPolicy VerificationPolicy `json:"verification_policy,omitempty"`
}

type PlannedAgentAssignment struct {
	AgentID string `json:"agent_id"`
	Reason  string `json:"reason,omitempty"`
}

type PlannedStep struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Phase   RunPhase `json:"phase"`
	AgentID string   `json:"agent_id,omitempty"`
}

type ExecutionPlan struct {
	Version              string                   `json:"version"`
	Mode                 string                   `json:"mode"`
	TaskKind             TaskKind                 `json:"task_kind"`
	Summary              string                   `json:"summary,omitempty"`
	Plan                 *PlannedAgentAssignment  `json:"plan,omitempty"`
	Preparation          []PlannedAgentAssignment `json:"preparation,omitempty"`
	Primary              PlannedAgentAssignment   `json:"primary"`
	Verify               []PlannedAgentAssignment `json:"verify,omitempty"`
	Recovery             *PlannedAgentAssignment  `json:"recovery,omitempty"`
	Finalize             *PlannedAgentAssignment  `json:"finalize,omitempty"`
	Steps                []PlannedStep            `json:"steps,omitempty"`
	RequiredCapabilities []string                 `json:"required_capabilities,omitempty"`
	Source               string                   `json:"source,omitempty"`
	FallbackReason       string                   `json:"fallback_reason,omitempty"`
}
