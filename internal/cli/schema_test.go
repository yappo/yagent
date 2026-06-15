package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRootCommandIncludesSchema(t *testing.T) {
	root := NewRootCommand()
	command, _, err := root.Find([]string{"schema"})
	if err != nil {
		t.Fatalf("Find returned error: %v", err)
	}
	if command == nil || command.Use != "schema" {
		t.Fatalf("expected schema command, got %+v", command)
	}
}

func TestSchemaCommandPrintsTaskSchema(t *testing.T) {
	command := newSchemaCommand()
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetArgs([]string{"tasks"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("schema output is not JSON: %v\n%s", err, out.String())
	}
	if decoded["title"] != "yagent task catalog" || !strings.Contains(out.String(), `"mcpservers"`) {
		t.Fatalf("unexpected task schema output: %s", out.String())
	}
}

func TestSchemaCommandWritesAgentSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.schema.json")
	command := newSchemaCommand()
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetArgs([]string{"agent", "--out", path})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("schema file was not written: %v", err)
	}
	if !strings.Contains(string(data), `"yagent agent DSL"`) || !strings.Contains(out.String(), "wrote ") {
		t.Fatalf("unexpected schema command result output=%q data=%s", out.String(), string(data))
	}
}
