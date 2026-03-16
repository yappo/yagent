package domain

import "context"

type PolicyDecision string

const (
	PolicyAllow           PolicyDecision = "allow"
	PolicyRequireApproval PolicyDecision = "require_approval"
	PolicyDeny            PolicyDecision = "deny"
)

type PolicyEngine interface {
	Evaluate(context.Context, ToolCall) (PolicyDecision, PermissionRequest, error)
}

type PathPolicy interface {
	ResolveFile(path string) (string, error)
	ResolveDir(path string) (string, error)
	ResolveSearchRoot(path string) (string, error)
}
