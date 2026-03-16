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

type ExecutionPhase string

const (
	ExecutionPhasePlan       ExecutionPhase = "plan"
	ExecutionPhaseGather     ExecutionPhase = "gather"
	ExecutionPhaseSynthesize ExecutionPhase = "synthesize"
	ExecutionPhaseFinish     ExecutionPhase = "finish"
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
	Phase              ExecutionPhase
	UserGoal           string
	TaskBrief          string
	RelevantFiles      []string
	FileSummaries      []string
	ArtifactRefs       []string
	Constraints        []string
	RecentSummary      string
	AvailableToolNames []string
	ExpectedOutput     map[string]any
	ApprovedPlan       string
	OpenQuestions      []string
	Findings           []string
	RecentObservations []string
}

type PlannedToolCall struct {
	Name      string
	Arguments map[string]any
}

type PlannedBatch struct {
	Purpose   string
	ToolCalls []PlannedToolCall
}

type ExecutionPlan struct {
	Summary        string
	TargetFiles    []string
	ExitConditions []string
	Batches        []PlannedBatch
}

type CapabilityDescriptor struct {
	Name string
	Kind string
}

type NoveltyDecision struct {
	NewInformation bool
	Reason         string
}

type ToolObservation struct {
	ToolName    string
	Capability  CapabilityDescriptor
	Target      string
	Discovered  []string
	Fingerprint string
	Summary     string
	Cached      bool
	Changed     bool
}

type WorkingSet struct {
	Phase               ExecutionPhase
	Plan                *ExecutionPlan
	OpenQuestions       []string
	Targets             []string
	ObservedResources   []string
	PendingTargets      []string
	Artifacts           []string
	Findings            []string
	Summaries           []string
	RecentObservations  []ToolObservation
	NoveltyState        string
	RepeatCounts        map[string]int
	SeenFingerprints    map[string]string
	NoNoveltyIterations int
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
