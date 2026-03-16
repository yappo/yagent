package tui

import (
	"context"
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

type recordingOrchestrator struct {
	last domain.TurnRequest
}

func (r *recordingOrchestrator) RunTurn(_ context.Context, request domain.TurnRequest) (domain.TurnResult, error) {
	r.last = request
	return domain.TurnResult{Message: domain.Message{Role: domain.RoleAssistant, Content: "ok"}}, nil
}

func newTestModel(t *testing.T) model {
	t.Helper()
	return newModel(stubOrchestrator{}, t.TempDir(), "")
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
	if len(m.output) == 0 || !strings.Contains(m.output[len(m.output)-1], "manager [main]") {
		t.Fatalf("expected requester in output, got %+v", m.output)
	}
}

func TestChatMessageErrorUsesExecutionLabel(t *testing.T) {
	m := newTestModel(t)

	modelValue, _ := m.Update(chatMessage{err: context.DeadlineExceeded})
	next := modelValue.(model)
	if len(next.output) == 0 || !strings.Contains(next.output[0], "実行エラー:") {
		t.Fatalf("expected execution error label, got %+v", next.output)
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
	if next.activeTool == nil {
		t.Fatalf("active tool was not set")
	}
	card := next.renderToolCard()
	if !strings.Contains(card, "fs_read") || !strings.Contains(card, "/tmp/a.txt") || !strings.Contains(card, "limit_bytes=100") {
		t.Fatalf("unexpected tool card: %q", card)
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
	m.output = appendOutputBlock(nil, assistantOutputLabel, strings.Repeat("a\n", 20))
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
	m.output = appendOutputBlock(nil, assistantOutputLabel, "aaaaaaaaaaaa")

	rendered := m.renderLog()
	if !strings.Contains(rendered, "aaaaaaaaaa\naa") {
		t.Fatalf("expected wrapped content, got %q", rendered)
	}
}

func TestCommandCandidatesForSlash(t *testing.T) {
	m := newTestModel(t)
	m.textarea.SetValue("/")

	candidates := m.activeSlashCompletion().candidates
	if len(candidates) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(candidates))
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
	m.output = appendOutputBlock(nil, assistantOutputLabel, "hello")
	m.applyStatusEvent(domain.ExecutionEvent{
		RunID:     "run-1",
		AgentID:   "manager",
		Type:      "agent_started",
		Timestamp: time.Now(),
	})
	m.syncLayout()
	m.logDirty = false
	m.statusDirty = false

	modelValue, _ := m.Update(tea.KeyPressMsg{Text: "a"})
	next := modelValue.(model)

	if next.logDirty {
		t.Fatal("expected typing not to dirty log viewport")
	}
	if next.statusDirty {
		t.Fatal("expected typing not to dirty status viewport")
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

func TestViewShowsCommandCandidates(t *testing.T) {
	m := newTestModel(t)
	m.width = 80
	m.height = 24
	m.textarea.SetValue("/he")
	m.syncLayout()

	view := m.View().Content
	if !strings.Contains(view, "候補: Tab で補完") {
		t.Fatalf("expected command hint in view, got %q", view)
	}
	if !strings.Contains(view, "/help") {
		t.Fatalf("expected /help candidate in view, got %q", view)
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
	m.output = appendOutputBlock(nil, assistantOutputLabel, "hello")
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
	m := newModel(stubOrchestrator{}, dir, "")
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
	m := newModel(stubOrchestrator{}, dir, "")
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
	m := newModel(stubOrchestrator{}, dir, "")
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
	m := newModel(stubOrchestrator{}, dir, "")
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
	m := newModel(stubOrchestrator{}, dir, "")
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
	m := newModel(stubOrchestrator{}, dir, "")
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
	m := newModel(stubOrchestrator{}, dir, "")
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
	m := newModel(stubOrchestrator{}, dir, "")
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

	m := newModel(stubOrchestrator{}, dir, "")
	m.selectedRefs["@main.go"] = "main.go"
	modelValue, _ := submitPrompt(m, "@main.go の概要を見せて")
	next := modelValue.(model)

	if len(next.messages) != 1 || next.messages[0].Content != "main.go の概要を見せて" {
		t.Fatalf("unexpected stored message: %+v", next.messages)
	}
	if !strings.Contains(strings.Join(next.output, "\n"), "@main.go の概要を見せて") {
		t.Fatalf("original prompt was not kept in output: %+v", next.output)
	}
}

func TestSubmitPromptKeepsManualAtReferenceUnchanged(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	m := newModel(stubOrchestrator{}, dir, "")
	modelValue, _ := submitPrompt(m, "@main.go の概要を見せて")
	next := modelValue.(model)

	if len(next.messages) != 1 || next.messages[0].Content != "@main.go の概要を見せて" {
		t.Fatalf("unexpected stored message: %+v", next.messages)
	}
}

func TestSubmitPromptPassesDefaultModelToTurnRequest(t *testing.T) {
	runner := &recordingOrchestrator{}
	m := newModel(runner, t.TempDir(), "gpt-5")

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
	if runner.last.Model != "gpt-5" {
		t.Fatalf("expected default model to be passed, got %q", runner.last.Model)
	}
}
