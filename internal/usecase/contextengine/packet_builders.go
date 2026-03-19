package contextengine

import "yagent/internal/domain"

type packetBase struct {
	run      *domain.RunState
	role     string
	context  domain.RunContext
	messages []domain.Message
}

type packetBuilder interface {
	Build(packetBase) domain.RunContext
}

type rolePacketBuilder struct {
	role             string
	messageLimit     int
	allowedArtifacts map[string]bool
	constraints      []string
}

func packetBuilderForRole(role string) packetBuilder {
	switch role {
	case "planner":
		return rolePacketBuilder{role: role, messageLimit: 6, allowedArtifacts: artifactKinds("agent_inventory", "repo_map", "evidence_bundle"), constraints: []string{"Return structured plan output only."}}
	case "researcher":
		return rolePacketBuilder{role: role, messageLimit: 5, allowedArtifacts: artifactKinds("execution_plan", "repo_map", "evidence_bundle"), constraints: []string{"Return facts and evidence only."}}
	case "coder":
		return rolePacketBuilder{role: role, messageLimit: 4, allowedArtifacts: artifactKinds("execution_plan", "repo_map", "evidence_bundle", "review_findings", "test_report", "change_set"), constraints: []string{"Prefer runtime facts over speculative reasoning."}}
	case "tester":
		return rolePacketBuilder{role: role, messageLimit: 4, allowedArtifacts: artifactKinds("execution", "change_set", "evidence_bundle", "test_report"), constraints: []string{"Report concrete verification evidence."}}
	case "reviewer":
		return rolePacketBuilder{role: role, messageLimit: 4, allowedArtifacts: artifactKinds("execution", "change_set", "evidence_bundle", "review_findings", "test_report"), constraints: []string{"Focus on bugs, regressions, and risks."}}
	case "manager", "finalizer":
		return rolePacketBuilder{role: role, messageLimit: 5, allowedArtifacts: artifactKinds("execution_plan", "execution", "change_set", "evidence_bundle", "test_report", "review_findings", "final_response"), constraints: []string{"Synthesize using artifact facts first."}}
	default:
		return rolePacketBuilder{role: role, messageLimit: 6}
	}
}

func (b rolePacketBuilder) Build(base packetBase) domain.RunContext {
	ctx := base.context
	if base.run == nil {
		ctx.RecentMessages = tailMessages(base.messages, b.messageLimit)
		ctx.ScopedConstraints = append([]string(nil), b.constraints...)
		return ctx
	}

	limit := b.messageLimit
	if limit <= 0 {
		limit = 6
	}
	ctx.PacketRole = b.role
	ctx.PacketKind = b.role
	ctx.RecentMessages = roleScopedMessages(base.messages, b.role, limit)
	relevantArtifacts := selectArtifactsByKind(base.run.Artifacts, b.allowedArtifacts)
	ctx.ArtifactRefs = artifactRefs(relevantArtifacts, 8)
	ctx.Artifacts = artifactReferences(relevantArtifacts, 8)
	ctx.ScopedConstraints = append(ctx.ScopedConstraints, b.constraints...)
	ctx.ScopedConstraints = append(ctx.ScopedConstraints, packetConstraints(base.run, b.role)...)
	return ctx
}

func artifactKinds(values ...string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		out[value] = true
	}
	return out
}

func selectArtifactsByKind(artifacts []domain.RunArtifact, allowed map[string]bool) []domain.RunArtifact {
	if len(artifacts) == 0 {
		return nil
	}
	if len(allowed) == 0 {
		return lastArtifacts(artifacts, 8)
	}
	filtered := make([]domain.RunArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		if allowed[artifact.Kind] {
			filtered = append(filtered, artifact)
		}
	}
	if len(filtered) == 0 {
		return lastArtifacts(artifacts, 8)
	}
	return filtered
}

func packetConstraints(run *domain.RunState, role string) []string {
	if run == nil {
		return nil
	}
	constraints := []string{}
	if len(run.KnownFailures) > 0 && (role == "coder" || role == "reviewer" || role == "tester") {
		constraints = append(constraints, "Known failures are authoritative and should be rechecked before concluding.")
	}
	if hasArtifactKind(run.Artifacts, "change_set") && (role == "tester" || role == "reviewer" || role == "finalizer") {
		constraints = append(constraints, "Use change_set artifacts as the primary record of mutations.")
	}
	return constraints
}

func hasArtifactKind(artifacts []domain.RunArtifact, kind string) bool {
	for _, artifact := range artifacts {
		if artifact.Kind == kind {
			return true
		}
	}
	return false
}
