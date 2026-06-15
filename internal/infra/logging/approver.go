package logging

import (
	"context"
	"strings"

	"yagent/internal/domain"
)

type LoggingApprover struct {
	Base   domain.Approver
	Logger *Logger
}

func (a LoggingApprover) Approve(ctx context.Context, request domain.PermissionRequest) (domain.PermissionDecision, error) {
	if a.Logger != nil {
		_ = a.Logger.Write("permission.requested", map[string]any{
			"tool_name":     request.ToolName,
			"operation":     request.Operation,
			"resource":      request.Resource,
			"agent_id":      request.AgentID,
			"purpose":       request.Purpose,
			"task":          request.Task,
			"risk":          request.Risk,
			"scope":         request.Scope,
			"summary":       request.Summary,
			"side_effects":  request.SideEffects,
			"preview_kind":  request.PreviewKind,
			"preview_lines": permissionPreviewLineCount(request.Preview),
			"change_files":  request.ChangeFiles,
			"additions":     request.Additions,
			"deletions":     request.Deletions,
		})
	}
	decision, err := a.Base.Approve(ctx, request)
	if a.Logger != nil {
		fields := map[string]any{
			"tool_name": request.ToolName,
			"resource":  request.Resource,
			"agent_id":  request.AgentID,
			"decision":  decision,
		}
		if err != nil {
			fields["error"] = err.Error()
		}
		_ = a.Logger.Write("permission.resolved", fields)
	}
	return decision, err
}

func permissionPreviewLineCount(preview string) int {
	if preview == "" {
		return 0
	}
	return len(strings.Split(preview, "\n"))
}
