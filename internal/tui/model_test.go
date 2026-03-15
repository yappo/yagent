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
