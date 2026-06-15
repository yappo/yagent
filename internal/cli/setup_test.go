package cli

import (
	"strings"
	"testing"

	setupusecase "yagent/internal/usecase/setup"
)

func TestRootCommandIncludesSetup(t *testing.T) {
	root := NewRootCommand()
	command, _, err := root.Find([]string{"setup"})
	if err != nil {
		t.Fatalf("Find returned error: %v", err)
	}
	if command == nil || command.Use != "setup" {
		t.Fatalf("expected setup command, got %+v", command)
	}
}

func TestRenderSetupResult(t *testing.T) {
	rendered := renderSetupResult(setupusecase.Result{
		Files: []setupusecase.FileResult{{
			Kind:   "config",
			Status: "created",
			Path:   "/tmp/repo/.yagent/config.toml",
			Bytes:  42,
		}},
	})
	for _, want := range []string{
		"yagent setup",
		"config: created /tmp/repo/.yagent/config.toml (42 bytes)",
		"yagent doctor --runtime --probe-structured",
		"yagent benchmark --list-cases",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in setup output, got %q", want, rendered)
		}
	}
}
