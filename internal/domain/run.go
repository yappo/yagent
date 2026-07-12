package domain

import (
	"context"
	"encoding/json"
	"time"
)

type RunPhase string

const (
	RunPhaseIntake   RunPhase = "intake"
	RunPhasePlan     RunPhase = "plan"
	RunPhaseExecute  RunPhase = "execute"
	RunPhaseVerify   RunPhase = "verify"
	RunPhaseRecover  RunPhase = "recover"
	RunPhaseFinalize RunPhase = "finalize"
)

type RunStatus string

const (
	RunStatusQueued    RunStatus = "queued"
	RunStatusRunning   RunStatus = "running"
	RunStatusCompleted RunStatus = "completed"
	// RunStatusNeedsAttention means the runtime completed its bounded recovery
	// loop but the latest verification still failed. The user-facing response is
	// available, but the requested outcome is not verified as complete.
	RunStatusNeedsAttention RunStatus = "needs_attention"
	RunStatusFailed         RunStatus = "failed"
)

type PhasePolicy struct {
	Phase                   RunPhase
	DefaultCapabilityGroups []string
	RequiredAgentIDs        []string
	ForceDelegation         bool
}

type VerificationPolicy struct {
	Required    bool
	MaxAttempts int
}

type ArtifactReference struct {
	ID   string `json:"id"`
	Kind string `json:"kind,omitempty"`
	Name string `json:"name,omitempty"`
}

type PlanNode struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

type ArtifactEnvelope struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	Kind          string              `json:"kind"`
	SchemaVersion string              `json:"schema_version,omitempty"`
	Phase         RunPhase            `json:"phase"`
	AgentID       string              `json:"agent_id,omitempty"`
	Summary       string              `json:"summary,omitempty"`
	Text          string              `json:"text,omitempty"`
	Content       string              `json:"content,omitempty"`
	Payload       json.RawMessage     `json:"payload,omitempty"`
	References    []ArtifactReference `json:"references,omitempty"`
	CreatedAt     time.Time           `json:"created_at"`
}

type RunArtifact = ArtifactEnvelope

type RunCheckpoint struct {
	ID        string    `json:"id"`
	Phase     RunPhase  `json:"phase"`
	Status    RunStatus `json:"status"`
	Attempt   int       `json:"attempt"`
	Summary   string    `json:"summary,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type VerificationRecord struct {
	Attempt     int       `json:"attempt"`
	SourceAgent string    `json:"source_agent"`
	Status      string    `json:"status"`
	Summary     string    `json:"summary,omitempty"`
	RepairBrief string    `json:"repair_brief,omitempty"`
	ArtifactID  string    `json:"artifact_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type VerificationResult = VerificationRecord

type WorkspacePathState struct {
	Path          string    `json:"path"`
	Exists        bool      `json:"exists"`
	IsDir         bool      `json:"is_dir"`
	Size          int64     `json:"size"`
	ModTimeUnix   int64     `json:"mod_time_unix"`
	ContentSHA256 string    `json:"content_sha256,omitempty"`
	ObservedAt    time.Time `json:"observed_at"`
}

type WorkspaceSnapshot struct {
	Revision  int64                         `json:"revision"`
	Paths     map[string]WorkspacePathState `json:"paths,omitempty"`
	UpdatedAt time.Time                     `json:"updated_at"`
}

type WorkspaceFact struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind,omitempty"`
	Summary    string    `json:"summary"`
	ArtifactID string    `json:"artifact_id,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type ObservationSummary struct {
	ObservationID   string    `json:"observation_id"`
	ToolName        string    `json:"tool_name"`
	Summary         string    `json:"summary"`
	ReadSet         []string  `json:"read_set,omitempty"`
	IntegritySHA256 string    `json:"integrity_sha256,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type WorkspaceMemory struct {
	StableFacts          []WorkspaceFact      `json:"stable_facts,omitempty"`
	KnownFailures        []string             `json:"known_failures,omitempty"`
	ReusableObservations []ObservationSummary `json:"reusable_observations,omitempty"`
	RecentArtifacts      []ArtifactReference  `json:"recent_artifacts,omitempty"`
	UpdatedAt            time.Time            `json:"updated_at"`
}

type RepoMemory = WorkspaceMemory

type ObservationRecord struct {
	ID               string               `json:"id"`
	SessionID        string               `json:"session_id,omitempty"`
	ToolName         string               `json:"tool_name"`
	SemanticKey      string               `json:"semantic_key"`
	Summary          string               `json:"summary,omitempty"`
	OutputArtifactID string               `json:"output_artifact_id,omitempty"`
	ReadSet          []string             `json:"read_set,omitempty"`
	PathStates       []WorkspacePathState `json:"path_states,omitempty"`
	SnapshotRevision int64                `json:"snapshot_revision"`
	IntegritySHA256  string               `json:"integrity_sha256,omitempty"`
	Reusable         bool                 `json:"reusable"`
	Stale            bool                 `json:"stale"`
	CreatedAt        time.Time            `json:"created_at"`
	UpdatedAt        time.Time            `json:"updated_at"`
}

type MutationRecord struct {
	ID                  string    `json:"id"`
	SessionID           string    `json:"session_id,omitempty"`
	AgentID             string    `json:"agent_id,omitempty"`
	ExecutionID         string    `json:"execution_id,omitempty"`
	ToolName            string    `json:"tool_name"`
	WriteSet            []string  `json:"write_set,omitempty"`
	MutationFingerprint string    `json:"mutation_fingerprint,omitempty"`
	BeforeRevision      int64     `json:"before_revision"`
	AfterRevision       int64     `json:"after_revision"`
	CreatedAt           time.Time `json:"created_at"`
}

type ToolExecutionRecord struct {
	ID                  string               `json:"id"`
	SessionID           string               `json:"session_id,omitempty"`
	ToolName            string               `json:"tool_name"`
	ToolClass           ToolClass            `json:"tool_class"`
	AgentID             string               `json:"agent_id,omitempty"`
	NormalizedArgs      string               `json:"normalized_args"`
	SemanticKey         string               `json:"semantic_key"`
	WorkspaceRevision   int64                `json:"workspace_revision"`
	ReadSet             []string             `json:"read_set,omitempty"`
	WriteSet            []string             `json:"write_set,omitempty"`
	PathStates          []WorkspacePathState `json:"path_states,omitempty"`
	OutputArtifactID    string               `json:"output_artifact_id,omitempty"`
	ObservationID       string               `json:"observation_id,omitempty"`
	MutationID          string               `json:"mutation_id,omitempty"`
	Success             bool                 `json:"success"`
	Output              string               `json:"output,omitempty"`
	Failure             string               `json:"failure,omitempty"`
	Reusable            bool                 `json:"reusable"`
	Stale               bool                 `json:"stale"`
	Source              string               `json:"source,omitempty"`
	SideEffectClass     SideEffectClass      `json:"side_effect_class,omitempty"`
	MutationFingerprint string               `json:"mutation_fingerprint,omitempty"`
	CreatedAt           time.Time            `json:"created_at"`
	UpdatedAt           time.Time            `json:"updated_at"`
}

type ModelInvocationSettings struct {
	MaxOutputTokens   int      `json:"max_output_tokens,omitempty"`
	Temperature       *float64 `json:"temperature,omitempty"`
	TopP              *float64 `json:"top_p,omitempty"`
	TopK              int      `json:"top_k,omitempty"`
	MinP              *float64 `json:"min_p,omitempty"`
	PresencePenalty   *float64 `json:"presence_penalty,omitempty"`
	RepetitionPenalty *float64 `json:"repetition_penalty,omitempty"`
	ReasoningEffort   string   `json:"reasoning_effort,omitempty"`
	TextVerbosity     string   `json:"text_verbosity,omitempty"`
	ParallelToolCalls *bool    `json:"parallel_tool_calls,omitempty"`
	Store             *bool    `json:"store,omitempty"`
}

type ModelInvocationRecord struct {
	ID                 string                  `json:"id"`
	RunID              string                  `json:"run_id,omitempty"`
	RootRunID          string                  `json:"root_run_id,omitempty"`
	AgentID            string                  `json:"agent_id,omitempty"`
	Phase              RunPhase                `json:"phase,omitempty"`
	Attempt            int                     `json:"attempt,omitempty"`
	ProfileName        string                  `json:"profile_name,omitempty"`
	ServerName         string                  `json:"server_name,omitempty"`
	Fallback           bool                    `json:"fallback,omitempty"`
	FallbackFromServer string                  `json:"fallback_from_server,omitempty"`
	URL                string                  `json:"url,omitempty"`
	API                string                  `json:"api,omitempty"`
	Model              string                  `json:"model,omitempty"`
	ResponseFormat     string                  `json:"response_format,omitempty"`
	Messages           int                     `json:"messages"`
	Tools              int                     `json:"tools"`
	Settings           ModelInvocationSettings `json:"settings,omitempty"`
	Usage              ModelUsage              `json:"usage"`
	DurationMS         int64                   `json:"duration_ms"`
	Success            bool                    `json:"success"`
	FinishReason       string                  `json:"finish_reason,omitempty"`
	Error              string                  `json:"error,omitempty"`
	CreatedAt          time.Time               `json:"created_at"`
}

type ConversationTurnRecord struct {
	ID                       string              `json:"id"`
	ConversationID           ConversationID      `json:"conversation_id"`
	WorkflowID               WorkflowID          `json:"workflow_id"`
	RunID                    string              `json:"run_id,omitempty"`
	RootRunID                string              `json:"root_run_id,omitempty"`
	Profile                  string              `json:"profile,omitempty"`
	Status                   RunStatus           `json:"status"`
	CurrentPhase             RunPhase            `json:"current_phase,omitempty"`
	Attempt                  int                 `json:"attempt,omitempty"`
	UserGoal                 string              `json:"user_goal,omitempty"`
	RequestMessages          []Message           `json:"request_messages,omitempty"`
	ContextMessages          []Message           `json:"context_messages,omitempty"`
	OutputMessage            Message             `json:"output_message,omitempty"`
	Error                    string              `json:"error,omitempty"`
	EventCount               int                 `json:"event_count"`
	ToolCallCount            int                 `json:"tool_call_count"`
	ToolFailureCount         int                 `json:"tool_failure_count"`
	ModelCallCount           int                 `json:"model_call_count"`
	CacheHitCount            int                 `json:"cache_hit_count"`
	VerificationFailureCount int                 `json:"verification_failure_count"`
	ArtifactRefs             []ArtifactReference `json:"artifact_refs,omitempty"`
	StartedAt                time.Time           `json:"started_at"`
	CompletedAt              time.Time           `json:"completed_at"`
}

type WorkUnit struct {
	ID               string              `json:"id"`
	Kind             string              `json:"kind"`
	Role             string              `json:"role"`
	Phase            RunPhase            `json:"phase"`
	Attempt          int                 `json:"attempt,omitempty"`
	Task             string              `json:"task"`
	Status           string              `json:"status"`
	DependsOn        []string            `json:"depends_on,omitempty"`
	ReadSet          []string            `json:"read_set,omitempty"`
	WriteSet         []string            `json:"write_set,omitempty"`
	Source           string              `json:"source,omitempty"`
	SideEffectClass  SideEffectClass     `json:"side_effect_class,omitempty"`
	DuplicateKey     string              `json:"duplicate_key,omitempty"`
	ArtifactRefs     []ArtifactReference `json:"artifact_refs,omitempty"`
	KnownFailureRefs []string            `json:"known_failure_refs,omitempty"`
	StartedAt        time.Time           `json:"started_at,omitempty"`
	CompletedAt      time.Time           `json:"completed_at,omitempty"`
}

type RunState struct {
	ID                  string               `json:"id"`
	RootRunID           string               `json:"root_run_id"`
	ConversationID      ConversationID       `json:"conversation_id"`
	ConversationTurnID  ConversationTurnID   `json:"conversation_turn_id"`
	WorkflowID          WorkflowID           `json:"workflow_id"`
	WorkflowRevision    int64                `json:"workflow_revision"`
	Status              RunStatus            `json:"status"`
	CurrentPhase        RunPhase             `json:"current_phase"`
	Attempt             int                  `json:"attempt"`
	Profile             string               `json:"profile,omitempty"`
	UserGoal            string               `json:"user_goal,omitempty"`
	Messages            []Message            `json:"messages,omitempty"`
	ExecutionPlan       *ExecutionPlan       `json:"execution_plan,omitempty"`
	Plan                []PlanNode           `json:"plan,omitempty"`
	WorkUnits           []WorkUnit           `json:"work_units,omitempty"`
	Artifacts           []RunArtifact        `json:"artifacts,omitempty"`
	Checkpoints         []RunCheckpoint      `json:"checkpoints,omitempty"`
	Verification        []VerificationResult `json:"verification,omitempty"`
	EnabledCapabilities []string             `json:"enabled_capabilities,omitempty"`
	KnownFailures       []string             `json:"known_failures,omitempty"`
	CreatedAt           time.Time            `json:"created_at"`
	UpdatedAt           time.Time            `json:"updated_at"`
}

type CommandMemoryEntry struct {
	Command   string    `json:"command"`
	Cwd       string    `json:"cwd,omitempty"`
	Summary   string    `json:"summary,omitempty"`
	Success   bool      `json:"success"`
	CreatedAt time.Time `json:"created_at"`
}

type RunStateStore interface {
	SaveRun(context.Context, *RunState) error
	LoadRun(context.Context, string) (*RunState, error)
	LoadLatestRun(context.Context) (*RunState, error)
}

type RepoMemoryStore interface {
	LoadMemory(context.Context) (*WorkspaceMemory, error)
	SaveMemory(context.Context, *WorkspaceMemory) error
	RecordCommand(context.Context, CommandMemoryEntry) error
}

type RuntimeStateStore interface {
	RunStateStore
	RepoMemoryStore
	SaveArtifact(context.Context, RunArtifact) error
	SaveObservation(context.Context, ObservationRecord) error
	ListObservations(context.Context, int) ([]ObservationRecord, error)
	SaveExecution(context.Context, ToolExecutionRecord) error
	ListExecutions(context.Context, int) ([]ToolExecutionRecord, error)
	FindReusableExecution(context.Context, string, []string) (*ToolExecutionRecord, error)
	SaveMutation(context.Context, MutationRecord) error
	ListMutations(context.Context, int) ([]MutationRecord, error)
	MarkStaleByPaths(context.Context, []string) error
	LoadWorkspaceSnapshot(context.Context) (*WorkspaceSnapshot, error)
	SaveWorkspaceSnapshot(context.Context, *WorkspaceSnapshot) error
	SaveScratch(context.Context, ScratchRecord) error
	ListScratch(context.Context, int) ([]ScratchRecord, error)
}

type ConversationStore interface {
	SaveConversationTurn(context.Context, ConversationTurnRecord) error
	ListConversationTurns(context.Context, int) ([]ConversationTurnRecord, error)
}

type ContextEngine interface {
	Build(*RunState, AgentSpec, RunPhase, []Message, []ToolDefinition) RunContext
	MaybeCompact(*RunState) (*RunArtifact, bool)
}
