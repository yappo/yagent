package taskcatalog

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"

	"yagent/internal/domain"
)

var invalidToolNameChars = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

type MCPBindings struct {
	factory  domain.MCPSessionFactory
	mu       sync.RWMutex
	sessions map[string]domain.MCPSession
	tools    map[string][]domain.BoundMCPTool
}

func NewMCPBindings(factory domain.MCPSessionFactory) *MCPBindings {
	return &MCPBindings{
		factory:  factory,
		sessions: map[string]domain.MCPSession{},
		tools:    map[string][]domain.BoundMCPTool{},
	}
}

func (b *MCPBindings) Bind(ctx context.Context, task domain.TaskDefinition) ([]domain.MCPToolDescriptor, error) {
	if task.Kind != domain.TaskKindMCPServer || task.MCPServer == nil {
		return nil, fmt.Errorf("MCP server task ではありません: %s", task.ID)
	}

	b.mu.RLock()
	session, ok := b.sessions[task.ID]
	if ok {
		b.mu.RUnlock()
		descriptors, err := session.ListTools(ctx)
		if err != nil {
			return nil, err
		}
		descriptors = filterDescriptors(task.MCPServer, descriptors)
		b.storeTools(task, descriptors)
		return descriptors, nil
	}
	b.mu.RUnlock()

	session, err := b.factory.Open(ctx, task)
	if err != nil {
		return nil, err
	}
	if err := session.Initialize(ctx); err != nil {
		_ = session.Close()
		return nil, err
	}
	descriptors, err := session.ListTools(ctx)
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	descriptors = filterDescriptors(task.MCPServer, descriptors)

	b.mu.Lock()
	if existing, ok := b.sessions[task.ID]; ok {
		b.mu.Unlock()
		_ = session.Close()
		descriptors, err = existing.ListTools(ctx)
		if err != nil {
			return nil, err
		}
		descriptors = filterDescriptors(task.MCPServer, descriptors)
		b.storeTools(task, descriptors)
		return descriptors, nil
	}
	b.sessions[task.ID] = session
	b.mu.Unlock()

	b.storeTools(task, descriptors)
	return descriptors, nil
}

func (b *MCPBindings) BoundTools() []domain.BoundMCPTool {
	b.mu.RLock()
	defer b.mu.RUnlock()

	names := make([]string, 0, len(b.tools))
	for taskID := range b.tools {
		names = append(names, taskID)
	}
	slices.Sort(names)

	var result []domain.BoundMCPTool
	for _, taskID := range names {
		result = append(result, b.tools[taskID]...)
	}
	return result
}

func (b *MCPBindings) CallTool(ctx context.Context, taskID string, toolName string, arguments map[string]any) (string, error) {
	b.mu.RLock()
	session, ok := b.sessions[taskID]
	b.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("MCP server は bind されていません: %s", taskID)
	}
	return session.CallTool(ctx, toolName, arguments)
}

func (b *MCPBindings) storeTools(task domain.TaskDefinition, descriptors []domain.MCPToolDescriptor) {
	bound := make([]domain.BoundMCPTool, 0, len(descriptors))
	for _, tool := range descriptors {
		bound = append(bound, domain.BoundMCPTool{
			TaskID:         task.ID,
			ToolName:       tool.Name,
			ServerToolName: tool.Name,
			QualifiedName:  QualifiedToolName(task.ID, prefixForTask(task), tool.Name),
			Description:    tool.Description,
			InputSchema:    compactSchema(tool.InputSchema),
			ReadOnly:       tool.ReadOnly,
			ParallelSafe:   task.MCPServer.ParallelSafe && tool.ParallelSafe,
		})
	}

	b.mu.Lock()
	b.tools[task.ID] = bound
	b.mu.Unlock()
}

func prefixForTask(task domain.TaskDefinition) string {
	if task.MCPServer == nil || task.MCPServer.ToolPrefix == "" {
		return task.ID
	}
	return task.MCPServer.ToolPrefix
}

func QualifiedToolName(taskID string, prefix string, toolName string) string {
	return "mcp__" + sanitizeName(prefix) + "__" + sanitizeName(toolName) + "__" + sanitizeName(taskID)
}

func sanitizeName(value string) string {
	value = strings.TrimSpace(value)
	value = invalidToolNameChars.ReplaceAllString(value, "_")
	value = strings.Trim(value, "_")
	if value == "" {
		return "tool"
	}
	return value
}

func compactSchema(schema map[string]any) map[string]any {
	if len(schema) == 0 {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}
	return compactMap(schema)
}

func compactMap(input map[string]any) map[string]any {
	output := map[string]any{}
	for _, key := range []string{"type", "required", "enum", "items", "properties", "additionalProperties"} {
		value, ok := input[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case map[string]any:
			if key == "properties" {
				props := map[string]any{}
				for propName, propValue := range typed {
					if propMap, ok := propValue.(map[string]any); ok {
						props[propName] = compactProperty(propMap)
					}
				}
				output[key] = props
				continue
			}
			output[key] = compactMap(typed)
		default:
			output[key] = value
		}
	}
	return output
}

func compactProperty(input map[string]any) map[string]any {
	output := compactMap(input)
	if description, ok := input["description"].(string); ok && strings.TrimSpace(description) != "" {
		output["description"] = description
	}
	return output
}

func filterDescriptors(spec *domain.MCPServerSpec, descriptors []domain.MCPToolDescriptor) []domain.MCPToolDescriptor {
	if spec == nil {
		return descriptors
	}
	include := make(map[string]struct{}, len(spec.IncludeTools))
	for _, name := range spec.IncludeTools {
		include[name] = struct{}{}
	}
	exclude := make(map[string]struct{}, len(spec.ExcludeTools))
	for _, name := range spec.ExcludeTools {
		exclude[name] = struct{}{}
	}

	result := make([]domain.MCPToolDescriptor, 0, len(descriptors))
	for _, descriptor := range descriptors {
		if len(include) > 0 {
			if _, ok := include[descriptor.Name]; !ok {
				continue
			}
		}
		if _, ok := exclude[descriptor.Name]; ok {
			continue
		}
		descriptor.InputSchema = compactSchema(descriptor.InputSchema)
		descriptor.Annotations = maps.Clone(descriptor.Annotations)
		result = append(result, descriptor)
	}
	return result
}
