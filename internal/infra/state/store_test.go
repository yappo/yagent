package state

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yagent/internal/domain"
)

func TestFileStoreCreatesNewLayoutAndLoadsLatestSession(t *testing.T) {
	root := t.TempDir()
	store, err := NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore returned error: %v", err)
	}

	run := &domain.RunState{
		ID:           "run-123",
		RootRunID:    "run-123",
		CurrentPhase: domain.RunPhaseExecute,
		Status:       domain.RunStatusRunning,
		Artifacts: []domain.RunArtifact{{
			ID:    "artifact-1",
			Name:  "Execution plan",
			Kind:  "plan",
			Text:  "{}",
			Phase: domain.RunPhasePlan,
		}},
	}
	if err := store.SaveRun(context.Background(), run); err != nil {
		t.Fatalf("SaveRun returned error: %v", err)
	}

	for _, path := range []string{
		filepath.Join(root, "sessions", "run-123.json"),
		filepath.Join(root, "artifacts", "artifact-1.json"),
		filepath.Join(root, "conversations"),
		filepath.Join(root, "latest_session"),
		filepath.Join(root, "workspace", workspaceFactsFile),
	} {
		if _, err := os.Stat(path); err == nil {
			continue
		} else if filepath.Base(path) == workspaceFactsFile {
			continue
		} else {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
	}

	latest, err := store.LoadLatestRun(context.Background())
	if err != nil {
		t.Fatalf("LoadLatestRun returned error: %v", err)
	}
	if latest == nil || latest.ID != run.ID {
		t.Fatalf("unexpected latest session: %+v", latest)
	}
}

func TestFileStoreSavesAndListsConversationTurns(t *testing.T) {
	root := t.TempDir()
	store, err := NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore returned error: %v", err)
	}

	first := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	second := first.Add(time.Minute)
	if err := store.SaveConversationTurn(context.Background(), domain.ConversationTurnRecord{
		ID:              "turn-1",
		RunID:           "run-1",
		Status:          domain.RunStatusCompleted,
		RequestMessages: []domain.Message{{Role: domain.RoleUser, Content: "first"}},
		OutputMessage:   domain.Message{Role: domain.RoleAssistant, Content: "done"},
		StartedAt:       first,
		CompletedAt:     first,
	}); err != nil {
		t.Fatalf("SaveConversationTurn returned error: %v", err)
	}
	if err := store.SaveConversationTurn(context.Background(), domain.ConversationTurnRecord{
		ID:          "turn-2",
		RunID:       "run-2",
		Status:      domain.RunStatusFailed,
		Error:       "failed",
		StartedAt:   second,
		CompletedAt: second,
	}); err != nil {
		t.Fatalf("SaveConversationTurn returned error: %v", err)
	}

	items, err := store.ListConversationTurns(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListConversationTurns returned error: %v", err)
	}
	if len(items) != 2 || items[0].ID != "turn-2" || items[1].ID != "turn-1" {
		t.Fatalf("expected reverse chronological conversation turns, got %+v", items)
	}
	if items[1].RequestMessages[0].Content != "first" || items[1].OutputMessage.Content != "done" {
		t.Fatalf("unexpected saved conversation messages: %+v", items[1])
	}
}

func TestFileStoreValidatesTypedArtifactPayloads(t *testing.T) {
	root := t.TempDir()
	store, err := NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore returned error: %v", err)
	}

	invalid := domain.RunArtifact{
		ID:            "artifact-invalid",
		Name:          "Final response",
		Kind:          "final_response",
		SchemaVersion: "final_response.v1",
		Payload:       json.RawMessage(`{"summary":"missing response"}`),
	}
	err = store.SaveArtifact(context.Background(), invalid)
	if err == nil || !strings.Contains(err.Error(), "payload.response") {
		t.Fatalf("expected invalid artifact error, got %v", err)
	}

	run := &domain.RunState{
		ID:        "run-invalid",
		RootRunID: "run-invalid",
		Artifacts: []domain.RunArtifact{{
			ID:            "artifact-invalid-run",
			Name:          "Tool output",
			Kind:          "tool_output",
			SchemaVersion: "tool_output.v1",
			Payload:       json.RawMessage(`{"success":true}`),
		}},
	}
	err = store.SaveRun(context.Background(), run)
	if err == nil || !strings.Contains(err.Error(), "payload.tool_name") {
		t.Fatalf("expected invalid run artifact error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "sessions", "run-invalid.json")); !os.IsNotExist(statErr) {
		t.Fatalf("expected invalid run not to be written, stat err=%v", statErr)
	}
}

func TestFileStoreFindsReusableExecutionAndInvalidatesOnMutation(t *testing.T) {
	root := t.TempDir()
	store, err := NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore returned error: %v", err)
	}

	target := filepath.Join(root, "note.txt")
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Stat returned error: %v", err)
	}
	state := domain.WorkspacePathState{
		Path:        target,
		Exists:      true,
		IsDir:       false,
		Size:        info.Size(),
		ModTimeUnix: info.ModTime().UnixNano(),
		ObservedAt:  time.Now(),
	}
	if err := store.SaveWorkspaceSnapshot(context.Background(), &domain.WorkspaceSnapshot{
		Revision:  1,
		Paths:     map[string]domain.WorkspacePathState{target: state},
		UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("SaveWorkspaceSnapshot returned error: %v", err)
	}
	if err := store.SaveObservation(context.Background(), domain.ObservationRecord{
		ID:               "obs-1",
		SessionID:        "run-1",
		ToolName:         "fs_read",
		SemanticKey:      "fs_read:key",
		Summary:          "hello",
		OutputArtifactID: "artifact-1",
		ReadSet:          []string{target},
		PathStates:       []domain.WorkspacePathState{state},
		SnapshotRevision: 1,
		Reusable:         true,
	}); err != nil {
		t.Fatalf("SaveObservation returned error: %v", err)
	}
	if err := store.SaveExecution(context.Background(), domain.ToolExecutionRecord{
		ID:                "exec-1",
		SessionID:         "run-1",
		ToolName:          "fs_read",
		ToolClass:         domain.ToolClassObserve,
		NormalizedArgs:    `{"path":"` + target + `"}`,
		SemanticKey:       "fs_read:key",
		WorkspaceRevision: 1,
		ReadSet:           []string{target},
		PathStates:        []domain.WorkspacePathState{state},
		OutputArtifactID:  "artifact-1",
		Success:           true,
		Output:            "hello",
		Reusable:          true,
		Source:            "fs",
	}); err != nil {
		t.Fatalf("SaveExecution returned error: %v", err)
	}

	record, err := store.FindReusableExecution(context.Background(), "fs_read:key", []string{target})
	if err != nil {
		t.Fatalf("FindReusableExecution returned error: %v", err)
	}
	if record == nil || record.ID != "exec-1" {
		t.Fatalf("expected reusable execution, got %+v", record)
	}

	if err := store.MarkStaleByPaths(context.Background(), []string{target}); err != nil {
		t.Fatalf("MarkStaleByPaths returned error: %v", err)
	}
	record, err = store.FindReusableExecution(context.Background(), "fs_read:key", []string{target})
	if err != nil {
		t.Fatalf("FindReusableExecution after stale returned error: %v", err)
	}
	if record != nil {
		t.Fatalf("expected stale execution to be hidden, got %+v", record)
	}

	memory, err := store.LoadMemory(context.Background())
	if err != nil {
		t.Fatalf("LoadMemory returned error: %v", err)
	}
	if len(memory.ReusableObservations) != 0 {
		t.Fatalf("expected stale observation to be removed from memory, got %+v", memory.ReusableObservations)
	}
}

func TestFileStoreSavesAndListsScratchRecords(t *testing.T) {
	root := t.TempDir()
	store, err := NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore returned error: %v", err)
	}

	now := time.Now()
	if err := store.SaveScratch(context.Background(), domain.ScratchRecord{
		ID:        "scratch-1",
		Kind:      "agent_packet",
		SessionID: "run-1",
		Summary:   "coder packet",
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("SaveScratch returned error: %v", err)
	}
	if err := store.SaveScratch(context.Background(), domain.ScratchRecord{
		ID:        "scratch-2",
		Kind:      "packet_digest",
		SessionID: "run-1",
		Summary:   "digest",
		CreatedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("SaveScratch returned error: %v", err)
	}

	items, err := store.ListScratch(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListScratch returned error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected two scratch records, got %+v", items)
	}
	if items[0].ID != "scratch-2" || items[1].ID != "scratch-1" {
		t.Fatalf("expected reverse chronological scratch ordering, got %+v", items)
	}
}

func TestFileStoreStoresObservationReadSetInMemorySummary(t *testing.T) {
	root := t.TempDir()
	store, err := NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore returned error: %v", err)
	}

	if err := store.SaveObservation(context.Background(), domain.ObservationRecord{
		ID:         "obs-1",
		ToolName:   "fs_read",
		Summary:    "README contents",
		ReadSet:    []string{"README.md"},
		Reusable:   true,
		PathStates: []domain.WorkspacePathState{{Path: "README.md", Exists: true}},
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}); err != nil {
		t.Fatalf("SaveObservation returned error: %v", err)
	}

	memory, err := store.LoadMemory(context.Background())
	if err != nil {
		t.Fatalf("LoadMemory returned error: %v", err)
	}
	if len(memory.ReusableObservations) != 1 || len(memory.ReusableObservations[0].ReadSet) != 1 || memory.ReusableObservations[0].ReadSet[0] != "README.md" {
		t.Fatalf("expected observation read set in memory summary, got %+v", memory.ReusableObservations)
	}
	if memory.ReusableObservations[0].IntegritySHA256 == "" {
		t.Fatalf("expected observation integrity hash in memory summary, got %+v", memory.ReusableObservations[0])
	}
	records, err := store.ListObservations(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListObservations returned error: %v", err)
	}
	if len(records) != 1 || records[0].IntegritySHA256 != memory.ReusableObservations[0].IntegritySHA256 {
		t.Fatalf("expected observation record and summary integrity to match, records=%+v memory=%+v", records, memory.ReusableObservations)
	}
}

func TestFileStoreFiltersObservationSummaryIntegrityMismatch(t *testing.T) {
	root := t.TempDir()
	store, err := NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore returned error: %v", err)
	}
	if err := store.SaveObservation(context.Background(), domain.ObservationRecord{
		ID:       "obs-1",
		ToolName: "fs_read",
		Summary:  "README contents",
		ReadSet:  []string{"README.md"},
		Reusable: true,
	}); err != nil {
		t.Fatalf("SaveObservation returned error: %v", err)
	}
	memory, err := store.LoadMemory(context.Background())
	if err != nil {
		t.Fatalf("LoadMemory returned error: %v", err)
	}
	if len(memory.ReusableObservations) != 1 {
		t.Fatalf("expected valid observation summary, got %+v", memory.ReusableObservations)
	}

	memory.ReusableObservations[0].IntegritySHA256 = "tampered"
	if err := store.SaveMemory(context.Background(), memory); err != nil {
		t.Fatalf("SaveMemory returned error: %v", err)
	}
	reloaded, err := store.LoadMemory(context.Background())
	if err != nil {
		t.Fatalf("LoadMemory returned error: %v", err)
	}
	if len(reloaded.ReusableObservations) != 0 {
		t.Fatalf("expected tampered observation summary to be filtered, got %+v", reloaded.ReusableObservations)
	}
}
