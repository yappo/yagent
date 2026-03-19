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

type plannerPacketBuilder struct{}
type researcherPacketBuilder struct{}
type coderPacketBuilder struct{}
type testerPacketBuilder struct{}
type reviewerPacketBuilder struct{}
type finalizerPacketBuilder struct{}
type defaultPacketBuilder struct{}

func packetBuilderForRole(role string) packetBuilder {
	switch role {
	case "planner":
		return plannerPacketBuilder{}
	case "researcher":
		return researcherPacketBuilder{}
	case "coder":
		return coderPacketBuilder{}
	case "tester":
		return testerPacketBuilder{}
	case "reviewer":
		return reviewerPacketBuilder{}
	case "manager", "finalizer":
		return finalizerPacketBuilder{}
	default:
		return defaultPacketBuilder{}
	}
}

func (plannerPacketBuilder) Build(base packetBase) domain.RunContext {
	return buildScopedPacket(base, "planner", 6, artifactKinds("agent_inventory", "repo_map", "evidence_bundle"), []string{"Return structured plan output only."})
}

func (researcherPacketBuilder) Build(base packetBase) domain.RunContext {
	return buildScopedPacket(base, "researcher", 5, artifactKinds("execution_plan", "repo_map", "evidence_bundle"), []string{"Return facts and evidence only."})
}

func (coderPacketBuilder) Build(base packetBase) domain.RunContext {
	return buildScopedPacket(base, "coder", 4, artifactKinds("execution_plan", "repo_map", "evidence_bundle", "review_findings", "test_report", "change_set"), []string{"Prefer runtime facts over speculative reasoning."})
}

func (testerPacketBuilder) Build(base packetBase) domain.RunContext {
	return buildScopedPacket(base, "tester", 4, artifactKinds("execution", "change_set", "evidence_bundle", "test_report"), []string{"Report concrete verification evidence."})
}

func (reviewerPacketBuilder) Build(base packetBase) domain.RunContext {
	return buildScopedPacket(base, "reviewer", 4, artifactKinds("execution", "change_set", "evidence_bundle", "review_findings", "test_report"), []string{"Focus on bugs, regressions, and risks."})
}

func (finalizerPacketBuilder) Build(base packetBase) domain.RunContext {
	return buildScopedPacket(base, "finalizer", 5, artifactKinds("execution_plan", "execution", "change_set", "evidence_bundle", "test_report", "review_findings", "final_response"), []string{"Synthesize using artifact facts first."})
}

func (defaultPacketBuilder) Build(base packetBase) domain.RunContext {
	return buildScopedPacket(base, base.role, 6, nil, nil)
}

func buildScopedPacket(base packetBase, role string, messageLimit int, allowedArtifacts map[string]bool, constraints []string) domain.RunContext {
	ctx := base.context
	if role == "" {
		role = base.role
	}
	if messageLimit <= 0 {
		messageLimit = 6
	}
	if base.run == nil {
		ctx.PacketRole = role
		ctx.PacketKind = role
		ctx.RecentMessages = tailMessages(base.messages, messageLimit)
		ctx.ScopedConstraints = append([]string(nil), constraints...)
		return ctx
	}

	ctx.PacketRole = role
	ctx.PacketKind = role
	ctx.RecentMessages = roleScopedMessages(base.messages, role, messageLimit)
	relevantArtifacts := selectArtifactsByKind(base.run.Artifacts, allowedArtifacts)
	ctx.ArtifactRefs = artifactRefs(relevantArtifacts, 8)
	ctx.Artifacts = artifactReferences(relevantArtifacts, 8)
	ctx.ScopedConstraints = append(ctx.ScopedConstraints, constraints...)
	ctx.ScopedConstraints = append(ctx.ScopedConstraints, packetConstraints(base.run, role)...)
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
