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

type FinalResponseArtifactPayload struct {
	Response            string              `json:"response"`
	Summary             string              `json:"summary,omitempty"`
	VerificationSummary string              `json:"verification_summary,omitempty"`
	ArtifactRefs        []ArtifactReference `json:"artifact_refs,omitempty"`
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

type ToolOutputArtifactPayload struct {
	ToolName       string   `json:"tool_name"`
	NormalizedArgs string   `json:"normalized_args"`
	SemanticKey    string   `json:"semantic_key"`
	Success        bool     `json:"success"`
	Output         string   `json:"output,omitempty"`
	ReadSet        []string `json:"read_set,omitempty"`
	WriteSet       []string `json:"write_set,omitempty"`
}
