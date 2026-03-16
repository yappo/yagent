package domain

import (
	"context"
	"path"
	"path/filepath"
	"strings"
)

type PermissionDecision string

const (
	PermissionAllowOnce    PermissionDecision = "allow_once"
	PermissionAllowSession PermissionDecision = "allow_session"
	PermissionDeny         PermissionDecision = "deny"
)

type PermissionRequest struct {
	ToolName     string
	Operation    string
	Resource     string
	Action       string
	ResourceKind string
	Risk         string
	Scope        string
	Summary      string
	SideEffects  []string
	AgentID      string
	Purpose      string
	Task         string
}

type Approver interface {
	Approve(context.Context, PermissionRequest) (PermissionDecision, error)
}

func PermissionRequestSupportsPatternApproval(request PermissionRequest) bool {
	switch request.ToolName {
	case "fs_read", "fs_write", "fs_list", "fs_stat", "search_text", "search_files", "fs_remove":
		return strings.TrimSpace(request.Resource) != ""
	default:
		return false
	}
}

func PermissionRequestMatchesPattern(request PermissionRequest, patternValue string) bool {
	patternValue = strings.TrimSpace(patternValue)
	if patternValue == "" {
		return false
	}

	resource := filepath.ToSlash(filepath.Clean(request.Resource))
	patternValue = filepath.ToSlash(patternValue)
	if strings.Contains(patternValue, "/") {
		if matched, err := path.Match(patternValue, resource); err == nil && matched {
			return true
		}
		trimmed := strings.TrimPrefix(resource, "/")
		if trimmed != resource {
			if matched, err := path.Match(patternValue, trimmed); err == nil && matched {
				return true
			}
		}
	}

	matched, err := path.Match(patternValue, path.Base(resource))
	return err == nil && matched
}
