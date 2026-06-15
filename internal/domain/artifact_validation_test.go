package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateArtifactPayloadAcceptsKnownTypedPayloads(t *testing.T) {
	cases := []RunArtifact{
		{
			Kind:          "final_response",
			SchemaVersion: "final_response.v1",
			Payload: mustArtifactPayload(t, FinalResponseArtifactPayload{
				Response: "done",
			}),
		},
		{
			Kind:          "evidence_bundle",
			SchemaVersion: "evidence_bundle.v1",
			Payload: mustArtifactPayload(t, EvidenceBundleArtifactPayload{
				Entries: []EvidenceBundleEntry{{
					Artifact: ArtifactReference{ID: "artifact-1", Kind: "execution"},
					Summary:  "evidence",
				}},
			}),
		},
		{
			Kind:          "evidence_bundle",
			SchemaVersion: "evidence_bundle.v1",
			Payload: mustArtifactPayload(t, AgentMessageArtifactPayload{
				Message: "prepared evidence",
			}),
		},
		{
			Kind:          "tool_output",
			SchemaVersion: "tool_output.v1",
			Payload: mustArtifactPayload(t, ToolOutputArtifactPayload{
				ToolName: "fs_read",
				Success:  true,
			}),
		},
		{
			Kind:          "benchmark_report",
			SchemaVersion: "benchmark_report.v1",
			Payload: mustArtifactPayload(t, BenchmarkReportArtifactPayload{
				Runs:        1,
				RecordCount: 1,
				Report:      json.RawMessage(`{"ok":true}`),
				Records:     json.RawMessage(`[{"passed":true}]`),
			}),
		},
	}

	for _, artifact := range cases {
		if err := ValidateArtifactPayload(artifact); err != nil {
			t.Fatalf("ValidateArtifactPayload(%s) returned error: %v", artifact.Kind, err)
		}
	}
}

func TestValidateArtifactPayloadRejectsInvalidTypedPayload(t *testing.T) {
	cases := []struct {
		name     string
		artifact RunArtifact
		want     string
	}{
		{
			name: "schema version mismatch",
			artifact: RunArtifact{
				Kind:          "final_response",
				SchemaVersion: "final_response.v2",
				Payload:       json.RawMessage(`{"response":"done"}`),
			},
			want: "schema_version",
		},
		{
			name: "missing required field",
			artifact: RunArtifact{
				Kind:          "final_response",
				SchemaVersion: "final_response.v1",
				Payload:       json.RawMessage(`{"summary":"missing response"}`),
			},
			want: "payload.response",
		},
		{
			name: "unknown field",
			artifact: RunArtifact{
				Kind:          "tool_output",
				SchemaVersion: "tool_output.v1",
				Payload:       json.RawMessage(`{"tool_name":"fs_read","unknown":true}`),
			},
			want: "unknown field",
		},
		{
			name: "benchmark missing records",
			artifact: RunArtifact{
				Kind:          "benchmark_report",
				SchemaVersion: "benchmark_report.v1",
				Payload:       json.RawMessage(`{"runs":1,"record_count":0}`),
			},
			want: "record_count",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateArtifactPayload(tc.artifact)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func mustArtifactPayload(t *testing.T, payload any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	return data
}
