package domain

import "context"

type PermissionDecision string

const (
	PermissionAllowOnce    PermissionDecision = "allow_once"
	PermissionAllowSession PermissionDecision = "allow_session"
	PermissionDeny         PermissionDecision = "deny"
)

type PermissionRequest struct {
	ToolName  string
	Operation string
	Resource  string
}

type Approver interface {
	Approve(context.Context, PermissionRequest) (PermissionDecision, error)
}
