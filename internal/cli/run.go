package cli

import (
	"yagent/internal/app"
	"yagent/internal/tui"
)

func runTUI(configPath string, logPath string) error {
	bridge := tui.NewApproverBridge()
	container, err := app.Build(configPath, bridge, app.BuildOptions{LogPath: logPath})
	if err != nil {
		return err
	}
	if container.Closer != nil {
		defer container.Closer.Close()
	}

	return tui.Run(
		container.Orchestrator,
		bridge,
		container.WorkingDir,
		container.DefaultModel,
		container.Tools,
		container.TaskCatalog,
		container.MCPBindings,
		container.AgentCatalog,
		container.RunStore,
		container.MemoryStore,
	)
}
