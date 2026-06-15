package logging

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"

	"yagent/internal/domain"
)

type Logger struct {
	mu   sync.Mutex
	file *os.File
}

type Record struct {
	Time   time.Time      `json:"time"`
	Type   string         `json:"type"`
	Fields map[string]any `json:"fields,omitempty"`
}

func NewFileLogger(path string) (*Logger, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &Logger{file: file}, nil
}

func (l *Logger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Close()
}

func (l *Logger) Append(_ context.Context, event domain.ExecutionEvent) error {
	if l == nil {
		return nil
	}
	fields := map[string]any{
		"run_id":        event.RunID,
		"parent_run_id": event.ParentRunID,
		"agent_id":      event.AgentID,
		"phase":         event.Phase,
		"attempt":       event.Attempt,
		"status":        event.Status,
		"detail":        event.Detail,
		"display":       event.Display,
		"artifact_ref":  event.ArtifactRef,
		"metrics":       event.Metrics,
		"context_count": event.ContextCount,
	}
	return l.Write("execution_event."+event.Type, fields)
}

func (l *Logger) Write(typ string, fields map[string]any) error {
	if l == nil || l.file == nil {
		return nil
	}
	record := Record{
		Time:   time.Now(),
		Type:   typ,
		Fields: fields,
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.file.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func (l *Logger) WriteRecord(_ context.Context, typ string, fields map[string]any) error {
	return l.Write(typ, fields)
}
