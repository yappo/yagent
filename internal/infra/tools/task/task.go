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
	catalog domain.TaskCatalog
}

type runTool struct {
	catalog  domain.TaskCatalog
	engine   domain.PolicyEngine
	approver domain.Approver
}

func NewListTool(catalog domain.TaskCatalog) domain.Tool {
	return &listTool{catalog: catalog}
}

func NewRunTool(catalog domain.TaskCatalog, engine domain.PolicyEngine, approver domain.Approver) domain.Tool {
	return &runTool{catalog: catalog, engine: engine, approver: approver}
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

func (t *listTool) Execute(ctx context.Context, call domain.ToolCall) domain.ToolResult {
	items := t.catalog.List(ctx)
	type taskInfo struct {
		ID           string   `json:"id"`
		Description  string   `json:"description"`
		Command      string   `json:"command"`
		Args         []string `json:"args"`
		Cwd          string   `json:"cwd"`
		Risk         string   `json:"risk"`
		AllowNetwork bool     `json:"allow_network"`
	}
	result := make([]taskInfo, 0, len(items))
	for _, item := range items {
		result = append(result, taskInfo{
			ID:           item.ID,
			Description:  item.Description,
			Command:      item.Command,
			Args:         append([]string(nil), item.Args...),
			Cwd:          item.Cwd,
			Risk:         item.Risk,
			AllowNetwork: item.AllowNetwork,
		})
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
	if err := authorize(ctx, t.engine, t.approver, call, taskDef); err != nil {
		return failure(call, err.Error())
	}

	timeout := time.Duration(taskDef.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, taskDef.Command, taskDef.Args...)
	cmd.Dir = taskDef.Cwd
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
	if taskDef.AllowNetwork {
		request.SideEffects = append(request.SideEffects, "network_access")
		request.Risk = "high"
	}
	request.Summary = fmt.Sprintf("%s (%s %v)", taskDef.Description, taskDef.Command, taskDef.Args)
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
