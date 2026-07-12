package orchestrator

import (
	"testing"

	"yagent/internal/domain"
)

func TestUngroundedRepositoryPathsRejectsUnobservedClaims(t *testing.T) {
	run := &domain.RunState{Artifacts: []domain.RunArtifact{newRepoMapArtifact(&domain.RunState{ID: "run"}, domain.RunPhaseExecute, "coder", []domain.RepoMapEntry{
		{Path: "internal/usecase/orchestrator/work_graph.go", Source: "observation:fs_read"},
		{Path: "internal/untrusted.go", Source: "messages"},
	})}}
	unknown := ungroundedRepositoryPaths(run, "The runner is internal/usecase/orchestrator/work_graph.go, but internal/imaginary/agent.go is also present.")
	if len(unknown) != 1 || unknown[0] != "internal/imaginary/agent.go" {
		t.Fatalf("ungrounded paths = %+v", unknown)
	}
}

func TestUngroundedRepositoryPathsAllowsObservedDirectoriesAndChanges(t *testing.T) {
	run := &domain.RunState{Artifacts: []domain.RunArtifact{
		newRepoMapArtifact(&domain.RunState{ID: "run"}, domain.RunPhaseExecute, "coder", []domain.RepoMapEntry{{Path: "internal/usecase/orchestrator/work_graph.go", Source: "observation:fs_read"}}),
		newChangeSetArtifact(&domain.RunState{ID: "run"}, domain.RunPhaseExecute, "coder", domain.ChangeSetArtifactPayload{Files: []domain.ChangeSetFile{{Path: "docs/runtime.md"}}}),
	}}
	if unknown := ungroundedRepositoryPaths(run, "See internal/usecase and docs/runtime.md."); len(unknown) != 0 {
		t.Fatalf("unexpected ungrounded paths = %+v", unknown)
	}
}

func TestValidateGroundedClaimsRequiresExistingEvidence(t *testing.T) {
	run := &domain.RunState{Artifacts: []domain.RunArtifact{{ID: "exec-1", Kind: "execution"}}}
	valid := domain.FinalResponseArtifactPayload{Claims: []domain.GroundedClaim{{Claim: "the execution completed", EvidenceRefs: []string{"exec-1"}}}}
	if issue := validateGroundedClaims(run, valid); issue != "" {
		t.Fatalf("valid claim rejected: %s", issue)
	}
	unknown := valid
	unknown.Claims = []domain.GroundedClaim{{Claim: "the execution completed", EvidenceRefs: []string{"missing"}}}
	if issue := validateGroundedClaims(run, unknown); issue == "" {
		t.Fatal("unknown evidence reference was accepted")
	}
	missing := valid
	missing.Claims = []domain.GroundedClaim{{Claim: "the execution completed", EvidenceRefs: nil}}
	if issue := validateGroundedClaims(run, missing); issue == "" {
		t.Fatal("claim without evidence was accepted")
	}
}

func TestFinalResponseGroundingRequiresStructuredContract(t *testing.T) {
	run := &domain.RunState{Artifacts: []domain.RunArtifact{{ID: "exec-1", Kind: "execution"}}}
	if issue := finalResponseGroundingIssue(run, domain.Message{Content: "plain final response"}); issue == "" {
		t.Fatal("plain final response bypassed the grounding contract")
	}
	valid := domain.Message{Metadata: map[string]string{
		finalResponseRawJSONMetadataKey: `{"response":"done","claims":[{"claim":"the execution completed","evidence_refs":["exec-1"]}]}`,
	}}
	if issue := finalResponseGroundingIssue(run, valid); issue != "" {
		t.Fatalf("valid structured final response rejected: %s", issue)
	}
}
