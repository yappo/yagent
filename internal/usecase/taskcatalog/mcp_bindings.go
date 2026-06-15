package taskcatalog

import (
	"context"
	"fmt"
	"maps"
	"path"
	"path/filepath"
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
	if task.Kind != domain.TaskSpecKindMCPServer || task.MCPServer == nil {
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
	roots := mcpRuntimeRoots(task)
	for _, tool := range descriptors {
		safety := resolveMCPToolSafety(task.MCPServer, tool)
		bound = append(bound, domain.BoundMCPTool{
			TaskID:         task.ID,
			ToolName:       tool.Name,
			ServerToolName: tool.Name,
			QualifiedName:  QualifiedToolName(task.ID, prefixForTask(task), tool.Name),
			Description:    tool.Description,
			InputSchema:    compactSchema(tool.InputSchema),
			ReadOnly:       safety.readOnly,
			ParallelSafe:   safety.parallelSafe,
			Risk:           safety.risk,
			AllowNetwork:   safety.allowNetwork,
			Roots:          append([]string(nil), roots...),
			TrustBoundary:  safety.trustBoundary,
			SafetySource:   safety.source,
		})
	}

	b.mu.Lock()
	b.tools[task.ID] = bound
	b.mu.Unlock()
}

func mcpRuntimeRoots(task domain.TaskDefinition) []string {
	if task.MCPServer == nil {
		return nil
	}
	roots := append([]string(nil), task.MCPServer.Roots...)
	if len(roots) == 0 && task.MCPServer.Cwd != "" {
		roots = append(roots, task.MCPServer.Cwd)
	}
	return compactBindingPaths(roots)
}

func compactBindingPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, 0, len(paths))
	seen := map[string]struct{}{}
	for _, item := range paths {
		item = filepath.Clean(strings.TrimSpace(item))
		if item == "" || item == "." {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	slices.Sort(out)
	return out
}

type mcpToolSafety struct {
	readOnly      bool
	parallelSafe  bool
	risk          string
	allowNetwork  bool
	trustBoundary string
	source        string
}

func resolveMCPToolSafety(spec *domain.MCPServerSpec, descriptor domain.MCPToolDescriptor) mcpToolSafety {
	if spec == nil {
		return mcpToolSafety{risk: "high", trustBoundary: "untrusted", source: "default"}
	}
	trustBoundary := spec.Trust
	if trustBoundary == "" {
		trustBoundary = "untrusted"
	}
	readOnly := false
	source := "default"
	switch {
	case matchesToolPattern(spec.MutatingTools, descriptor.Name):
		readOnly = false
		source = "task_mutating_tools"
	case matchesToolPattern(spec.ReadOnlyTools, descriptor.Name):
		readOnly = true
		source = "task_read_only_tools"
	case spec.TrustToolAnnotations && descriptor.ReadOnly:
		readOnly = true
		source = "trusted_mcp_annotations"
	}

	parallelSafe := false
	switch {
	case matchesToolPattern(spec.ParallelSafeTools, descriptor.Name):
		parallelSafe = true
	case spec.TrustToolAnnotations && spec.ParallelSafe && descriptor.ParallelSafe:
		parallelSafe = true
	}

	risk := spec.Risk
	if risk == "" {
		risk = "medium"
	}
	if !readOnly {
		risk = "high"
	}
	if spec.AllowNetwork {
		risk = "high"
	}
	return mcpToolSafety{
		readOnly:      readOnly,
		parallelSafe:  parallelSafe,
		risk:          risk,
		allowNetwork:  spec.AllowNetwork,
		trustBoundary: trustBoundary,
		source:        source,
	}
}

func matchesToolPattern(patterns []string, toolName string) bool {
	for _, patternValue := range patterns {
		patternValue = strings.TrimSpace(patternValue)
		if patternValue == "" {
			continue
		}
		if patternValue == toolName {
			return true
		}
		if matched, err := path.Match(patternValue, toolName); err == nil && matched {
			return true
		}
		if strings.HasSuffix(patternValue, "*") && strings.HasPrefix(toolName, strings.TrimSuffix(patternValue, "*")) {
			return true
		}
	}
	return false
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
