package registry

import (
	"context"
	"fmt"
	"sort"

	"yagent/internal/domain"
	"yagent/internal/infra/tools/execctx"
)

type Registry struct {
	tools     map[string]domain.Tool
	providers []domain.DynamicToolProvider
}

func New(tools ...domain.Tool) *Registry {
	r := &Registry{tools: map[string]domain.Tool{}}
	for _, tool := range tools {
		r.Register(tool)
	}
	return r
}

func (r *Registry) Register(tool domain.Tool) {
	r.tools[tool.Definition().Name] = tool
}

func (r *Registry) RegisterProvider(provider domain.DynamicToolProvider) {
	r.providers = append(r.providers, provider)
}

func (r *Registry) Definitions(agent domain.AgentSpec) []domain.ToolDefinition {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		if !isAllowed(agent, name) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	definitions := make([]domain.ToolDefinition, 0, len(names))
	for _, name := range names {
		definitions = append(definitions, r.tools[name].Definition())
	}
	for _, provider := range r.providers {
		definitions = append(definitions, provider.Definitions(agent)...)
	}
	sort.Slice(definitions, func(i, j int) bool {
		return definitions[i].Name < definitions[j].Name
	})
	return definitions
}

func (r *Registry) Execute(ctx context.Context, agent domain.AgentSpec, call domain.ToolCall) domain.ToolResult {
	for _, provider := range r.providers {
		if result, ok := provider.Execute(ctx, agent, call); ok {
			return result
		}
	}
	if !isAllowed(agent, call.Name) {
		return domain.ToolResult{
			CallID:  call.ID,
			Name:    call.Name,
			Success: false,
			Output:  fmt.Sprintf("エラー: agent %q はツール %q を使用できません", agent.ID, call.Name),
		}
	}

	tool, ok := r.tools[call.Name]
	if !ok {
		return domain.ToolResult{
			CallID:  call.ID,
			Name:    call.Name,
			Success: false,
			Output:  fmt.Sprintf("エラー: ツール %q が見つかりません", call.Name),
		}
	}

	ctx = filetoolContext(ctx, agent, call)
	return tool.Execute(ctx, call)
}

func isAllowed(agent domain.AgentSpec, toolName string) bool {
	if len(agent.AllowedTools) == 0 {
		return true
	}
	for _, allowed := range agent.AllowedTools {
		if allowed == toolName {
			return true
		}
		if len(allowed) > 1 && allowed[len(allowed)-1] == '*' && len(toolName) >= len(allowed)-1 && toolName[:len(allowed)-1] == allowed[:len(allowed)-1] {
			return true
		}
	}
	return false
}

func filetoolContext(ctx context.Context, agent domain.AgentSpec, call domain.ToolCall) context.Context {
	return execctx.WithExecutionContext(ctx, agent.ID, call.Purpose)
}
