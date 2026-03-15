package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	chatusecase "yagent/internal/usecase/chat"
)

type toolEventMsg struct {
	event chatusecase.ToolEvent
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

func (b *ToolObserverBridge) OnToolEvent(_ context.Context, event chatusecase.ToolEvent) {
	if b.program != nil {
		b.program.Send(toolEventMsg{event: event})
	}
}
