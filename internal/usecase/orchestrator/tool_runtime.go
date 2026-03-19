package orchestrator

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"yagent/internal/domain"
)

type toolRuntimeSpec struct {
	call           domain.ToolCall
	definition     domain.ToolDefinition
	normalizedArgs string
	semanticKey    string
	readSet        []string
	writeSet       []string
	pathStates     []domain.WorkspacePathState
	semantics      domain.ToolSemantics
}

type toolRuntimeDescriptor struct {
	normalizedArgs string
	semanticKey    string
	readSet        []string
	writeSet       []string
	semantics      domain.ToolSemantics
}

func (s *Service) prepareToolRuntimeSpec(ctx context.Context, agent domain.AgentSpec, item executableCall) toolRuntimeSpec {
	descriptor := s.describeToolRuntime(ctx, agent, item)
	pathStates := s.capturePathStates(ctx, append([]string(nil), descriptor.readSet...))
	return toolRuntimeSpec{
		call:           item.call,
		definition:     item.definition,
		normalizedArgs: descriptor.normalizedArgs,
		semanticKey:    descriptor.semanticKey,
		readSet:        descriptor.readSet,
		writeSet:       descriptor.writeSet,
		pathStates:     pathStates,
		semantics:      descriptor.semantics,
	}
}

func (s *Service) describeToolRuntime(ctx context.Context, agent domain.AgentSpec, item executableCall) toolRuntimeDescriptor {
	normalizedArgs := normalizeArguments(item.call.Arguments)
	semantics := effectiveSemantics(item.definition)
	hint := s.inferToolRuntime(ctx, agent, item.call, item.definition)
	semantics = applyRuntimeHint(semantics, hint)
	semanticArgs := semanticArguments(item.call.Arguments, semantics.IdentityArgs)
	readSet, writeSet := resolveAccessSets(item.call, item.definition, semantics, hint)
	return toolRuntimeDescriptor{
		normalizedArgs: normalizedArgs,
		semanticKey:    semanticFingerprint(item.call.Name, semanticArgs),
		readSet:        readSet,
		writeSet:       writeSet,
		semantics:      semantics,
	}
}

func effectiveSemantics(def domain.ToolDefinition) domain.ToolSemantics {
	sem := def.Semantics
	if sem.Class == "" {
		switch {
		case def.MutatesWorkspace:
			sem.Class = domain.ToolClassMutate
		case def.ReadOnly || strings.HasPrefix(def.Name, "fs_") || strings.HasPrefix(def.Name, "git_") || strings.HasPrefix(def.Name, "search_"):
			sem.Class = domain.ToolClassObserve
		default:
			sem.Class = domain.ToolClassExecute
		}
	}
	if sem.ReusePolicy == "" {
		switch sem.Class {
		case domain.ToolClassObserve, domain.ToolClassCompute:
			sem.ReusePolicy = domain.ToolReuseOnSuccess
		default:
			sem.ReusePolicy = domain.ToolReuseNever
		}
	}
	if sem.DuplicatePolicy == "" {
		switch sem.Class {
		case domain.ToolClassObserve, domain.ToolClassCompute:
			sem.DuplicatePolicy = domain.ToolDuplicateSuppressInflight
		default:
			sem.DuplicatePolicy = domain.ToolDuplicateAllow
		}
	}
	if sem.Freshness.Strategy == "" {
		switch sem.Class {
		case domain.ToolClassObserve, domain.ToolClassCompute:
			sem.Freshness.Strategy = domain.ToolFreshnessReadSet
		default:
			sem.Freshness.Strategy = domain.ToolFreshnessNone
		}
	}
	if sem.SideEffectClass == "" {
		switch sem.Class {
		case domain.ToolClassMutate:
			sem.SideEffectClass = domain.SideEffectWorkspace
		case domain.ToolClassExecute:
			sem.SideEffectClass = domain.SideEffectProcess
		default:
			sem.SideEffectClass = domain.SideEffectNone
		}
	}
	if sem.Source == "" {
		if value, ok := def.Metadata["category"].(string); ok && value != "" {
			sem.Source = value
		} else {
			sem.Source = "tool"
		}
	}
	if sem.SourceLimit <= 0 {
		sem.SourceLimit = defaultSourceLimit(sem.Source)
	}
	return sem
}

func resolveAccessSets(call domain.ToolCall, def domain.ToolDefinition, sem domain.ToolSemantics, hint domain.ToolRuntimeHint) ([]string, []string) {
	readSet := valuesFromArgs(call.Arguments, sem.ReadPathArgs)
	writeSet := valuesFromArgs(call.Arguments, sem.WritePathArgs)

	if !hint.ReplaceAccess && (len(readSet) > 0 || len(writeSet) > 0) {
		readSet = append(readSet, hint.ReadSet...)
		writeSet = append(writeSet, hint.WriteSet...)
		readSet = compactPaths(readSet)
		writeSet = compactPaths(writeSet)
		return readSet, writeSet
	}

	if !hint.ReplaceAccess && len(readSet) == 0 && len(writeSet) == 0 {
		switch call.Name {
		case "fs_read", "fs_stat", "fs_list":
			readSet = append(readSet, firstPathArg(call.Arguments, "path"))
		case "fs_write":
			writeSet = append(writeSet, firstPathArg(call.Arguments, "path"))
		case "fs_remove":
			writeSet = append(writeSet, firstPathArg(call.Arguments, "path"))
		case "fs_move":
			readSet = append(readSet, firstPathArg(call.Arguments, "source_path"))
			writeSet = append(writeSet, firstPathArg(call.Arguments, "source_path"), firstPathArg(call.Arguments, "destination_path"))
		case "search_text", "search_files":
			readSet = append(readSet, firstPathArg(call.Arguments, "root"))
		case "git_status", "git_diff", "git_log", "git_show":
			readSet = append(readSet, firstPathArg(call.Arguments, "repo_path"))
		case "task_run":
			writeSet = append(writeSet, firstPathArg(call.Arguments, "task_id"))
		case "task_bind":
			readSet = append(readSet, firstPathArg(call.Arguments, "task_id"))
		case "patch_apply":
			writeSet = append(writeSet, patchOperationPaths(call.Arguments)...)
		default:
			if strings.HasPrefix(call.Name, "mcp__") {
				if sem.Stateful || !def.ReadOnly {
					writeSet = append(writeSet, call.Name)
				} else {
					readSet = append(readSet, call.Name)
				}
			}
		}
	}

	if hint.ReplaceAccess {
		readSet = append([]string(nil), hint.ReadSet...)
		writeSet = append([]string(nil), hint.WriteSet...)
	} else {
		readSet = append(readSet, hint.ReadSet...)
		writeSet = append(writeSet, hint.WriteSet...)
	}

	readSet = compactPaths(readSet)
	writeSet = compactPaths(writeSet)
	return readSet, writeSet
}

func patchOperationPaths(args map[string]any) []string {
	raw, ok := args["operations"].([]any)
	if !ok {
		return nil
	}
	paths := make([]string, 0, len(raw))
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if path, ok := entry["path"].(string); ok && path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

func valuesFromArgs(args map[string]any, keys []string) []string {
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, firstPathArg(args, key))
	}
	return compactPaths(values)
}

func firstPathArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}

func compactPaths(paths []string) []string {
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
	sort.Strings(out)
	return out
}

func normalizeArguments(args map[string]any) string {
	if len(args) == 0 {
		return "{}"
	}
	data, err := json.Marshal(canonicalizeValue(args))
	if err != nil {
		return "{}"
	}
	return string(data)
}

func semanticArguments(args map[string]any, keys []string) string {
	if keys == nil {
		return normalizeArguments(args)
	}
	filtered := make(map[string]any, len(keys))
	for _, key := range keys {
		if value, ok := args[key]; ok {
			filtered[key] = value
		}
	}
	return normalizeArguments(filtered)
}

func canonicalizeValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(typed))
		for _, key := range keys {
			out[key] = canonicalizeValue(typed[key])
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, canonicalizeValue(item))
		}
		return out
	default:
		return value
	}
}

func semanticFingerprint(name string, normalizedArgs string) string {
	sum := sha1.Sum([]byte(name + "\n" + normalizedArgs))
	return name + ":" + hex.EncodeToString(sum[:])
}

func (s *Service) inferToolRuntime(ctx context.Context, agent domain.AgentSpec, call domain.ToolCall, def domain.ToolDefinition) domain.ToolRuntimeHint {
	inspector, ok := s.tools.(domain.ToolRuntimeInspector)
	if !ok {
		return domain.ToolRuntimeHint{}
	}
	hint, ok := inspector.InferRuntime(ctx, agent, call, def)
	if !ok {
		return domain.ToolRuntimeHint{}
	}
	return hint
}

func applyRuntimeHint(sem domain.ToolSemantics, hint domain.ToolRuntimeHint) domain.ToolSemantics {
	if hint.SideEffectClass != "" {
		sem.SideEffectClass = hint.SideEffectClass
	}
	if hint.Source != "" {
		sem.Source = hint.Source
	}
	if hint.SourceLimit > 0 {
		sem.SourceLimit = hint.SourceLimit
	}
	return sem
}

func (s *Service) capturePathStates(_ context.Context, paths []string) []domain.WorkspacePathState {
	states := make([]domain.WorkspacePathState, 0, len(paths))
	for _, path := range compactPaths(paths) {
		state := domain.WorkspacePathState{
			Path:       path,
			ObservedAt: time.Now(),
		}
		info, err := os.Stat(path)
		if err == nil {
			state.Exists = true
			state.IsDir = info.IsDir()
			state.Size = info.Size()
			state.ModTimeUnix = info.ModTime().UnixNano()
		}
		states = append(states, state)
	}
	return states
}

func nextExecutionID(prefix string, semanticKey string) string {
	short := semanticKey
	if len(short) > 16 {
		short = short[len(short)-16:]
	}
	return fmt.Sprintf("%s-%s", prefix, strings.Trim(short, ":"))
}

func snapshotFromStates(states []domain.WorkspacePathState) *domain.WorkspaceSnapshot {
	snapshot := &domain.WorkspaceSnapshot{Paths: map[string]domain.WorkspacePathState{}, UpdatedAt: time.Now()}
	for _, state := range states {
		snapshot.Paths[state.Path] = state
	}
	return snapshot
}

func writeSetFingerprint(paths []string) string {
	sum := sha1.Sum([]byte(strings.Join(compactPaths(paths), "\n")))
	return hex.EncodeToString(sum[:])
}

func normalizePathForWorkspace(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return path
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}
