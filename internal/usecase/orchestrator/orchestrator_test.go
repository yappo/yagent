package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"yagent/internal/domain"
	"yagent/internal/infra/state"
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

type concurrentModelClient struct {
	responses     map[string]domain.ModelResponse
	delay         time.Duration
	inFlight      atomic.Int32
	maxConcurrent atomic.Int32
}

func (c *concurrentModelClient) Generate(_ context.Context, request domain.ModelRequest) (domain.ModelResponse, error) {
	current := c.inFlight.Add(1)
	for {
		maxSeen := c.maxConcurrent.Load()
		if current <= maxSeen {
			break
		}
		if c.maxConcurrent.CompareAndSwap(maxSeen, current) {
			break
		}
	}
	time.Sleep(c.delay)
	c.inFlight.Add(-1)
	if response, ok := c.responses[request.Agent.ID]; ok {
		return response, nil
	}
	return domain.ModelResponse{Message: domain.Message{Role: domain.RoleAssistant, Content: "ok"}}, nil
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

func TestRunTurnPersistsConversationTurn(t *testing.T) {
	store, err := state.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore returned error: %v", err)
	}
	service := New(
		&fakeModelClient{
			responses: map[string][]domain.ModelResponse{
				"manager": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: "conversation done"},
				}},
			},
		},
		&fakeToolExecutor{},
		fakeCatalog{agents: map[string]domain.AgentSpec{
			"manager": {ID: "manager", Mode: domain.AgentModeManager, MaxTurns: 4},
		}},
		Config{
			MaxParallelAgents:   1,
			MaxHandoffDepth:     1,
			DisablePhaseHarness: true,
			RunStore:            store,
			ConversationStore:   store,
		},
	)

	result, err := service.RunTurn(context.Background(), domain.TurnRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "remember this turn"}},
		Profile:  "fast",
	})
	if err != nil {
		t.Fatalf("RunTurn returned error: %v", err)
	}
	turns, err := store.ListConversationTurns(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListConversationTurns returned error: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("expected one conversation turn, got %+v", turns)
	}
	turn := turns[0]
	if turn.RunID != result.Run.ID || turn.Profile != "fast" || turn.Status != domain.RunStatusCompleted {
		t.Fatalf("unexpected conversation metadata: %+v", turn)
	}
	if len(turn.RequestMessages) != 1 || turn.RequestMessages[0].Content != "remember this turn" {
		t.Fatalf("unexpected request messages: %+v", turn.RequestMessages)
	}
	if turn.OutputMessage.Content != "conversation done" {
		t.Fatalf("unexpected output message: %+v", turn.OutputMessage)
	}
	if turn.EventCount == 0 || turn.ModelCallCount == 0 {
		t.Fatalf("expected event/model counts, got %+v", turn)
	}
}

func TestParseVerificationPrefersStructuredJSON(t *testing.T) {
	result := parseVerification(`{"status":"fail","summary":"missing regression coverage","repair_brief":"add a focused test"}`, "reviewer", 2)
	if result.SourceAgent != "reviewer" || result.Attempt != 2 {
		t.Fatalf("unexpected result metadata: %+v", result)
	}
	if result.Status != "fail" {
		t.Fatalf("expected fail status, got %+v", result)
	}
	if result.Summary != "missing regression coverage" {
		t.Fatalf("unexpected summary: %q", result.Summary)
	}
	if result.RepairBrief != "add a focused test" {
		t.Fatalf("unexpected repair brief: %q", result.RepairBrief)
	}
}

func TestRunTurnPersistsTypedArtifacts(t *testing.T) {
	service := New(
		&fakeModelClient{
			responses: map[string][]domain.ModelResponse{
				"manager": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: "done directly with README.md"},
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
	if result.Run == nil || len(result.Run.Artifacts) < 3 {
		t.Fatalf("expected artifacts, got %+v", result.Run)
	}

	var planPayload domain.ExecutionPlanArtifactPayload
	if err := json.Unmarshal(result.Run.Artifacts[1].Payload, &planPayload); err != nil {
		t.Fatalf("expected typed execution plan payload: %v", err)
	}
	if planPayload.Plan == nil || planPayload.Plan.Source == "" {
		t.Fatalf("expected execution plan payload, got %+v", planPayload)
	}

	finalArtifact := result.Run.Artifacts[len(result.Run.Artifacts)-1]
	if finalArtifact.Kind != "final_response" {
		t.Fatalf("expected final response artifact, got %+v", finalArtifact)
	}
	var finalPayload domain.FinalResponseArtifactPayload
	if err := json.Unmarshal(finalArtifact.Payload, &finalPayload); err != nil {
		t.Fatalf("expected typed final response payload: %v", err)
	}
	if !strings.Contains(finalPayload.Response, "README.md") {
		t.Fatalf("expected final response payload content, got %+v", finalPayload)
	}
}

func TestRunTurnSeesBoundMCPToolsOnLaterTurn(t *testing.T) {
	seenBoundTool := false
	seenTaskBind := false
	var executor *fakeToolExecutor
	executor = &fakeToolExecutor{
		defs: map[string][]domain.ToolDefinition{
			"manager": {
				{Name: "task_bind", CapabilityGroup: "mcp", Parameters: map[string]any{"type": "object"}},
			},
		},
		exec: func(_ context.Context, _ domain.AgentSpec, call domain.ToolCall) domain.ToolResult {
			if call.Name == "task_bind" {
				executor.defs["manager"] = append(executor.defs["manager"], domain.ToolDefinition{
					Name:            "mcp__docs__search_docs__docs",
					CapabilityGroup: "mcp",
					Parameters:      map[string]any{"type": "object"},
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
					if tool.Name == "task_bind" {
						seenTaskBind = true
					}
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
	if !seenTaskBind {
		t.Fatal("expected task_bind to be visible before binding")
	}
	if !seenBoundTool {
		t.Fatal("expected bound MCP tool to appear on a later LLM turn")
	}
}

func TestRunTurnIncludesStructuredToolStateInInstructions(t *testing.T) {
	var managerInstruction string
	service := New(
		&fakeModelClient{
			responses: map[string][]domain.ModelResponse{
				"manager": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: "done"},
				}},
			},
			inspect: func(request domain.ModelRequest) {
				managerInstruction = request.Instructions
			},
		},
		&fakeToolExecutor{
			defs: map[string][]domain.ToolDefinition{
				"manager": {
					{Name: "task_list", CapabilityGroup: "task_read", Parameters: map[string]any{"type": "object"}},
					{Name: "task_bind", CapabilityGroup: "mcp", Parameters: map[string]any{"type": "object"}},
					{Name: "mcp__docs__search_docs__docs", CapabilityGroup: "mcp", Parameters: map[string]any{"type": "object"}},
					{Name: "fs_write", CapabilityGroup: "fs_write", MutatesWorkspace: true, Parameters: map[string]any{"type": "object"}},
				},
			},
		},
		fakeCatalog{agents: map[string]domain.AgentSpec{
			"manager": {ID: "manager", Mode: domain.AgentModeManager, MaxTurns: 4},
		}},
		Config{MaxParallelAgents: 1, MaxHandoffDepth: 2, DisablePhaseHarness: true},
	)

	_, err := service.RunTurn(context.Background(), domain.TurnRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "inspect tool state"}},
	})
	if err != nil {
		t.Fatalf("RunTurn returned error: %v", err)
	}
	for _, fragment := range []string{
		"Tool state:",
		"\"current_agent_id\": \"manager\"",
		"\"file_write_allowed\": true",
		"\"write_capability_available\": true",
		"\"visible_write_tools\": [",
		"\"fs_write\"",
		"\"task_discovery_available\": true",
		"\"mcp_binding_available\": true",
		"\"mcp_tools_lazy_bind\": true",
		"\"visible_mcp_tools\": [",
		"Workflow hints:",
		`kind="mcp_server"`,
		"call fs_write directly",
		"approval dialog will be shown automatically",
	} {
		if !strings.Contains(managerInstruction, fragment) {
			t.Fatalf("expected instruction to contain %q, got %q", fragment, managerInstruction)
		}
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

func TestRunTurnStreamsModelDeltasAsTransientEvents(t *testing.T) {
	service := New(
		&fakeModelClient{
			responses: map[string][]domain.ModelResponse{
				"manager": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: "hello"},
				}},
			},
			inspect: func(request domain.ModelRequest) {
				if !request.Stream {
					t.Fatalf("expected stream request")
				}
				if request.StreamHandler == nil {
					t.Fatalf("expected stream handler")
				}
				request.StreamHandler(domain.ModelStreamEvent{
					Type:         "content_delta",
					ContentDelta: "hel",
					RawEventType: "chat.completion.chunk",
				})
			},
		},
		&fakeToolExecutor{},
		fakeCatalog{agents: map[string]domain.AgentSpec{
			"manager": {ID: "manager", Mode: domain.AgentModeManager, MaxTurns: 4},
		}},
		Config{MaxParallelAgents: 1, MaxHandoffDepth: 2, DefaultModel: "gpt-5"},
	)
	events, cancel := service.SubscribeEvents()
	defer cancel()

	result, err := service.RunTurn(context.Background(), domain.TurnRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "hello"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("RunTurn returned error: %v", err)
	}

	select {
	case event := <-events:
		if event.Type != "agent_started" {
			t.Fatalf("expected agent start before delta, got %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for agent start event")
	}
	select {
	case event := <-events:
		if event.Type != "llm_delta" || event.Detail != "hel" || event.Metrics["raw_event_type"] != "chat.completion.chunk" {
			t.Fatalf("unexpected stream event: %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream event")
	}
	if hasEventType(result.Events, "llm_delta") {
		t.Fatalf("stream deltas should not be persisted in turn events: %+v", result.Events)
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

func TestRunAgentEmitsModelInvocationMetrics(t *testing.T) {
	service := New(
		&fakeModelClient{
			responses: map[string][]domain.ModelResponse{
				"coder": {{
					Message:      domain.Message{Role: domain.RoleAssistant, Content: "done"},
					FinishReason: "stop",
					Invocation: domain.ModelInvocationMetadata{
						ServerName:         "openai",
						Fallback:           true,
						FallbackFromServer: "local",
						API:                "responses",
						Model:              "gpt-5.5",
						ProfileName:        "strong",
						DurationMS:         1234,
					},
				}},
			},
		},
		&fakeToolExecutor{defs: map[string][]domain.ToolDefinition{
			"coder": {{Name: "fs_read"}},
		}},
		fakeCatalog{agents: map[string]domain.AgentSpec{}},
		Config{MaxParallelAgents: 1, MaxHandoffDepth: 1},
	)

	result, err := service.runAgent(context.Background(), domain.AgentInvocation{
		RunID:     "coder-1",
		RootRunID: "root-1",
		Agent:     domain.AgentSpec{ID: "coder", RoutingProfile: "strong"},
		Phase:     domain.RunPhaseExecute,
		Attempt:   2,
	}, 0)
	if err != nil {
		t.Fatalf("runAgent returned error: %v", err)
	}
	var llmEvent domain.ExecutionEvent
	for _, event := range result.Events {
		if event.Type == "llm_called" {
			llmEvent = event
			break
		}
	}
	if llmEvent.Type == "" {
		t.Fatalf("expected llm_called event, got %+v", result.Events)
	}
	if llmEvent.Metrics["server_name"] != "openai" || llmEvent.Metrics["api"] != "responses" || llmEvent.Metrics["model"] != "gpt-5.5" || llmEvent.Metrics["profile_name"] != "strong" {
		t.Fatalf("unexpected llm_called model metrics: %+v", llmEvent.Metrics)
	}
	if llmEvent.Metrics["fallback"] != true || llmEvent.Metrics["fallback_from_server"] != "local" || llmEvent.Metrics["duration_ms"] != int64(1234) {
		t.Fatalf("unexpected llm_called metrics: %+v", llmEvent.Metrics)
	}
	if visibleTools, ok := llmEvent.Metrics["visible_tools"].(int); !ok || visibleTools <= 0 {
		t.Fatalf("expected visible tool count, got %+v", llmEvent.Metrics)
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

func TestRunAgentContinuationPolicyAllowSkipsApproval(t *testing.T) {
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
		Config{MaxParallelAgents: 1, MaxHandoffDepth: 2, ContinuationPolicy: "allow"},
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

func TestRunAgentContinuationPolicyDenySkipsApproval(t *testing.T) {
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
		Config{MaxParallelAgents: 1, MaxHandoffDepth: 2, ContinuationPolicy: "deny", Approver: approver},
	)

	_, err := service.RunTurn(context.Background(), domain.TurnRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "hello"}},
	})
	if err == nil {
		t.Fatalf("expected continuation denial to fail")
	}
	if len(approver.requests) != 0 {
		t.Fatalf("expected deny policy to skip approval request, got %+v", approver.requests)
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
	var plannerFormat *domain.ResponseFormat
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
					plannerFormat = request.ResponseFormat
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
	if plannerFormat == nil || plannerFormat.Type != "json_schema" || plannerFormat.Name != "execution_plan" || !plannerFormat.Strict {
		t.Fatalf("expected planner structured output format, got %+v", plannerFormat)
	}
}

func TestRunTurnFinalizerUsesStructuredResponse(t *testing.T) {
	var finalizerFormat *domain.ResponseFormat
	service := New(
		&fakeModelClient{
			responses: map[string][]domain.ModelResponse{
				"planner": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: `{
  "version": "v1",
  "mode": "full",
  "task_kind": "mutate",
  "summary": "Use coder and finalizer.",
  "plan": null,
  "preparation": [],
  "primary": {
    "agent_id": "coder",
    "reason": "Make the change."
  },
  "verify": [],
  "recovery": null,
  "finalize": {
    "agent_id": "manager",
    "reason": "Summarize the result."
  },
  "steps": [
    {
      "id": "step-1",
      "title": "Execute primary task",
      "phase": "execute",
      "agent_id": "coder"
    },
    {
      "id": "step-2",
      "title": "Summarize the completed work",
      "phase": "finalize",
      "agent_id": "manager"
    }
  ],
  "required_capabilities": [],
  "source": "planner",
  "fallback_reason": ""
}`},
				}},
				"coder": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: "implementation complete"},
				}},
				"manager": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: `{
  "response": "Implemented the requested change.",
  "summary": "Change completed.",
  "verification_summary": "Verification was not run.",
  "remaining_risks": ["tests not run"],
  "next_steps": ["run go test ./..."]
}`},
				}},
			},
			inspect: func(request domain.ModelRequest) {
				if request.Agent.ID == "manager" && request.Phase == domain.RunPhaseFinalize {
					finalizerFormat = request.ResponseFormat
				}
			},
		},
		&fakeToolExecutor{},
		fakeCatalog{agents: map[string]domain.AgentSpec{
			"manager": {ID: "manager", Mode: domain.AgentModeManager, MaxTurns: 4},
			"planner": {ID: "planner", Mode: domain.AgentModeTool, ReadOnly: true, MaxTurns: 4},
			"coder":   {ID: "coder", Mode: domain.AgentModeHandoff, MaxTurns: 4},
		}},
		Config{MaxParallelAgents: 1, MaxHandoffDepth: 2},
	)

	result, err := service.RunTurn(context.Background(), domain.TurnRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "make a small change"}},
	})
	if err != nil {
		t.Fatalf("RunTurn returned error: %v", err)
	}
	if finalizerFormat == nil || finalizerFormat.Type != "json_schema" || finalizerFormat.Name != "final_response" || !finalizerFormat.Strict {
		t.Fatalf("expected final response format, got %+v", finalizerFormat)
	}
	if result.Message.Content != "Implemented the requested change." {
		t.Fatalf("expected normalized final response content, got %q", result.Message.Content)
	}
	finalArtifact := result.Run.Artifacts[len(result.Run.Artifacts)-1]
	var payload domain.FinalResponseArtifactPayload
	if err := json.Unmarshal(finalArtifact.Payload, &payload); err != nil {
		t.Fatalf("expected final response payload: %v", err)
	}
	if len(payload.RemainingRisks) != 1 || payload.RemainingRisks[0] != "tests not run" {
		t.Fatalf("expected structured remaining risks, got %+v", payload)
	}
	if len(payload.NextSteps) != 1 || payload.NextSteps[0] != "run go test ./..." {
		t.Fatalf("expected structured next steps, got %+v", payload)
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

func TestRunTurnAllowsDirectWorkspaceMutationForWritableAgent(t *testing.T) {
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
		t.Fatalf("expected direct mutation tool call to execute once, got %d", toolCalls)
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
	foundTestReport := false
	foundSecondAttempt := false
	for _, artifact := range result.Run.Artifacts {
		if artifact.Kind == "test_report" {
			foundTestReport = true
		}
	}
	for _, unit := range result.Run.WorkUnits {
		if unit.ID == "recover:2:coder" || unit.ID == "verify:2:tester" {
			foundSecondAttempt = true
		}
	}
	if !foundTestReport {
		t.Fatalf("expected typed test_report artifact, got %+v", result.Run.Artifacts)
	}
	if !foundSecondAttempt {
		t.Fatalf("expected dynamic second-attempt work units, got %+v", result.Run.WorkUnits)
	}
}

func TestRunTurnBuildsTypedArtifactsAndTypedMemoryFacts(t *testing.T) {
	store, err := state.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore returned error: %v", err)
	}
	service := New(
		&fakeModelClient{
			responses: map[string][]domain.ModelResponse{
				"planner": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: `{
  "version": "v1",
  "mode": "full",
  "task_kind": "mutate",
  "summary": "Update README, verify it, then summarize.",
  "primary": {
    "agent_id": "coder",
    "reason": "Update README.md"
  },
  "verify": [
    {
      "agent_id": "tester",
      "reason": "Verify the README update."
    }
  ],
  "finalize": {
    "agent_id": "manager",
    "reason": "Summarize the README update."
  }
}`},
				}},
				"coder": {{
					Message: domain.Message{
						Role: domain.RoleAssistant,
						ToolCalls: []domain.ToolCall{
							{ID: "call-read", Name: "fs_read", Arguments: map[string]any{"path": "README.md"}},
							{ID: "call-write", Name: "fs_write", Arguments: map[string]any{"path": "README.md", "content": "updated"}},
						},
					},
				}, {
					Message: domain.Message{Role: domain.RoleAssistant, Content: "README updated"},
				}},
				"tester": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: "VERIFICATION_STATUS: pass\nSUMMARY: README looks good\nREPAIR_BRIEF: none"},
				}},
				"manager": {{
					Message: domain.Message{Role: domain.RoleAssistant, Content: "all done"},
				}},
			},
		},
		&fakeToolExecutor{
			defs: map[string][]domain.ToolDefinition{
				"coder": {{
					Name:         "fs_read",
					ReadOnly:     true,
					ParallelSafe: true,
					Semantics: domain.ToolSemantics{
						Class:           domain.ToolClassObserve,
						ReusePolicy:     domain.ToolReuseOnSuccess,
						DuplicatePolicy: domain.ToolDuplicateSuppressInflight,
						Freshness:       domain.ToolFreshnessPolicy{Strategy: domain.ToolFreshnessReadSet},
						SideEffectClass: domain.SideEffectNone,
						Source:          "fs",
						ReadPathArgs:    []string{"path"},
					},
				}, {
					Name:             "fs_write",
					MutatesWorkspace: true,
					Semantics: domain.ToolSemantics{
						Class:           domain.ToolClassMutate,
						ReusePolicy:     domain.ToolReuseNever,
						DuplicatePolicy: domain.ToolDuplicateAllow,
						Freshness:       domain.ToolFreshnessPolicy{Strategy: domain.ToolFreshnessNone},
						SideEffectClass: domain.SideEffectWorkspace,
						Source:          "fs",
						WritePathArgs:   []string{"path"},
					},
				}},
			},
			exec: func(_ context.Context, _ domain.AgentSpec, call domain.ToolCall) domain.ToolResult {
				switch call.Name {
				case "fs_read":
					return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: true, Output: "old README"}
				case "fs_write":
					return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: true, Output: "wrote README"}
				default:
					return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: true, Output: "ok"}
				}
			},
		},
		fakeCatalog{agents: map[string]domain.AgentSpec{
			"manager": {ID: "manager", Mode: domain.AgentModeManager, MaxTurns: 4},
			"planner": {ID: "planner", Mode: domain.AgentModeTool, ReadOnly: true, MaxTurns: 4},
			"coder":   {ID: "coder", Mode: domain.AgentModeHandoff, MaxTurns: 4},
			"tester":  {ID: "tester", Mode: domain.AgentModeTool, ReadOnly: true, MaxTurns: 4},
		}},
		Config{
			MaxParallelAgents:       2,
			MaxHandoffDepth:         2,
			MaxVerificationAttempts: 1,
			RunStore:                store,
			MemoryStore:             store,
			RuntimeStore:            store,
		},
	)

	result, err := service.RunTurn(context.Background(), domain.TurnRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "update README.md"}},
	})
	if err != nil {
		t.Fatalf("RunTurn returned error: %v", err)
	}

	foundKinds := map[string]domain.RunArtifact{}
	for _, artifact := range result.Run.Artifacts {
		foundKinds[artifact.Kind] = artifact
	}
	for _, kind := range []string{"repo_map", "change_set", "test_report", "final_response"} {
		if _, ok := foundKinds[kind]; !ok {
			t.Fatalf("expected %s artifact, got %+v", kind, result.Run.Artifacts)
		}
	}

	var repoMap domain.RepoMapArtifactPayload
	if err := json.Unmarshal(foundKinds["repo_map"].Payload, &repoMap); err != nil {
		t.Fatalf("repo_map payload decode failed: %v", err)
	}
	foundREADMERef := false
	for _, entry := range repoMap.Entries {
		if strings.HasSuffix(entry.Path, "README.md") {
			foundREADMERef = true
			break
		}
	}
	if !foundREADMERef {
		t.Fatalf("expected README.md in repo_map, got %+v", repoMap.Entries)
	}

	var changeSet domain.ChangeSetArtifactPayload
	if err := json.Unmarshal(foundKinds["change_set"].Payload, &changeSet); err != nil {
		t.Fatalf("change_set payload decode failed: %v", err)
	}
	foundChangedREADME := false
	for _, file := range changeSet.Files {
		if strings.HasSuffix(file.Path, "README.md") {
			foundChangedREADME = true
			break
		}
	}
	if !foundChangedREADME {
		t.Fatalf("expected README.md in change_set, got %+v", changeSet.Files)
	}

	memory, err := store.LoadMemory(context.Background())
	if err != nil {
		t.Fatalf("LoadMemory returned error: %v", err)
	}
	foundTypedFact := false
	for _, fact := range memory.StableFacts {
		if strings.HasPrefix(fact.ID, workspaceFactRepoPathPrefix) || strings.HasPrefix(fact.ID, workspaceFactChangedPathPrefix) {
			foundTypedFact = true
			break
		}
	}
	if !foundTypedFact {
		t.Fatalf("expected typed workspace facts, got %+v", memory.StableFacts)
	}

	expectedPath := normalizePathForWorkspace("README.md")
	foundPrimaryScope := false
	foundVerifyScope := false
	for _, unit := range result.Run.WorkUnits {
		switch {
		case unit.Kind == "primary" && len(unit.WriteSet) > 0:
			if containsString(unit.WriteSet, expectedPath) {
				foundPrimaryScope = true
			}
		case unit.Kind == "verification" && len(unit.ReadSet) > 0:
			if containsString(unit.ReadSet, expectedPath) {
				foundVerifyScope = true
			}
		}
	}
	if !foundPrimaryScope || !foundVerifyScope {
		t.Fatalf("expected scoped work units, got %+v", result.Run.WorkUnits)
	}
}

func TestRunTurnSuppressesDuplicateToolCalls(t *testing.T) {
	var calls atomic.Int32
	service := New(
		&fakeModelClient{
			responses: map[string][]domain.ModelResponse{
				"manager": {{
					Message: domain.Message{
						Role: domain.RoleAssistant,
						ToolCalls: []domain.ToolCall{
							{ID: "call-1", Name: "fs_read", Arguments: map[string]any{"path": "README.md"}},
							{ID: "call-2", Name: "fs_read", Arguments: map[string]any{"path": "README.md"}},
						},
					},
				}, {
					Message: domain.Message{Role: domain.RoleAssistant, Content: "done"},
				}},
			},
		},
		&fakeToolExecutor{
			defs: map[string][]domain.ToolDefinition{
				"manager": {{
					Name:         "fs_read",
					ReadOnly:     true,
					ParallelSafe: true,
					Metadata:     map[string]any{"category": "fs"},
					Semantics: domain.ToolSemantics{
						Class:           domain.ToolClassObserve,
						ReusePolicy:     domain.ToolReuseOnSuccess,
						DuplicatePolicy: domain.ToolDuplicateSuppressInflight,
						Freshness:       domain.ToolFreshnessPolicy{Strategy: domain.ToolFreshnessReadSet},
						SideEffectClass: domain.SideEffectNone,
						Source:          "fs",
						ReadPathArgs:    []string{"path"},
					},
				}},
			},
			exec: func(context.Context, domain.AgentSpec, domain.ToolCall) domain.ToolResult {
				calls.Add(1)
				return domain.ToolResult{Success: true, Output: "hello"}
			},
		},
		fakeCatalog{agents: map[string]domain.AgentSpec{
			"manager": {ID: "manager", Mode: domain.AgentModeManager, MaxTurns: 4},
		}},
		Config{MaxParallelAgents: 4, MaxHandoffDepth: 1, DisablePhaseHarness: true},
	)

	result, err := service.RunTurn(context.Background(), domain.TurnRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "read file"}},
	})
	if err != nil {
		t.Fatalf("RunTurn returned error: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected one tool execution, got %d", calls.Load())
	}
	if !hasEventType(result.Events, "duplicate_suppressed") {
		t.Fatalf("expected duplicate_suppressed event, got %+v", result.Events)
	}
}

func TestDescribeToolRuntimeNormalizesIdentityDefaults(t *testing.T) {
	service := &Service{}
	def := domain.ToolDefinition{
		Name: "fs_list",
		Semantics: domain.ToolSemantics{
			Class:           domain.ToolClassObserve,
			ReusePolicy:     domain.ToolReuseOnSuccess,
			DuplicatePolicy: domain.ToolDuplicateSuppressSemantic,
			Freshness:       domain.ToolFreshnessPolicy{Strategy: domain.ToolFreshnessReadSet},
			SideEffectClass: domain.SideEffectNone,
			Source:          "fs",
			ReadPathArgs:    []string{"path"},
			IdentityArgs:    []string{"path", "depth", "include_hidden", "limit_entries"},
			IdentityDefaults: map[string]any{
				"depth":          0,
				"include_hidden": false,
				"limit_entries":  80,
			},
		},
	}

	implicit := service.describeToolRuntime(context.Background(), domain.AgentSpec{ID: "manager"}, executableCall{
		call:       domain.ToolCall{Name: "fs_list", Arguments: map[string]any{"path": "."}},
		definition: def,
	})
	explicit := service.describeToolRuntime(context.Background(), domain.AgentSpec{ID: "manager"}, executableCall{
		call: domain.ToolCall{Name: "fs_list", Arguments: map[string]any{
			"path":           ".",
			"depth":          0,
			"include_hidden": false,
			"limit_entries":  80,
		}},
		definition: def,
	})
	deeper := service.describeToolRuntime(context.Background(), domain.AgentSpec{ID: "manager"}, executableCall{
		call: domain.ToolCall{Name: "fs_list", Arguments: map[string]any{
			"path":          ".",
			"depth":         1,
			"limit_entries": 80,
		}},
		definition: def,
	})

	if implicit.semanticKey != explicit.semanticKey {
		t.Fatalf("expected implicit and explicit defaults to share semantic key: %q != %q", implicit.semanticKey, explicit.semanticKey)
	}
	if implicit.semanticKey == deeper.semanticKey {
		t.Fatalf("expected changed depth to change semantic key: %q", implicit.semanticKey)
	}
}

func TestRunTurnReusesCachedObservationsAcrossTurns(t *testing.T) {
	store, err := state.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore returned error: %v", err)
	}

	var calls atomic.Int32
	service := New(
		&fakeModelClient{
			responses: map[string][]domain.ModelResponse{
				"manager": {{
					Message: domain.Message{
						Role:      domain.RoleAssistant,
						ToolCalls: []domain.ToolCall{{ID: "call-1", Name: "fs_read", Arguments: map[string]any{"path": "README.md"}}},
					},
				}, {
					Message: domain.Message{Role: domain.RoleAssistant, Content: "first"},
				}, {
					Message: domain.Message{
						Role:      domain.RoleAssistant,
						ToolCalls: []domain.ToolCall{{ID: "call-2", Name: "fs_read", Arguments: map[string]any{"path": "README.md"}}},
					},
				}, {
					Message: domain.Message{Role: domain.RoleAssistant, Content: "second"},
				}},
			},
		},
		&fakeToolExecutor{
			defs: map[string][]domain.ToolDefinition{
				"manager": {{
					Name:         "fs_read",
					ReadOnly:     true,
					ParallelSafe: true,
					Metadata:     map[string]any{"category": "fs"},
					Semantics: domain.ToolSemantics{
						Class:           domain.ToolClassObserve,
						ReusePolicy:     domain.ToolReuseOnSuccess,
						DuplicatePolicy: domain.ToolDuplicateSuppressInflight,
						Freshness:       domain.ToolFreshnessPolicy{Strategy: domain.ToolFreshnessReadSet},
						SideEffectClass: domain.SideEffectNone,
						Source:          "fs",
						ReadPathArgs:    []string{"path"},
					},
				}},
			},
			exec: func(context.Context, domain.AgentSpec, domain.ToolCall) domain.ToolResult {
				calls.Add(1)
				return domain.ToolResult{Success: true, Output: "cached hello"}
			},
		},
		fakeCatalog{agents: map[string]domain.AgentSpec{
			"manager": {ID: "manager", Mode: domain.AgentModeManager, MaxTurns: 4},
		}},
		Config{
			MaxParallelAgents:   2,
			MaxHandoffDepth:     1,
			DisablePhaseHarness: true,
			RunStore:            store,
			MemoryStore:         store,
			RuntimeStore:        store,
		},
	)

	_, err = service.RunTurn(context.Background(), domain.TurnRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "read file"}},
	})
	if err != nil {
		t.Fatalf("first RunTurn returned error: %v", err)
	}
	second, err := service.RunTurn(context.Background(), domain.TurnRequest{
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "read file again"}},
	})
	if err != nil {
		t.Fatalf("second RunTurn returned error: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected cached second execution, got %d calls", calls.Load())
	}
	if !hasEventType(second.Events, "cache_hit") {
		t.Fatalf("expected cache_hit event, got %+v", second.Events)
	}
}

func TestRunVerifyPhaseRunsIndependentReviewersInParallel(t *testing.T) {
	model := &concurrentModelClient{
		delay: 60 * time.Millisecond,
		responses: map[string]domain.ModelResponse{
			"coder": {
				Message: domain.Message{Role: domain.RoleAssistant, Content: "implemented"},
			},
			"tester": {
				Message: domain.Message{Role: domain.RoleAssistant, Content: "VERIFICATION_STATUS: pass\nSUMMARY: tests good\nREPAIR_BRIEF: none"},
			},
			"reviewer": {
				Message: domain.Message{Role: domain.RoleAssistant, Content: "VERIFICATION_STATUS: pass\nSUMMARY: review good\nREPAIR_BRIEF: none"},
			},
		},
	}
	service := New(
		model,
		&fakeToolExecutor{},
		fakeCatalog{agents: map[string]domain.AgentSpec{
			"coder":    {ID: "coder", Mode: domain.AgentModeHandoff, MaxTurns: 4},
			"tester":   {ID: "tester", Mode: domain.AgentModeTool, ReadOnly: true, MaxTurns: 4},
			"reviewer": {ID: "reviewer", Mode: domain.AgentModeTool, ReadOnly: true, MaxTurns: 4},
		}},
		Config{MaxParallelAgents: 4, MaxHandoffDepth: 1},
	)

	plan := &domain.ExecutionPlan{
		Primary: domain.PlannedAgentAssignment{AgentID: "coder"},
		Verify: []domain.PlannedAgentAssignment{
			{AgentID: "tester", Reason: "Run tests"},
			{AgentID: "reviewer", Reason: "Review risks"},
		},
	}
	run := &domain.RunState{
		ID:        "run-1",
		RootRunID: "run-1",
		Messages:  []domain.Message{{Role: domain.RoleUser, Content: "fix it"}},
	}
	run.WorkUnits = workUnitsFromExecutionPlan(run, plan)

	_, _, err := service.runWorkGraph(context.Background(), run, plan, domain.TurnRequest{})
	if err != nil {
		t.Fatalf("runWorkGraph returned error: %v", err)
	}
	if model.maxConcurrent.Load() < 2 {
		t.Fatalf("expected parallel verification, max concurrency was %d", model.maxConcurrent.Load())
	}
	for _, unit := range run.WorkUnits {
		if strings.HasPrefix(unit.ID, "verify:") && unit.Status != "done" {
			t.Fatalf("expected verify work unit to be done, got %+v", unit)
		}
	}
}

func TestMutationFingerprintIncludesContentState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}

	service := New(nil, nil, nil, Config{})
	firstStates := service.capturePathStates(context.Background(), []string{path})
	first := mutationFingerprint([]string{path}, firstStates)
	if len(firstStates) != 1 || firstStates[0].ContentSHA256 == "" {
		t.Fatalf("expected content hash in path state, got %+v", firstStates)
	}

	if err := os.WriteFile(path, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	secondStates := service.capturePathStates(context.Background(), []string{path})
	second := mutationFingerprint([]string{path}, secondStates)
	if first == second {
		t.Fatalf("expected fingerprint to change when same-size content changes: %s", first)
	}
}

func TestExecuteToolCallEmitsMutationFingerprint(t *testing.T) {
	store, err := state.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")

	service := New(
		nil,
		&fakeToolExecutor{
			exec: func(_ context.Context, _ domain.AgentSpec, call domain.ToolCall) domain.ToolResult {
				if err := os.WriteFile(path, []byte("new"), 0o644); err != nil {
					return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: false, Output: err.Error()}
				}
				return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: true, Output: "ok"}
			},
		},
		nil,
		Config{RuntimeStore: store},
	)
	invocation := domain.AgentInvocation{
		RunID:     "run-1",
		RootRunID: "run-1",
		Agent:     domain.AgentSpec{ID: "coder"},
		Phase:     domain.RunPhaseExecute,
	}
	item := executableCall{
		call: domain.ToolCall{
			ID:        "call-1",
			Name:      "fs_write",
			Arguments: map[string]any{"path": path, "content": "new", "overwrite": true},
		},
		definition: domain.ToolDefinition{
			Name:             "fs_write",
			MutatesWorkspace: true,
			Semantics: domain.ToolSemantics{
				Class:           domain.ToolClassMutate,
				ReusePolicy:     domain.ToolReuseNever,
				DuplicatePolicy: domain.ToolDuplicateAllow,
				Freshness:       domain.ToolFreshnessPolicy{Strategy: domain.ToolFreshnessNone},
				SideEffectClass: domain.SideEffectWorkspace,
				Source:          "fs",
				WritePathArgs:   []string{"path"},
			},
		},
	}

	_, events := service.executeToolCall(context.Background(), invocation, item)
	if len(events) != 1 {
		t.Fatalf("expected one event, got %+v", events)
	}
	fingerprint, _ := events[0].Metrics["mutation_fingerprint"].(string)
	if fingerprint == "" {
		t.Fatalf("expected mutation fingerprint in event metrics, got %+v", events[0].Metrics)
	}
	mutations, err := store.ListMutations(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(mutations) != 1 || mutations[0].MutationFingerprint != fingerprint {
		t.Fatalf("expected stored mutation fingerprint %q, got %+v", fingerprint, mutations)
	}
}

func TestExecuteToolCallAnnotatesCallWithRunContext(t *testing.T) {
	var seen domain.ToolCall
	service := New(
		nil,
		&fakeToolExecutor{
			exec: func(_ context.Context, _ domain.AgentSpec, call domain.ToolCall) domain.ToolResult {
				seen = call
				return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: true, Output: "ok"}
			},
		},
		nil,
		Config{},
	)
	invocation := domain.AgentInvocation{
		RunID:     "run-1",
		RootRunID: "root-1",
		Agent:     domain.AgentSpec{ID: "coder"},
		Phase:     domain.RunPhaseExecute,
		Attempt:   3,
	}

	service.executeToolCall(context.Background(), invocation, executableCall{
		call: domain.ToolCall{ID: "call-1", Name: "fs_stat", Arguments: map[string]any{"path": "/tmp/a.txt"}},
		definition: domain.ToolDefinition{
			Name: "fs_stat",
			Semantics: domain.ToolSemantics{
				Class:           domain.ToolClassObserve,
				ReusePolicy:     domain.ToolReuseNever,
				DuplicatePolicy: domain.ToolDuplicateAllow,
				Freshness:       domain.ToolFreshnessPolicy{Strategy: domain.ToolFreshnessNone},
				SideEffectClass: domain.SideEffectNone,
			},
		},
	})

	if seen.RunID != "run-1" || seen.RootRunID != "root-1" || seen.Phase != domain.RunPhaseExecute || seen.Attempt != 3 {
		t.Fatalf("expected call runtime context, got %+v", seen)
	}
}

func TestNewEventSeparatesRawDetailAndDisplay(t *testing.T) {
	service := New(nil, nil, nil, Config{})
	event := service.newEvent("run-1", "", "coder", "tool_failed", domain.RunPhaseExecute, 1, "failed", "first line\nsecond line", "", nil, 3)

	if event.Detail != "first line\nsecond line" {
		t.Fatalf("expected raw detail to remain intact, got %q", event.Detail)
	}
	if event.Display != "first line (+1 lines)" {
		t.Fatalf("expected compact display detail, got %q", event.Display)
	}
}

func TestBuildPermissionAuditArtifactFromScratch(t *testing.T) {
	store, err := state.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	record := domain.PermissionDecisionRecord{
		RunID:        "run-1",
		RootRunID:    "root-1",
		Phase:        domain.RunPhaseExecute,
		AgentID:      "coder",
		ToolName:     "fs_write",
		Operation:    "ファイル書き込み",
		Resource:     "/tmp/a.txt",
		Action:       "write",
		ResourceKind: "file",
		Risk:         "high",
		Scope:        "/tmp/a.txt",
		PreviewKind:  "diff",
		PreviewLines: 3,
		ChangeFiles:  1,
		Additions:    1,
		Deletions:    1,
		Decision:     domain.PermissionAllowOnce,
		CreatedAt:    time.Now(),
	}
	payload, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveScratch(context.Background(), domain.ScratchRecord{
		ID:        "permission-1",
		Kind:      "permission_decision",
		SessionID: "root-1",
		Payload:   payload,
		CreatedAt: record.CreatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	service := New(nil, nil, nil, Config{RuntimeStore: store})
	run := &domain.RunState{ID: "run-1", RootRunID: "root-1"}

	artifact := service.buildPermissionAuditArtifact(context.Background(), run, domain.RunPhaseFinalize)
	if artifact.ID == "" || artifact.Kind != "permission_audit" {
		t.Fatalf("expected permission audit artifact, got %+v", artifact)
	}
	var auditPayload domain.PermissionAuditArtifactPayload
	if err := json.Unmarshal(artifact.Payload, &auditPayload); err != nil {
		t.Fatal(err)
	}
	if len(auditPayload.Records) != 1 || auditPayload.Records[0].Decision != domain.PermissionAllowOnce || auditPayload.Records[0].ChangeFiles != 1 || auditPayload.Records[0].Additions != 1 || auditPayload.Records[0].Deletions != 1 {
		t.Fatalf("unexpected audit payload: %+v", auditPayload)
	}
	if !strings.Contains(artifact.Text, "fs_write") || !strings.Contains(artifact.Text, "/tmp/a.txt") || !strings.Contains(artifact.Text, "changes=files=1 +1 -1") {
		t.Fatalf("expected readable audit content, got %q", artifact.Text)
	}
}

func hasEventType(events []domain.ExecutionEvent, typ string) bool {
	for _, event := range events {
		if event.Type == typ {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
