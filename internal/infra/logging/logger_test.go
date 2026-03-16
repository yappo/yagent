package logging

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yagent/internal/domain"
)

func TestLoggerWritesExecutionEvent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "yagent.log")

	logger, err := NewFileLogger(path)
	if err != nil {
		t.Fatalf("NewFileLogger returned error: %v", err)
	}
	defer logger.Close()

	if err := logger.Append(context.Background(), domain.ExecutionEvent{
		RunID:        "run-1",
		AgentID:      "manager",
		Type:         "agent_started",
		Detail:       "task",
		ContextCount: 3,
	}); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"type":"execution_event.agent_started"`) {
		t.Fatalf("unexpected log output: %s", text)
	}
	if !strings.Contains(text, `"context_count":3`) {
		t.Fatalf("context count not logged: %s", text)
	}
}
