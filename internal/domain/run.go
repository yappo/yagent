package domain

import (
	"context"
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
	RunStatusFailed    RunStatus = "failed"
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

type PlanNode struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

type RunArtifact struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Phase     RunPhase  `json:"phase"`
	AgentID   string    `json:"agent_id,omitempty"`
	Summary   string    `json:"summary,omitempty"`
	Content   string    `json:"content,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type RunCheckpoint struct {
	ID        string    `json:"id"`
	Phase     RunPhase  `json:"phase"`
	Status    RunStatus `json:"status"`
	Attempt   int       `json:"attempt"`
	Summary   string    `json:"summary,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type VerificationResult struct {
	Attempt     int       `json:"attempt"`
	SourceAgent string    `json:"source_agent"`
	Status      string    `json:"status"`
	Summary     string    `json:"summary,omitempty"`
	RepairBrief string    `json:"repair_brief,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type RunState struct {
	ID                  string               `json:"id"`
	RootRunID           string               `json:"root_run_id"`
	Status              RunStatus            `json:"status"`
	CurrentPhase        RunPhase             `json:"current_phase"`
	Attempt             int                  `json:"attempt"`
	Profile             string               `json:"profile,omitempty"`
	UserGoal            string               `json:"user_goal,omitempty"`
	ConversationSummary string               `json:"conversation_summary,omitempty"`
	Messages            []Message            `json:"messages,omitempty"`
	Plan                []PlanNode           `json:"plan,omitempty"`
	Artifacts           []RunArtifact        `json:"artifacts,omitempty"`
	Checkpoints         []RunCheckpoint      `json:"checkpoints,omitempty"`
	Verification        []VerificationResult `json:"verification,omitempty"`
	EnabledCapabilities []string             `json:"enabled_capabilities,omitempty"`
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

type RepoMemory struct {
	SuccessfulCommands []CommandMemoryEntry `json:"successful_commands,omitempty"`
	FailurePatterns    []string             `json:"failure_patterns,omitempty"`
	Constraints        []string             `json:"constraints,omitempty"`
	RecentArtifacts    []string             `json:"recent_artifacts,omitempty"`
	UpdatedAt          time.Time            `json:"updated_at"`
}

type RunStateStore interface {
	SaveRun(context.Context, *RunState) error
	LoadRun(context.Context, string) (*RunState, error)
	LoadLatestRun(context.Context) (*RunState, error)
}

type RepoMemoryStore interface {
	LoadMemory(context.Context) (*RepoMemory, error)
	SaveMemory(context.Context, *RepoMemory) error
	RecordCommand(context.Context, CommandMemoryEntry) error
}

type ContextEngine interface {
	Build(*RunState, AgentSpec, RunPhase, []Message, []ToolDefinition) RunContext
	MaybeCompact(*RunState) (*RunArtifact, bool)
}
