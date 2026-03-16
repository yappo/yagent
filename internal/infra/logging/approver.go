package logging

import (
	"context"

	"yagent/internal/domain"
)

type LoggingApprover struct {
	Base   domain.Approver
	Logger *Logger
}

func (a LoggingApprover) Approve(ctx context.Context, request domain.PermissionRequest) (domain.PermissionDecision, error) {
	if a.Logger != nil {
		_ = a.Logger.Write("permission.requested", map[string]any{
			"tool_name": request.ToolName,
			"operation": request.Operation,
			"resource":  request.Resource,
			"agent_id":  request.AgentID,
			"purpose":   request.Purpose,
			"task":      request.Task,
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
