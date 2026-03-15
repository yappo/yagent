package tui

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"yagent/internal/domain"
)

type permissionRequestMsg struct {
	request  domain.PermissionRequest
	response chan domain.PermissionDecision
}

type ApproverBridge struct {
	program *tea.Program
}

func NewApproverBridge() *ApproverBridge {
	return &ApproverBridge{}
}

func (b *ApproverBridge) Attach(program *tea.Program) {
	b.program = program
}

func (b *ApproverBridge) Approve(ctx context.Context, request domain.PermissionRequest) (domain.PermissionDecision, error) {
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
