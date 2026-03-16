package task

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
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
}

type bindTool struct {
	catalog   domain.TaskCatalog
	bindings  domain.MCPConnectionManager
	engine    domain.PolicyEngine
	approver  domain.Approver
}

func NewListTool(catalog domain.TaskCatalog, bindings domain.MCPConnectionManager) domain.Tool {
	return &listTool{catalog: catalog, bindings: bindings}
}

func NewRunTool(catalog domain.TaskCatalog, engine domain.PolicyEngine, approver domain.Approver) domain.Tool {
	return &runTool{catalog: catalog, engine: engine, approver: approver}
}

func NewBindTool(catalog domain.TaskCatalog, bindings domain.MCPConnectionManager, engine domain.PolicyEngine, approver domain.Approver) domain.Tool {
	return &bindTool{catalog: catalog, bindings: bindings, engine: engine, approver: approver}
}

func (t *listTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "task_list",
		Description: "実行可能な登録済み task 一覧を返します。task_run の前に確認してください。",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Metadata: map[string]any{"category": "task"},
	}
}

func (t *runTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "task_run",
		Description: "task_list に存在する task_id だけを実行します。自由コマンドは実行できません。",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{"type": "string", "description": "task_list で確認した task id"},
			},
			"required": []string{"task_id"},
		},
		Metadata: map[string]any{"category": "task"},
	}
}

func (t *bindTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "task_bind",
		Description: "task_list に存在する MCP server task を bind して、その server の tool を利用可能にします。",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{"type": "string", "description": "task_list で確認した mcpserver id"},
			},
			"required": []string{"task_id"},
		},
		Metadata: map[string]any{"category": "task"},
	}
}

func (t *listTool) Execute(ctx context.Context, call domain.ToolCall) domain.ToolResult {
	items := t.catalog.List(ctx)
	type taskInfo struct {
		ID           string   `json:"id"`
		Description  string   `json:"description"`
		Kind         string   `json:"kind"`
		Command      string   `json:"command,omitempty"`
		Args         []string `json:"args,omitempty"`
		Cwd          string   `json:"cwd,omitempty"`
		Risk         string   `json:"risk,omitempty"`
		AllowNetwork bool     `json:"allow_network"`
		BindRequired bool     `json:"bind_required"`
		Bound        bool     `json:"bound"`
		Source       string   `json:"source,omitempty"`
	}
	bound := map[string]struct{}{}
	if t.bindings != nil {
		for _, item := range t.bindings.BoundTools() {
			bound[item.TaskID] = struct{}{}
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
		_, info.Bound = bound[item.ID]
		switch item.Kind {
		case domain.TaskKindCommand:
			if item.Command != nil {
				info.Command = item.Command.Command
				info.Args = append([]string(nil), item.Command.Args...)
				info.Cwd = item.Command.Cwd
				info.Risk = item.Command.Risk
				info.AllowNetwork = item.Command.AllowNetwork
			}
		case domain.TaskKindMCPServer:
			info.BindRequired = true
			if item.MCPServer != nil {
				info.Command = item.MCPServer.Command
				info.Args = append([]string(nil), item.MCPServer.Args...)
				info.Cwd = item.MCPServer.Cwd
				info.Risk = item.MCPServer.Risk
				info.AllowNetwork = item.MCPServer.AllowNetwork
			}
		}
		result = append(result, info)
	}
	return marshalSuccess(call, result)
}

func (t *runTool) Execute(ctx context.Context, call domain.ToolCall) domain.ToolResult {
	taskID, ok := call.Arguments["task_id"].(string)
	if !ok || taskID == "" {
		return failure(call, "task_id パラメータが必要です")
	}
	taskDef, ok := t.catalog.Get(ctx, taskID)
	if !ok {
		return failure(call, fmt.Sprintf("登録されていない task_id です: %s", taskID))
	}
	if taskDef.Kind != domain.TaskKindCommand || taskDef.Command == nil {
		return failure(call, fmt.Sprintf("task_run では command task だけ実行できます: %s", taskID))
	}
	if err := authorize(ctx, t.engine, t.approver, call, taskDef); err != nil {
		return failure(call, err.Error())
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
		return failure(call, fmt.Sprintf("task failed: %v\n%s", err, stderr.String()))
	}
	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\n[stderr]\n" + stderr.String()
	}
	return success(call, output)
}

func (t *bindTool) Execute(ctx context.Context, call domain.ToolCall) domain.ToolResult {
	taskID, ok := call.Arguments["task_id"].(string)
	if !ok || taskID == "" {
		return failure(call, "task_id パラメータが必要です")
	}
	taskDef, ok := t.catalog.Get(ctx, taskID)
	if !ok {
		return failure(call, fmt.Sprintf("登録されていない task_id です: %s", taskID))
	}
	if taskDef.Kind != domain.TaskKindMCPServer {
		return failure(call, fmt.Sprintf("task_bind では MCP server task だけ bind できます: %s", taskID))
	}
	if err := authorize(ctx, t.engine, t.approver, call, taskDef); err != nil {
		return failure(call, err.Error())
	}

	bindCtx := ctx
	cancel := func() {}
	if taskDef.MCPServer != nil && taskDef.MCPServer.Timeout > 0 {
		bindCtx, cancel = context.WithTimeout(ctx, time.Duration(taskDef.MCPServer.Timeout)*time.Second)
	}
	defer cancel()

	tools, err := t.bindings.Bind(bindCtx, taskDef)
	if err != nil {
		return failure(call, err.Error())
	}
	result := map[string]any{
		"task_id":      taskDef.ID,
		"kind":         taskDef.Kind,
		"tool_count":   len(tools),
		"tool_names":   toolNames(tools),
		"description":  taskDef.Description,
		"bound_tools":  len(t.bindings.BoundTools()),
		"bind_status":  "ready",
		"source":       taskDef.Source,
	}
	return marshalSuccess(call, result)
}

func authorize(ctx context.Context, engine domain.PolicyEngine, approver domain.Approver, call domain.ToolCall, taskDef domain.TaskDefinition) error {
	if engine == nil || approver == nil {
		return nil
	}
	decision, request, err := engine.Evaluate(ctx, call)
	if err != nil {
		return err
	}
	request.AgentID = execctx.AgentID(ctx)
	request.Purpose = execctx.Purpose(ctx)
	var (
		allowNetwork bool
		command      string
		args         []string
		description  = taskDef.Description
	)
	switch taskDef.Kind {
	case domain.TaskKindCommand:
		if taskDef.Command != nil {
			allowNetwork = taskDef.Command.AllowNetwork
			command = taskDef.Command.Command
			args = append(args, taskDef.Command.Args...)
		}
	case domain.TaskKindMCPServer:
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

func marshalSuccess(call domain.ToolCall, value any) domain.ToolResult {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return failure(call, err.Error())
	}
	return success(call, string(data))
}

func success(call domain.ToolCall, output string) domain.ToolResult {
	return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: true, Output: output}
}

func failure(call domain.ToolCall, output string) domain.ToolResult {
	return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: false, Output: "エラー: " + output}
}

func toolNames(items []domain.MCPToolDescriptor) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item.Name)
	}
	return result
}
