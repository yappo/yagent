package orchestrator

import (
	"context"
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
	if result.Events[0].ContextCount != 3 {
		t.Fatalf("expected context count 3, got %d", result.Events[0].ContextCount)
	}
}

func TestPlannerCannotDelegateToCoder(t *testing.T) {
	service := New(
		&fakeModelClient{
			responses: map[string][]domain.ModelResponse{
				"manager": {{
					Message: domain.Message{
						Role: domain.RoleAssistant,
						ToolCalls: []domain.ToolCall{{
							ID:   "1",
							Name: "delegate_to_planner",
							Arguments: map[string]any{
								"task": "Inspect cmd directory",
							},
						}},
					},
				}, {
					Message: domain.Message{Role: domain.RoleAssistant, Content: "done"},
				}},
				"planner": {{
					Message: domain.Message{
						Role: domain.RoleAssistant,
						ToolCalls: []domain.ToolCall{{
							ID:   "2",
							Name: "delegate_to_coder",
							Arguments: map[string]any{
								"task": "Write a script",
							},
						}},
					},
				}, {
					Message: domain.Message{Role: domain.RoleAssistant, Content: "planner done"},
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
					{Message: domain.Message{Role: domain.RoleAssistant, ToolCalls: []domain.ToolCall{{ID: "1", Name: "directory_list", Arguments: map[string]any{"directory_path": "cmd"}}}}},
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
							Name: "directory_list",
							Arguments: map[string]any{
								"directory_path": "cmd",
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
							Name: "directory_list",
							Arguments: map[string]any{
								"directory_path": "cmd",
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

func TestRunTurnBiasesManagerTowardDelegationForBroadResearchTask(t *testing.T) {
	var managerInstruction string
	service := New(
		&fakeModelClient{
			responses: map[string][]domain.ModelResponse{
				"manager": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: "done"},
				}},
			},
			inspect: func(request domain.ModelRequest) {
				if request.Agent.ID == "manager" {
					managerInstruction = request.Instructions
				}
			},
		},
		&fakeToolExecutor{},
		fakeCatalog{agents: map[string]domain.AgentSpec{
			"manager": {ID: "manager", Mode: domain.AgentModeManager, Instruction: "base", MaxTurns: 4},
		}},
		Config{MaxParallelAgents: 1, MaxHandoffDepth: 2},
	)

	_, err := service.RunTurn(context.Background(), domain.TurnRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "サブエージェントを活用して internal/ 以下を全て調査し、品質レポートを作ってください"}},
	})
	if err != nil {
		t.Fatalf("RunTurn returned error: %v", err)
	}
	if !strings.Contains(managerInstruction, "actively use subagents") {
		t.Fatalf("expected manager instruction to bias delegation, got %q", managerInstruction)
	}
	if !strings.Contains(managerInstruction, "planner and/or researcher") {
		t.Fatalf("expected manager instruction to mention planner/researcher, got %q", managerInstruction)
	}
}
