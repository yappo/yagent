package tui

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	chatusecase "yagent/internal/usecase/chat"
)

func Run(runner *chatusecase.Service, approver *ApproverBridge, workingDir string) error {
	m := newModel(runner, workingDir)
	program := tea.NewProgram(m, tea.WithAltScreen())
	approver.Attach(program)
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "エラー: %v\n", err)
		return err
	}
	return nil
}
