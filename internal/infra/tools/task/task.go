package task

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"yagent/internal/domain"
	"yagent/internal/infra/tools/execctx"
)

type listTool struct {
	catalog  domain.TaskCatalog
	bindings domain.MCPConnectionManager
}

type runTool struct {
	catalog  domain.TaskCatalog
	engine   domain.PolicyEngine
	approver domain.Approver
	memory   domain.RepoMemoryStore
}

type bindTool struct {
	catalog  domain.TaskCatalog
	bindings domain.MCPConnectionManager
	engine   domain.PolicyEngine
	approver domain.Approver
}

func NewListTool(catalog domain.TaskCatalog, bindings domain.MCPConnectionManager) domain.Tool {
	return &listTool{catalog: catalog, bindings: bindings}
}

func NewRunTool(catalog domain.TaskCatalog, engine domain.PolicyEngine, approver domain.Approver, memory domain.RepoMemoryStore) domain.Tool {
	return &runTool{catalog: catalog, engine: engine, approver: approver, memory: memory}
}

func NewBindTool(catalog domain.TaskCatalog, bindings domain.MCPConnectionManager, engine domain.PolicyEngine, approver domain.Approver) domain.Tool {
	return &bindTool{catalog: catalog, bindings: bindings, engine: engine, approver: approver}
}

func (t *listTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:            "task_list",
		Description:     "実行可能な登録済み task 一覧を返します。task_run の前に確認してください。",
		CapabilityGroup: "task_read",
		Risk:            "low",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Metadata: map[string]any{"category": "task"},
		Semantics: domain.ToolSemantics{
			Class:           domain.ToolClassObserve,
			ReusePolicy:     domain.ToolReuseOnSuccess,
			DuplicatePolicy: domain.ToolDuplicateSuppressInflight,
			Freshness:       domain.ToolFreshnessPolicy{Strategy: domain.ToolFreshnessSnapshot},
			SideEffectClass: domain.SideEffectNone,
			Source:          "task",
			IdentityArgs:    []string{},
			SourceLimit:     2,
		},
	}
}

func (t *runTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:             "task_run",
		Description:      "task_list に存在する task_id だけを実行します。自由コマンドは実行できません。",
		CapabilityGroup:  "task_exec",
		Risk:             "high",
		RequiresApproval: true,
		MutatesWorkspace: true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{"type": "string", "description": "task_list で確認した task id"},
			},
			"required": []string{"task_id"},
		},
		Metadata: map[string]any{"category": "task"},
		Semantics: domain.ToolSemantics{
			Class:           domain.ToolClassMutate,
			ReusePolicy:     domain.ToolReuseNever,
			DuplicatePolicy: domain.ToolDuplicateAllow,
			Freshness:       domain.ToolFreshnessPolicy{Strategy: domain.ToolFreshnessNone},
			SideEffectClass: domain.SideEffectProcess,
			Source:          "task",
			WritePathArgs:   []string{"task_id"},
			IdentityArgs:    []string{"task_id"},
			SourceLimit:     1,
		},
	}
}

func (t *bindTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:             "task_bind",
		Description:      "task_list に存在する MCP server task を bind して、その server の tool を利用可能にします。",
		CapabilityGroup:  "mcp",
		Risk:             "high",
		RequiresApproval: true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{"type": "string", "description": "task_list で確認した mcpserver id"},
			},
			"required": []string{"task_id"},
		},
		Metadata: map[string]any{"category": "task"},
		Semantics: domain.ToolSemantics{
			Class:           domain.ToolClassExecute,
			ReusePolicy:     domain.ToolReuseNever,
			DuplicatePolicy: domain.ToolDuplicateSuppressInflight,
			Freshness:       domain.ToolFreshnessPolicy{Strategy: domain.ToolFreshnessNone},
			SideEffectClass: domain.SideEffectExternal,
			Source:          "task",
			ReadPathArgs:    []string{"task_id"},
			IdentityArgs:    []string{"task_id"},
			SourceLimit:     1,
			Stateful:        true,
		},
	}
}

func (t *runTool) InferRuntime(ctx context.Context, _ domain.AgentSpec, call domain.ToolCall, _ domain.ToolDefinition) (domain.ToolRuntimeHint, bool) {
	taskID, _ := call.Arguments["task_id"].(string)
	if strings.TrimSpace(taskID) == "" {
		return domain.ToolRuntimeHint{}, false
	}
	taskDef, ok := t.catalog.Get(ctx, taskID)
	if !ok || taskDef.Kind != domain.TaskSpecKindCommand || taskDef.Command == nil {
		return domain.ToolRuntimeHint{}, false
	}
	readSet, writeSet := inferCommandAccess(taskDef)
	sideEffect := domain.SideEffectProcess
	if taskDef.Command.AllowNetwork {
		sideEffect = domain.SideEffectNetwork
	}
	return domain.ToolRuntimeHint{
		ReadSet:         readSet,
		WriteSet:        writeSet,
		ReplaceAccess:   true,
		SideEffectClass: sideEffect,
		Source:          "task:" + taskID,
		SourceLimit:     1,
	}, true
}

func (t *bindTool) InferRuntime(ctx context.Context, _ domain.AgentSpec, call domain.ToolCall, _ domain.ToolDefinition) (domain.ToolRuntimeHint, bool) {
	taskID, _ := call.Arguments["task_id"].(string)
	if strings.TrimSpace(taskID) == "" {
		return domain.ToolRuntimeHint{}, false
	}
	taskDef, ok := t.catalog.Get(ctx, taskID)
	if !ok || taskDef.Kind != domain.TaskSpecKindMCPServer || taskDef.MCPServer == nil {
		return domain.ToolRuntimeHint{}, false
	}
	scope := mcpTaskScope(taskDef.ID)
	readSet := []string{}
	if taskDef.MCPServer.Cwd != "" {
		readSet = append(readSet, taskDef.MCPServer.Cwd)
	}
	return domain.ToolRuntimeHint{
		ReadSet:         compactRuntimePaths(readSet),
		WriteSet:        []string{scope},
		ReplaceAccess:   true,
		SideEffectClass: domain.SideEffectExternal,
		Source:          "mcp:" + taskDef.ID,
		SourceLimit:     1,
	}, true
}

func (t *listTool) Execute(ctx context.Context, call domain.ToolCall) domain.ToolResult {
	items := t.catalog.List(ctx)
	type taskInfo struct {
		ID                string   `json:"id"`
		Description       string   `json:"description"`
		Kind              string   `json:"kind"`
		Command           string   `json:"command,omitempty"`
		Args              []string `json:"args,omitempty"`
		Cwd               string   `json:"cwd,omitempty"`
		Risk              string   `json:"risk,omitempty"`
		AllowNetwork      bool     `json:"allow_network"`
		BindRequired      bool     `json:"bind_required"`
		Bound             bool     `json:"bound"`
		Trust             string   `json:"trust,omitempty"`
		TrustAnnotations  bool     `json:"trust_tool_annotations,omitempty"`
		Roots             []string `json:"roots,omitempty"`
		ReadOnlyTools     []string `json:"read_only_tools,omitempty"`
		MutatingTools     []string `json:"mutating_tools,omitempty"`
		ParallelSafeTools []string `json:"parallel_safe_tools,omitempty"`
		UsageHint         string   `json:"usage_hint,omitempty"`
		BindHint          string   `json:"bind_hint,omitempty"`
		ExposedToolPrefix string   `json:"exposed_tool_prefix,omitempty"`
		ExposedTools      []string `json:"exposed_tools,omitempty"`
		Source            string   `json:"source,omitempty"`
	}
	bound := map[string][]string{}
	if t.bindings != nil {
		for _, item := range t.bindings.BoundTools() {
			bound[item.TaskID] = append(bound[item.TaskID], item.QualifiedName)
		}
	}
	result := make([]taskInfo, 0, len(items))
	for _, item := range items {
		info := taskInfo{
			ID:          item.ID,
			Description: item.Description,
			Kind:        string(item.Kind),
			Source:      item.Source,
		}
		info.ExposedTools = append(info.ExposedTools, bound[item.ID]...)
		info.Bound = len(info.ExposedTools) > 0
		switch item.Kind {
		case domain.TaskSpecKindCommand:
			if item.Command != nil {
				info.Command = item.Command.Command
				info.Args = append([]string(nil), item.Command.Args...)
				info.Cwd = item.Command.Cwd
				info.Risk = item.Command.Risk
				info.AllowNetwork = item.Command.AllowNetwork
			}
		case domain.TaskSpecKindMCPServer:
			info.BindRequired = true
			info.ExposedToolPrefix = exposedToolPrefix(item)
			info.UsageHint = "MCP tools are exposed lazily. Check task_list first, then bind a relevant server before concluding MCP is unavailable."
			info.BindHint = fmt.Sprintf("If this MCP server is relevant and not bound yet, call task_bind with task_id=%q.", item.ID)
			if item.MCPServer != nil {
				info.Command = item.MCPServer.Command
				info.Args = append([]string(nil), item.MCPServer.Args...)
				info.Cwd = item.MCPServer.Cwd
				info.Risk = item.MCPServer.Risk
				info.AllowNetwork = item.MCPServer.AllowNetwork
				info.Roots = append([]string(nil), item.MCPServer.Roots...)
				info.Trust = item.MCPServer.Trust
				info.TrustAnnotations = item.MCPServer.TrustToolAnnotations
				info.ReadOnlyTools = append([]string(nil), item.MCPServer.ReadOnlyTools...)
				info.MutatingTools = append([]string(nil), item.MCPServer.MutatingTools...)
				info.ParallelSafeTools = append([]string(nil), item.MCPServer.ParallelSafeTools...)
			}
		}
		result = append(result, info)
	}
	return marshalSuccess(call, result)
}

func (t *runTool) Execute(ctx context.Context, call domain.ToolCall) domain.ToolResult {
	taskID, ok := call.Arguments["task_id"].(string)
	if !ok || taskID == "" {
		return failure(call, "missing_task_id", "task_id パラメータが必要です", nil)
	}
	taskDef, ok := t.catalog.Get(ctx, taskID)
	if !ok {
		return taskFailure(call, "unknown_task", fmt.Sprintf("登録されていない task_id です: %s", taskID), taskID, nil)
	}
	if taskDef.Kind != domain.TaskSpecKindCommand || taskDef.Command == nil {
		return taskFailure(call, "invalid_task_kind", fmt.Sprintf("task_run では command task だけ実行できます: %s", taskID), taskID, map[string]any{
			"kind": string(taskDef.Kind),
		})
	}
	if err := authorize(ctx, t.engine, t.approver, call, taskDef); err != nil {
		return taskFailure(call, "authorization_failed", err.Error(), taskID, nil)
	}

	timeout := time.Duration(taskDef.Command.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, taskDef.Command.Command, taskDef.Command.Args...)
	cmd.Dir = taskDef.Command.Cwd
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return taskFailure(call, "task_failed", "task failed: "+err.Error(), taskID, map[string]any{
			"stderr": stderr.String(),
		})
	}
	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\n[stderr]\n" + stderr.String()
	}
	if t.memory != nil && taskDef.Command != nil {
		_ = t.memory.RecordCommand(ctx, domain.CommandMemoryEntry{
			Command: taskDef.Command.Command + " " + strings.Join(taskDef.Command.Args, " "),
			Cwd:     taskDef.Command.Cwd,
			Summary: "task " + taskDef.ID,
			Success: true,
		})
	}
	return success(call, output)
}

func (t *bindTool) Execute(ctx context.Context, call domain.ToolCall) domain.ToolResult {
	taskID, ok := call.Arguments["task_id"].(string)
	if !ok || taskID == "" {
		return failure(call, "missing_task_id", "task_id パラメータが必要です", nil)
	}
	taskDef, ok := t.catalog.Get(ctx, taskID)
	if !ok {
		return taskFailure(call, "unknown_task", fmt.Sprintf("登録されていない task_id です: %s", taskID), taskID, nil)
	}
	if taskDef.Kind != domain.TaskSpecKindMCPServer {
		return taskFailure(call, "invalid_task_kind", fmt.Sprintf("task_bind では MCP server task だけ bind できます: %s", taskID), taskID, map[string]any{
			"kind": string(taskDef.Kind),
		})
	}
	if err := authorize(ctx, t.engine, t.approver, call, taskDef); err != nil {
		return taskFailure(call, "authorization_failed", err.Error(), taskID, nil)
	}

	bindCtx := ctx
	cancel := func() {}
	if taskDef.MCPServer != nil && taskDef.MCPServer.Timeout > 0 {
		bindCtx, cancel = context.WithTimeout(ctx, time.Duration(taskDef.MCPServer.Timeout)*time.Second)
	}
	defer cancel()

	tools, err := t.bindings.Bind(bindCtx, taskDef)
	if err != nil {
		return taskFailure(call, "bind_failed", err.Error(), taskID, nil)
	}
	exposedToolNames := boundToolNamesForTask(t.bindings, taskDef.ID)
	result := map[string]any{
		"task_id":             taskDef.ID,
		"kind":                taskDef.Kind,
		"tool_count":          len(exposedToolNames),
		"tool_names":          exposedToolNames,
		"server_tool_names":   toolNames(tools),
		"description":         taskDef.Description,
		"bound_tools":         len(t.bindings.BoundTools()),
		"bind_status":         "ready",
		"next_action_hint":    "Use one of tool_names directly in your next tool call.",
		"exposed_tool_prefix": exposedToolPrefix(taskDef),
		"source":              taskDef.Source,
	}
	return marshalSuccess(call, result)
}

func authorize(ctx context.Context, engine domain.PolicyEngine, approver domain.Approver, call domain.ToolCall, taskDef domain.TaskDefinition) error {
	if engine == nil || approver == nil {
		return nil
	}
	policyCall := callWithTaskPolicyMetadata(call, taskDef)
	decision, request, err := engine.Evaluate(ctx, policyCall)
	if err != nil {
		return err
	}
	execctx.FillPermissionRequest(ctx, &request)
	var (
		allowNetwork bool
		command      string
		args         []string
		description  = taskDef.Description
	)
	switch taskDef.Kind {
	case domain.TaskSpecKindCommand:
		if taskDef.Command != nil {
			allowNetwork = taskDef.Command.AllowNetwork
			command = taskDef.Command.Command
			args = append(args, taskDef.Command.Args...)
		}
	case domain.TaskSpecKindMCPServer:
		if taskDef.MCPServer != nil {
			allowNetwork = taskDef.MCPServer.AllowNetwork
			command = taskDef.MCPServer.Command
			args = append(args, taskDef.MCPServer.Args...)
		}
	}
	if allowNetwork {
		request.SideEffects = append(request.SideEffects, "network_access")
		request.Risk = "high"
	}
	request.Summary = fmt.Sprintf("%s (%s %v)", description, command, args)
	if decision == domain.PolicyAllow {
		return nil
	}
	if decision == domain.PolicyDeny {
		return fmt.Errorf("この操作は policy により拒否されました")
	}
	userDecision, err := approver.Approve(ctx, request)
	if err != nil {
		return err
	}
	if userDecision == domain.PermissionDeny {
		return fmt.Errorf("ユーザーによってキャンセルされました")
	}
	return nil
}

func callWithTaskPolicyMetadata(call domain.ToolCall, taskDef domain.TaskDefinition) domain.ToolCall {
	args := make(map[string]any, len(call.Arguments)+2)
	for key, value := range call.Arguments {
		args[key] = value
	}
	switch taskDef.Kind {
	case domain.TaskSpecKindCommand:
		if taskDef.Command != nil {
			args["_policy_risk"] = taskDef.Command.Risk
			args["_policy_allow_network"] = taskDef.Command.AllowNetwork
		}
	case domain.TaskSpecKindMCPServer:
		if taskDef.MCPServer != nil {
			args["_policy_risk"] = taskDef.MCPServer.Risk
			args["_policy_allow_network"] = taskDef.MCPServer.AllowNetwork
		}
	}
	call.Arguments = args
	return call
}

func inferCommandAccess(taskDef domain.TaskDefinition) ([]string, []string) {
	if taskDef.Command == nil {
		return nil, nil
	}
	readSet := append([]string(nil), taskDef.Command.ReadPaths...)
	writeSet := append([]string(nil), taskDef.Command.WritePaths...)
	if len(readSet) == 0 && len(writeSet) == 0 && taskDef.Command.Cwd != "" {
		writeSet = append(writeSet, taskDef.Command.Cwd)
	}
	return compactRuntimePaths(readSet), compactRuntimePaths(writeSet)
}

func compactRuntimePaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, 0, len(paths))
	seen := map[string]struct{}{}
	for _, path := range paths {
		path = filepath.Clean(strings.TrimSpace(path))
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

func mcpTaskScope(taskID string) string {
	return "mcp/" + strings.TrimSpace(taskID) + "/state"
}

func marshalSuccess(call domain.ToolCall, value any) domain.ToolResult {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return failure(call, "marshal_failed", err.Error(), nil)
	}
	return success(call, string(data))
}

func success(call domain.ToolCall, output string) domain.ToolResult {
	return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: true, Output: output}
}

type taskErrorOutput struct {
	OK    bool          `json:"ok"`
	Error taskErrorBody `json:"error"`
}

type taskErrorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Tool    string         `json:"tool"`
	TaskID  string         `json:"task_id,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

func taskFailure(call domain.ToolCall, code string, message string, taskID string, details map[string]any) domain.ToolResult {
	if details == nil {
		details = map[string]any{}
	}
	if taskID != "" {
		details["task_id"] = taskID
	}
	return failure(call, code, message, details)
}

func failure(call domain.ToolCall, code string, message string, details map[string]any) domain.ToolResult {
	taskID := ""
	if details != nil {
		if value, ok := details["task_id"].(string); ok {
			taskID = value
			delete(details, "task_id")
		}
		if len(details) == 0 {
			details = nil
		}
	}
	payload := taskErrorOutput{
		OK: false,
		Error: taskErrorBody{
			Code:    code,
			Message: message,
			Tool:    call.Name,
			TaskID:  taskID,
			Details: details,
		},
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: false, Output: "エラー: " + message}
	}
	return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: false, Output: string(data)}
}

func toolNames(items []domain.MCPToolDescriptor) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item.Name)
	}
	return result
}

func boundToolNamesForTask(bindings domain.MCPConnectionManager, taskID string) []string {
	if bindings == nil {
		return nil
	}
	names := []string{}
	for _, item := range bindings.BoundTools() {
		if item.TaskID != taskID {
			continue
		}
		names = append(names, item.QualifiedName)
	}
	return names
}

func exposedToolPrefix(task domain.TaskDefinition) string {
	if task.MCPServer == nil || task.MCPServer.ToolPrefix == "" {
		return task.ID
	}
	return task.MCPServer.ToolPrefix
}
