package domain

type AgentInventoryArtifactPayload struct {
	Agents []AgentInventoryEntry `json:"agents"`
}

type ExecutionPlanArtifactPayload struct {
	Plan *ExecutionPlan `json:"plan,omitempty"`
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

type ToolOutputArtifactPayload struct {
	ToolName       string   `json:"tool_name"`
	NormalizedArgs string   `json:"normalized_args"`
	SemanticKey    string   `json:"semantic_key"`
	Success        bool     `json:"success"`
	Output         string   `json:"output,omitempty"`
	ReadSet        []string `json:"read_set,omitempty"`
	WriteSet       []string `json:"write_set,omitempty"`
}
