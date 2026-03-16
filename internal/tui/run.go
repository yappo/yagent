package tui

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"yagent/internal/domain"
)

func Run(runner domain.Orchestrator, approver *ApproverBridge, workingDir string, defaultModel string) error {
	m := newModel(runner, workingDir, defaultModel)
	program := tea.NewProgram(m)
	approver.Attach(program)

	if observable, ok := runner.(domain.ObservableOrchestrator); ok {
		observer := NewToolObserverBridge()
		observer.Attach(program)
		observable.SetObserver(observer)
	}

	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "エラー: %v\n", err)
		return err
	}
	return nil
}
