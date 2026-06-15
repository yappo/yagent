package tui

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"yagent/internal/domain"
)

func Run(runner domain.Orchestrator, bridge *RuntimeBridge, workingDir string, defaultModel string, tools domain.ToolExecutor, tasks domain.TaskCatalog, mcpBindings domain.MCPConnectionManager, agents domain.AgentCatalog, runStore domain.RunStateStore, memoryStore domain.RepoMemoryStore, routingProfiles []string) error {
	m := newModelWithStoresAndProfiles(runner, workingDir, defaultModel, tools, tasks, mcpBindings, agents, runStore, memoryStore, routingProfiles)
	program := tea.NewProgram(m)
	bridge.Attach(program)

	if observable, ok := runner.(domain.ObservableOrchestrator); ok {
		observable.SetObserver(bridge)
	}

	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "エラー: %v\n", err)
		return err
	}
	return nil
}
