package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"yagent/internal/domain"
)

type fakeModelClient struct {
	responses map[string][]domain.ModelResponse
	indexes   map[string]int
	inspect   func(domain.ModelRequest)
}

func (f *fakeModelClient) Generate(_ context.Context, request domain.ModelRequest) (domain.ModelResponse, error) {
	if f.indexes == nil {
		f.indexes = map[string]int{}
	}
	if f.inspect != nil {
		f.inspect(request)
	}
	agentID := request.Agent.ID
	idx := f.indexes[agentID]
	if idx >= len(f.responses[agentID]) {
		panic(fmt.Sprintf("missing fake response for agent %q at index %d", agentID, idx))
	}
	response := f.responses[agentID][idx]
	f.indexes[agentID] = idx + 1
	return response, nil
}

type fakeToolExecutor struct {
	defs  map[string][]domain.ToolDefinition
	calls []domain.ToolCall
	exec  func(context.Context, domain.AgentSpec, domain.ToolCall) domain.ToolResult
}

func (f *fakeToolExecutor) Definitions(agent domain.AgentSpec) []domain.ToolDefinition {
	if defs, ok := f.defs[agent.ID]; ok {
		return defs
	}
	return nil
}

func (f *fakeToolExecutor) Execute(ctx context.Context, agent domain.AgentSpec, call domain.ToolCall) domain.ToolResult {
	if f.exec != nil {
		return f.exec(ctx, agent, call)
	}
	f.calls = append(f.calls, call)
	return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: true, Output: "tool output"}
}

type fakeCatalog struct {
	agents map[string]domain.AgentSpec
}

type fakeApprover struct {
	decision domain.PermissionDecision
	requests []domain.PermissionRequest
}

func (f *fakeApprover) Approve(_ context.Context, request domain.PermissionRequest) (domain.PermissionDecision, error) {
	f.requests = append(f.requests, request)
	return f.decision, nil
}

type inspectingApprover struct {
	decision    domain.PermissionDecision
	hadDeadline bool
}

func (a *inspectingApprover) Approve(ctx context.Context, request domain.PermissionRequest) (domain.PermissionDecision, error) {
	if _, ok := ctx.Deadline(); ok {
		a.hadDeadline = true
	}
	return a.decision, nil
}

func (f fakeCatalog) List() []domain.AgentSpec {
	out := make([]domain.AgentSpec, 0, len(f.agents))
	for _, agent := range f.agents {
		out = append(out, agent)
	}
	return out
}

func (f fakeCatalog) Resolve(id string) (domain.AgentSpec, bool) {
	agent, ok := f.agents[id]
	return agent, ok
}

func (f fakeCatalog) LoadUserAgents(_ []string) error {
	return nil
}

func TestRunTurnDelegatesToBuiltInAgent(t *testing.T) {
	service := New(
		&fakeModelClient{
			responses: map[string][]domain.ModelResponse{
				"manager": {{
					Message: domain.Message{
						Role: domain.RoleAssistant,
						ToolCalls: []domain.ToolCall{{
							ID:   "1",
							Name: "delegate_to_researcher",
							Arguments: map[string]any{
								"task": "Inspect files",
							},
						}},
					},
				}, {
					Message: domain.Message{Role: domain.RoleAssistant, Content: "done"},
				}},
				"researcher": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: "findings"},
				}},
			},
		},
		&fakeToolExecutor{},
		fakeCatalog{agents: map[string]domain.AgentSpec{
			"manager":    {ID: "manager", Mode: domain.AgentModeManager, MaxTurns: 4},
			"researcher": {ID: "researcher", Mode: domain.AgentModeTool, ReadOnly: true, MaxTurns: 4},
		}},
		Config{MaxParallelAgents: 1, MaxHandoffDepth: 2},
	)

	result, err := service.RunTurn(context.Background(), domain.TurnRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("RunTurn returned error: %v", err)
	}
	if result.Message.Content != "done" {
		t.Fatalf("unexpected result: %q", result.Message.Content)
	}
}

func TestRunTurnSupportsHandoff(t *testing.T) {
	service := New(
		&fakeModelClient{
			responses: map[string][]domain.ModelResponse{
				"manager": {{
					Message: domain.Message{
						Role: domain.RoleAssistant,
						ToolCalls: []domain.ToolCall{{
							ID:   "1",
							Name: "handoff_to_coder",
							Arguments: map[string]any{
								"task": "Implement change",
							},
						}},
					},
				}},
				"coder": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: "implemented"},
				}},
			},
		},
		&fakeToolExecutor{},
		fakeCatalog{agents: map[string]domain.AgentSpec{
			"manager": {ID: "manager", Mode: domain.AgentModeManager, MaxTurns: 4},
			"coder":   {ID: "coder", Mode: domain.AgentModeHandoff, MaxTurns: 4},
		}},
		Config{MaxParallelAgents: 1, MaxHandoffDepth: 2},
	)

	result, err := service.RunTurn(context.Background(), domain.TurnRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("RunTurn returned error: %v", err)
	}
	if result.Message.Content != "implemented" {
		t.Fatalf("unexpected handoff result: %q", result.Message.Content)
	}
}

func TestRunTurnSupportsEphemeralAgent(t *testing.T) {
	service := New(
		&fakeModelClient{
			responses: map[string][]domain.ModelResponse{
				"manager": {{
					Message: domain.Message{
						Role: domain.RoleAssistant,
						ToolCalls: []domain.ToolCall{{
							ID:   "1",
							Name: "run_ephemeral_agent",
							Arguments: map[string]any{
								"task":        "Summarize",
								"instruction": "Be concise",
								"read_only":   true,
							},
						}},
					},
				}, {
					Message: domain.Message{Role: domain.RoleAssistant, Content: "done"},
				}},
				"ephemeral": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: "ephemeral output"},
				}},
			},
		},
		&fakeToolExecutor{},
		fakeCatalog{agents: map[string]domain.AgentSpec{
			"manager": {ID: "manager", Mode: domain.AgentModeManager, MaxTurns: 4},
		}},
		Config{MaxParallelAgents: 1, MaxHandoffDepth: 2},
	)

	result, err := service.RunTurn(context.Background(), domain.TurnRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("RunTurn returned error: %v", err)
	}
	if result.Message.Content != "done" {
		t.Fatalf("unexpected result: %q", result.Message.Content)
	}
}

func TestRunTurnCanDisablePhaseHarness(t *testing.T) {
	service := New(
		&fakeModelClient{
			responses: map[string][]domain.ModelResponse{
				"manager": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: "done directly"},
				}},
			},
		},
		&fakeToolExecutor{},
		fakeCatalog{agents: map[string]domain.AgentSpec{
			"manager": {ID: "manager", Mode: domain.AgentModeManager, MaxTurns: 4},
		}},
		Config{MaxParallelAgents: 1, MaxHandoffDepth: 2, DisablePhaseHarness: true},
	)

	result, err := service.RunTurn(context.Background(), domain.TurnRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("RunTurn returned error: %v", err)
	}
	if result.Message.Content != "done directly" {
		t.Fatalf("unexpected result: %q", result.Message.Content)
	}
	if result.Run == nil || result.Run.ExecutionPlan == nil || result.Run.ExecutionPlan.Source != "disabled_harness" || len(result.Run.Verification) != 0 {
		t.Fatalf("expected disabled harness execution plan, got %+v", result.Run)
	}
}

func TestRunTurnSeesBoundMCPToolsOnLaterTurn(t *testing.T) {
	seenBoundTool := false
	var executor *fakeToolExecutor
	executor = &fakeToolExecutor{
		defs: map[string][]domain.ToolDefinition{
			"manager": {
				{Name: "task_bind", Parameters: map[string]any{"type": "object"}},
			},
		},
		exec: func(_ context.Context, _ domain.AgentSpec, call domain.ToolCall) domain.ToolResult {
			if call.Name == "task_bind" {
				executor.defs["manager"] = append(executor.defs["manager"], domain.ToolDefinition{
					Name:       "mcp__docs__search_docs__docs",
					Parameters: map[string]any{"type": "object"},
				})
				return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: true, Output: "bound"}
			}
			return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: true, Output: "ok"}
		},
	}
	service := New(
		&fakeModelClient{
			responses: map[string][]domain.ModelResponse{
				"manager": {{
					Message: domain.Message{
						Role: domain.RoleAssistant,
						ToolCalls: []domain.ToolCall{{
							ID:        "1",
							Name:      "task_bind",
							Arguments: map[string]any{"task_id": "docs"},
						}},
					},
				}, {
					Message: domain.Message{Role: domain.RoleAssistant, Content: "done"},
				}},
			},
			inspect: func(request domain.ModelRequest) {
				for _, tool := range request.Tools {
					if tool.Name == "mcp__docs__search_docs__docs" {
						seenBoundTool = true
					}
				}
			},
		},
		executor,
		fakeCatalog{agents: map[string]domain.AgentSpec{
			"manager": {ID: "manager", Mode: domain.AgentModeManager, MaxTurns: 4},
		}},
		Config{MaxParallelAgents: 1, MaxHandoffDepth: 2},
	)

	result, err := service.RunTurn(context.Background(), domain.TurnRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "bind docs"}},
	})
	if err != nil {
		t.Fatalf("RunTurn returned error: %v", err)
	}
	if result.Message.Content != "done" {
		t.Fatalf("unexpected result: %q", result.Message.Content)
	}
	if !seenBoundTool {
		t.Fatal("expected bound MCP tool to appear on a later LLM turn")
	}
}

func TestToolMessageCarriesToolCallID(t *testing.T) {
	msg := toolMessage(domain.ToolCall{
		ID:                 "call-123",
		Name:               "fs_list",
		RequestedByAgentID: "manager",
	}, "output")

	if msg.Role != domain.RoleTool {
		t.Fatalf("unexpected role: %s", msg.Role)
	}
	if msg.ToolCallID != "call-123" {
		t.Fatalf("expected tool call id, got %+v", msg)
	}
}

func TestRunTurnUsesConfigDefaultModelWhenRequestAndAgentAreEmpty(t *testing.T) {
	var gotModel string
	service := New(
		&fakeModelClient{
			responses: map[string][]domain.ModelResponse{
				"manager": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: "done"},
				}},
			},
			inspect: func(request domain.ModelRequest) {
				gotModel = request.Model
			},
		},
		&fakeToolExecutor{},
		fakeCatalog{agents: map[string]domain.AgentSpec{
			"manager": {ID: "manager", Mode: domain.AgentModeManager, MaxTurns: 4},
		}},
		Config{MaxParallelAgents: 1, MaxHandoffDepth: 2, DefaultModel: "gpt-5"},
	)

	_, err := service.RunTurn(context.Background(), domain.TurnRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("RunTurn returned error: %v", err)
	}
	if gotModel != "gpt-5" {
		t.Fatalf("expected default model to be used, got %q", gotModel)
	}
}

func TestRunTurnPrefersRequestModelOverConfigDefault(t *testing.T) {
	var gotModel string
	service := New(
		&fakeModelClient{
			responses: map[string][]domain.ModelResponse{
				"manager": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: "done"},
				}},
			},
			inspect: func(request domain.ModelRequest) {
				gotModel = request.Model
			},
		},
		&fakeToolExecutor{},
		fakeCatalog{agents: map[string]domain.AgentSpec{
			"manager": {ID: "manager", Mode: domain.AgentModeManager, MaxTurns: 4},
		}},
		Config{MaxParallelAgents: 1, MaxHandoffDepth: 2, DefaultModel: "gpt-5"},
	)

	_, err := service.RunTurn(context.Background(), domain.TurnRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "hello"}},
		Model:    "gpt-5-mini",
	})
	if err != nil {
		t.Fatalf("RunTurn returned error: %v", err)
	}
	if gotModel != "gpt-5-mini" {
		t.Fatalf("expected request model to be used, got %q", gotModel)
	}
}

func TestRunTurnPrefersAgentModelOverRequestAndDefault(t *testing.T) {
	var gotModel string
	service := New(
		&fakeModelClient{
			responses: map[string][]domain.ModelResponse{
				"manager": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: "done"},
				}},
			},
			inspect: func(request domain.ModelRequest) {
				gotModel = request.Model
			},
		},
		&fakeToolExecutor{},
		fakeCatalog{agents: map[string]domain.AgentSpec{
			"manager": {ID: "manager", Mode: domain.AgentModeManager, MaxTurns: 4, Model: "agent-model"},
		}},
		Config{MaxParallelAgents: 1, MaxHandoffDepth: 2, DefaultModel: "default-model"},
	)

	_, err := service.RunTurn(context.Background(), domain.TurnRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "hello"}},
		Model:    "request-model",
	})
	if err != nil {
		t.Fatalf("RunTurn returned error: %v", err)
	}
	if gotModel != "agent-model" {
		t.Fatalf("expected agent model to be used, got %q", gotModel)
	}
}

func TestRunTurnCountsMessageAndFileContext(t *testing.T) {
	service := New(
		&fakeModelClient{
			responses: map[string][]domain.ModelResponse{
				"manager": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: "done"},
				}},
			},
		},
		&fakeToolExecutor{},
		fakeCatalog{agents: map[string]domain.AgentSpec{
			"manager": {ID: "manager", Mode: domain.AgentModeManager, MaxTurns: 4},
		}},
		Config{MaxParallelAgents: 1, MaxHandoffDepth: 2},
	)

	result, err := service.RunTurn(context.Background(), domain.TurnRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "check README.md and internal/app/bootstrap.go"}},
	})
	if err != nil {
		t.Fatalf("RunTurn returned error: %v", err)
	}
	if len(result.Events) == 0 {
		t.Fatalf("expected events")
	}
	if result.Events[0].ContextCount != 4 {
		t.Fatalf("expected context count 4, got %d", result.Events[0].ContextCount)
	}
}

func TestPlannerCannotDelegateToCoder(t *testing.T) {
	service := New(
		&fakeModelClient{
			responses: map[string][]domain.ModelResponse{
				"manager": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: "done"},
				}},
				"planner": {{
					Message: domain.Message{
						Role: domain.RoleAssistant,
						ToolCalls: []domain.ToolCall{{
							ID:   "1",
							Name: "delegate_to_coder",
							Arguments: map[string]any{
								"task": "Write a script",
							},
						}},
					},
				}, {
					Message: domain.Message{Role: domain.RoleAssistant, Content: `{
  "version": "v1",
  "mode": "direct",
  "task_kind": "question",
  "summary": "Answer directly after planning.",
  "primary": {
    "agent_id": "manager",
    "reason": "Respond directly to the user."
  },
  "steps": [
    {
      "id": "step-1",
      "title": "Execute primary task",
      "phase": "execute",
      "agent_id": "manager"
    }
  ]
}`},
				}},
			},
		},
		&fakeToolExecutor{},
		fakeCatalog{agents: map[string]domain.AgentSpec{
			"manager": {ID: "manager", Mode: domain.AgentModeManager, MaxTurns: 4},
			"planner": {ID: "planner", Mode: domain.AgentModeTool, ReadOnly: true, MaxTurns: 4},
			"coder":   {ID: "coder", Mode: domain.AgentModeHandoff, MaxTurns: 4},
		}},
		Config{MaxParallelAgents: 1, MaxHandoffDepth: 3},
	)

	result, err := service.RunTurn(context.Background(), domain.TurnRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("RunTurn returned error: %v", err)
	}
	found := false
	for _, event := range result.Events {
		if event.Type == "tool_failed" && event.AgentID == "planner" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected planner tool_failed event, got %+v", result.Events)
	}
}

func TestRunAgentEmitsFailedEventOnDepthLimit(t *testing.T) {
	service := New(
		&fakeModelClient{},
		&fakeToolExecutor{},
		fakeCatalog{agents: map[string]domain.AgentSpec{}},
		Config{MaxParallelAgents: 1, MaxHandoffDepth: 1},
	)

	result, err := service.runAgent(context.Background(), domain.AgentInvocation{
		RunID:   "planner-1",
		Agent:   domain.AgentSpec{ID: "planner"},
		Context: domain.ContextPack{},
	}, 2)
	if err == nil {
		t.Fatalf("expected depth limit error")
	}
	if len(result.Events) == 0 || result.Events[0].Type != "agent_failed" {
		t.Fatalf("expected agent_failed event, got %+v", result.Events)
	}
}

func TestRunAgentRequestsContinuationAtMaxTurns(t *testing.T) {
	approver := &fakeApprover{decision: domain.PermissionAllowOnce}
	service := New(
		&fakeModelClient{
			responses: map[string][]domain.ModelResponse{
				"manager": {
					{Message: domain.Message{Role: domain.RoleAssistant, ToolCalls: []domain.ToolCall{{ID: "1", Name: "fs_list", Arguments: map[string]any{"path": "cmd"}}}}},
					{Message: domain.Message{Role: domain.RoleAssistant, Content: "done"}},
				},
			},
		},
		&fakeToolExecutor{},
		fakeCatalog{agents: map[string]domain.AgentSpec{
			"manager": {ID: "manager", Mode: domain.AgentModeManager, MaxTurns: 1},
		}},
		Config{MaxParallelAgents: 1, MaxHandoffDepth: 2, Approver: approver},
	)

	result, err := service.RunTurn(context.Background(), domain.TurnRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("RunTurn returned error: %v", err)
	}
	if result.Message.Content != "done" {
		t.Fatalf("unexpected result: %q", result.Message.Content)
	}
	if len(approver.requests) != 1 || approver.requests[0].ToolName != "agent_turn_limit" {
		t.Fatalf("expected continuation approval request, got %+v", approver.requests)
	}
}

func TestContinueApprovalDoesNotUseDeadlineContext(t *testing.T) {
	approver := &inspectingApprover{decision: domain.PermissionDeny}
	service := New(
		&fakeModelClient{
			responses: map[string][]domain.ModelResponse{
				"manager": {{
					Message: domain.Message{
						Role: domain.RoleAssistant,
						ToolCalls: []domain.ToolCall{{
							ID:   "1",
							Name: "fs_list",
							Arguments: map[string]any{
								"path": "cmd",
							},
						}},
					},
				}},
			},
		},
		&fakeToolExecutor{},
		fakeCatalog{agents: map[string]domain.AgentSpec{
			"manager": {ID: "manager", Mode: domain.AgentModeManager, MaxTurns: 1},
		}},
		Config{MaxParallelAgents: 1, MaxHandoffDepth: 2, DefaultTimeout: time.Millisecond, Approver: approver},
	)

	_, _ = service.RunTurn(context.Background(), domain.TurnRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "hello"}},
	})
	if approver.hadDeadline {
		t.Fatalf("expected continue approval context without deadline")
	}
}

func TestToolExecutionDoesNotUseDeadlineContext(t *testing.T) {
	hadDeadline := false
	service := New(
		&fakeModelClient{
			responses: map[string][]domain.ModelResponse{
				"manager": {{
					Message: domain.Message{
						Role: domain.RoleAssistant,
						ToolCalls: []domain.ToolCall{{
							ID:   "1",
							Name: "fs_list",
							Arguments: map[string]any{
								"path": "cmd",
							},
						}},
					},
				}, {
					Message: domain.Message{Role: domain.RoleAssistant, Content: "done"},
				}},
			},
		},
		&fakeToolExecutor{
			exec: func(ctx context.Context, _ domain.AgentSpec, call domain.ToolCall) domain.ToolResult {
				if _, ok := ctx.Deadline(); ok {
					hadDeadline = true
				}
				return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: true, Output: "ok"}
			},
		},
		fakeCatalog{agents: map[string]domain.AgentSpec{
			"manager": {ID: "manager", Mode: domain.AgentModeManager, MaxTurns: 4},
		}},
		Config{MaxParallelAgents: 1, MaxHandoffDepth: 2, DefaultTimeout: time.Millisecond},
	)

	_, err := service.RunTurn(context.Background(), domain.TurnRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("RunTurn returned error: %v", err)
	}
	if hadDeadline {
		t.Fatalf("expected tool execution context without deadline")
	}
}

func TestRunTurnPlannerReceivesAgentInventory(t *testing.T) {
	var plannerInstruction string
	service := New(
		&fakeModelClient{
			responses: map[string][]domain.ModelResponse{
				"planner": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: `{
  "version": "v1",
  "mode": "assisted",
  "task_kind": "research",
  "summary": "Use the repo analyst before answering.",
  "plan": {
    "agent_id": "planner",
    "reason": "Select the execution path."
  },
  "preparation": [
    {
      "agent_id": "repo-analyst",
      "reason": "Inspect the repository and summarize the relevant areas."
    }
  ],
  "primary": {
    "agent_id": "manager",
    "reason": "Answer the user with the prepared findings."
  },
  "steps": [
    {
      "id": "step-1",
      "title": "Create execution plan",
      "phase": "plan",
      "agent_id": "planner"
    },
    {
      "id": "step-2",
      "title": "Prepare focused context",
      "phase": "execute",
      "agent_id": "repo-analyst"
    },
    {
      "id": "step-3",
      "title": "Execute primary task",
      "phase": "execute",
      "agent_id": "manager"
    }
  ]
}`},
				}},
				"repo-analyst": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: "internal/ 以下を中心に確認しました"},
				}},
				"manager": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: "done"},
				}},
			},
			inspect: func(request domain.ModelRequest) {
				if request.Agent.ID == "planner" {
					plannerInstruction = request.Instructions
				}
			},
		},
		&fakeToolExecutor{},
		fakeCatalog{agents: map[string]domain.AgentSpec{
			"manager":      {ID: "manager", Mode: domain.AgentModeManager, Instruction: "base", MaxTurns: 4},
			"planner":      {ID: "planner", Mode: domain.AgentModeTool, ReadOnly: true, MaxTurns: 4},
			"repo-analyst": {ID: "repo-analyst", Mode: domain.AgentModeTool, ReadOnly: true, MaxTurns: 4, Description: "internal/ 以下の調査に強い agent", Tags: []string{"research", "analysis"}},
		}},
		Config{MaxParallelAgents: 1, MaxHandoffDepth: 2},
	)

	_, err := service.RunTurn(context.Background(), domain.TurnRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "サブエージェントを活用して internal/ 以下を全て調査し、品質レポートを作ってください"}},
	})
	if err != nil {
		t.Fatalf("RunTurn returned error: %v", err)
	}
	if !strings.Contains(plannerInstruction, "Agent inventory") {
		t.Fatalf("expected planner instruction to include inventory, got %q", plannerInstruction)
	}
	if !strings.Contains(plannerInstruction, "repo-analyst") {
		t.Fatalf("expected planner instruction to mention user-defined agent, got %q", plannerInstruction)
	}
}

func TestRunTurnDoesNotUseLocalKeywordBypassForGreeting(t *testing.T) {
	seenAgents := []string{}
	service := New(
		&fakeModelClient{
			responses: map[string][]domain.ModelResponse{
				"planner": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: `{
  "version": "v1",
  "mode": "direct",
  "task_kind": "casual",
  "summary": "Reply directly to the greeting.",
  "primary": {
    "agent_id": "manager",
    "reason": "Respond to the user directly."
  },
  "steps": [
    {
      "id": "step-1",
      "title": "Execute primary task",
      "phase": "execute",
      "agent_id": "manager"
    }
  ]
}`},
				}},
				"manager": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: "こんにちは！"},
				}},
			},
			inspect: func(request domain.ModelRequest) {
				seenAgents = append(seenAgents, request.Agent.ID)
			},
		},
		&fakeToolExecutor{},
		fakeCatalog{agents: map[string]domain.AgentSpec{
			"manager":  {ID: "manager", Mode: domain.AgentModeManager, MaxTurns: 4},
			"planner":  {ID: "planner", Mode: domain.AgentModeTool, ReadOnly: true, MaxTurns: 4},
			"coder":    {ID: "coder", Mode: domain.AgentModeHandoff, MaxTurns: 4},
			"tester":   {ID: "tester", Mode: domain.AgentModeTool, ReadOnly: true, MaxTurns: 4},
			"reviewer": {ID: "reviewer", Mode: domain.AgentModeTool, ReadOnly: true, MaxTurns: 4},
		}},
		Config{MaxParallelAgents: 1, MaxHandoffDepth: 2},
	)

	result, err := service.RunTurn(context.Background(), domain.TurnRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "こんにちわ！"}},
	})
	if err != nil {
		t.Fatalf("RunTurn returned error: %v", err)
	}
	if result.Message.Content != "こんにちは！" {
		t.Fatalf("unexpected result: %q", result.Message.Content)
	}
	if len(seenAgents) != 2 || seenAgents[0] != "planner" || seenAgents[1] != "manager" {
		t.Fatalf("expected planner then manager, got %+v", seenAgents)
	}
}

func TestRunTurnUsesMatchingUserDefinedAgentBeforePrimaryExecution(t *testing.T) {
	seenAgents := []string{}
	service := New(
		&fakeModelClient{
			responses: map[string][]domain.ModelResponse{
				"planner": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: `{
  "version": "v1",
  "mode": "full",
  "task_kind": "docs",
  "summary": "Use the docs specialist to prepare context, then let coder update the file and reviewer verify it.",
  "plan": {
    "agent_id": "planner",
    "reason": "Create the execution plan."
  },
  "preparation": [
    {
      "agent_id": "docs-writer",
      "reason": "Prepare the README-specific context."
    }
  ],
  "primary": {
    "agent_id": "coder",
    "reason": "Apply the requested README update."
  },
  "verify": [
    {
      "agent_id": "reviewer",
      "reason": "Check the documentation update for regressions."
    }
  ],
  "finalize": {
    "agent_id": "manager",
    "reason": "Summarize the completed update."
  },
  "steps": [
    {
      "id": "step-1",
      "title": "Create execution plan",
      "phase": "plan",
      "agent_id": "planner"
    },
    {
      "id": "step-2",
      "title": "Prepare focused context",
      "phase": "execute",
      "agent_id": "docs-writer"
    },
    {
      "id": "step-3",
      "title": "Execute primary task",
      "phase": "execute",
      "agent_id": "coder"
    },
    {
      "id": "step-4",
      "title": "Verify latest result",
      "phase": "verify",
      "agent_id": "reviewer"
    },
    {
      "id": "step-5",
      "title": "Summarize the completed work",
      "phase": "finalize",
      "agent_id": "manager"
    }
  ]
}`},
				}},
				"docs-writer": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: "README の変更観点を整理しました"},
				}},
				"coder": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: "README を更新しました"},
				}},
				"reviewer": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: "VERIFICATION_STATUS: pass\nSUMMARY: docs update looks good\nREPAIR_BRIEF: none"},
				}},
				"manager": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: "README を更新し、確認も完了しました"},
				}},
			},
			inspect: func(request domain.ModelRequest) {
				seenAgents = append(seenAgents, request.Agent.ID)
			},
		},
		&fakeToolExecutor{},
		fakeCatalog{agents: map[string]domain.AgentSpec{
			"manager":     {ID: "manager", Mode: domain.AgentModeManager, MaxTurns: 4},
			"planner":     {ID: "planner", Mode: domain.AgentModeTool, ReadOnly: true, MaxTurns: 4},
			"coder":       {ID: "coder", Mode: domain.AgentModeHandoff, MaxTurns: 4},
			"reviewer":    {ID: "reviewer", Mode: domain.AgentModeTool, ReadOnly: true, MaxTurns: 4},
			"docs-writer": {ID: "docs-writer", Mode: domain.AgentModeTool, ReadOnly: true, MaxTurns: 4, Description: "README や設計メモの更新を担当", Tags: []string{"docs", "readme"}},
		}},
		Config{MaxParallelAgents: 1, MaxHandoffDepth: 2},
	)

	result, err := service.RunTurn(context.Background(), domain.TurnRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "README を更新して"}},
	})
	if err != nil {
		t.Fatalf("RunTurn returned error: %v", err)
	}
	if result.Message.Content != "README を更新し、確認も完了しました" {
		t.Fatalf("unexpected result: %q", result.Message.Content)
	}
	if len(seenAgents) < 5 {
		t.Fatalf("expected planner, docs-writer, coder, reviewer, manager sequence, got %+v", seenAgents)
	}
	if seenAgents[0] != "planner" || seenAgents[1] != "docs-writer" || seenAgents[2] != "coder" || seenAgents[3] != "reviewer" || seenAgents[4] != "manager" {
		t.Fatalf("unexpected agent sequence: %+v", seenAgents)
	}
}

func TestRunTurnRequiresCapabilityEnableBeforeWorkspaceMutation(t *testing.T) {
	toolCalls := 0
	service := New(
		&fakeModelClient{
			responses: map[string][]domain.ModelResponse{
				"manager": {{
					Message: domain.Message{
						Role: domain.RoleAssistant,
						ToolCalls: []domain.ToolCall{{
							ID:   "1",
							Name: "fs_write",
							Arguments: map[string]any{
								"path":      "README.md",
								"content":   "x",
								"create":    true,
								"overwrite": true,
							},
						}},
					},
				}, {
					Message: domain.Message{
						Role: domain.RoleAssistant,
						ToolCalls: []domain.ToolCall{{
							ID:   "2",
							Name: "enable_capability",
							Arguments: map[string]any{
								"capability": "fs_write",
							},
						}},
					},
				}, {
					Message: domain.Message{
						Role: domain.RoleAssistant,
						ToolCalls: []domain.ToolCall{{
							ID:   "3",
							Name: "fs_write",
							Arguments: map[string]any{
								"path":      "README.md",
								"content":   "x",
								"create":    true,
								"overwrite": true,
							},
						}},
					},
				}, {
					Message: domain.Message{Role: domain.RoleAssistant, Content: "done"},
				}},
			},
		},
		&fakeToolExecutor{
			defs: map[string][]domain.ToolDefinition{
				"manager": {{
					Name:             "fs_write",
					CapabilityGroup:  "fs_write",
					MutatesWorkspace: true,
				}},
			},
			exec: func(_ context.Context, _ domain.AgentSpec, call domain.ToolCall) domain.ToolResult {
				toolCalls++
				return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: true, Output: "ok"}
			},
		},
		fakeCatalog{agents: map[string]domain.AgentSpec{
			"manager": {ID: "manager", Mode: domain.AgentModeManager, MaxTurns: 8},
		}},
		Config{MaxParallelAgents: 1, MaxHandoffDepth: 2},
	)

	result, err := service.RunTurn(context.Background(), domain.TurnRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "update the README"}},
	})
	if err != nil {
		t.Fatalf("RunTurn returned error: %v", err)
	}
	if result.Message.Content != "done" {
		t.Fatalf("unexpected result: %q", result.Message.Content)
	}
	if toolCalls != 1 {
		t.Fatalf("expected only the enabled mutation tool call to execute, got %d", toolCalls)
	}
}

func TestRunTurnRunsVerificationAndRecoveryLoop(t *testing.T) {
	service := New(
		&fakeModelClient{
			responses: map[string][]domain.ModelResponse{
				"planner": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: `{
  "version": "v1",
  "mode": "full",
  "task_kind": "mutate",
  "summary": "Implement the change, then run tester and reviewer before summarizing.",
  "plan": {
    "agent_id": "planner",
    "reason": "Create the execution plan."
  },
  "primary": {
    "agent_id": "coder",
    "reason": "Implement the requested change."
  },
  "verify": [
    {
      "agent_id": "tester",
      "reason": "Run validation and catch missing regression handling."
    },
    {
      "agent_id": "reviewer",
      "reason": "Review the implementation for regressions."
    }
  ],
  "recovery": {
    "agent_id": "coder",
    "reason": "Repair the implementation using the verification brief."
  },
  "finalize": {
    "agent_id": "manager",
    "reason": "Summarize the repaired implementation."
  },
  "steps": [
    {
      "id": "step-1",
      "title": "Create execution plan",
      "phase": "plan",
      "agent_id": "planner"
    },
    {
      "id": "step-2",
      "title": "Execute primary task",
      "phase": "execute",
      "agent_id": "coder"
    },
    {
      "id": "step-3",
      "title": "Verify latest result",
      "phase": "verify",
      "agent_id": "tester"
    },
    {
      "id": "step-4",
      "title": "Verify latest result",
      "phase": "verify",
      "agent_id": "reviewer"
    },
    {
      "id": "step-5",
      "title": "Repair from verification brief",
      "phase": "recover",
      "agent_id": "coder"
    },
    {
      "id": "step-6",
      "title": "Summarize the completed work",
      "phase": "finalize",
      "agent_id": "manager"
    }
  ]
}`},
				}},
				"manager": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: "final summary"},
				}},
				"coder": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: "initial implementation"},
				}, {
					Message: domain.Message{Role: domain.RoleAssistant, Content: "fixed implementation"},
				}},
				"tester": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: "VERIFICATION_STATUS: fail\nSUMMARY: missing regression coverage\nREPAIR_BRIEF: add the missing regression handling"},
				}, {
					Message: domain.Message{Role: domain.RoleAssistant, Content: "VERIFICATION_STATUS: pass\nSUMMARY: tests are now green\nREPAIR_BRIEF: none"},
				}},
				"reviewer": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: "VERIFICATION_STATUS: pass\nSUMMARY: no regressions found\nREPAIR_BRIEF: none"},
				}, {
					Message: domain.Message{Role: domain.RoleAssistant, Content: "VERIFICATION_STATUS: pass\nSUMMARY: review looks good\nREPAIR_BRIEF: none"},
				}},
			},
		},
		&fakeToolExecutor{},
		fakeCatalog{agents: map[string]domain.AgentSpec{
			"manager":  {ID: "manager", Mode: domain.AgentModeManager, MaxTurns: 4},
			"planner":  {ID: "planner", Mode: domain.AgentModeTool, ReadOnly: true, MaxTurns: 4},
			"coder":    {ID: "coder", Mode: domain.AgentModeHandoff, MaxTurns: 4},
			"tester":   {ID: "tester", Mode: domain.AgentModeTool, ReadOnly: true, MaxTurns: 4},
			"reviewer": {ID: "reviewer", Mode: domain.AgentModeTool, ReadOnly: true, MaxTurns: 4},
		}},
		Config{MaxParallelAgents: 1, MaxHandoffDepth: 2, MaxVerificationAttempts: 2},
	)

	result, err := service.RunTurn(context.Background(), domain.TurnRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "fix the bug"}},
	})
	if err != nil {
		t.Fatalf("RunTurn returned error: %v", err)
	}
	if result.Message.Content != "final summary" {
		t.Fatalf("unexpected final content: %q", result.Message.Content)
	}
	if result.Run == nil || len(result.Run.Verification) < 2 {
		t.Fatalf("expected verification results to be recorded, got %+v", result.Run)
	}
	foundRecovery := false
	for _, artifact := range result.Run.Artifacts {
		if artifact.Kind == "recovery" {
			foundRecovery = true
		}
	}
	if !foundRecovery {
		t.Fatalf("expected recovery artifact, got %+v", result.Run.Artifacts)
	}
}
