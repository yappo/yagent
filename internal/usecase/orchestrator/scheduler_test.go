package orchestrator

import (
	"testing"

	"yagent/internal/domain"
)

func TestRuntimeSchedulerParallelizesIndependentReads(t *testing.T) {
	scheduler := newRuntimeScheduler(4)
	items := []scheduleSpec{
		{ID: "a", ReadSet: []string{"README.md"}, Source: "fs", SourceLimit: 4},
		{ID: "b", ReadSet: []string{"internal/app"}, Source: "search", SourceLimit: 4},
	}

	batch := scheduler.nextBatch(items, map[string]bool{})
	if len(batch) != 2 {
		t.Fatalf("expected both read-only items in one batch, got %+v", batch)
	}
}

func TestRuntimeSchedulerSerializesWriteConflicts(t *testing.T) {
	scheduler := newRuntimeScheduler(4)
	items := []scheduleSpec{
		{
			ID:              "read",
			ReadSet:         []string{"internal/app/bootstrap.go"},
			Source:          "fs",
			SourceLimit:     4,
			SideEffectClass: domain.SideEffectNone,
		},
		{
			ID:              "write",
			WriteSet:        []string{"internal/app/bootstrap.go"},
			Source:          "patch",
			SourceLimit:     1,
			SideEffectClass: domain.SideEffectWorkspace,
		},
	}

	batch := scheduler.nextBatch(items, map[string]bool{})
	if len(batch) != 1 {
		t.Fatalf("expected conflicting read/write items to be split, got %+v", batch)
	}
}

func TestRuntimeSchedulerParallelizesScopedProcessAndExternalSideEffects(t *testing.T) {
	scheduler := newRuntimeScheduler(4)
	items := []scheduleSpec{
		{
			ID:              "task-go-test",
			ReadSet:         []string{"go.mod"},
			WriteSet:        []string{".yagent/task/go-test"},
			Source:          "task:go:test",
			SourceLimit:     1,
			SideEffectClass: domain.SideEffectProcess,
		},
		{
			ID:              "mcp-docs-search",
			ReadSet:         []string{"/docs"},
			Source:          "mcp:docs",
			SourceLimit:     2,
			SideEffectClass: domain.SideEffectExternal,
		},
		{
			ID:              "mcp-repo-search",
			ReadSet:         []string{"/repo"},
			Source:          "mcp:docs",
			SourceLimit:     2,
			SideEffectClass: domain.SideEffectExternal,
		},
	}

	batch := scheduler.nextBatch(items, map[string]bool{})
	if len(batch) != 3 {
		t.Fatalf("expected scoped process/external side effects to parallelize, got %+v", batch)
	}
}

func TestRuntimeSchedulerSerializesUnscopedProcessSideEffects(t *testing.T) {
	scheduler := newRuntimeScheduler(4)
	items := []scheduleSpec{
		{
			ID:              "task-unknown-a",
			Source:          "task:a",
			SourceLimit:     1,
			SideEffectClass: domain.SideEffectProcess,
		},
		{
			ID:              "task-unknown-b",
			Source:          "task:b",
			SourceLimit:     1,
			SideEffectClass: domain.SideEffectProcess,
		},
	}

	batch := scheduler.nextBatch(items, map[string]bool{})
	if len(batch) != 1 {
		t.Fatalf("expected unscoped process side effects to stay serial, got %+v", batch)
	}
}

func TestRuntimeSchedulerRespectsSourceLimitForScopedExternalSideEffects(t *testing.T) {
	scheduler := newRuntimeScheduler(4)
	items := []scheduleSpec{
		{
			ID:              "mcp-write-a",
			WriteSet:        []string{"mcp:docs:a"},
			Source:          "mcp:docs",
			SourceLimit:     1,
			SideEffectClass: domain.SideEffectExternal,
		},
		{
			ID:              "mcp-write-b",
			WriteSet:        []string{"mcp:docs:b"},
			Source:          "mcp:docs",
			SourceLimit:     1,
			SideEffectClass: domain.SideEffectExternal,
		},
	}

	batch := scheduler.nextBatch(items, map[string]bool{})
	if len(batch) != 1 {
		t.Fatalf("expected source limit to cap scoped external side effects, got %+v", batch)
	}
}

func TestRuntimeSchedulerRespectsDependenciesAndDuplicateKeys(t *testing.T) {
	scheduler := newRuntimeScheduler(4)
	items := []scheduleSpec{
		{ID: "prep", Source: "researcher", SourceLimit: 1},
		{ID: "dup-1", DuplicateKey: "same", Source: "fs", SourceLimit: 4},
		{ID: "dup-2", DuplicateKey: "same", Source: "fs", SourceLimit: 4},
		{ID: "primary", DependsOn: []string{"prep"}, Source: "coder", SourceLimit: 1},
	}

	batch := scheduler.nextBatch(items, map[string]bool{})
	if len(batch) != 2 {
		t.Fatalf("expected ready non-duplicate work only, got %+v", batch)
	}

	completed := map[string]bool{"prep": true, "dup-1": true, "dup-2": true}
	batch = scheduler.nextBatch(items, completed)
	if len(batch) != 1 || items[batch[0]].ID != "primary" {
		t.Fatalf("expected dependent primary task next, got %+v", batch)
	}
}
