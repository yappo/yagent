package audit

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"yagent/internal/domain"
)

type PermissionAuditApprover struct {
	Base  domain.Approver
	Store domain.RuntimeStateStore
}

func (a PermissionAuditApprover) Approve(ctx context.Context, request domain.PermissionRequest) (domain.PermissionDecision, error) {
	if a.Base == nil {
		err := fmt.Errorf("permission approver is not configured")
		a.save(ctx, request, domain.PermissionDeny, err)
		return domain.PermissionDeny, err
	}
	decision, err := a.Base.Approve(ctx, request)
	a.save(ctx, request, decision, err)
	return decision, err
}

func (a PermissionAuditApprover) save(ctx context.Context, request domain.PermissionRequest, decision domain.PermissionDecision, err error) {
	if a.Store == nil {
		return
	}
	record := PermissionRecordFromRequest(request, decision, err)
	payload, marshalErr := json.Marshal(record)
	if marshalErr != nil {
		return
	}
	_ = a.Store.SaveScratch(ctx, domain.ScratchRecord{
		ID:        permissionDecisionID(record),
		Kind:      "permission_decision",
		SessionID: fallbackString(record.RootRunID, record.RunID),
		Summary:   permissionDecisionSummary(record),
		Payload:   payload,
		CreatedAt: record.CreatedAt,
	})
}

func PermissionRecordFromRequest(request domain.PermissionRequest, decision domain.PermissionDecision, err error) domain.PermissionDecisionRecord {
	record := domain.PermissionDecisionRecord{
		RunID:        request.RunID,
		RootRunID:    request.RootRunID,
		Phase:        request.Phase,
		Attempt:      request.Attempt,
		AgentID:      request.AgentID,
		ToolName:     request.ToolName,
		Operation:    request.Operation,
		Resource:     request.Resource,
		Action:       request.Action,
		ResourceKind: request.ResourceKind,
		Risk:         request.Risk,
		Scope:        request.Scope,
		Summary:      request.Summary,
		SideEffects:  append([]string(nil), request.SideEffects...),
		Purpose:      request.Purpose,
		Task:         request.Task,
		PreviewKind:  request.PreviewKind,
		PreviewLines: previewLineCount(request.Preview),
		ChangeFiles:  request.ChangeFiles,
		Additions:    request.Additions,
		Deletions:    request.Deletions,
		Decision:     decision,
		CreatedAt:    time.Now(),
	}
	if err != nil {
		record.Error = err.Error()
	}
	return record
}

func permissionDecisionID(record domain.PermissionDecisionRecord) string {
	sum := sha1.Sum([]byte(strings.Join([]string{
		record.RootRunID,
		record.RunID,
		string(record.Phase),
		record.AgentID,
		record.ToolName,
		record.Resource,
		string(record.Decision),
		record.CreatedAt.Format(time.RFC3339Nano),
	}, "\x00")))
	return "permission-" + hex.EncodeToString(sum[:])
}

func permissionDecisionSummary(record domain.PermissionDecisionRecord) string {
	return strings.TrimSpace(strings.Join([]string{
		string(record.Decision),
		record.ToolName,
		record.Resource,
	}, " "))
}

func previewLineCount(preview string) int {
	preview = strings.TrimSpace(preview)
	if preview == "" {
		return 0
	}
	return len(strings.Split(preview, "\n"))
}

func fallbackString(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
