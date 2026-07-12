package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

func ValidateArtifactPayload(artifact RunArtifact) error {
	kind := strings.TrimSpace(artifact.Kind)
	if kind == "" {
		return nil
	}
	if len(artifact.Payload) == 0 {
		if isKnownTypedArtifactKind(kind) && artifact.SchemaVersion != "" {
			return fmt.Errorf("artifact %s payload が空です", kind)
		}
		return nil
	}
	if isKnownTypedArtifactKind(kind) && artifact.SchemaVersion != "" && artifact.SchemaVersion != kind+".v1" {
		return fmt.Errorf("artifact %s schema_version=%q は %q ではありません", kind, artifact.SchemaVersion, kind+".v1")
	}

	switch kind {
	case "workflow_input":
		var payload WorkflowInputArtifactPayload
		if err := decodeArtifactPayload(kind, artifact.Payload, &payload); err != nil {
			return err
		}
		if len(payload.Messages) == 0 {
			return fmt.Errorf("artifact %s payload.messages が必要です", kind)
		}
	case "agent_inventory":
		var payload AgentInventoryArtifactPayload
		return decodeArtifactPayload(kind, artifact.Payload, &payload)
	case "execution_plan":
		var payload ExecutionPlanArtifactPayload
		if err := decodeArtifactPayload(kind, artifact.Payload, &payload); err != nil {
			return err
		}
		if payload.Plan == nil {
			return fmt.Errorf("artifact %s payload.plan が必要です", kind)
		}
	case "repo_map":
		var payload RepoMapArtifactPayload
		return decodeArtifactPayload(kind, artifact.Payload, &payload)
	case "execution":
		var payload AgentMessageArtifactPayload
		if err := decodeArtifactPayload(kind, artifact.Payload, &payload); err != nil {
			return err
		}
		if strings.TrimSpace(payload.Message) == "" {
			return fmt.Errorf("artifact %s payload.message が必要です", kind)
		}
	case "evidence_bundle":
		return validateEvidenceBundlePayload(artifact.Payload)
	case "review_findings":
		var payload ReviewFindingsArtifactPayload
		return decodeArtifactPayload(kind, artifact.Payload, &payload)
	case "permission_audit":
		var payload PermissionAuditArtifactPayload
		return decodeArtifactPayload(kind, artifact.Payload, &payload)
	case "change_set":
		var payload ChangeSetArtifactPayload
		return decodeArtifactPayload(kind, artifact.Payload, &payload)
	case "test_report":
		var payload TestReportArtifactPayload
		return decodeArtifactPayload(kind, artifact.Payload, &payload)
	case "benchmark_report":
		return validateBenchmarkReportPayload(artifact.Payload)
	case "final_response":
		var payload FinalResponseArtifactPayload
		if err := decodeArtifactPayload(kind, artifact.Payload, &payload); err != nil {
			return err
		}
		if strings.TrimSpace(payload.Response) == "" {
			return fmt.Errorf("artifact %s payload.response が必要です", kind)
		}
	case "packet_digest":
		var payload PacketDigestArtifactPayload
		return decodeArtifactPayload(kind, artifact.Payload, &payload)
	case "tool_output":
		var payload ToolOutputArtifactPayload
		if err := decodeArtifactPayload(kind, artifact.Payload, &payload); err != nil {
			return err
		}
		if strings.TrimSpace(payload.ToolName) == "" {
			return fmt.Errorf("artifact %s payload.tool_name が必要です", kind)
		}
	}
	return nil
}

func validateBenchmarkReportPayload(raw json.RawMessage) error {
	var payload BenchmarkReportArtifactPayload
	if err := decodeArtifactPayload("benchmark_report", raw, &payload); err != nil {
		return err
	}
	if payload.Runs < 1 {
		return fmt.Errorf("artifact benchmark_report payload.runs は 1 以上が必要です")
	}
	if payload.RecordCount < 1 {
		return fmt.Errorf("artifact benchmark_report payload.record_count は 1 以上が必要です")
	}
	if len(payload.Report) > 0 && !json.Valid(payload.Report) {
		return fmt.Errorf("artifact benchmark_report payload.report が JSON ではありません")
	}
	if len(payload.Records) > 0 && !json.Valid(payload.Records) {
		return fmt.Errorf("artifact benchmark_report payload.records が JSON ではありません")
	}
	return nil
}

func validateEvidenceBundlePayload(raw json.RawMessage) error {
	var bundle EvidenceBundleArtifactPayload
	if err := decodeArtifactPayload("evidence_bundle", raw, &bundle); err == nil && len(bundle.Entries) > 0 {
		return nil
	}

	var message AgentMessageArtifactPayload
	if err := decodeArtifactPayload("evidence_bundle", raw, &message); err != nil {
		return err
	}
	if strings.TrimSpace(message.Message) == "" {
		return fmt.Errorf("artifact evidence_bundle payload.entries または payload.message が必要です")
	}
	return nil
}

func decodeArtifactPayload(kind string, raw json.RawMessage, into any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return fmt.Errorf("artifact %s payload の decode に失敗しました: %w", kind, err)
	}
	if decoder.More() {
		return fmt.Errorf("artifact %s payload に余分な JSON token があります", kind)
	}
	return nil
}

func isKnownTypedArtifactKind(kind string) bool {
	switch kind {
	case "workflow_input",
		"agent_inventory",
		"execution_plan",
		"repo_map",
		"execution",
		"evidence_bundle",
		"review_findings",
		"permission_audit",
		"change_set",
		"test_report",
		"benchmark_report",
		"final_response",
		"packet_digest",
		"tool_output":
		return true
	default:
		return false
	}
}
