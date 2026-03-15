package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"yagent/internal/domain"
	chatusecase "yagent/internal/usecase/chat"
)

func TestPermissionRequestState(t *testing.T) {
	m := newModel(chatusecase.NewService(nil, nil, 1))
	response := make(chan domain.PermissionDecision, 1)

	modelValue, _ := m.Update(permissionRequestMsg{
		request: domain.PermissionRequest{
			ToolName:  "file_reader",
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

func TestViewportScrollKey(t *testing.T) {
	m := newModel(chatusecase.NewService(nil, nil, 1))
	m.width = 80
	m.height = 24
	m.output = appendOutputBlock(nil, assistantOutputLabel, strings.Repeat("a\n", 20))
	m.syncLayout()

	modelValue, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	next := modelValue.(model)
	if next.viewport.YOffset < 0 {
		t.Fatalf("viewport offset should not be negative")
	}
}

func TestRenderLogWrapsLongLines(t *testing.T) {
	m := newModel(chatusecase.NewService(nil, nil, 1))
	m.viewport.Width = 10
	m.output = appendOutputBlock(nil, assistantOutputLabel, "aaaaaaaaaaaa")

	rendered := m.renderLog()
	if !strings.Contains(rendered, "aaaaaaaaaa\naa") {
		t.Fatalf("expected wrapped content, got %q", rendered)
	}
}

func TestCommandCandidatesForSlash(t *testing.T) {
	m := newModel(chatusecase.NewService(nil, nil, 1))
	m.textarea.SetValue("/")

	candidates := m.commandCandidates()
	if len(candidates) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(candidates))
	}
}

func TestCommandCandidatesFiltered(t *testing.T) {
	m := newModel(chatusecase.NewService(nil, nil, 1))
	m.textarea.SetValue("/cl")

	candidates := m.commandCandidates()
	if len(candidates) != 1 || candidates[0].name != "/clear" {
		t.Fatalf("unexpected candidates: %+v", candidates)
	}
}

func TestTabCompletesFirstCommandCandidate(t *testing.T) {
	m := newModel(chatusecase.NewService(nil, nil, 1))
	m.textarea.SetValue("/he")

	modelValue, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	next := modelValue.(model)
	if next.textarea.Value() != "/help" {
		t.Fatalf("expected /help, got %q", next.textarea.Value())
	}
}

func TestTabDoesNothingWithoutCandidate(t *testing.T) {
	m := newModel(chatusecase.NewService(nil, nil, 1))
	m.textarea.SetValue("/x")

	modelValue, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	next := modelValue.(model)
	if next.textarea.Value() != "/x" {
		t.Fatalf("textarea value changed unexpectedly: %q", next.textarea.Value())
	}
}

func TestPermissionTabDoesNotTriggerCommandCompletion(t *testing.T) {
	m := newModel(chatusecase.NewService(nil, nil, 1))
	m.textarea.SetValue("/he")
	response := make(chan domain.PermissionDecision, 1)
	m.permission = &permissionState{
		request: domain.PermissionRequest{
			ToolName:  "file_reader",
			Operation: "ファイル読み取り",
			Resource:  "/tmp/a.txt",
		},
		response:      response,
		selectedIndex: 0,
	}

	modelValue, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	next := modelValue.(model)
	if next.textarea.Value() != "/he" {
		t.Fatalf("permission tab should not complete command: %q", next.textarea.Value())
	}
}

func TestViewShowsCommandCandidates(t *testing.T) {
	m := newModel(chatusecase.NewService(nil, nil, 1))
	m.width = 80
	m.height = 24
	m.textarea.SetValue("/he")
	m.syncLayout()

	view := m.View()
	if !strings.Contains(view, "候補コマンド: Tab で補完") {
		t.Fatalf("expected command hint in view, got %q", view)
	}
	if !strings.Contains(view, "/help") {
		t.Fatalf("expected /help candidate in view, got %q", view)
	}
}
