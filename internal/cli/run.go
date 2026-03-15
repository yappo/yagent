package cli

import (
	"yagent/internal/app"
	"yagent/internal/tui"
)

func runTUI(configPath string) error {
	bridge := tui.NewApproverBridge()
	container, err := app.Build(configPath, bridge)
	if err != nil {
		return err
	}

	return tui.Run(container.ChatService, bridge, container.WorkingDir)
}
