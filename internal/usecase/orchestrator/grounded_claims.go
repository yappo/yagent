package orchestrator

import (
	"fmt"
	"strings"

	"yagent/internal/domain"
)

func finalResponseGroundingIssue(run *domain.RunState, message domain.Message) string {
	raw := strings.TrimSpace(message.Metadata[finalResponseRawJSONMetadataKey])
	if raw == "" {
		return "final response did not satisfy the grounded claim contract"
	}
	payload, ok := parseFinalResponseJSON(raw)
	if !ok || strings.TrimSpace(payload.Response) == "" {
		return "final response did not satisfy the grounded claim contract"
	}
	return validateGroundedClaims(run, payload)
}

func validateGroundedClaims(run *domain.RunState, payload domain.FinalResponseArtifactPayload) string {
	if len(payload.Claims) == 0 {
		return "final response did not provide grounded claims"
	}
	byID := map[string]struct{}{}
	byKind := map[string]struct{}{}
	if run != nil {
		for _, artifact := range run.Artifacts {
			if artifact.Kind == "final_response" {
				continue
			}
			byID[artifact.ID] = struct{}{}
			byKind[artifact.Kind] = struct{}{}
		}
	}
	for _, claim := range payload.Claims {
		if strings.TrimSpace(claim.Claim) == "" {
			return "final response contained an empty grounded claim"
		}
		if len(claim.EvidenceRefs) == 0 {
			return fmt.Sprintf("grounded claim has no evidence references: %s", claim.Claim)
		}
		for _, ref := range claim.EvidenceRefs {
			if _, ok := byID[ref]; ok {
				continue
			}
			if _, ok := byKind[ref]; ok {
				continue
			}
			return fmt.Sprintf("grounded claim references unknown evidence %q: %s", ref, claim.Claim)
		}
		if unknown := ungroundedRepositoryPaths(run, claim.Claim); len(unknown) > 0 {
			return "grounded claim contains unobserved repository paths: " + strings.Join(unknown, ", ")
		}
	}
	return ""
}
