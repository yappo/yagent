package tui

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"yagent/internal/domain"
)

func Run(runner domain.Orchestrator, approver *ApproverBridge, workingDir string, defaultModel string, tools domain.ToolExecutor, tasks domain.TaskCatalog, mcpBindings domain.MCPConnectionManager, agents domain.AgentCatalog, runStore domain.RunStateStore, memoryStore domain.RepoMemoryStore) error {
	m := newModelWithStores(runner, workingDir, defaultModel, tools, tasks, mcpBindings, agents, runStore, memoryStore)
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
