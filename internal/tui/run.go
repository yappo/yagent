package tui

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	chatusecase "yagent/internal/usecase/chat"
)

func Run(runner *chatusecase.Service, approver *ApproverBridge, workingDir string) error {
	m := newModel(runner, workingDir)
	program := tea.NewProgram(m)
	approver.Attach(program)
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "エラー: %v\n", err)
		return err
	}
	return nil
}
