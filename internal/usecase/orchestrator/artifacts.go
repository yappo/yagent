package orchestrator

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"yagent/internal/domain"
)

var artifactIDCounter atomic.Uint64

func newInventoryArtifact(run *domain.RunState, phase domain.RunPhase, agentID string, inventory []domain.AgentInventoryEntry) domain.RunArtifact {
	return newTypedArtifact(run, phase, agentID, "Agent inventory", "agent_inventory", inventoryArtifactSummary(inventory), domain.AgentInventoryArtifactPayload{
		Agents: append([]domain.AgentInventoryEntry(nil), inventory...),
	}, nil)
}

func newExecutionPlanArtifact(run *domain.RunState, phase domain.RunPhase, agentID string, plan *domain.ExecutionPlan) domain.RunArtifact {
	content := stablePlanJSON(plan)
	return newTypedArtifact(run, phase, agentID, "Execution plan", "execution_plan", content, domain.ExecutionPlanArtifactPayload{
		Plan: plan,
	}, nil)
}

func newWorkflowInputArtifact(run *domain.RunState, payload domain.WorkflowInputArtifactPayload) domain.RunArtifact {
	return newTypedArtifact(run, domain.RunPhaseIntake, "runtime", "Workflow input", "workflow_input", run.UserGoal, payload, nil)
}

func newRepoMapArtifact(run *domain.RunState, phase domain.RunPhase, agentID string, entries []domain.RepoMapEntry) domain.RunArtifact {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		line := entry.Path
		if entry.Observation != "" {
			line += ": " + entry.Observation
		}
		lines = append(lines, line)
	}
	return newTypedArtifact(run, phase, agentID, "Repository map", "repo_map", strings.Join(lines, "\n"), domain.RepoMapArtifactPayload{
		Entries: entries,
	}, nil)
}

func newAgentMessageArtifact(run *domain.RunState, phase domain.RunPhase, agentID string, name string, kind string, content string, refs []domain.ArtifactReference) domain.RunArtifact {
	return newTypedArtifact(run, phase, agentID, name, kind, content, domain.AgentMessageArtifactPayload{
		Message:       content,
		Summary:       truncateSummary(content),
		RelevantFiles: extractArtifactFiles(content, 8),
	}, refs)
}

func newVerificationArtifact(run *domain.RunState, phase domain.RunPhase, agentID string, name string, content string, parsed domain.VerificationResult) domain.RunArtifact {
	return newTypedArtifact(run, phase, agentID, name, "review_findings", content, domain.ReviewFindingsArtifactPayload{
		Result:  parsed,
		Message: content,
	}, nil)
}

func newPermissionAuditArtifact(run *domain.RunState, phase domain.RunPhase, payload domain.PermissionAuditArtifactPayload) domain.RunArtifact {
	lines := make([]string, 0, len(payload.Records))
	for _, record := range payload.Records {
		line := strings.TrimSpace(strings.Join([]string{
			string(record.Decision),
			record.ToolName,
			record.Resource,
		}, " "))
		if record.AgentID != "" {
			line += " [" + record.AgentID + "]"
		}
		if changes := permissionRecordChangeSummary(record); changes != "" {
			line += " changes=" + changes
		}
		lines = append(lines, line)
	}
	return newTypedArtifact(run, phase, "permission", "Permission audit", "permission_audit", strings.Join(lines, "\n"), payload, nil)
}

func permissionRecordChangeSummary(record domain.PermissionDecisionRecord) string {
	parts := make([]string, 0, 3)
	if record.ChangeFiles > 0 {
		parts = append(parts, fmt.Sprintf("files=%d", record.ChangeFiles))
	}
	if record.Additions > 0 || record.Deletions > 0 {
		parts = append(parts, fmt.Sprintf("+%d", record.Additions), fmt.Sprintf("-%d", record.Deletions))
	}
	return strings.Join(parts, " ")
}

func newChangeSetArtifact(run *domain.RunState, phase domain.RunPhase, agentID string, payload domain.ChangeSetArtifactPayload) domain.RunArtifact {
	lines := make([]string, 0, len(payload.Files))
	for _, file := range payload.Files {
		line := file.Path
		if file.Operation != "" {
			line += " [" + file.Operation + "]"
		}
		lines = append(lines, line)
	}
	return newTypedArtifact(run, phase, agentID, "Change set", "change_set", strings.Join(lines, "\n"), payload, payload.SourceArtifacts)
}

func newTestReportArtifact(run *domain.RunState, phase domain.RunPhase, agentID string, payload domain.TestReportArtifactPayload) domain.RunArtifact {
	lines := make([]string, 0, len(payload.Entries))
	for _, entry := range payload.Entries {
		lines = append(lines, entry.AgentID+": "+entry.Status+" - "+entry.Summary)
	}
	return newTypedArtifact(run, phase, agentID, "Test report", "test_report", strings.Join(lines, "\n"), payload, nil)
}

func newFinalResponseArtifact(run *domain.RunState, phase domain.RunPhase, message domain.Message) domain.RunArtifact {
	refs := recentArtifactReferences(lastArtifacts(run.Artifacts, 6), 6)
	agentID := message.AgentID
	content := strings.TrimSpace(message.Content)
	payload := domain.FinalResponseArtifactPayload{
		Response:            content,
		Summary:             truncateSummary(content),
		VerificationSummary: latestVerificationSummary(run),
		ArtifactRefs:        refs,
	}
	if raw := message.Metadata[finalResponseRawJSONMetadataKey]; raw != "" {
		if parsed, ok := parseFinalResponseJSON(raw); ok {
			payload = parsed
			if payload.Response == "" {
				payload.Response = content
			}
			if payload.Summary == "" {
				payload.Summary = truncateSummary(payload.Response)
			}
			if payload.VerificationSummary == "" {
				payload.VerificationSummary = latestVerificationSummary(run)
			}
			payload.ArtifactRefs = refs
			content = payload.Response
		}
	}
	return newTypedArtifact(run, phase, agentID, "Final response", "final_response", content, domain.FinalResponseArtifactPayload{
		Response:            payload.Response,
		Summary:             payload.Summary,
		VerificationSummary: payload.VerificationSummary,
		RemainingRisks:      payload.RemainingRisks,
		NextSteps:           payload.NextSteps,
		Claims:              payload.Claims,
		ArtifactRefs:        payload.ArtifactRefs,
	}, refs)
}

func newTypedArtifact(run *domain.RunState, phase domain.RunPhase, agentID string, name string, kind string, content string, payload any, refs []domain.ArtifactReference) domain.RunArtifact {
	return domain.RunArtifact{
		ID:            newArtifactID(),
		Name:          name,
		Kind:          kind,
		SchemaVersion: kind + ".v1",
		Phase:         phase,
		AgentID:       agentID,
		Summary:       truncateSummary(content),
		Text:          content,
		Content:       content,
		Payload:       marshalArtifactPayload(payload),
		References:    append([]domain.ArtifactReference(nil), refs...),
		CreatedAt:     time.Now(),
	}
}

func newArtifactID() string {
	var random [16]byte
	if _, err := rand.Read(random[:]); err == nil {
		return "artifact-" + hex.EncodeToString(random[:])
	}
	return fmt.Sprintf("artifact-%d-%d", time.Now().UnixNano(), artifactIDCounter.Add(1))
}

func marshalArtifactPayload(payload any) json.RawMessage {
	if payload == nil {
		return nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return data
}

func extractArtifactFiles(content string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	seen := map[string]struct{}{}
	files := make([]string, 0, limit)
	for _, token := range strings.Fields(content) {
		token = strings.Trim(token, "[](){}<>\"'`,.:;!?")
		if token == "" || strings.Contains(token, "://") {
			continue
		}
		if !strings.Contains(token, "/") && !strings.Contains(token, ".") {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		files = append(files, token)
		if len(files) >= limit {
			break
		}
	}
	return files
}

func recentArtifactReferences(artifacts []domain.RunArtifact, limit int) []domain.ArtifactReference {
	if limit > 0 && len(artifacts) > limit {
		artifacts = artifacts[len(artifacts)-limit:]
	}
	refs := make([]domain.ArtifactReference, 0, len(artifacts))
	for _, artifact := range artifacts {
		refs = append(refs, domain.ArtifactReference{
			ID:   artifact.ID,
			Kind: artifact.Kind,
			Name: artifact.Name,
		})
	}
	return refs
}
