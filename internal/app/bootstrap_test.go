package app

import (
	"context"
	"strings"
	"testing"

	"yagent/internal/config"
	"yagent/internal/domain"
)

func TestIsolatedWorkspaceDisablesProcessLaunchingTools(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Memory.StateDir = "state"
	container, err := BuildFromConfig(cfg, nil, BuildOptions{WorkingDir: root, IsolatedWorkspace: true})
	if err != nil {
		t.Fatalf("BuildFromConfig() error = %v", err)
	}
	if container.Closer != nil {
		defer container.Closer.Close()
	}

	for _, name := range []string{"task_run", "task_bind"} {
		result := container.Tools.Execute(context.Background(), domain.AgentSpec{}, domain.ToolCall{ID: name + "-1", Name: name, Arguments: map[string]any{"task_id": "irrelevant"}})
		if result.Success || !strings.Contains(result.Output, "process_execution_disabled") {
			t.Fatalf("%s result = %+v, want process_execution_disabled", name, result)
		}
	}
}
