package registry

import (
	"context"
	"fmt"
	"sort"

	"yagent/internal/domain"
)

type Registry struct {
	tools map[string]domain.Tool
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

func (r *Registry) Definitions() []domain.ToolDefinition {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	definitions := make([]domain.ToolDefinition, 0, len(names))
	for _, name := range names {
		definitions = append(definitions, r.tools[name].Definition())
	}
	return definitions
}

func (r *Registry) Execute(ctx context.Context, call domain.ToolCall) domain.ToolResult {
	tool, ok := r.tools[call.Name]
	if !ok {
		return domain.ToolResult{
			CallID:  call.ID,
			Name:    call.Name,
			Success: false,
			Output:  fmt.Sprintf("エラー: ツール %q が見つかりません", call.Name),
		}
	}

	return tool.Execute(ctx, call)
}
