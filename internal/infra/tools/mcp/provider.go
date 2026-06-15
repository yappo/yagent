package mcp

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"yagent/internal/domain"
	"yagent/internal/infra/tools/execctx"
)

type Provider struct {
	bindings domain.MCPConnectionManager
	engine   domain.PolicyEngine
	approver domain.Approver
}

func NewProvider(bindings domain.MCPConnectionManager, engine domain.PolicyEngine, approver domain.Approver) *Provider {
	return &Provider{
		bindings: bindings,
		engine:   engine,
		approver: approver,
	}
}

func (p *Provider) Definitions(agent domain.AgentSpec) []domain.ToolDefinition {
	bound := p.bindings.BoundTools()
	definitions := make([]domain.ToolDefinition, 0, len(bound))
	for _, item := range bound {
		if !allowed(agent, item.QualifiedName) {
			continue
		}
		definitions = append(definitions, domain.ToolDefinition{
			Name:             item.QualifiedName,
			Description:      item.Description,
			CapabilityGroup:  "mcp",
			Risk:             fallbackString(item.Risk, "high"),
			RequiresApproval: true,
			Parameters:       item.InputSchema,
			Metadata: map[string]any{
				"category":         "mcp",
				"task_id":          item.TaskID,
				"server_tool_name": item.ServerToolName,
				"source":           "mcp",
				"trust_boundary":   item.TrustBoundary,
				"safety_source":    item.SafetySource,
				"allow_network":    item.AllowNetwork,
				"roots":            append([]string(nil), item.Roots...),
			},
			ReadOnly:     item.ReadOnly,
			ParallelSafe: item.ParallelSafe,
			Semantics: domain.ToolSemantics{
				Class: func() domain.ToolClass {
					if item.ReadOnly {
						return domain.ToolClassObserve
					}
					return domain.ToolClassExecute
				}(),
				ReusePolicy: func() domain.ToolReusePolicy {
					if item.ReadOnly {
						return domain.ToolReuseOnSuccess
					}
					return domain.ToolReuseNever
				}(),
				DuplicatePolicy: func() domain.ToolDuplicatePolicy {
					if item.ReadOnly {
						return domain.ToolDuplicateSuppressInflight
					}
					return domain.ToolDuplicateAllow
				}(),
				Freshness: domain.ToolFreshnessPolicy{Strategy: domain.ToolFreshnessReadSet},
				SideEffectClass: func() domain.SideEffectClass {
					if item.ReadOnly {
						return domain.SideEffectNone
					}
					return domain.SideEffectExternal
				}(),
				Source: "mcp",
				SourceLimit: func() int {
					if item.ParallelSafe {
						return 2
					}
					return 1
				}(),
				Stateful: !item.ReadOnly,
			},
		})
	}
	return definitions
}

func (p *Provider) Execute(ctx context.Context, agent domain.AgentSpec, call domain.ToolCall) (domain.ToolResult, bool) {
	if !strings.HasPrefix(call.Name, "mcp__") {
		return domain.ToolResult{}, false
	}
	if !allowed(agent, call.Name) {
		return domain.ToolResult{
			CallID:  call.ID,
			Name:    call.Name,
			Success: false,
			Output:  fmt.Sprintf("エラー: agent %q はツール %q を使用できません", agent.ID, call.Name),
		}, true
	}

	bound := p.lookupBoundTool(call.Name)
	if bound == nil {
		return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: false, Output: "エラー: bind 済み MCP tool が見つかりません"}, true
	}
	if err := p.authorize(ctx, call, *bound); err != nil {
		return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: false, Output: "エラー: " + err.Error()}, true
	}
	output, err := p.bindings.CallTool(ctx, bound.TaskID, bound.ServerToolName, call.Arguments)
	if err != nil {
		return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: false, Output: "エラー: " + err.Error()}, true
	}
	return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: true, Output: output}, true
}

func (p *Provider) InferRuntime(_ context.Context, _ domain.AgentSpec, call domain.ToolCall, _ domain.ToolDefinition) (domain.ToolRuntimeHint, bool) {
	if !strings.HasPrefix(call.Name, "mcp__") {
		return domain.ToolRuntimeHint{}, false
	}
	bound := p.lookupBoundTool(call.Name)
	if bound == nil {
		return domain.ToolRuntimeHint{}, false
	}

	readSet, writeSet := inferMCPAccess(call.Arguments, *bound)
	if !bound.ReadOnly && len(writeSet) == 0 {
		writeSet = append(writeSet, mcpStateScope(bound.TaskID))
	}
	return domain.ToolRuntimeHint{
		ReadSet:       readSet,
		WriteSet:      writeSet,
		ReplaceAccess: true,
		SideEffectClass: func() domain.SideEffectClass {
			if bound.ReadOnly {
				return domain.SideEffectNone
			}
			return domain.SideEffectExternal
		}(),
		Source: "mcp:" + bound.TaskID,
		SourceLimit: func() int {
			if bound.ParallelSafe {
				return 2
			}
			return 1
		}(),
	}, true
}

func (p *Provider) authorize(ctx context.Context, call domain.ToolCall, bound domain.BoundMCPTool) error {
	if p.engine == nil || p.approver == nil {
		return nil
	}
	policyCall := callWithMCPPolicyMetadata(call, bound)
	decision, request, err := p.engine.Evaluate(ctx, policyCall)
	if err != nil {
		return err
	}
	execctx.FillPermissionRequest(ctx, &request)
	request.Task = bound.TaskID
	request.Resource = bound.ServerToolName
	request.Scope = bound.TaskID + ":" + bound.ServerToolName
	request.Summary = fmt.Sprintf("MCP tool %s on %s", bound.ServerToolName, bound.TaskID)
	if decision == domain.PolicyAllow {
		return nil
	}
	if decision == domain.PolicyDeny {
		return fmt.Errorf("この操作は policy により拒否されました")
	}
	userDecision, err := p.approver.Approve(ctx, request)
	if err != nil {
		return err
	}
	if userDecision == domain.PermissionDeny {
		return fmt.Errorf("ユーザーによってキャンセルされました")
	}
	return nil
}

func callWithMCPPolicyMetadata(call domain.ToolCall, bound domain.BoundMCPTool) domain.ToolCall {
	args := make(map[string]any, len(call.Arguments)+5)
	for key, value := range call.Arguments {
		args[key] = value
	}
	args["_policy_task_id"] = bound.TaskID
	args["_policy_server_tool_name"] = bound.ServerToolName
	args["_policy_risk"] = bound.Risk
	args["_policy_read_only"] = bound.ReadOnly
	args["_policy_allow_network"] = bound.AllowNetwork
	call.Arguments = args
	return call
}

func allowed(agent domain.AgentSpec, toolName string) bool {
	if len(agent.AllowedTools) == 0 {
		return true
	}
	for _, item := range agent.AllowedTools {
		if item == toolName {
			return true
		}
		if strings.HasSuffix(item, "*") && strings.HasPrefix(toolName, strings.TrimSuffix(item, "*")) {
			return true
		}
	}
	return false
}

func (p *Provider) lookupBoundTool(name string) *domain.BoundMCPTool {
	for _, item := range p.bindings.BoundTools() {
		if item.QualifiedName != name {
			continue
		}
		copied := item
		return &copied
	}
	return nil
}

func inferMCPAccess(args map[string]any, bound domain.BoundMCPTool) ([]string, []string) {
	reads := []string{}
	writes := []string{}
	visitArgumentPaths(args, func(key string, value string) {
		switch {
		case bound.ReadOnly:
			if looksLikePathKey(key) {
				reads = append(reads, normalizeToolPath(value, bound.Roots))
			}
		case looksLikeReadPathKey(key):
			reads = append(reads, normalizeToolPath(value, bound.Roots))
		case looksLikeWritePathKey(key):
			writes = append(writes, normalizeToolPath(value, bound.Roots))
		case looksLikePathKey(key):
			writes = append(writes, normalizeToolPath(value, bound.Roots))
		}
	})
	if bound.ReadOnly && len(reads) == 0 {
		reads = append(reads, bound.Roots...)
	}
	if !bound.ReadOnly {
		if len(writes) == 0 {
			writes = append(writes, bound.Roots...)
		}
		writes = append(writes, mcpStateScope(bound.TaskID))
	}
	return compactToolPaths(reads), compactToolPaths(writes)
}

func visitArgumentPaths(value any, visit func(key string, value string)) {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			switch child := item.(type) {
			case string:
				visit(key, child)
			default:
				visitArgumentPaths(child, visit)
			}
		}
	case []any:
		for _, item := range typed {
			visitArgumentPaths(item, visit)
		}
	}
}

func looksLikePathKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, token := range []string{"path", "file", "dir", "directory", "root", "cwd", "repo", "workspace"} {
		if strings.Contains(key, token) {
			return true
		}
	}
	return false
}

func looksLikeReadPathKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, token := range []string{"source", "src", "input", "from", "read"} {
		if strings.Contains(key, token) {
			return true
		}
	}
	return false
}

func looksLikeWritePathKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, token := range []string{"target", "destination", "dest", "output", "write", "create", "save", "path"} {
		if strings.Contains(key, token) {
			return true
		}
	}
	return false
}

func normalizeToolPath(value string, roots []string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" {
		if parsed.Scheme != "file" {
			return ""
		}
		value = parsed.Path
	}
	if strings.Contains(value, "://") {
		return ""
	}
	if !filepath.IsAbs(value) && len(roots) > 0 {
		value = filepath.Join(roots[0], value)
	}
	return filepath.Clean(value)
}

func fallbackString(value string, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func compactToolPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, 0, len(paths))
	seen := map[string]struct{}{}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	slices.Sort(out)
	return out
}

func mcpStateScope(taskID string) string {
	return "mcp/" + strings.TrimSpace(taskID) + "/state"
}
