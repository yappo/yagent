package tui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"yagent/internal/domain"
)

type permissionRequestMsg struct {
	request  domain.PermissionRequest
	response chan domain.PermissionDecision
}

type toolEventMsg struct {
	event domain.ToolEvent
}

type RuntimeBridge struct {
	program *tea.Program
}

var _ domain.Approver = (*RuntimeBridge)(nil)
var _ domain.ToolObserver = (*RuntimeBridge)(nil)

func NewRuntimeBridge() *RuntimeBridge {
	return &RuntimeBridge{}
}

func (b *RuntimeBridge) Attach(program *tea.Program) {
	b.program = program
}

func (b *RuntimeBridge) Approve(ctx context.Context, request domain.PermissionRequest) (domain.PermissionDecision, error) {
	if b.program == nil {
		return domain.PermissionDeny, fmt.Errorf("permission bridge is not attached")
	}

	response := make(chan domain.PermissionDecision, 1)
	b.program.Send(permissionRequestMsg{
		request:  request,
		response: response,
	})

	select {
	case <-ctx.Done():
		return domain.PermissionDeny, ctx.Err()
	case decision := <-response:
		return decision, nil
	}
}

func (b *RuntimeBridge) OnToolEvent(_ context.Context, event domain.ToolEvent) {
	if b.program != nil {
		b.program.Send(toolEventMsg{event: event})
	}
}
