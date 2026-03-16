package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"yagent/internal/domain"
)

type toolEventMsg struct {
	event domain.ToolEvent
}

type ToolObserverBridge struct {
	program *tea.Program
}

func NewToolObserverBridge() *ToolObserverBridge {
	return &ToolObserverBridge{}
}

func (b *ToolObserverBridge) Attach(program *tea.Program) {
	b.program = program
}

func (b *ToolObserverBridge) OnToolEvent(_ context.Context, event domain.ToolEvent) {
	if b.program != nil {
		b.program.Send(toolEventMsg{event: event})
	}
}
