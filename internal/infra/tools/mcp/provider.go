package mcp

import (
	"context"
	"fmt"
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
			Risk:             "high",
			RequiresApproval: true,
			Parameters:       item.InputSchema,
			Metadata:         map[string]any{"category": "mcp", "task_id": item.TaskID, "server_tool_name": item.ServerToolName, "source": "mcp"},
			ReadOnly:         item.ReadOnly,
			ParallelSafe:     item.ParallelSafe,
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

	var bound *domain.BoundMCPTool
	for _, item := range p.bindings.BoundTools() {
		if item.QualifiedName == call.Name {
			copied := item
			bound = &copied
			break
		}
	}
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

func (p *Provider) authorize(ctx context.Context, call domain.ToolCall, bound domain.BoundMCPTool) error {
	if p.engine == nil || p.approver == nil {
		return nil
	}
	decision, request, err := p.engine.Evaluate(ctx, call)
	if err != nil {
		return err
	}
	request.AgentID = execctx.AgentID(ctx)
	request.Purpose = execctx.Purpose(ctx)
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
