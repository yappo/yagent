package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"yagent/internal/domain"
)

type stubOrchestrator struct{}

func (stubOrchestrator) RunTurn(_ context.Context, _ domain.TurnRequest) (domain.TurnResult, error) {
	return domain.TurnResult{}, nil
}

func (stubOrchestrator) ContinueConversation(_ context.Context, _ domain.ConversationTurnRequest) (domain.TurnResult, error) {
	return domain.TurnResult{}, nil
}

func (stubOrchestrator) RecoverWorkflow(_ context.Context, _ domain.WorkflowRecoveryRequest) (domain.TurnResult, error) {
	return domain.TurnResult{}, nil
}

type recordingOrchestrator struct {
	last            domain.TurnRequest
	runCalls        int
	continueRequest domain.ConversationTurnRequest
	continueCalls   int
	continueResult  domain.TurnResult
	recoveryRequest domain.WorkflowRecoveryRequest
	recoveryCalls   int
	recoveryResult  domain.TurnResult
}

func (r *recordingOrchestrator) RunTurn(_ context.Context, request domain.TurnRequest) (domain.TurnResult, error) {
	r.runCalls++
	r.last = request
	return domain.TurnResult{Message: domain.Message{Role: domain.RoleAssistant, Content: "ok"}}, nil
}

func (r *recordingOrchestrator) ContinueConversation(_ context.Context, request domain.ConversationTurnRequest) (domain.TurnResult, error) {
	r.continueCalls++
	r.continueRequest = request
	if r.continueResult.Message.Role == "" {
		r.continueResult.Message = domain.Message{Role: domain.RoleAssistant, Content: "continued"}
	}
	return r.continueResult, nil
}

func (r *recordingOrchestrator) RecoverWorkflow(_ context.Context, request domain.WorkflowRecoveryRequest) (domain.TurnResult, error) {
	r.recoveryCalls++
	r.recoveryRequest = request
	if r.recoveryResult.Message.Role == "" {
		r.recoveryResult.Message = domain.Message{Role: domain.RoleAssistant, Content: "recovered"}
	}
	return r.recoveryResult, nil
}

type stubToolExecutor struct {
	definitions []domain.ToolDefinition
}

func (s stubToolExecutor) Definitions(_ domain.AgentSpec) []domain.ToolDefinition {
	return append([]domain.ToolDefinition(nil), s.definitions...)
}

func (s stubToolExecutor) Execute(_ context.Context, _ domain.AgentSpec, call domain.ToolCall) domain.ToolResult {
	return domain.ToolResult{CallID: call.ID, Name: call.Name, Success: true}
}

type stubTaskCatalog struct {
	tasks []domain.TaskDefinition
}

func (s stubTaskCatalog) List(_ context.Context) []domain.TaskDefinition {
	return append([]domain.TaskDefinition(nil), s.tasks...)
}

func (s stubTaskCatalog) Get(_ context.Context, id string) (domain.TaskDefinition, bool) {
	for _, task := range s.tasks {
		if task.ID == id {
			return task, true
		}
	}
	return domain.TaskDefinition{}, false
}

type stubMCPBindings struct {
	bound []domain.BoundMCPTool
}

func (s stubMCPBindings) Bind(_ context.Context, _ domain.TaskDefinition) ([]domain.MCPToolDescriptor, error) {
	return nil, nil
}

func (s stubMCPBindings) BoundTools() []domain.BoundMCPTool {
	return append([]domain.BoundMCPTool(nil), s.bound...)
}

func (s stubMCPBindings) CallTool(_ context.Context, _ string, _ string, _ map[string]any, _ map[string]any) (domain.MCPToolCallResult, error) {
	return domain.MCPToolCallResult{}, nil
}

type stubAgentCatalog struct {
	agents []domain.AgentSpec
}

func (s stubAgentCatalog) List() []domain.AgentSpec {
	return append([]domain.AgentSpec(nil), s.agents...)
}

func (s stubAgentCatalog) Resolve(id string) (domain.AgentSpec, bool) {
	for _, agent := range s.agents {
		if agent.ID == id {
			return agent, true
		}
	}
	return domain.AgentSpec{}, false
}

func (s stubAgentCatalog) LoadUserAgents(_ []string) error {
	return nil
}

type stubMemoryStore struct {
	memory *domain.RepoMemory
}

type stubRunStore struct {
	runs          map[string]*domain.RunState
	latest        string
	conversations []domain.ConversationTurnRecord
}

func (s stubRunStore) SaveRun(context.Context, *domain.RunState) error {
	return nil
}

func (s stubRunStore) LoadRun(_ context.Context, id string) (*domain.RunState, error) {
	return s.runs[id], nil
}

func (s stubRunStore) LoadLatestRun(ctx context.Context) (*domain.RunState, error) {
	return s.LoadRun(ctx, s.latest)
}

func (s stubRunStore) SaveConversationTurn(context.Context, domain.ConversationTurnRecord) error {
	return nil
}

func (s stubRunStore) ListConversationTurns(context.Context, int) ([]domain.ConversationTurnRecord, error) {
	return append([]domain.ConversationTurnRecord(nil), s.conversations...), nil
}

func (s stubMemoryStore) LoadMemory(context.Context) (*domain.RepoMemory, error) {
	if s.memory == nil {
		return &domain.RepoMemory{}, nil
	}
	return s.memory, nil
}

func (s stubMemoryStore) SaveMemory(context.Context, *domain.RepoMemory) error {
	return nil
}

func (s stubMemoryStore) RecordCommand(context.Context, domain.CommandMemoryEntry) error {
	return nil
}

type countingMemoryStore struct {
	memory *domain.RepoMemory
	loads  int
}

func (s *countingMemoryStore) LoadMemory(context.Context) (*domain.RepoMemory, error) {
	s.loads++
	if s.memory == nil {
		return &domain.RepoMemory{}, nil
	}
	return s.memory, nil
}

func (s *countingMemoryStore) SaveMemory(context.Context, *domain.RepoMemory) error {
	return nil
}

func (s *countingMemoryStore) RecordCommand(context.Context, domain.CommandMemoryEntry) error {
	return nil
}

func newTestModel(t *testing.T) model {
	t.Helper()
	return newModel(stubOrchestrator{}, t.TempDir(), "", nil, nil, nil, nil)
}

func flattenChatBlocks(blocks []chatBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		parts = append(parts, strings.Join(block.rawLines, "\n"))
	}
	return strings.Join(parts, "\n")
}

func TestPermissionRequestState(t *testing.T) {
	m := newTestModel(t)
	response := make(chan domain.PermissionDecision, 1)

	modelValue, _ := m.Update(permissionRequestMsg{
		request: domain.PermissionRequest{
			ToolName:  "fs_read",
			Operation: "ファイル読み取り",
			Resource:  "/tmp/a.txt",
		},
		response: response,
	})

	next := modelValue.(model)
	if next.permission == nil {
		t.Fatalf("permission state was not set")
	}
}

func TestPermissionRequestsQueueInsteadOfOverwriting(t *testing.T) {
	m := newTestModel(t)
	first := make(chan domain.PermissionDecision, 1)
	second := make(chan domain.PermissionDecision, 1)

	modelValue, _ := m.Update(permissionRequestMsg{
		request: domain.PermissionRequest{
			ToolName:  "fs_list",
			Operation: "ディレクトリ一覧取得",
			Resource:  "/tmp/one",
		},
		response: first,
	})
	next := modelValue.(model)

	modelValue, _ = next.Update(permissionRequestMsg{
		request: domain.PermissionRequest{
			ToolName:  "fs_list",
			Operation: "ディレクトリ一覧取得",
			Resource:  "/tmp/two",
		},
		response: second,
	})
	next = modelValue.(model)

	if next.permission == nil || next.permission.request.Resource != "/tmp/one" {
		t.Fatalf("expected first permission to remain active, got %+v", next.permission)
	}
	if len(next.permissionQueue) != 1 || next.permissionQueue[0].request.Resource != "/tmp/two" {
		t.Fatalf("expected second permission to be queued, got %+v", next.permissionQueue)
	}

	next.resolvePermission(domain.PermissionAllowOnce)
	if next.permission == nil || next.permission.request.Resource != "/tmp/two" {
		t.Fatalf("expected queued permission to become active, got %+v", next.permission)
	}
}

func TestPermissionRequestsAggregateMatchingActiveRequest(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	first := make(chan domain.PermissionDecision, 1)
	second := make(chan domain.PermissionDecision, 1)
	request := domain.PermissionRequest{
		ToolName:     "fs_read",
		Operation:    "ファイル読み取り",
		Resource:     "/tmp/a.txt",
		Action:       "read",
		ResourceKind: "file",
		Risk:         "medium",
		Scope:        "/tmp/a.txt",
		SideEffects:  []string{"llm_disclosure"},
		AgentID:      "coder",
	}
	secondRequest := request
	secondRequest.AgentID = "reviewer"

	modelValue, _ := m.Update(permissionRequestMsg{request: request, response: first})
	next := modelValue.(model)
	modelValue, _ = next.Update(permissionRequestMsg{request: secondRequest, response: second})
	next = modelValue.(model)

	if next.permission == nil || next.permission.batchSize() != 2 || len(next.permissionQueue) != 0 {
		t.Fatalf("expected matching permission to aggregate into active state, active=%+v queue=%+v", next.permission, next.permissionQueue)
	}
	if next.pendingApprovalCount() != 2 {
		t.Fatalf("expected pending count to include aggregated request, got %d", next.pendingApprovalCount())
	}
	card := next.renderPermissionCard()
	for _, want := range []string{"same-kind requests: 2", "resources: /tmp/a.txt", "requesters: coder (subagent), reviewer (subagent)"} {
		if !strings.Contains(card, want) {
			t.Fatalf("expected %q in permission card, got %q", want, card)
		}
	}

	next.resolvePermission(domain.PermissionAllowOnce)
	if next.permission != nil || len(next.permissionQueue) != 0 {
		t.Fatalf("expected aggregated permission to resolve completely, active=%+v queue=%+v", next.permission, next.permissionQueue)
	}
	if got := <-first; got != domain.PermissionAllowOnce {
		t.Fatalf("expected first allow once decision, got %s", got)
	}
	if got := <-second; got != domain.PermissionAllowOnce {
		t.Fatalf("expected second allow once decision, got %s", got)
	}
	output := flattenChatBlocks(next.chatBlocks)
	if !strings.Contains(output, "同種許可 (2件)") || !strings.Contains(output, "同種 request 2件") {
		t.Fatalf("expected grouped resolution in chat log, got %q", output)
	}
}

func TestPermissionRequestsAggregateMatchingQueuedRequest(t *testing.T) {
	m := newTestModel(t)
	first := make(chan domain.PermissionDecision, 1)
	second := make(chan domain.PermissionDecision, 1)
	third := make(chan domain.PermissionDecision, 1)
	activeRequest := domain.PermissionRequest{
		ToolName:     "task_run",
		Operation:    "task 実行",
		Resource:     "go:test",
		Action:       "execute",
		ResourceKind: "task",
		Risk:         "high",
		Scope:        "go:test",
	}
	queuedRequest := domain.PermissionRequest{
		ToolName:     "fs_read",
		Operation:    "ファイル読み取り",
		Resource:     "/tmp/a.txt",
		Action:       "read",
		ResourceKind: "file",
		Risk:         "medium",
		Scope:        "/tmp/a.txt",
		SideEffects:  []string{"llm_disclosure"},
	}

	modelValue, _ := m.Update(permissionRequestMsg{request: activeRequest, response: first})
	next := modelValue.(model)
	modelValue, _ = next.Update(permissionRequestMsg{request: queuedRequest, response: second})
	next = modelValue.(model)
	modelValue, _ = next.Update(permissionRequestMsg{request: queuedRequest, response: third})
	next = modelValue.(model)

	if len(next.permissionQueue) != 1 || next.permissionQueue[0].batchSize() != 2 {
		t.Fatalf("expected matching queued permissions to aggregate, queue=%+v", next.permissionQueue)
	}
	if next.queuedPermissionCount() != 2 || next.pendingApprovalCount() != 3 {
		t.Fatalf("unexpected pending counts: queue=%d total=%d", next.queuedPermissionCount(), next.pendingApprovalCount())
	}
	if approvals := strings.Join(next.listApprovals(), "\n"); !strings.Contains(approvals, "same_kind=2") {
		t.Fatalf("expected grouped queued approval in list, got %q", approvals)
	}

	next.resolvePermission(domain.PermissionDeny)
	if got := <-first; got != domain.PermissionDeny {
		t.Fatalf("expected active deny decision, got %s", got)
	}
	if next.permission == nil || next.permission.batchSize() != 2 {
		t.Fatalf("expected queued group to become active, got %+v", next.permission)
	}
	next.resolvePermission(domain.PermissionAllowOnce)
	if got := <-second; got != domain.PermissionAllowOnce {
		t.Fatalf("expected second allow once decision, got %s", got)
	}
	if got := <-third; got != domain.PermissionAllowOnce {
		t.Fatalf("expected third allow once decision, got %s", got)
	}
}

func TestSessionPermissionApprovalAutoApprovesQueuedMatchingRequests(t *testing.T) {
	m := newTestModel(t)
	first := make(chan domain.PermissionDecision, 1)
	second := make(chan domain.PermissionDecision, 1)
	request := domain.PermissionRequest{
		ToolName:     "fs_read",
		Operation:    "ファイル読み取り",
		Resource:     "/tmp/a.txt",
		Action:       "read",
		ResourceKind: "file",
		Risk:         "medium",
		Scope:        "/tmp/a.txt",
	}

	modelValue, _ := m.Update(permissionRequestMsg{request: request, response: first})
	next := modelValue.(model)
	modelValue, _ = next.Update(permissionRequestMsg{request: request, response: second})
	next = modelValue.(model)

	next.resolvePermission(domain.PermissionAllowSession)

	if next.permission != nil || len(next.permissionQueue) != 0 {
		t.Fatalf("expected queued matching permission to auto-resolve, got active=%+v queue=%+v", next.permission, next.permissionQueue)
	}
	if got := <-first; got != domain.PermissionAllowSession {
		t.Fatalf("expected first allow session decision, got %s", got)
	}
	if got := <-second; got != domain.PermissionAllowSession {
		t.Fatalf("expected second allow session decision, got %s", got)
	}
}

func TestPermissionCtrlABatchAllowsActiveAndQueuedRequests(t *testing.T) {
	m := newTestModel(t)
	first := make(chan domain.PermissionDecision, 1)
	second := make(chan domain.PermissionDecision, 1)

	modelValue, _ := m.Update(permissionRequestMsg{
		request: domain.PermissionRequest{
			ToolName:  "fs_write",
			Operation: "ファイル書き込み",
			Resource:  "/tmp/one",
		},
		response: first,
	})
	next := modelValue.(model)
	modelValue, _ = next.Update(permissionRequestMsg{
		request: domain.PermissionRequest{
			ToolName:  "patch_apply",
			Operation: "patch 適用",
			Resource:  "/tmp/two",
		},
		response: second,
	})
	next = modelValue.(model)

	modelValue, _ = next.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	next = modelValue.(model)

	if next.permission != nil || len(next.permissionQueue) != 0 {
		t.Fatalf("expected all permissions resolved, got active=%+v queue=%+v", next.permission, next.permissionQueue)
	}
	if got := <-first; got != domain.PermissionAllowOnce {
		t.Fatalf("expected first allow once decision, got %s", got)
	}
	if got := <-second; got != domain.PermissionAllowOnce {
		t.Fatalf("expected second allow once decision, got %s", got)
	}
	output := flattenChatBlocks(next.chatBlocks)
	if !strings.Contains(output, "一括許可 (2件)") {
		t.Fatalf("expected batch label in chat log, got %q", output)
	}
}

func TestPermissionCtrlDBatchDeniesActiveAndQueuedRequests(t *testing.T) {
	m := newTestModel(t)
	first := make(chan domain.PermissionDecision, 1)
	second := make(chan domain.PermissionDecision, 1)

	modelValue, _ := m.Update(permissionRequestMsg{
		request: domain.PermissionRequest{
			ToolName:  "task_run",
			Operation: "task 実行",
			Resource:  "go:test",
		},
		response: first,
	})
	next := modelValue.(model)
	modelValue, _ = next.Update(permissionRequestMsg{
		request: domain.PermissionRequest{
			ToolName:  "fs_write",
			Operation: "ファイル書き込み",
			Resource:  "/tmp/two",
		},
		response: second,
	})
	next = modelValue.(model)

	modelValue, _ = next.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	next = modelValue.(model)

	if next.permission != nil || len(next.permissionQueue) != 0 {
		t.Fatalf("expected all permissions resolved, got active=%+v queue=%+v", next.permission, next.permissionQueue)
	}
	if got := <-first; got != domain.PermissionDeny {
		t.Fatalf("expected first deny decision, got %s", got)
	}
	if got := <-second; got != domain.PermissionDeny {
		t.Fatalf("expected second deny decision, got %s", got)
	}
}

func TestPermissionCardShowsRequester(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.permission = &permissionState{
		request: domain.PermissionRequest{
			ToolName:  "fs_read",
			Operation: "ファイル読み取り",
			Resource:  "/tmp/a.txt",
			AgentID:   "researcher",
		},
		response: make(chan domain.PermissionDecision, 1),
	}

	card := m.renderPermissionCard()
	if !strings.Contains(card, "requester: researcher (subagent)") {
		t.Fatalf("expected requester in permission card, got %q", card)
	}
}

func TestPermissionCardShowsPreview(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.permission = &permissionState{
		request: domain.PermissionRequest{
			ToolName:    "fs_write",
			Operation:   "ファイル書き込み",
			Resource:    "/tmp/a.txt",
			PreviewKind: "diff",
			Preview:     "/tmp/a.txt\n- old\n+ new",
			ChangeFiles: 1,
			Additions:   1,
			Deletions:   1,
		},
		response: make(chan domain.PermissionDecision, 1),
	}

	card := m.renderPermissionCard()
	if !strings.Contains(card, "diff:") || !strings.Contains(card, "- old") || !strings.Contains(card, "+ new") {
		t.Fatalf("expected preview in permission card, got %q", card)
	}
	if !strings.Contains(card, "changes: files=1 +1 -1") {
		t.Fatalf("expected change stats in permission card, got %q", card)
	}
}

func TestPermissionCardUsesFourOptionsForFileRequests(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.permission = &permissionState{
		request: domain.PermissionRequest{
			ToolName:     "fs_read",
			Operation:    "ファイル読み取り",
			Resource:     "/tmp/a.txt",
			Action:       "read",
			ResourceKind: "file",
			Risk:         "medium",
			SideEffects:  []string{"llm_disclosure"},
		},
		response: make(chan domain.PermissionDecision, 1),
	}

	card := m.renderPermissionCard()
	if !strings.Contains(card, "3. ファイルパターン指定で以後許可") || !strings.Contains(card, "4. 拒否") {
		t.Fatalf("expected four-option file permission card, got %q", card)
	}
}

func TestPermissionCardKeepsThreeOptionsForNonFileRequests(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.permission = &permissionState{
		request: domain.PermissionRequest{
			ToolName:     "task_run",
			Operation:    "タスク実行",
			Resource:     "go:test",
			Action:       "execute",
			ResourceKind: "task",
			Risk:         "high",
			SideEffects:  []string{"process_spawn"},
		},
		response: make(chan domain.PermissionDecision, 1),
	}

	card := m.renderPermissionCard()
	if strings.Contains(card, "4. ") || strings.Contains(card, "3. ファイルパターン指定で以後許可") {
		t.Fatalf("expected three-option non-file permission card, got %q", card)
	}
}

func TestResolvePermissionAppendsRequesterToOutput(t *testing.T) {
	m := newTestModel(t)
	m.permission = &permissionState{
		request: domain.PermissionRequest{
			ToolName:  "fs_read",
			Operation: "ファイル読み取り",
			Resource:  "/tmp/a.txt",
			AgentID:   "manager",
		},
		response: make(chan domain.PermissionDecision, 1),
	}

	m.resolvePermission(domain.PermissionAllowSession)
	output := flattenChatBlocks(m.chatBlocks)
	if !strings.Contains(output, "manager [main]") {
		t.Fatalf("expected requester in output, got %q", output)
	}
}

func TestChatMessageErrorUsesExecutionLabel(t *testing.T) {
	m := newTestModel(t)

	modelValue, _ := m.Update(chatMessage{err: context.DeadlineExceeded})
	next := modelValue.(model)
	output := flattenChatBlocks(next.chatBlocks)
	if !strings.Contains(output, "実行エラー:") {
		t.Fatalf("expected execution error label, got %q", output)
	}
}

func TestToolEventShowsActiveToolCard(t *testing.T) {
	m := newTestModel(t)
	modelValue, _ := m.Update(toolEventMsg{event: domain.ToolEvent{
		Phase: "start",
		Call: domain.ToolCall{
			Name: "fs_read",
			Arguments: map[string]any{
				"path":        "/tmp/a.txt",
				"limit_bytes": 100.0,
			},
		},
	}})

	next := modelValue.(model)
	if !next.hasActiveTools() {
		t.Fatalf("active tool was not set")
	}
	card := next.renderToolCard()
	if !strings.Contains(card, "fs_read") || !strings.Contains(card, "/tmp/a.txt") || !strings.Contains(card, "limit_bytes=100") {
		t.Fatalf("unexpected tool card: %q", card)
	}
}

func TestToolEventTracksMultipleActiveTools(t *testing.T) {
	m := newTestModel(t)
	modelValue, _ := m.Update(toolEventMsg{event: domain.ToolEvent{
		Phase: "start",
		Call: domain.ToolCall{
			ID:   "call-1",
			Name: "fs_read",
			Arguments: map[string]any{
				"path": "/tmp/a.txt",
			},
		},
	}})
	next := modelValue.(model)
	modelValue, _ = next.Update(toolEventMsg{event: domain.ToolEvent{
		Phase: "start",
		Call: domain.ToolCall{
			ID:   "call-2",
			Name: "task_run",
			Arguments: map[string]any{
				"task_id": "go:test",
			},
		},
	}})
	next = modelValue.(model)

	card := next.renderToolCard()
	if !strings.Contains(card, "Tool Use (2 active)") || !strings.Contains(card, "fs_read") || !strings.Contains(card, "task_run") {
		t.Fatalf("expected both active tools in card, got %q", card)
	}

	modelValue, _ = next.Update(toolEventMsg{event: domain.ToolEvent{
		Phase: "finish",
		Call: domain.ToolCall{
			ID:   "call-1",
			Name: "fs_read",
			Arguments: map[string]any{
				"path": "/tmp/a.txt",
			},
		},
		Result: domain.ToolResult{Name: "fs_read", Success: true, Output: "ok"},
	}})
	next = modelValue.(model)
	card = next.renderToolCard()
	if strings.Contains(card, "fs_read") || !strings.Contains(card, "task_run") || !strings.Contains(card, "Tool Use (1 active)") {
		t.Fatalf("expected finished tool to be removed from active card, got %q", card)
	}
	if len(next.toolLogs) != 1 || !strings.Contains(next.toolLogs[0].title, "fs_read") {
		t.Fatalf("expected finished tool log, got %+v", next.toolLogs)
	}
}

func TestToolEventFinishAppendsToolLog(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.height = 30

	modelValue, _ := m.Update(toolEventMsg{event: domain.ToolEvent{
		Phase: "finish",
		Call: domain.ToolCall{
			Name: "task_run",
			Arguments: map[string]any{
				"task_id": "go:test",
			},
		},
		Result: domain.ToolResult{
			Name:    "task_run",
			Success: true,
			Output:  "ok\n[stderr]\nwarn",
		},
	}})

	next := modelValue.(model)
	if !next.hasToolLogs() {
		t.Fatalf("expected tool logs to be present")
	}
	card := next.renderToolLogCard()
	if !strings.Contains(card, "Tool Logs") || !strings.Contains(card, "task_run") || !strings.Contains(card, "[stderr]") {
		t.Fatalf("unexpected tool log card: %q", card)
	}
}

func TestToolLogsKeepRecentEntriesOnly(t *testing.T) {
	m := newTestModel(t)
	for i := 0; i < maxToolLogEntries+3; i++ {
		modelValue, _ := m.Update(toolEventMsg{event: domain.ToolEvent{
			Phase: "finish",
			Call: domain.ToolCall{
				Name: "task_run",
				Arguments: map[string]any{
					"task_id": fmt.Sprintf("task-%02d", i),
				},
			},
			Result: domain.ToolResult{
				Name:    "task_run",
				Success: true,
				Output:  fmt.Sprintf("line %d", i),
			},
		}})
		m = modelValue.(model)
	}

	if len(m.toolLogs) != maxToolLogEntries {
		t.Fatalf("expected %d tool logs, got %d", maxToolLogEntries, len(m.toolLogs))
	}
	if strings.Contains(m.toolLogs[0].title, "task-00") {
		t.Fatalf("expected oldest tool log to be pruned, got %+v", m.toolLogs)
	}
}

func TestSlashPlanSwitchesPanel(t *testing.T) {
	m := newTestModel(t)
	m.lastRun = &domain.RunState{
		Plan: []domain.PlanNode{{Title: "Implement harness", Status: "done"}},
	}

	modelValue, _ := handleSlashCommand(m, "/plan")
	next := modelValue.(model)

	if next.activePanel != sidePanelPlan {
		t.Fatalf("expected plan panel, got %s", next.activePanel)
	}
	output := flattenChatBlocks(next.chatBlocks)
	if !strings.Contains(output, "Current plan:") {
		t.Fatalf("expected plan output, got %q", output)
	}
}

func TestCtrlRightCyclesPanels(t *testing.T) {
	m := newTestModel(t)

	modelValue, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModCtrl})
	next := modelValue.(model)
	if next.activePanel != sidePanelPlan {
		t.Fatalf("expected plan panel after first cycle, got %s", next.activePanel)
	}

	modelValue, _ = next.Update(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModCtrl})
	next = modelValue.(model)
	if next.activePanel != sidePanelVerification {
		t.Fatalf("expected verification panel after second cycle, got %s", next.activePanel)
	}
}

func TestCtrlHAndCtrlLCyclePanels(t *testing.T) {
	m := newTestModel(t)

	modelValue, _ := m.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	next := modelValue.(model)
	if next.activePanel != sidePanelPlan {
		t.Fatalf("expected plan panel after ctrl+l, got %s", next.activePanel)
	}

	modelValue, _ = next.Update(tea.KeyPressMsg{Code: 'h', Mod: tea.ModCtrl})
	next = modelValue.(model)
	if next.activePanel != sidePanelRunGraph {
		t.Fatalf("expected run graph panel after ctrl+h, got %s", next.activePanel)
	}
}

func TestAltBracketCyclesPanels(t *testing.T) {
	m := newTestModel(t)

	modelValue, _ := m.Update(tea.KeyPressMsg{Code: ']', Mod: tea.ModAlt})
	next := modelValue.(model)
	if next.activePanel != sidePanelPlan {
		t.Fatalf("expected plan panel after alt+], got %s", next.activePanel)
	}

	modelValue, _ = next.Update(tea.KeyPressMsg{Code: '[', Mod: tea.ModAlt})
	next = modelValue.(model)
	if next.activePanel != sidePanelRunGraph {
		t.Fatalf("expected run graph panel after alt+[ , got %s", next.activePanel)
	}
}

func TestViewShowsPanelTabs(t *testing.T) {
	m := newTestModel(t)
	m.width = 120
	m.height = 28
	m.syncLayout()

	view := m.View().Content
	if !strings.Contains(view, "Run Graph") || !strings.Contains(view, "Verification") || !strings.Contains(view, "Memory") {
		t.Fatalf("expected panel tabs in view, got %q", view)
	}
}

func TestRenderVerificationPanelUsesRunState(t *testing.T) {
	m := newTestModel(t)
	m.width = 120
	m.height = 28
	m.activePanel = sidePanelVerification
	m.lastRun = &domain.RunState{
		ID:           "run-1",
		CurrentPhase: domain.RunPhaseVerify,
		Status:       domain.RunStatusRunning,
		Attempt:      2,
		Verification: []domain.VerificationResult{{
			Attempt:     2,
			SourceAgent: "reviewer",
			Status:      "fail",
			Summary:     "missing regression coverage",
			RepairBrief: "add a regression test",
		}},
	}
	m.syncLayout()

	rendered := m.renderStatus()
	if !strings.Contains(rendered, "reviewer") || !strings.Contains(rendered, "repair: add a regression test") {
		t.Fatalf("unexpected verification panel: %q", rendered)
	}
}

func TestRenderMemoryPanelUsesMemoryStore(t *testing.T) {
	m := newModelWithStores(stubOrchestrator{}, t.TempDir(), "", nil, nil, nil, nil, nil, stubMemoryStore{memory: &domain.RepoMemory{
		StableFacts:          []domain.WorkspaceFact{{ID: "fact-1", Summary: "Keep README updated."}},
		KnownFailures:        []string{"missing regression coverage"},
		ReusableObservations: []domain.ObservationSummary{{ObservationID: "obs-1", ToolName: "task_run", Summary: "go test ./..."}},
		RecentArtifacts:      []domain.ArtifactReference{{ID: "artifact-1", Name: "Final response", Kind: "final_response"}},
	}})
	m.memory.loading = false
	m.memory.data = &domain.RepoMemory{
		StableFacts:          []domain.WorkspaceFact{{ID: "fact-1", Summary: "Keep README updated."}},
		KnownFailures:        []string{"missing regression coverage"},
		ReusableObservations: []domain.ObservationSummary{{ObservationID: "obs-1", ToolName: "task_run", Summary: "go test ./..."}},
		RecentArtifacts:      []domain.ArtifactReference{{ID: "artifact-1", Name: "Final response", Kind: "final_response"}},
	}
	m.width = 120
	m.height = 28
	m.activePanel = sidePanelMemory
	m.syncLayout()

	rendered := m.renderStatus()
	if !strings.Contains(rendered, "Keep README updated.") || !strings.Contains(rendered, "go test ./...") {
		t.Fatalf("unexpected memory panel: %q", rendered)
	}
}

func TestToolLogViewportHeightIsBounded(t *testing.T) {
	m := newTestModel(t)
	m.height = 18
	if got := m.toolLogViewportHeight(); got < 4 {
		t.Fatalf("expected minimum bounded height, got %d", got)
	}

	m.height = 80
	if got := m.toolLogViewportHeight(); got > 10 {
		t.Fatalf("expected maximum bounded height, got %d", got)
	}
}

func TestViewportScrollKey(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 24
	m.appendOutputBlock(assistantOutputLabel, strings.Repeat("a\n", 20))
	m.syncLayout()

	modelValue, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	next := modelValue.(model)
	if next.viewport.YOffset() < 0 {
		t.Fatalf("viewport offset should not be negative")
	}
}

func TestRenderLogWrapsLongLines(t *testing.T) {
	m := newTestModel(t)
	m.viewport.SetWidth(10)
	m.appendOutputBlock(assistantOutputLabel, "aaaaaaaaaaaa")

	rendered := m.renderLog()
	if !strings.Contains(rendered, "aaaaaaaaaa\naa") {
		t.Fatalf("expected wrapped content, got %q", rendered)
	}
}

func TestCommandCandidatesForSlash(t *testing.T) {
	m := newTestModel(t)
	m.textarea.SetValue("/")

	candidates := m.activeSlashCompletion().candidates
	if len(candidates) != len(slashCommands) {
		t.Fatalf("expected %d candidates, got %d", len(slashCommands), len(candidates))
	}
}

func TestCommandCandidatesFiltered(t *testing.T) {
	m := newTestModel(t)
	m.textarea.SetValue("/cl")

	candidates := m.activeSlashCompletion().candidates
	if len(candidates) != 1 || candidates[0].value != "/clear" {
		t.Fatalf("unexpected candidates: %+v", candidates)
	}
}

func TestTabCompletesFirstCommandCandidate(t *testing.T) {
	m := newTestModel(t)
	m.textarea.SetValue("/he")

	modelValue, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	next := modelValue.(model)
	if next.textarea.Value() != "/help" {
		t.Fatalf("expected /help, got %q", next.textarea.Value())
	}
}

func TestProfileCompletionCandidates(t *testing.T) {
	m := newModelWithStoresAndProfiles(stubOrchestrator{}, t.TempDir(), "qwen", nil, nil, nil, nil, nil, nil, []string{"strong", "fast"})
	m.textarea.SetValue("/profile f")

	candidates := m.activeSlashCompletion().candidates
	if len(candidates) != 1 || candidates[0].value != "fast" {
		t.Fatalf("unexpected profile candidates: %+v", candidates)
	}

	modelValue, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	next := modelValue.(model)
	if next.textarea.Value() != "/profile fast" {
		t.Fatalf("expected /profile fast, got %q", next.textarea.Value())
	}
}

func TestThemeCompletionCandidates(t *testing.T) {
	m := newTestModel(t)
	m.textarea.SetValue("/theme c")

	candidates := m.activeSlashCompletion().candidates
	if len(candidates) != 1 || candidates[0].value != "contrast" {
		t.Fatalf("unexpected theme candidates: %+v", candidates)
	}

	modelValue, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	next := modelValue.(model)
	if next.textarea.Value() != "/theme contrast" {
		t.Fatalf("expected /theme contrast, got %q", next.textarea.Value())
	}
}

func TestTabDoesNothingWithoutCandidate(t *testing.T) {
	m := newTestModel(t)
	m.textarea.SetValue("/x")

	modelValue, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	next := modelValue.(model)
	if next.textarea.Value() != "/x" {
		t.Fatalf("textarea value changed unexpectedly: %q", next.textarea.Value())
	}
}

func TestPasteMsgUpdatesTextarea(t *testing.T) {
	m := newTestModel(t)

	modelValue, _ := m.Update(tea.PasteMsg{Content: "pasted text"})
	next := modelValue.(model)
	if next.textarea.Value() != "pasted text" {
		t.Fatalf("expected pasted text, got %q", next.textarea.Value())
	}
}

func TestTypingDoesNotDirtyLogOrStatusViewports(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.height = 30
	m.appendOutputBlock(assistantOutputLabel, "hello")
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:     "run-1",
		AgentID:   "manager",
		Type:      "agent_started",
		Timestamp: time.Now(),
	})
	m.syncLayout()
	m.logDirty = false

	modelValue, _ := m.Update(tea.KeyPressMsg{Text: "a"})
	next := modelValue.(model)

	if next.logDirty {
		t.Fatal("expected typing not to dirty log viewport")
	}
	if next.panelCache[next.activePanel].dirty {
		t.Fatal("expected typing not to dirty active panel cache")
	}
}

func TestHeightOnlyCompletionLayoutChangeDoesNotDirtyLogOrStatus(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.height = 30
	m.appendOutputBlock(assistantOutputLabel, "hello")
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:     "run-1",
		AgentID:   "manager",
		Type:      "agent_started",
		Timestamp: time.Now(),
	})
	m.syncLayout()
	m.logDirty = false
	m.panelCache[m.activePanel] = panelRenderCache{dirty: false, width: m.statusViewport.Width(), content: m.renderStatus()}
	m.textarea.SetValue("/he")

	_ = m.syncAfterComposerChange(false)

	if m.logDirty {
		t.Fatal("expected height-only change not to dirty chat log")
	}
	if m.panelCache[m.activePanel].dirty {
		t.Fatal("expected height-only change not to dirty active panel cache")
	}
}

func TestPermissionTabDoesNotTriggerCommandCompletion(t *testing.T) {
	m := newTestModel(t)
	m.textarea.SetValue("/he")
	response := make(chan domain.PermissionDecision, 1)
	m.permission = &permissionState{
		request: domain.PermissionRequest{
			ToolName:  "fs_read",
			Operation: "ファイル読み取り",
			Resource:  "/tmp/a.txt",
		},
		response:      response,
		selectedIndex: 0,
	}

	modelValue, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	next := modelValue.(model)
	if next.textarea.Value() != "/he" {
		t.Fatalf("permission tab should not complete command: %q", next.textarea.Value())
	}
}

func TestFilePermissionOptionThreeStartsPatternMode(t *testing.T) {
	m := newTestModel(t)
	response := make(chan domain.PermissionDecision, 1)
	m.permission = &permissionState{
		request: domain.PermissionRequest{
			ToolName:     "fs_read",
			Operation:    "ファイル読み取り",
			Resource:     "/tmp/a.txt",
			Action:       "read",
			ResourceKind: "file",
			Risk:         "medium",
		},
		response: response,
	}

	modelValue, _ := m.Update(tea.KeyPressMsg{Text: "3"})
	next := modelValue.(model)

	if !next.permission.patternMode {
		t.Fatal("expected option 3 to open pattern mode")
	}
	select {
	case decision := <-response:
		t.Fatalf("did not expect immediate resolution, got %s", decision)
	default:
	}
}

func TestPatternPermissionApprovalAutoApprovesMatchingRequest(t *testing.T) {
	m := newTestModel(t)
	response := make(chan domain.PermissionDecision, 1)
	request := domain.PermissionRequest{
		ToolName:     "fs_read",
		Operation:    "ファイル読み取り",
		Resource:     "/tmp/example.txt",
		Action:       "read",
		ResourceKind: "file",
		Risk:         "medium",
	}
	m.permission = &permissionState{
		request:       request,
		response:      response,
		selectedIndex: 2,
		patternMode:   true,
		patternInput:  "*.txt",
	}

	modelValue, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	next := modelValue.(model)

	if next.permission != nil {
		t.Fatalf("expected permission prompt to close, got %+v", next.permission)
	}
	if len(next.patternApprovals) != 1 {
		t.Fatalf("expected pattern approval to be stored, got %+v", next.patternApprovals)
	}
	if got := <-response; got != domain.PermissionAllowSession {
		t.Fatalf("expected allow session decision, got %s", got)
	}

	followup := make(chan domain.PermissionDecision, 1)
	modelValue, _ = next.Update(permissionRequestMsg{
		request: domain.PermissionRequest{
			ToolName:     "fs_read",
			Operation:    "ファイル読み取り",
			Resource:     "/tmp/another.txt",
			Action:       "read",
			ResourceKind: "file",
			Risk:         "medium",
		},
		response: followup,
	})
	next = modelValue.(model)
	if next.permission != nil {
		t.Fatalf("expected matching pattern approval to skip prompt, got %+v", next.permission)
	}
	if got := <-followup; got != domain.PermissionAllowSession {
		t.Fatalf("expected automatic session approval, got %s", got)
	}
}

func TestPatternPermissionApprovalAutoApprovesQueuedMatchingRequest(t *testing.T) {
	m := newTestModel(t)
	first := make(chan domain.PermissionDecision, 1)
	second := make(chan domain.PermissionDecision, 1)

	modelValue, _ := m.Update(permissionRequestMsg{
		request: domain.PermissionRequest{
			ToolName:     "fs_read",
			Operation:    "ファイル読み取り",
			Resource:     "/tmp/example.txt",
			Action:       "read",
			ResourceKind: "file",
			Risk:         "medium",
		},
		response: first,
	})
	next := modelValue.(model)
	modelValue, _ = next.Update(permissionRequestMsg{
		request: domain.PermissionRequest{
			ToolName:     "fs_read",
			Operation:    "ファイル読み取り",
			Resource:     "/tmp/another.txt",
			Action:       "read",
			ResourceKind: "file",
			Risk:         "medium",
		},
		response: second,
	})
	next = modelValue.(model)
	next.permission.patternMode = true
	next.permission.patternInput = "*.txt"

	modelValue, _ = next.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	next = modelValue.(model)

	if next.permission != nil || len(next.permissionQueue) != 0 {
		t.Fatalf("expected queued pattern permission to auto-resolve, got active=%+v queue=%+v", next.permission, next.permissionQueue)
	}
	if got := <-first; got != domain.PermissionAllowSession {
		t.Fatalf("expected first allow session decision, got %s", got)
	}
	if got := <-second; got != domain.PermissionAllowSession {
		t.Fatalf("expected second allow session decision, got %s", got)
	}
}

func TestListApprovalsShowsApprovalScopes(t *testing.T) {
	m := newTestModel(t)
	request := domain.PermissionRequest{
		ToolName:     "fs_write",
		Operation:    "ファイル書き込み",
		Resource:     "/tmp/a.txt",
		Action:       "write",
		ResourceKind: "file",
		Scope:        "/tmp/a.txt",
		Risk:         "high",
		SideEffects:  []string{"filesystem_write"},
		AgentID:      "coder",
	}
	m.sessionApprovals[approvalKey(request)] = true
	m.patternApprovals = append(m.patternApprovals, patternApproval{
		toolName:     "fs_read",
		action:       "read",
		resourceKind: "file",
		risk:         "medium",
		pattern:      "*.go",
	})
	m.permission = &permissionState{request: request, response: make(chan domain.PermissionDecision, 1)}
	m.permissionQueue = append(m.permissionQueue, permissionState{
		request: domain.PermissionRequest{
			ToolName:     "fs_remove",
			Operation:    "ファイル削除",
			Resource:     "/tmp/b.txt",
			Action:       "remove",
			ResourceKind: "file",
			Scope:        "/tmp/b.txt",
			Risk:         "high",
		},
		response: make(chan domain.PermissionDecision, 1),
	})

	rendered := strings.Join(m.listApprovals(), "\n")
	for _, want := range []string{
		"session approval: tool=fs_write action=write kind=file scope=/tmp/a.txt risk=high",
		"pattern approval: tool=fs_read action=read kind=file risk=medium pattern=*.go",
		"pending approval: ファイル書き込み (/tmp/a.txt)",
		"requester=coder (subagent)",
		"effects=filesystem_write",
		"queued approval 1: ファイル削除 (/tmp/b.txt)",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in approvals list, got %q", want, rendered)
		}
	}
}

func TestViewShowsCommandCandidates(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 24
	m.textarea.SetValue("/he")
	m.refreshCompletionState(false)
	m.syncLayout()

	view := m.View().Content
	if !strings.Contains(view, "候補: Tab で補完") {
		t.Fatalf("expected command hint in view, got %q", view)
	}
	if !strings.Contains(view, "/help") {
		t.Fatalf("expected /help candidate in view, got %q", view)
	}
}

func TestNormalTypingDoesNotReadPathCompletion(t *testing.T) {
	originalReadDir := readDirEntries
	defer func() { readDirEntries = originalReadDir }()

	readCalls := 0
	readDirEntries = func(string) ([]os.DirEntry, error) {
		readCalls++
		return nil, nil
	}

	m := newTestModel(t)
	modelValue, _ := m.Update(tea.KeyPressMsg{Text: "a"})
	next := modelValue.(model)

	if readCalls != 0 {
		t.Fatalf("expected normal typing not to read dirs, got %d", readCalls)
	}
	if next.hasCompletionCandidates() {
		t.Fatal("expected normal typing not to produce completion candidates")
	}
}

func TestPathCompletionDebouncesDirectoryReads(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	originalReadDir := readDirEntries
	defer func() { readDirEntries = originalReadDir }()

	readCalls := 0
	readDirEntries = func(path string) ([]os.DirEntry, error) {
		readCalls++
		return originalReadDir(path)
	}

	m := newModel(stubOrchestrator{}, dir, "", nil, nil, nil, nil)
	modelValue, _ := m.Update(tea.KeyPressMsg{Text: "@"})
	next := modelValue.(model)
	modelValue, _ = next.Update(tea.KeyPressMsg{Text: "R"})
	next = modelValue.(model)

	if readCalls != 0 {
		t.Fatalf("expected debounce to avoid immediate reads, got %d", readCalls)
	}
	if next.hasCompletionCandidates() {
		t.Fatal("expected candidates to stay hidden before debounce fires")
	}
	if next.completion.pendingSeq == 0 {
		t.Fatal("expected pending debounce sequence")
	}

	modelValue, _ = next.Update(pathCompletionDebounceMsg{seq: next.completion.pendingSeq})
	next = modelValue.(model)
	if readCalls != 1 {
		t.Fatalf("expected one read after debounce, got %d", readCalls)
	}
	if !next.hasCompletionCandidates() {
		t.Fatal("expected candidates after debounce")
	}
	if next.completion.ctx.candidates[0].display != "README.md" {
		t.Fatalf("unexpected candidates: %+v", next.completion.ctx.candidates)
	}

	_ = next.View()
	if readCalls != 1 {
		t.Fatalf("expected cached completion in view, got %d reads", readCalls)
	}
}

func TestTabBypassesPendingPathDebounce(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	originalReadDir := readDirEntries
	defer func() { readDirEntries = originalReadDir }()

	readCalls := 0
	readDirEntries = func(path string) ([]os.DirEntry, error) {
		readCalls++
		return originalReadDir(path)
	}

	m := newModel(stubOrchestrator{}, dir, "", nil, nil, nil, nil)
	modelValue, _ := m.Update(tea.KeyPressMsg{Text: "@"})
	next := modelValue.(model)
	modelValue, _ = next.Update(tea.KeyPressMsg{Text: "R"})
	next = modelValue.(model)

	if readCalls != 0 {
		t.Fatalf("expected no reads before tab, got %d", readCalls)
	}

	modelValue, _ = next.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	next = modelValue.(model)
	if readCalls != 1 {
		t.Fatalf("expected tab to trigger one immediate read, got %d", readCalls)
	}
	if next.textarea.Value() != "@README.md" {
		t.Fatalf("expected @README.md, got %q", next.textarea.Value())
	}
}

func TestHandleSlashCommandListsTools(t *testing.T) {
	m := newModel(
		stubOrchestrator{},
		t.TempDir(),
		"",
		stubToolExecutor{definitions: []domain.ToolDefinition{
			{Name: "fs_read", Description: "ファイルを読み取る"},
			{Name: "task_list", Description: "task を一覧する"},
			{Name: "mcp__docs__search", Description: "MCP search"},
		}},
		nil,
		nil,
		nil,
	)

	modelValue, _ := handleSlashCommand(m, "/tools")
	next := modelValue.(model)
	output := flattenChatBlocks(next.chatBlocks)

	if !strings.Contains(output, "利用可能な tool:") {
		t.Fatalf("expected tools header, got %q", output)
	}
	if !strings.Contains(output, "fs_read - ファイルを読み取る") {
		t.Fatalf("expected fs_read entry, got %q", output)
	}
	if strings.Contains(output, "mcp__docs__search") {
		t.Fatalf("expected /tools to exclude bound MCP tools, got %q", output)
	}
}

func TestHandleSlashCommandListsTasks(t *testing.T) {
	m := newModel(
		stubOrchestrator{},
		t.TempDir(),
		"",
		nil,
		stubTaskCatalog{tasks: []domain.TaskDefinition{
			{ID: "go:test", Description: "Go のテストを実行"},
			{ID: "docs:mcp", Description: "Docs MCP server を bind"},
		}},
		nil,
		nil,
	)

	modelValue, _ := handleSlashCommand(m, "/tasks")
	next := modelValue.(model)
	output := flattenChatBlocks(next.chatBlocks)

	if !strings.Contains(output, "登録済み task:") {
		t.Fatalf("expected tasks header, got %q", output)
	}
	if !strings.Contains(output, "go:test - Go のテストを実行") {
		t.Fatalf("expected go:test entry, got %q", output)
	}
}

func TestHandleSlashCommandListsBoundMCPTools(t *testing.T) {
	m := newModel(
		stubOrchestrator{},
		t.TempDir(),
		"",
		nil,
		nil,
		stubMCPBindings{bound: []domain.BoundMCPTool{
			{QualifiedName: "mcp__docs__search", Description: "ドキュメント検索"},
		}},
		nil,
	)

	modelValue, _ := handleSlashCommand(m, "/mcp")
	next := modelValue.(model)
	output := flattenChatBlocks(next.chatBlocks)

	if !strings.Contains(output, "bind 済み MCP tool:") {
		t.Fatalf("expected mcp header, got %q", output)
	}
	if !strings.Contains(output, "mcp__docs__search - ドキュメント検索") {
		t.Fatalf("expected bound MCP tool entry, got %q", output)
	}
}

func TestViewKeepsTextareaVisibleWithStatusPane(t *testing.T) {
	m := newTestModel(t)
	m.width = 140
	m.height = 24
	m.textarea.SetValue("hello world")
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:     "run-1",
		AgentID:   "manager",
		Type:      "agent_started",
		Timestamp: time.Now(),
	})
	m.syncLayout()

	view := m.View().Content
	if !strings.Contains(view, "hello world") {
		t.Fatalf("expected textarea content in view, got %q", view)
	}
}

func TestViewKeepsTextareaVisibleWithStackedStatusPane(t *testing.T) {
	m := newTestModel(t)
	m.width = 90
	m.height = 20
	m.textarea.SetValue("stacked input")
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:     "run-1",
		AgentID:   "manager",
		Type:      "agent_started",
		Timestamp: time.Now(),
	})
	m.syncLayout()

	view := m.View().Content
	if !strings.Contains(view, "stacked input") {
		t.Fatalf("expected textarea content in stacked view, got %q", view)
	}
}

func TestMainPaneSeparatorDoesNotWrap(t *testing.T) {
	m := newTestModel(t)
	m.width = 140
	m.height = 24
	m.appendOutputBlock(assistantOutputLabel, "hello")
	m.syncLayout()

	view := m.renderMainPanels()
	if strings.Contains(view, "─\n─") {
		t.Fatalf("expected pane separator to stay on one line, got %q", view)
	}
}

func TestStatusAndChatShowMetrics(t *testing.T) {
	m := newTestModel(t)
	m.width = 140
	m.height = 24
	now := time.Now()
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:        "run-1",
		AgentID:      "manager",
		Type:         "agent_started",
		Timestamp:    now.Add(-2 * time.Second),
		ContextCount: 3,
	})
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:        "run-1",
		AgentID:      "manager",
		Type:         "llm_called",
		Timestamp:    now,
		ContextCount: 3,
	})
	m.syncLayout()

	view := m.renderMainPanels()
	if !strings.Contains(view, "elapsed") || !strings.Contains(view, "ctx 3") {
		t.Fatalf("expected metrics in panes, got %q", view)
	}
	if strings.Contains(view, "Agent Status  elapsed") {
		t.Fatalf("expected status title to stay compact, got %q", view)
	}
}

func TestRunGraphShowsFailureDetail(t *testing.T) {
	m := newTestModel(t)
	m.width = 140
	m.height = 24
	m.statusViewport.SetWidth(58)
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:        "run-1",
		AgentID:      "coder",
		Type:         "tool_failed",
		Phase:        domain.RunPhaseExecute,
		Attempt:      2,
		Status:       "failed",
		Detail:       "fs_read: permission denied\nfull path: /tmp/secret.txt",
		ArtifactRef:  "artifact-1",
		ContextCount: 7,
		Metrics: map[string]any{
			"semantic_key": "fs_read:key",
			"duration_ms":  int64(42),
		},
		Timestamp: time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC),
	})

	rendered := m.renderStatus()
	for _, want := range []string{
		"Failure detail",
		"tool_failed",
		"agent=coder",
		"artifact=artifact-1",
		"ctx=7",
		"fs_read: permission denied",
		"full path: /tmp/secret.txt",
		"duration_ms=42",
		"semantic_key=fs_read:key",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in failure detail, got %q", want, rendered)
		}
	}
}

func TestRunGraphFailureDetailSelectionKeys(t *testing.T) {
	m := newTestModel(t)
	m.width = 140
	m.height = 24
	m.statusViewport.SetWidth(58)
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:     "run-old",
		AgentID:   "coder",
		Type:      "agent_failed",
		Status:    "failed",
		Detail:    "old failure",
		Timestamp: time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC),
	})
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:     "run-new",
		AgentID:   "reviewer",
		Type:      "tool_failed",
		Status:    "failed",
		Detail:    "new failure",
		Timestamp: time.Date(2026, 6, 13, 12, 1, 0, 0, time.UTC),
	})

	if !strings.Contains(m.renderStatus(), "new failure") {
		t.Fatalf("expected newest failure selected, got %q", m.renderStatus())
	}
	modelValue, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModCtrl})
	next := modelValue.(model)
	if !strings.Contains(next.renderStatus(), "old failure") {
		t.Fatalf("expected older failure after ctrl+up, got %q", next.renderStatus())
	}
	modelValue, _ = next.Update(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModCtrl})
	next = modelValue.(model)
	if !strings.Contains(next.renderStatus(), "new failure") {
		t.Fatalf("expected newer failure after ctrl+down, got %q", next.renderStatus())
	}
	modelValue, _ = next.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	next = modelValue.(model)
	if strings.Contains(next.renderStatus(), "Failure detail") {
		t.Fatalf("expected failure detail to close on esc, got %q", next.renderStatus())
	}
}

func TestFailuresSlashCommandOpensRunGraphDetail(t *testing.T) {
	m := newTestModel(t)
	m.activePanel = sidePanelPlan
	m.status.showFailureDetail = false
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:     "run-1",
		AgentID:   "coder",
		Type:      "agent_failed",
		Status:    "failed",
		Detail:    "build failed",
		Timestamp: time.Now(),
	})
	m.status.showFailureDetail = false

	modelValue, _ := handleSlashCommand(m, "/failures")
	next := modelValue.(model)
	if next.activePanel != sidePanelRunGraph || !next.status.showFailureDetail {
		t.Fatalf("expected /failures to open run graph detail, panel=%s show=%t", next.activePanel, next.status.showFailureDetail)
	}
	if !strings.Contains(next.renderStatus(), "build failed") {
		t.Fatalf("expected failure detail in run graph, got %q", next.renderStatus())
	}
}

func TestStatusFilterSlashCommandFiltersRunGraph(t *testing.T) {
	m := newTestModel(t)
	now := time.Now()
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:     "root",
		AgentID:   "manager",
		Type:      "agent_started",
		Timestamp: now,
	})
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:       "coder",
		ParentRunID: "root",
		AgentID:     "coder",
		Type:        "agent_started",
		Detail:      "editing files",
		Timestamp:   now,
	})
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:       "reviewer",
		ParentRunID: "root",
		AgentID:     "reviewer",
		Type:        "agent_started",
		Detail:      "reviewing patch",
		Timestamp:   now,
	})

	modelValue, _ := handleSlashCommand(m, "/status-filter review")
	next := modelValue.(model)
	rendered := next.renderStatus()
	if !strings.Contains(rendered, "Reviewer") || strings.Contains(rendered, "Coder") {
		t.Fatalf("expected filter to keep reviewer branch only, got %q", rendered)
	}

	modelValue, _ = handleSlashCommand(next, "/status-filter clear")
	next = modelValue.(model)
	rendered = next.renderStatus()
	if !strings.Contains(rendered, "Reviewer") || !strings.Contains(rendered, "Coder") {
		t.Fatalf("expected filter clear to restore all branches, got %q", rendered)
	}
}

func TestStatusFoldSlashCommandHidesCompletedLeafNodes(t *testing.T) {
	m := newTestModel(t)
	now := time.Now()
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:     "done-run",
		AgentID:   "coder",
		Type:      "agent_completed",
		Timestamp: now,
	})
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:     "failed-run",
		AgentID:   "reviewer",
		Type:      "agent_failed",
		Detail:    "tests failed",
		Timestamp: now,
	})

	modelValue, _ := handleSlashCommand(m, "/status-fold on")
	next := modelValue.(model)
	rendered := next.renderStatus()
	if strings.Contains(rendered, "Coder") || !strings.Contains(rendered, "Reviewer") {
		t.Fatalf("expected completed node folded while failed node remains, got %q", rendered)
	}

	modelValue, _ = handleSlashCommand(next, "/status-fold off")
	next = modelValue.(model)
	rendered = next.renderStatus()
	if !strings.Contains(rendered, "Coder") || !strings.Contains(rendered, "Reviewer") {
		t.Fatalf("expected fold off to restore completed node, got %q", rendered)
	}
}

func TestStatusSearchSlashCommandShowsMatchesAndFiltersRecent(t *testing.T) {
	m := newTestModel(t)
	m.statusViewport.SetWidth(100)
	now := time.Now()
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:     "first",
		AgentID:   "coder",
		Type:      "tool_called",
		Timestamp: now,
	})
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:     "second",
		AgentID:   "tester",
		Type:      "tool_failed",
		Detail:    "needle-alpha permission denied",
		Metrics:   map[string]any{"exit_code": 1},
		Timestamp: now.Add(time.Second),
	})

	modelValue, _ := handleSlashCommand(m, "/status-search needle-alpha")
	next := modelValue.(model)
	rendered := next.renderStatus()
	for _, want := range []string{"Search", "needle-alpha permission denied", "matches="} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in status search view, got %q", want, rendered)
		}
	}
	if strings.Contains(rendered, "tool  coder") {
		t.Fatalf("expected search to filter recent events, got %q", rendered)
	}
}

func TestWideLayoutGivesStatusPaneMoreWidth(t *testing.T) {
	chatWidth, statusWidth, stacked := layoutWidths(140)
	if stacked {
		t.Fatal("expected wide layout not to stack")
	}
	if statusWidth < 58 {
		t.Fatalf("expected wider status pane, got %d", statusWidth)
	}
	if chatWidth <= statusWidth {
		t.Fatalf("expected chat pane to remain wider, got chat=%d status=%d", chatWidth, statusWidth)
	}
}

func TestUpInMultilineComposerMovesCursorBeforeHistory(t *testing.T) {
	m := newTestModel(t)
	m.history = []string{"previous prompt"}
	m.historyIndex = len(m.history)
	m.textarea.SetValue("first line\nsecond line")

	modelValue, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	next := modelValue.(model)

	if next.textarea.Value() != "first line\nsecond line" {
		t.Fatalf("textarea value should stay in draft, got %q", next.textarea.Value())
	}
	if next.textarea.Line() != 0 {
		t.Fatalf("cursor should move to first line, got line %d", next.textarea.Line())
	}
}

func TestUpAtFirstLineFallsBackToHistory(t *testing.T) {
	m := newTestModel(t)
	m.history = []string{"previous prompt"}
	m.historyIndex = len(m.history)
	m.textarea.SetValue("first line\nsecond line")
	m.textarea.CursorUp()

	modelValue, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	next := modelValue.(model)

	if next.textarea.Value() != "previous prompt" {
		t.Fatalf("expected history prompt, got %q", next.textarea.Value())
	}
}

func TestDownInMultilineComposerMovesCursorBeforeHistory(t *testing.T) {
	m := newTestModel(t)
	m.history = []string{"previous prompt"}
	m.historyIndex = 0
	m.textarea.SetValue("first line\nsecond line")
	m.textarea.CursorUp()

	modelValue, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	next := modelValue.(model)

	if next.textarea.Value() != "first line\nsecond line" {
		t.Fatalf("textarea value should stay in draft, got %q", next.textarea.Value())
	}
	if next.textarea.Line() != 1 {
		t.Fatalf("cursor should move to last line, got line %d", next.textarea.Line())
	}
}

func TestDownAtLastLineFallsBackToHistoryBehavior(t *testing.T) {
	m := newTestModel(t)
	m.history = []string{"previous prompt"}
	m.historyIndex = 0
	m.textarea.SetValue("first line\nsecond line")

	modelValue, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	next := modelValue.(model)

	if next.textarea.Value() != "" {
		t.Fatalf("expected composer reset after leaving history, got %q", next.textarea.Value())
	}
	if next.historyIndex != len(next.history) {
		t.Fatalf("expected history index at end, got %d", next.historyIndex)
	}
}

func TestPathCandidatesForRelativeFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	m := newModel(stubOrchestrator{}, dir, "", nil, nil, nil, nil)
	m.textarea.SetValue("@R")

	ctx := m.activePathCompletion()
	if ctx == nil || len(ctx.candidates) != 1 || ctx.candidates[0].display != "README.md" {
		t.Fatalf("unexpected path candidates: %+v", ctx)
	}
}

func TestPathCandidatesForCurrentDirectoryPrefix(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	m := newModel(stubOrchestrator{}, dir, "", nil, nil, nil, nil)
	m.textarea.SetValue("@./")

	ctx := m.activePathCompletion()
	if ctx == nil || len(ctx.candidates) != 1 || ctx.candidates[0].display != "./README.md" {
		t.Fatalf("unexpected current directory candidates: %+v", ctx)
	}
}

func TestPathCandidatesIncludeDirectoriesAndFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatalf("mkdir lib: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "libexe"), 0o755); err != nil {
		t.Fatalf("mkdir libexe: %v", err)
	}
	m := newModel(stubOrchestrator{}, dir, "", nil, nil, nil, nil)
	m.textarea.SetValue("@l")

	ctx := m.activePathCompletion()
	if ctx == nil || len(ctx.candidates) != 2 {
		t.Fatalf("unexpected candidates: %+v", ctx)
	}
	if ctx.candidates[0].display != "lib/" || ctx.candidates[1].display != "libexe/" {
		t.Fatalf("unexpected candidate order: %+v", ctx.candidates)
	}
}

func TestPathCandidatesForDirectoryContents(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatalf("mkdir lib: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lib", "file.go"), []byte("package lib"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	m := newModel(stubOrchestrator{}, dir, "", nil, nil, nil, nil)
	m.textarea.SetValue("@lib/")

	ctx := m.activePathCompletion()
	if ctx == nil || len(ctx.candidates) != 1 || ctx.candidates[0].display != "lib/file.go" {
		t.Fatalf("unexpected nested candidates: %+v", ctx)
	}
}

func TestPathCandidatesIncludeHiddenEntries(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("A=B"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	m := newModel(stubOrchestrator{}, dir, "", nil, nil, nil, nil)
	m.textarea.SetValue("@.")

	ctx := m.activePathCompletion()
	if ctx == nil {
		t.Fatalf("expected hidden candidates")
	}
	displays := []string{ctx.candidates[0].display, ctx.candidates[1].display}
	if !strings.Contains(strings.Join(displays, ","), ".git/") || !strings.Contains(strings.Join(displays, ","), ".env") {
		t.Fatalf("hidden entries not found: %+v", ctx.candidates)
	}
}

func TestPathCompletionTabCompletesSingleFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	m := newModel(stubOrchestrator{}, dir, "", nil, nil, nil, nil)
	m.textarea.SetValue("@R")

	modelValue, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	next := modelValue.(model)
	if next.textarea.Value() != "@README.md" {
		t.Fatalf("expected @README.md, got %q", next.textarea.Value())
	}
}

func TestPathCompletionTabCompletesDirectoryWithSlash(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatalf("mkdir lib: %v", err)
	}
	m := newModel(stubOrchestrator{}, dir, "", nil, nil, nil, nil)
	m.textarea.SetValue("@l")

	modelValue, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	next := modelValue.(model)
	if next.textarea.Value() != "@lib/" {
		t.Fatalf("expected @lib/, got %q", next.textarea.Value())
	}
}

func TestPathCompletionTabUsesLongestCommonPrefix(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatalf("mkdir lib: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "libexe"), 0o755); err != nil {
		t.Fatalf("mkdir libexe: %v", err)
	}
	m := newModel(stubOrchestrator{}, dir, "", nil, nil, nil, nil)
	m.textarea.SetValue("@l")

	modelValue, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	next := modelValue.(model)
	if next.textarea.Value() != "@lib" {
		t.Fatalf("expected @lib, got %q", next.textarea.Value())
	}
}

func TestNormalizePromptReferencesUsesOnlySelectedRefs(t *testing.T) {
	got := normalizePromptReferences("@main.go の概要を見せて", map[string]string{
		"@main.go": "main.go",
	})
	if got != "main.go の概要を見せて" {
		t.Fatalf("unexpected normalized prompt: %q", got)
	}
}

func TestNormalizePromptReferencesKeepsUnselectedToken(t *testing.T) {
	got := normalizePromptReferences("@missing.go の概要を見せて", map[string]string{
		"@main.go": "main.go",
	})
	if got != "@missing.go の概要を見せて" {
		t.Fatalf("unexpected normalized prompt: %q", got)
	}
}

func TestSubmitPromptStoresNormalizedMessageOnlyForSelectedReference(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	m := newModel(stubOrchestrator{}, dir, "", nil, nil, nil, nil)
	m.selectedRefs["@main.go"] = "main.go"
	modelValue, _ := submitPrompt(m, "@main.go の概要を見せて")
	next := modelValue.(model)

	if len(next.messages) != 1 || next.messages[0].Content != "main.go の概要を見せて" {
		t.Fatalf("unexpected stored message: %+v", next.messages)
	}
	output := flattenChatBlocks(next.chatBlocks)
	if !strings.Contains(output, "@main.go の概要を見せて") {
		t.Fatalf("original prompt was not kept in output: %q", output)
	}
}

func TestSubmitPromptKeepsManualAtReferenceUnchanged(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	m := newModel(stubOrchestrator{}, dir, "", nil, nil, nil, nil)
	modelValue, _ := submitPrompt(m, "@main.go の概要を見せて")
	next := modelValue.(model)

	if len(next.messages) != 1 || next.messages[0].Content != "@main.go の概要を見せて" {
		t.Fatalf("unexpected stored message: %+v", next.messages)
	}
}

func TestSubmitPromptUsesExplicitModelAndProfileSelection(t *testing.T) {
	runner := &recordingOrchestrator{}
	m := newModelWithStoresAndProfiles(runner, t.TempDir(), "Qwen/Qwen3.6-35B-A3B", nil, nil, nil, nil, nil, nil, []string{"strong", "fast"})

	modelValue, _ := handleSlashCommand(m, "/profile strong")
	m = modelValue.(model)
	modelValue, _ = handleSlashCommand(m, "/model gpt-5.5")
	m = modelValue.(model)
	modelValue, _ = handleSlashCommand(m, "/stream on")
	m = modelValue.(model)

	modelValue, cmd := submitPrompt(m, "hello")
	if cmd == nil {
		t.Fatal("expected command")
	}
	_ = modelValue

	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected batch message, got %T", msg)
	}
	if len(batch) == 0 {
		t.Fatal("expected batch commands")
	}
	if _, ok := batch[0]().(chatMessage); !ok {
		t.Fatalf("expected first batch command to return chatMessage")
	}
	if runner.last.Model != "gpt-5.5" || runner.last.Profile != "strong" {
		t.Fatalf("expected selected model/profile, got %+v", runner.last)
	}
	if !runner.last.Stream {
		t.Fatalf("expected streaming flag to be sent")
	}
}

func TestSubmitPromptDoesNotSendDefaultModelOverride(t *testing.T) {
	runner := &recordingOrchestrator{}
	m := newModel(runner, t.TempDir(), "Qwen/Qwen3.6-35B-A3B", nil, nil, nil, nil)

	_, cmd := submitPrompt(m, "hello")
	if cmd == nil {
		t.Fatal("expected command")
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok || len(batch) == 0 {
		t.Fatalf("expected batch command, got %T", msg)
	}
	_ = batch[0]()
	if runner.last.Model != "" || runner.last.Profile != "" {
		t.Fatalf("expected routing defaults to resolve model/profile, got %+v", runner.last)
	}
}

func TestModelAndProfileSlashCommands(t *testing.T) {
	m := newModelWithStoresAndProfiles(stubOrchestrator{}, t.TempDir(), "qwen", nil, nil, nil, nil, nil, nil, []string{"strong", "fast", "fast"})

	modelValue, _ := handleSlashCommand(m, "/profile")
	next := modelValue.(model)
	output := flattenChatBlocks(next.chatBlocks)
	if !strings.Contains(output, "routing profile: (default)") || !strings.Contains(output, "available: fast, strong") {
		t.Fatalf("expected profile status and sorted candidates, got %q", output)
	}

	modelValue, _ = handleSlashCommand(next, "/profile fast")
	next = modelValue.(model)
	if next.selectedProfile != "fast" {
		t.Fatalf("expected selected profile fast, got %q", next.selectedProfile)
	}

	modelValue, _ = handleSlashCommand(next, "/profile missing")
	next = modelValue.(model)
	if next.selectedProfile != "fast" {
		t.Fatalf("expected unknown profile to leave selection unchanged, got %q", next.selectedProfile)
	}
	output = flattenChatBlocks(next.chatBlocks)
	if !strings.Contains(output, "unknown routing profile: missing") {
		t.Fatalf("expected unknown profile warning, got %q", output)
	}

	modelValue, _ = handleSlashCommand(next, "/model Qwen/custom")
	next = modelValue.(model)
	if next.modelOverride != "Qwen/custom" {
		t.Fatalf("expected model override, got %q", next.modelOverride)
	}

	modelValue, _ = handleSlashCommand(next, "/model clear")
	next = modelValue.(model)
	if next.modelOverride != "" {
		t.Fatalf("expected model override cleared, got %q", next.modelOverride)
	}
}

func TestThemeSlashCommandAndHeader(t *testing.T) {
	m := newTestModel(t)
	m.width = 80

	modelValue, _ := handleSlashCommand(m, "/theme")
	next := modelValue.(model)
	output := flattenChatBlocks(next.chatBlocks)
	if !strings.Contains(output, "TUI theme: default") || !strings.Contains(output, "available: default, contrast, mono") {
		t.Fatalf("expected theme status and candidates, got %q", output)
	}

	modelValue, _ = handleSlashCommand(next, "/theme contrast")
	next = modelValue.(model)
	if next.selectedTheme != "contrast" {
		t.Fatalf("expected contrast theme, got %q", next.selectedTheme)
	}
	if header := next.cachedHeaderView(); !strings.Contains(header, "theme=contrast") || !strings.Contains(header, "/theme") {
		t.Fatalf("expected theme in header, got %q", header)
	}

	modelValue, _ = handleSlashCommand(next, "/theme missing")
	next = modelValue.(model)
	if next.selectedTheme != "contrast" {
		t.Fatalf("expected unknown theme to leave selection unchanged, got %q", next.selectedTheme)
	}
	output = flattenChatBlocks(next.chatBlocks)
	if !strings.Contains(output, "unknown TUI theme: missing") {
		t.Fatalf("expected unknown theme warning, got %q", output)
	}

	modelValue, _ = handleSlashCommand(next, "/theme clear")
	next = modelValue.(model)
	if next.selectedTheme != defaultThemeName {
		t.Fatalf("expected theme reset to default, got %q", next.selectedTheme)
	}
}

func TestThemeSwitchInvalidatesRenderedLogCache(t *testing.T) {
	m := newTestModel(t)
	m.viewport.SetWidth(40)
	m.appendChatBlock("## Cached heading")
	_ = m.renderLog()
	if len(m.chatBlocks) != 1 || m.chatBlocks[0].rendered == "" {
		t.Fatal("expected rendered chat block cache")
	}

	modelValue, _ := handleSlashCommand(m, "/theme mono")
	next := modelValue.(model)
	if next.selectedTheme != "mono" {
		t.Fatalf("expected mono theme, got %q", next.selectedTheme)
	}
	if next.chatBlocks[0].rendered != "" || next.chatBlocks[0].cachedWidth != 0 {
		t.Fatalf("expected existing chat block cache invalidated, got width=%d rendered=%q", next.chatBlocks[0].cachedWidth, next.chatBlocks[0].rendered)
	}
	if !next.logDirty || !next.logRender.dirty {
		t.Fatal("expected log render state to be dirty after theme switch")
	}
	if !next.headerCache.dirty || !next.mainPanelsCache.dirty {
		t.Fatal("expected visible chrome caches to be dirty after theme switch")
	}
}

func TestStreamSlashCommandAndDeltaBlock(t *testing.T) {
	m := newModel(stubOrchestrator{}, t.TempDir(), "qwen", nil, nil, nil, nil)

	modelValue, _ := handleSlashCommand(m, "/stream on")
	next := modelValue.(model)
	if !next.streaming {
		t.Fatal("expected streaming enabled")
	}
	output := flattenChatBlocks(next.chatBlocks)
	if !strings.Contains(output, "streaming: on") {
		t.Fatalf("expected streaming status, got %q", output)
	}

	modelValue, _ = next.Update(statusEventMsg{event: domain.ExecutionEvent{Type: "llm_delta", Detail: "hel"}})
	next = modelValue.(model)
	modelValue, _ = next.Update(statusEventMsg{event: domain.ExecutionEvent{Type: "llm_delta", Detail: "lo"}})
	next = modelValue.(model)
	output = flattenChatBlocks(next.chatBlocks)
	if !strings.Contains(output, "hello") {
		t.Fatalf("expected streaming content, got %q", output)
	}

	modelValue, _ = next.Update(chatMessage{content: "hello!"})
	next = modelValue.(model)
	output = flattenChatBlocks(next.chatBlocks)
	if !strings.Contains(output, "hello!") || strings.Contains(output, "hello\n") {
		t.Fatalf("expected final content to replace streaming block, got %q", output)
	}
}

func TestContinueSlashCommandUsesSelectedConversationForANewWorkflow(t *testing.T) {
	runner := &recordingOrchestrator{
		continueResult: domain.TurnResult{
			Message: domain.Message{Role: domain.RoleAssistant, Content: "continued answer"},
			Run: &domain.RunState{
				ID:             "run-new",
				WorkflowID:     "workflow-new",
				ConversationID: "conversation-a",
			},
		},
	}
	store := stubRunStore{
		runs: map[string]*domain.RunState{
			"run-old": {
				ID:             "run-old",
				WorkflowID:     "workflow-old",
				ConversationID: "conversation-a",
				Messages: []domain.Message{
					{Role: domain.RoleUser, Content: "old workflow prompt"},
				},
			},
		},
		conversations: []domain.ConversationTurnRecord{{
			ConversationID: "conversation-a",
			WorkflowID:     "workflow-old",
			Profile:        "fast",
		}},
	}
	m := newModelWithStoresAndProfiles(runner, t.TempDir(), "qwen", nil, nil, nil, nil, store, nil, []string{"fast"})
	m.lastRun = store.runs["run-old"]
	m.messages = append([]domain.Message(nil), m.lastRun.Messages...)

	modelValue, _ := handleSlashCommand(m, "/continue conversation-a")
	next := modelValue.(model)
	if next.conversationID != "conversation-a" {
		t.Fatalf("expected selected conversation, got %q", next.conversationID)
	}
	if next.lastRun != nil || len(next.messages) != 0 {
		t.Fatalf("continue must not restore the old run state, run=%+v messages=%+v", next.lastRun, next.messages)
	}
	if next.selectedProfile != "fast" {
		t.Fatalf("expected conversation profile to be selected, got %q", next.selectedProfile)
	}

	modelValue, cmd := submitPrompt(next, "new workflow prompt")
	submitted := modelValue.(model)
	msg := firstBatchChatMessage(t, cmd)
	if runner.runCalls != 0 || runner.continueCalls != 1 {
		t.Fatalf("expected ContinueConversation only, run=%d continue=%d", runner.runCalls, runner.continueCalls)
	}
	if runner.continueRequest.ConversationID != "conversation-a" || len(runner.continueRequest.Messages) != 1 || runner.continueRequest.Messages[0].Content != "new workflow prompt" {
		t.Fatalf("unexpected continuation request: %+v", runner.continueRequest)
	}
	modelValue, _ = submitted.Update(msg)
	completed := modelValue.(model)
	if completed.lastRun == nil || completed.lastRun.WorkflowID != "workflow-new" || completed.lastRun.WorkflowID == "workflow-old" {
		t.Fatalf("expected a distinct workflow identity, got %+v", completed.lastRun)
	}
	if completed.conversationID != "conversation-a" {
		t.Fatalf("expected selected conversation to remain active, got %q", completed.conversationID)
	}
}

func TestContinueSlashCommandSelectsLatestSavedConversation(t *testing.T) {
	store := stubRunStore{conversations: []domain.ConversationTurnRecord{
		{ConversationID: "conversation-latest"},
		{ConversationID: "conversation-older"},
	}}
	m := newModelWithStores(stubOrchestrator{}, t.TempDir(), "", nil, nil, nil, nil, store, nil)

	modelValue, _ := handleSlashCommand(m, "/continue latest")
	next := modelValue.(model)
	if next.conversationID != "conversation-latest" {
		t.Fatalf("expected the latest saved conversation, got %q", next.conversationID)
	}
}

func TestRecoverSlashCommandDoesNotAddAUserMessage(t *testing.T) {
	runner := &recordingOrchestrator{recoveryResult: domain.TurnResult{
		Message: domain.Message{Role: domain.RoleAssistant, Content: "workflow recovered"},
		Run:     &domain.RunState{ID: "run-recovered", WorkflowID: "workflow-pending"},
	}}
	m := newModel(runner, t.TempDir(), "", nil, nil, nil, nil)
	m.messages = []domain.Message{{Role: domain.RoleUser, Content: "existing conversation message"}}

	modelValue, cmd := handleSlashCommand(m, "/recover workflow-pending")
	next := modelValue.(model)
	if len(next.messages) != 1 || next.messages[0].Content != "existing conversation message" {
		t.Fatalf("recover must not append a user message before execution, got %+v", next.messages)
	}
	msg := firstBatchChatMessage(t, cmd)
	if runner.recoveryCalls != 1 || runner.recoveryRequest.WorkflowID != "workflow-pending" || runner.runCalls != 0 || runner.continueCalls != 0 {
		t.Fatalf("unexpected recovery calls: recovery=%d request=%+v run=%d continue=%d", runner.recoveryCalls, runner.recoveryRequest, runner.runCalls, runner.continueCalls)
	}
	modelValue, _ = next.Update(msg)
	completed := modelValue.(model)
	if len(completed.messages) != 1 || completed.messages[0].Content != "existing conversation message" {
		t.Fatalf("recover must not mutate the local conversation, got %+v", completed.messages)
	}
	if completed.lastRun == nil || completed.lastRun.WorkflowID != "workflow-pending" {
		t.Fatalf("expected recovered workflow state, got %+v", completed.lastRun)
	}
}

func firstBatchChatMessage(t *testing.T, cmd tea.Cmd) chatMessage {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected command")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) == 0 {
		t.Fatalf("expected batch command")
	}
	message, ok := batch[0]().(chatMessage)
	if !ok {
		t.Fatalf("expected chat message")
	}
	return message
}

func TestHandleSlashCommandListsAgents(t *testing.T) {
	m := newModel(
		stubOrchestrator{},
		t.TempDir(),
		"",
		nil,
		nil,
		nil,
		stubAgentCatalog{agents: []domain.AgentSpec{
			{ID: "manager", Description: "ユーザー窓口として委譲と最終応答を担当します。"},
			{ID: "docs-writer", Description: "README や設計メモの更新を担当"},
		}},
	)

	modelValue, _ := handleSlashCommand(m, "/agents")
	next := modelValue.(model)
	output := flattenChatBlocks(next.chatBlocks)

	if !strings.Contains(output, "利用可能な agent:") {
		t.Fatalf("expected agents header, got %q", output)
	}
	if !strings.Contains(output, "docs-writer - README や設計メモの更新を担当") {
		t.Fatalf("expected docs-writer entry, got %q", output)
	}
}

func TestRenderMemoryPanelDoesNotHitStoreAfterLoad(t *testing.T) {
	store := &countingMemoryStore{memory: &domain.RepoMemory{
		StableFacts: []domain.WorkspaceFact{{ID: "fact-1", Summary: "Keep README updated."}},
	}}
	m := newModelWithStores(stubOrchestrator{}, t.TempDir(), "", nil, nil, nil, nil, nil, store)
	m.width = 120
	m.height = 28
	m.activePanel = sidePanelMemory

	msg := initialMemoryLoadCmd(store)()
	modelValue, _ := m.Update(msg)
	next := modelValue.(model)
	if store.loads != 1 {
		t.Fatalf("expected one initial load, got %d", store.loads)
	}

	next.syncLayout()
	before := store.loads
	rendered := next.renderStatus()
	if store.loads != before {
		t.Fatalf("expected renderStatus not to call memory store, got %d -> %d", before, store.loads)
	}
	if !strings.Contains(rendered, "Keep README updated.") {
		t.Fatalf("unexpected memory panel: %q", rendered)
	}
}

func TestMemoryPanelShowsLoadingPlaceholderBeforeLoadCompletes(t *testing.T) {
	m := newModelWithStores(stubOrchestrator{}, t.TempDir(), "", nil, nil, nil, nil, nil, stubMemoryStore{})
	m.width = 120
	m.height = 28
	m.activePanel = sidePanelMemory
	m.memory.loading = true
	m.syncLayout()

	rendered := m.renderStatus()
	if !strings.Contains(rendered, "(memory loading...)") {
		t.Fatalf("expected loading placeholder, got %q", rendered)
	}
}

func TestApplyStatusEventMaintainsChildrenIndex(t *testing.T) {
	m := newTestModel(t)
	now := time.Now()
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:     "root",
		AgentID:   "manager",
		Type:      "agent_started",
		Timestamp: now,
	})
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:       "child-b",
		ParentRunID: "root",
		AgentID:     "coder",
		Type:        "agent_started",
		Timestamp:   now,
	})
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:       "child-a",
		ParentRunID: "root",
		AgentID:     "reviewer",
		Type:        "agent_started",
		Timestamp:   now,
	})

	children := m.status.children["root"]
	if len(children) != 2 || children[0] != "child-a" || children[1] != "child-b" {
		t.Fatalf("unexpected child index: %+v", children)
	}
}

func TestApplyStatusEventOrdersRootRunsByActiveAndLatest(t *testing.T) {
	m := newTestModel(t)
	base := time.Now()
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:     "done-run",
		AgentID:   "manager",
		Type:      "agent_completed",
		Timestamp: base.Add(-3 * time.Minute),
	})
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:     "running-older",
		AgentID:   "coder",
		Type:      "agent_started",
		Timestamp: base.Add(-2 * time.Minute),
	})
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:     "running-newer",
		AgentID:   "reviewer",
		Type:      "tool_called",
		Timestamp: base.Add(-1 * time.Minute),
	})

	if got := m.status.rootRunIDs; len(got) != 3 || got[0] != "running-newer" || got[1] != "running-older" || got[2] != "done-run" {
		t.Fatalf("unexpected root ordering: %+v", got)
	}
}

func TestApplyStatusEventReordersChildrenWhenRunBecomesActive(t *testing.T) {
	m := newTestModel(t)
	base := time.Now()
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:     "root",
		AgentID:   "manager",
		Type:      "agent_started",
		Timestamp: base.Add(-5 * time.Minute),
	})
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:       "done-child",
		ParentRunID: "root",
		AgentID:     "coder",
		Type:        "agent_completed",
		Timestamp:   base.Add(-4 * time.Minute),
	})
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:       "active-child",
		ParentRunID: "root",
		AgentID:     "reviewer",
		Type:        "agent_started",
		Timestamp:   base.Add(-3 * time.Minute),
	})

	children := m.status.children["root"]
	if len(children) != 2 || children[0] != "active-child" || children[1] != "done-child" {
		t.Fatalf("unexpected initial child ordering: %+v", children)
	}

	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:       "done-child",
		ParentRunID: "root",
		AgentID:     "coder",
		Type:        "tool_called",
		Timestamp:   base.Add(-1 * time.Minute),
	})

	children = m.status.children["root"]
	if len(children) != 2 || children[0] != "done-child" || children[1] != "active-child" {
		t.Fatalf("unexpected reordered child ordering: %+v", children)
	}
}

func TestApplyStatusEventPrunesOldTerminalRoots(t *testing.T) {
	m := newTestModel(t)
	base := time.Now()
	for i := 0; i < maxTerminalRootRuns+3; i++ {
		m.applyStatusEvent(domain.ExecutionEvent{
			RunID:     fmt.Sprintf("done-%02d", i),
			AgentID:   "manager",
			Type:      "agent_completed",
			Timestamp: base.Add(time.Duration(i) * time.Second),
		})
	}
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:     "active-root",
		AgentID:   "coder",
		Type:      "agent_started",
		Timestamp: base.Add(100 * time.Second),
	})

	if len(m.status.rootRunIDs) != maxTerminalRootRuns+1 {
		t.Fatalf("expected active root plus %d terminal roots, got %d", maxTerminalRootRuns, len(m.status.rootRunIDs))
	}
	if _, ok := m.status.nodes["done-00"]; ok {
		t.Fatal("expected oldest terminal root to be pruned")
	}
	if _, ok := m.status.nodes["active-root"]; !ok {
		t.Fatal("expected active root to be retained")
	}
}

func TestApplyStatusEventPrunesOldTerminalChildren(t *testing.T) {
	m := newTestModel(t)
	base := time.Now()
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:     "root",
		AgentID:   "manager",
		Type:      "agent_started",
		Timestamp: base,
	})
	for i := 0; i < maxTerminalChildren+3; i++ {
		m.applyStatusEvent(domain.ExecutionEvent{
			RunID:       fmt.Sprintf("done-child-%02d", i),
			ParentRunID: "root",
			AgentID:     "coder",
			Type:        "agent_completed",
			Timestamp:   base.Add(time.Duration(i+1) * time.Second),
		})
	}
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:       "active-child",
		ParentRunID: "root",
		AgentID:     "reviewer",
		Type:        "tool_called",
		Timestamp:   base.Add(100 * time.Second),
	})

	children := m.status.children["root"]
	if len(children) != maxTerminalChildren+1 {
		t.Fatalf("expected active child plus %d terminal children, got %d", maxTerminalChildren, len(children))
	}
	if _, ok := m.status.nodes["done-child-00"]; ok {
		t.Fatal("expected oldest terminal child to be pruned")
	}
	if _, ok := m.status.nodes["active-child"]; !ok {
		t.Fatal("expected active child to be retained")
	}
}

func TestApplyStatusEventSummarizesJSONDetail(t *testing.T) {
	m := newTestModel(t)
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:     "root",
		AgentID:   "manager",
		Type:      "tool_called",
		Timestamp: time.Now(),
		Detail:    `{"status":"ok","path":"internal/tui/model.go","count":12}`,
	})
	detail := m.status.nodes["root"].Detail
	if strings.Contains(detail, `{"status"`) {
		t.Fatalf("expected JSON detail to be summarized, got %q", detail)
	}
	if !strings.Contains(detail, "status=ok") || !strings.Contains(detail, "path=internal/tui/model.go") {
		t.Fatalf("expected summarized detail, got %q", detail)
	}
}

func TestApplyStatusEventUsesDisplayWithoutLosingRawFailureDetail(t *testing.T) {
	m := newTestModel(t)
	m.statusViewport.SetWidth(100)
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:     "root",
		AgentID:   "coder",
		Type:      "tool_failed",
		Status:    "failed",
		Detail:    "raw failure line one\nraw failure line two",
		Display:   "compact failure",
		Timestamp: time.Now(),
	})

	if got := m.status.nodes["root"].Detail; got != "compact failure" {
		t.Fatalf("expected node detail to use display, got %q", got)
	}
	rendered := m.renderStatus()
	if !strings.Contains(rendered, "compact failure") || !strings.Contains(rendered, "raw failure line one") {
		t.Fatalf("expected compact tree and raw failure detail, got %q", rendered)
	}
}

func TestRenderLogReusesCachedBlockWithoutWidthChange(t *testing.T) {
	m := newTestModel(t)
	m.viewport.SetWidth(20)
	m.appendOutputBlock(assistantOutputLabel, "hello world")

	first := m.renderLog()
	if len(m.chatBlocks) != 1 || m.chatBlocks[0].rendered == "" {
		t.Fatalf("expected rendered cache to be populated: %+v", m.chatBlocks)
	}
	cached := m.chatBlocks[0].rendered
	second := m.renderLog()
	if second != first {
		t.Fatalf("expected stable render output, got %q vs %q", first, second)
	}
	if m.chatBlocks[0].rendered != cached {
		t.Fatal("expected cached block render to be reused")
	}
}

func TestRenderLogRebuildsCacheWhenWidthChanges(t *testing.T) {
	m := newTestModel(t)
	m.viewport.SetWidth(20)
	m.appendOutputBlock(assistantOutputLabel, "aaaaaaaaaaaa")
	_ = m.renderLog()
	initial := m.chatBlocks[0].rendered

	m.viewport.SetWidth(5)
	rendered := m.renderLog()
	if m.chatBlocks[0].rendered == initial {
		t.Fatal("expected cached render to change after width update")
	}
	if !strings.Contains(rendered, "aaaaa\naaaaa\naa") {
		t.Fatalf("expected wrapped output after width change, got %q", rendered)
	}
}

func TestRenderLogRendersBasicMarkdown(t *testing.T) {
	m := newTestModel(t)
	m.viewport.SetWidth(80)
	m.appendOutputBlock(assistantOutputLabel, strings.Join([]string{
		"# Result",
		"- **bold** item",
		"1) `code` item",
		"> quoted text",
	}, "\n"))

	rendered := m.renderLog()
	for _, want := range []string{
		"Result",
		"• bold item",
		"1. code item",
		"│ quoted text",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected markdown render to contain %q, got %q", want, rendered)
		}
	}
	if strings.Contains(rendered, "# Result") || strings.Contains(rendered, "**bold**") || strings.Contains(rendered, "`code`") {
		t.Fatalf("expected markdown markers to be rendered away, got %q", rendered)
	}
}

func TestRenderLogKeepsCodeFenceContentLiteral(t *testing.T) {
	m := newTestModel(t)
	m.viewport.SetWidth(80)
	m.appendOutputBlock(assistantOutputLabel, "```go\nfmt.Println(`hi`)\n```")

	rendered := m.renderLog()
	if !strings.Contains(rendered, "code: go") || !strings.Contains(rendered, "fmt.Println(`hi`)") {
		t.Fatalf("expected code fence label and literal code, got %q", rendered)
	}
}

func BenchmarkRenderLogLargeHistory(b *testing.B) {
	m := newModel(stubOrchestrator{}, b.TempDir(), "", nil, nil, nil, nil)
	m.viewport.SetWidth(80)
	for i := 0; i < 1000; i++ {
		m.appendOutputBlock(assistantOutputLabel, fmt.Sprintf("message %d %s", i, strings.Repeat("x", 80)))
	}
	_ = m.renderLog()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.logDirty = true
		_ = m.renderLog()
	}
}

func BenchmarkRenderStatusRunGraphManyNodes(b *testing.B) {
	m := newModel(stubOrchestrator{}, b.TempDir(), "", nil, nil, nil, nil)
	m.width = 140
	m.height = 30
	m.statusViewport.SetWidth(58)
	now := time.Now()
	for i := 0; i < 400; i++ {
		runID := fmt.Sprintf("run-%03d", i)
		parentID := ""
		if i > 0 {
			parentID = fmt.Sprintf("run-%03d", (i-1)/2)
		}
		m.applyStatusEvent(domain.ExecutionEvent{
			RunID:       runID,
			ParentRunID: parentID,
			AgentID:     "agent",
			Type:        "agent_started",
			Timestamp:   now,
		})
	}
	m.activePanel = sidePanelRunGraph
	_ = m.renderStatus()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.invalidatePanel(sidePanelRunGraph)
		_ = m.renderStatus()
	}
}

func BenchmarkRenderStatusMemoryPanel(b *testing.B) {
	m := newModelWithStores(stubOrchestrator{}, b.TempDir(), "", nil, nil, nil, nil, nil, stubMemoryStore{memory: &domain.RepoMemory{
		StableFacts: []domain.WorkspaceFact{{ID: "fact-1", Summary: "Keep README updated."}},
	}})
	m.width = 140
	m.height = 30
	m.statusViewport.SetWidth(58)
	m.activePanel = sidePanelMemory
	m.memory.loading = false
	m.memory.data = &domain.RepoMemory{
		StableFacts: []domain.WorkspaceFact{{ID: "fact-1", Summary: "Keep README updated."}},
	}
	_ = m.renderStatus()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.invalidatePanel(sidePanelMemory)
		_ = m.renderStatus()
	}
}
