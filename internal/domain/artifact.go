package domain

import (
	"encoding/json"
	"time"
)

type AgentInventoryArtifactPayload struct {
	Agents []AgentInventoryEntry `json:"agents"`
}

type ExecutionPlanArtifactPayload struct {
	Plan *ExecutionPlan `json:"plan,omitempty"`
}

// WorkflowInputArtifactPayload freezes the request-scoped inputs needed to
// resume a workflow after the creating process is gone.
type WorkflowInputArtifactPayload struct {
	Messages            []Message `json:"messages"`
	Model               string    `json:"model,omitempty"`
	Profile             string    `json:"profile,omitempty"`
	EnabledCapabilities []string  `json:"enabled_capabilities,omitempty"`
	Stream              bool      `json:"stream,omitempty"`
}

type RepoMapEntry struct {
	Path        string   `json:"path"`
	Kind        string   `json:"kind,omitempty"`
	Source      string   `json:"source,omitempty"`
	Observation string   `json:"observation,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

type RepoMapArtifactPayload struct {
	Entries []RepoMapEntry `json:"entries,omitempty"`
}

type AgentMessageArtifactPayload struct {
	Message       string   `json:"message"`
	Summary       string   `json:"summary,omitempty"`
	RelevantFiles []string `json:"relevant_files,omitempty"`
}

type EvidenceBundleEntry struct {
	Artifact ArtifactReference `json:"artifact"`
	AgentID  string            `json:"agent_id,omitempty"`
	Summary  string            `json:"summary,omitempty"`
}

type EvidenceBundleArtifactPayload struct {
	Entries []EvidenceBundleEntry `json:"entries,omitempty"`
}

type ReviewFindingsArtifactPayload struct {
	Result  VerificationRecord `json:"result"`
	Message string             `json:"message,omitempty"`
}

type PermissionDecisionRecord struct {
	RunID        string             `json:"run_id,omitempty"`
	RootRunID    string             `json:"root_run_id,omitempty"`
	Phase        RunPhase           `json:"phase,omitempty"`
	Attempt      int                `json:"attempt,omitempty"`
	AgentID      string             `json:"agent_id,omitempty"`
	ToolName     string             `json:"tool_name,omitempty"`
	Operation    string             `json:"operation,omitempty"`
	Resource     string             `json:"resource,omitempty"`
	Action       string             `json:"action,omitempty"`
	ResourceKind string             `json:"resource_kind,omitempty"`
	Risk         string             `json:"risk,omitempty"`
	Scope        string             `json:"scope,omitempty"`
	Summary      string             `json:"summary,omitempty"`
	SideEffects  []string           `json:"side_effects,omitempty"`
	Purpose      string             `json:"purpose,omitempty"`
	Task         string             `json:"task,omitempty"`
	PreviewKind  string             `json:"preview_kind,omitempty"`
	PreviewLines int                `json:"preview_lines,omitempty"`
	ChangeFiles  int                `json:"change_files,omitempty"`
	Additions    int                `json:"additions,omitempty"`
	Deletions    int                `json:"deletions,omitempty"`
	Decision     PermissionDecision `json:"decision,omitempty"`
	Error        string             `json:"error,omitempty"`
	CreatedAt    time.Time          `json:"created_at"`
}

type PermissionAuditArtifactPayload struct {
	SessionID string                     `json:"session_id,omitempty"`
	Records   []PermissionDecisionRecord `json:"records,omitempty"`
}

type ChangeSetFile struct {
	Path                string              `json:"path"`
	Operation           string              `json:"operation,omitempty"`
	ToolName            string              `json:"tool_name,omitempty"`
	MutationFingerprint string              `json:"mutation_fingerprint,omitempty"`
	ExecutionID         string              `json:"execution_id,omitempty"`
	MutationID          string              `json:"mutation_id,omitempty"`
	Artifacts           []ArtifactReference `json:"artifacts,omitempty"`
}

type ChangeSetArtifactPayload struct {
	AgentID          string              `json:"agent_id,omitempty"`
	SessionID        string              `json:"session_id,omitempty"`
	Phase            RunPhase            `json:"phase,omitempty"`
	Files            []ChangeSetFile     `json:"files,omitempty"`
	MutationRefs     []string            `json:"mutation_refs,omitempty"`
	ExecutionRefs    []string            `json:"execution_refs,omitempty"`
	SourceArtifacts  []ArtifactReference `json:"source_artifacts,omitempty"`
	WorkspaceVersion int64               `json:"workspace_version,omitempty"`
}

type TestReportEntry struct {
	AgentID      string `json:"agent_id,omitempty"`
	Status       string `json:"status,omitempty"`
	Summary      string `json:"summary,omitempty"`
	RepairBrief  string `json:"repair_brief,omitempty"`
	ArtifactID   string `json:"artifact_id,omitempty"`
	ExecutionRef string `json:"execution_ref,omitempty"`
}

type TestReportArtifactPayload struct {
	Attempt   int               `json:"attempt"`
	Status    string            `json:"status,omitempty"`
	Entries   []TestReportEntry `json:"entries,omitempty"`
	KnownFail []string          `json:"known_fail,omitempty"`
}

type BenchmarkReportArtifactPayload struct {
	Prompt           string          `json:"prompt,omitempty"`
	Runs             int             `json:"runs"`
	Profiles         []string        `json:"profiles,omitempty"`
	Cases            []string        `json:"cases,omitempty"`
	RecordCount      int             `json:"record_count"`
	EvaluationPasses int             `json:"evaluation_passes"`
	EvaluationFailed int             `json:"evaluation_failed"`
	PreflightDoctor  bool            `json:"preflight_doctor,omitempty"`
	Report           json.RawMessage `json:"report,omitempty"`
	Records          json.RawMessage `json:"records,omitempty"`
}

type FinalResponseArtifactPayload struct {
	Response            string              `json:"response"`
	Summary             string              `json:"summary,omitempty"`
	VerificationSummary string              `json:"verification_summary,omitempty"`
	RemainingRisks      []string            `json:"remaining_risks,omitempty"`
	NextSteps           []string            `json:"next_steps,omitempty"`
	Claims              []GroundedClaim     `json:"claims,omitempty"`
	ArtifactRefs        []ArtifactReference `json:"artifact_refs,omitempty"`
}

type GroundedClaim struct {
	Claim        string   `json:"claim"`
	EvidenceRefs []string `json:"evidence_refs"`
}

type WorkUnitDigest struct {
	ID     string   `json:"id"`
	Kind   string   `json:"kind,omitempty"`
	Role   string   `json:"role,omitempty"`
	Phase  RunPhase `json:"phase,omitempty"`
	Status string   `json:"status,omitempty"`
	Task   string   `json:"task,omitempty"`
}

type PacketDigestArtifactPayload struct {
	LatestArtifacts []ArtifactReference `json:"latest_artifacts,omitempty"`
	KnownFailures   []string            `json:"known_failures,omitempty"`
	WorkUnits       []WorkUnitDigest    `json:"work_units,omitempty"`
}

type ScratchRecord struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	SessionID string          `json:"session_id,omitempty"`
	Summary   string          `json:"summary,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

const ScratchKindModelInvocation = "model_invocation"

type ToolOutputArtifactPayload struct {
	ToolName       string   `json:"tool_name"`
	NormalizedArgs string   `json:"normalized_args"`
	SemanticKey    string   `json:"semantic_key"`
	Success        bool     `json:"success"`
	Output         string   `json:"output,omitempty"`
	ReadSet        []string `json:"read_set,omitempty"`
	WriteSet       []string `json:"write_set,omitempty"`
}
