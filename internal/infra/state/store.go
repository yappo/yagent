package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"yagent/internal/domain"
)

const memoryFileName = "memory.json"

type FileStore struct {
	root string
	mu   sync.Mutex
}

func NewFileStore(root string) (*FileStore, error) {
	if root == "" {
		return nil, fmt.Errorf("state root が必要です")
	}
	store := &FileStore{root: root}
	if err := os.MkdirAll(store.runsDir(), 0o755); err != nil {
		return nil, fmt.Errorf("state ディレクトリの作成に失敗しました: %w", err)
	}
	return store, nil
}

func (s *FileStore) SaveRun(_ context.Context, run *domain.RunState) error {
	if run == nil || run.ID == "" {
		return fmt.Errorf("run state が不正です")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	run.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.runPath(run.ID), data, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.root, "latest"), []byte(run.ID), 0o644)
}

func (s *FileStore) LoadRun(_ context.Context, id string) (*domain.RunState, error) {
	if id == "" {
		return nil, fmt.Errorf("run id が必要です")
	}
	data, err := os.ReadFile(s.runPath(id))
	if err != nil {
		return nil, err
	}
	var run domain.RunState
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

func (s *FileStore) LoadLatestRun(ctx context.Context) (*domain.RunState, error) {
	data, err := os.ReadFile(filepath.Join(s.root, "latest"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return s.LoadRun(ctx, string(data))
}

func (s *FileStore) LoadMemory(_ context.Context) (*domain.RepoMemory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(filepath.Join(s.root, memoryFileName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &domain.RepoMemory{}, nil
		}
		return nil, err
	}
	var memory domain.RepoMemory
	if err := json.Unmarshal(data, &memory); err != nil {
		return nil, err
	}
	return &memory, nil
}

func (s *FileStore) SaveMemory(_ context.Context, memory *domain.RepoMemory) error {
	if memory == nil {
		memory = &domain.RepoMemory{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	memory.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(memory, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.root, memoryFileName), data, 0o644)
}

func (s *FileStore) RecordCommand(ctx context.Context, entry domain.CommandMemoryEntry) error {
	memory, err := s.LoadMemory(ctx)
	if err != nil {
		return err
	}
	entry.CreatedAt = time.Now()
	memory.SuccessfulCommands = append(memory.SuccessfulCommands, entry)
	if len(memory.SuccessfulCommands) > 50 {
		memory.SuccessfulCommands = memory.SuccessfulCommands[len(memory.SuccessfulCommands)-50:]
	}
	return s.SaveMemory(ctx, memory)
}

func (s *FileStore) ListRuns() ([]string, error) {
	entries, err := os.ReadDir(s.runsDir())
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		ids = append(ids, entry.Name()[:len(entry.Name())-len(".json")])
	}
	sort.Strings(ids)
	return ids, nil
}

func (s *FileStore) runsDir() string {
	return filepath.Join(s.root, "runs")
}

func (s *FileStore) runPath(id string) string {
	return filepath.Join(s.runsDir(), id+".json")
}
