package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"yagent/internal/domain"
)

const (
	workspaceFactsFile    = "facts.json"
	workspaceSnapshotFile = "snapshot.json"
	latestSessionFile     = "latest_session"
)

type FileStore struct {
	root string
	mu   sync.Mutex
}

func NewFileStore(root string) (*FileStore, error) {
	if root == "" {
		return nil, fmt.Errorf("state root が必要です")
	}
	store := &FileStore{root: root}
	for _, dir := range []string{
		store.sessionsDir(),
		store.workspaceDir(),
		store.observationsDir(),
		store.artifactsDir(),
		store.executionsDir(),
		store.mutationsDir(),
		store.conversationsDir(),
		store.scratchDir(),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("state ディレクトリの作成に失敗しました: %w", err)
		}
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
	for _, artifact := range run.Artifacts {
		if artifact.ID == "" {
			continue
		}
		if err := domain.ValidateArtifactPayload(artifact); err != nil {
			return err
		}
	}
	if err := s.writeJSON(s.sessionPath(run.ID), run); err != nil {
		return err
	}
	for _, artifact := range run.Artifacts {
		if artifact.ID == "" {
			continue
		}
		if err := s.writeJSON(s.artifactPath(artifact.ID), artifact); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(s.root, latestSessionFile), []byte(run.ID), 0o644)
}

func (s *FileStore) LoadRun(_ context.Context, id string) (*domain.RunState, error) {
	if id == "" {
		return nil, fmt.Errorf("run id が必要です")
	}
	var run domain.RunState
	if err := s.readJSON(s.sessionPath(id), &run); err != nil {
		return nil, err
	}
	return &run, nil
}

func (s *FileStore) LoadLatestRun(ctx context.Context) (*domain.RunState, error) {
	data, err := os.ReadFile(filepath.Join(s.root, latestSessionFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return s.LoadRun(ctx, strings.TrimSpace(string(data)))
}

func (s *FileStore) LoadMemory(_ context.Context) (*domain.WorkspaceMemory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var memory domain.WorkspaceMemory
	if err := s.readJSON(filepath.Join(s.workspaceDir(), workspaceFactsFile), &memory); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &domain.WorkspaceMemory{}, nil
		}
		return nil, err
	}
	if len(memory.ReusableObservations) > 0 {
		if observations, err := s.readObservationRecords(); err == nil {
			memory.ReusableObservations = validObservationSummaries(memory.ReusableObservations, observations)
		}
	}
	return &memory, nil
}

func (s *FileStore) SaveMemory(_ context.Context, memory *domain.WorkspaceMemory) error {
	if memory == nil {
		memory = &domain.WorkspaceMemory{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	memory.UpdatedAt = time.Now()
	return s.writeJSON(filepath.Join(s.workspaceDir(), workspaceFactsFile), memory)
}

func (s *FileStore) RecordCommand(ctx context.Context, entry domain.CommandMemoryEntry) error {
	memory, err := s.LoadMemory(ctx)
	if err != nil {
		return err
	}
	entry.CreatedAt = time.Now()
	if entry.Summary != "" {
		memory.StableFacts = appendOrReplaceFact(memory.StableFacts, domain.WorkspaceFact{
			ID:        sanitizeID(entry.Command + "-" + entry.Summary),
			Kind:      "command",
			Summary:   entry.Summary,
			UpdatedAt: entry.CreatedAt,
		})
	}
	return s.SaveMemory(ctx, memory)
}

func (s *FileStore) SaveArtifact(_ context.Context, artifact domain.RunArtifact) error {
	if artifact.ID == "" {
		return fmt.Errorf("artifact id が必要です")
	}
	if err := domain.ValidateArtifactPayload(artifact); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeJSON(s.artifactPath(artifact.ID), artifact)
}

func (s *FileStore) SaveObservation(ctx context.Context, observation domain.ObservationRecord) error {
	if observation.ID == "" {
		return fmt.Errorf("observation id が必要です")
	}
	s.mu.Lock()
	if observation.CreatedAt.IsZero() {
		observation.CreatedAt = time.Now()
	}
	observation.UpdatedAt = time.Now()
	observation.IntegritySHA256 = observationIntegritySHA256(observation)
	if err := s.writeJSON(s.observationPath(observation.ID), observation); err != nil {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()

	if observation.Reusable && !observation.Stale {
		memory, err := s.LoadMemory(ctx)
		if err != nil {
			return err
		}
		memory.ReusableObservations = appendOrReplaceObservation(memory.ReusableObservations, domain.ObservationSummary{
			ObservationID:   observation.ID,
			ToolName:        observation.ToolName,
			Summary:         observation.Summary,
			ReadSet:         append([]string(nil), observation.ReadSet...),
			IntegritySHA256: observation.IntegritySHA256,
			UpdatedAt:       observation.UpdatedAt,
		})
		return s.SaveMemory(ctx, memory)
	}
	return nil
}

func (s *FileStore) ListObservations(_ context.Context, limit int) ([]domain.ObservationRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	items, err := s.readObservationRecords()
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *FileStore) SaveExecution(_ context.Context, execution domain.ToolExecutionRecord) error {
	if execution.ID == "" {
		return fmt.Errorf("execution id が必要です")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if execution.CreatedAt.IsZero() {
		execution.CreatedAt = time.Now()
	}
	execution.UpdatedAt = time.Now()
	return s.writeJSON(s.executionPath(execution.ID), execution)
}

func (s *FileStore) ListExecutions(_ context.Context, limit int) ([]domain.ToolExecutionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	items, err := s.readExecutionRecords()
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *FileStore) FindReusableExecution(_ context.Context, semanticKey string, readSet []string) (*domain.ToolExecutionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	snapshot, err := s.loadWorkspaceSnapshotLocked()
	if err != nil {
		return nil, err
	}
	items, err := s.readExecutionRecords()
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	for _, item := range items {
		if item.SemanticKey != semanticKey || !item.Success || !item.Reusable || item.Stale {
			continue
		}
		if !sameStringSet(item.ReadSet, readSet) {
			continue
		}
		if !pathStatesFresh(snapshot, item.PathStates) {
			continue
		}
		copied := item
		return &copied, nil
	}
	return nil, nil
}

func (s *FileStore) SaveMutation(ctx context.Context, mutation domain.MutationRecord) error {
	if mutation.ID == "" {
		return fmt.Errorf("mutation id が必要です")
	}
	s.mu.Lock()
	if mutation.CreatedAt.IsZero() {
		mutation.CreatedAt = time.Now()
	}
	if err := s.writeJSON(s.mutationPath(mutation.ID), mutation); err != nil {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	return s.MarkStaleByPaths(ctx, mutation.WriteSet)
}

func (s *FileStore) ListMutations(_ context.Context, limit int) ([]domain.MutationRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	items, err := s.readMutationRecords()
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *FileStore) MarkStaleByPaths(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	s.mu.Lock()
	executions, err := s.readExecutionRecords()
	if err != nil {
		s.mu.Unlock()
		return err
	}
	for idx := range executions {
		if !pathsIntersect(executions[idx].ReadSet, paths) {
			continue
		}
		executions[idx].Stale = true
		executions[idx].UpdatedAt = time.Now()
		if err := s.writeJSON(s.executionPath(executions[idx].ID), executions[idx]); err != nil {
			s.mu.Unlock()
			return err
		}
	}
	observations, err := s.readObservationRecords()
	if err != nil {
		s.mu.Unlock()
		return err
	}
	for idx := range observations {
		if !pathsIntersect(observations[idx].ReadSet, paths) {
			continue
		}
		observations[idx].Stale = true
		observations[idx].UpdatedAt = time.Now()
		if err := s.writeJSON(s.observationPath(observations[idx].ID), observations[idx]); err != nil {
			s.mu.Unlock()
			return err
		}
	}
	s.mu.Unlock()

	memory, err := s.LoadMemory(ctx)
	if err != nil {
		return err
	}
	filtered := memory.ReusableObservations[:0]
	for _, item := range memory.ReusableObservations {
		if observationStale(observations, item.ObservationID) {
			continue
		}
		filtered = append(filtered, item)
	}
	memory.ReusableObservations = filtered
	return s.SaveMemory(ctx, memory)
}

func (s *FileStore) LoadWorkspaceSnapshot(_ context.Context) (*domain.WorkspaceSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadWorkspaceSnapshotLocked()
}

func (s *FileStore) SaveWorkspaceSnapshot(_ context.Context, snapshot *domain.WorkspaceSnapshot) error {
	if snapshot == nil {
		snapshot = &domain.WorkspaceSnapshot{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot.UpdatedAt = time.Now()
	if snapshot.Paths == nil {
		snapshot.Paths = map[string]domain.WorkspacePathState{}
	}
	return s.writeJSON(filepath.Join(s.workspaceDir(), workspaceSnapshotFile), snapshot)
}

func (s *FileStore) SaveScratch(_ context.Context, record domain.ScratchRecord) error {
	if record.ID == "" {
		return fmt.Errorf("scratch id が必要です")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now()
	}
	return s.writeJSON(s.scratchPath(record.ID), record)
}

func (s *FileStore) SaveConversationTurn(_ context.Context, record domain.ConversationTurnRecord) error {
	if record.ID == "" {
		return fmt.Errorf("conversation turn id が必要です")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if record.StartedAt.IsZero() {
		record.StartedAt = time.Now()
	}
	if record.CompletedAt.IsZero() {
		record.CompletedAt = time.Now()
	}
	return s.writeJSON(s.conversationPath(record.ID), record)
}

func (s *FileStore) ListConversationTurns(_ context.Context, limit int) ([]domain.ConversationTurnRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.conversationsDir())
	if err != nil {
		return nil, err
	}
	items := make([]domain.ConversationTurnRecord, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var item domain.ConversationTurnRecord
		if err := s.readJSON(filepath.Join(s.conversationsDir(), entry.Name()), &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CompletedAt.After(items[j].CompletedAt)
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *FileStore) ListScratch(_ context.Context, limit int) ([]domain.ScratchRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.scratchDir())
	if err != nil {
		return nil, err
	}
	items := make([]domain.ScratchRecord, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var item domain.ScratchRecord
		if err := s.readJSON(filepath.Join(s.scratchDir(), entry.Name()), &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *FileStore) ListRuns() ([]string, error) {
	entries, err := os.ReadDir(s.sessionsDir())
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		ids = append(ids, strings.TrimSuffix(entry.Name(), ".json"))
	}
	sort.Strings(ids)
	return ids, nil
}

func (s *FileStore) loadWorkspaceSnapshotLocked() (*domain.WorkspaceSnapshot, error) {
	var snapshot domain.WorkspaceSnapshot
	if err := s.readJSON(filepath.Join(s.workspaceDir(), workspaceSnapshotFile), &snapshot); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &domain.WorkspaceSnapshot{Paths: map[string]domain.WorkspacePathState{}}, nil
		}
		return nil, err
	}
	if snapshot.Paths == nil {
		snapshot.Paths = map[string]domain.WorkspacePathState{}
	}
	return &snapshot, nil
}

func (s *FileStore) readObservationRecords() ([]domain.ObservationRecord, error) {
	entries, err := os.ReadDir(s.observationsDir())
	if err != nil {
		return nil, err
	}
	items := make([]domain.ObservationRecord, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var item domain.ObservationRecord
		if err := s.readJSON(filepath.Join(s.observationsDir(), entry.Name()), &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *FileStore) readExecutionRecords() ([]domain.ToolExecutionRecord, error) {
	entries, err := os.ReadDir(s.executionsDir())
	if err != nil {
		return nil, err
	}
	items := make([]domain.ToolExecutionRecord, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var item domain.ToolExecutionRecord
		if err := s.readJSON(filepath.Join(s.executionsDir(), entry.Name()), &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *FileStore) readMutationRecords() ([]domain.MutationRecord, error) {
	entries, err := os.ReadDir(s.mutationsDir())
	if err != nil {
		return nil, err
	}
	items := make([]domain.MutationRecord, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var item domain.MutationRecord
		if err := s.readJSON(filepath.Join(s.mutationsDir(), entry.Name()), &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *FileStore) writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (s *FileStore) readJSON(path string, dest any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dest)
}

func (s *FileStore) sessionsDir() string {
	return filepath.Join(s.root, "sessions")
}

func (s *FileStore) sessionPath(id string) string {
	return filepath.Join(s.sessionsDir(), id+".json")
}

func (s *FileStore) workspaceDir() string {
	return filepath.Join(s.root, "workspace")
}

func (s *FileStore) observationsDir() string {
	return filepath.Join(s.root, "observations")
}

func (s *FileStore) observationPath(id string) string {
	return filepath.Join(s.observationsDir(), id+".json")
}

func (s *FileStore) artifactsDir() string {
	return filepath.Join(s.root, "artifacts")
}

func (s *FileStore) artifactPath(id string) string {
	return filepath.Join(s.artifactsDir(), id+".json")
}

func (s *FileStore) executionsDir() string {
	return filepath.Join(s.root, "executions")
}

func (s *FileStore) executionPath(id string) string {
	return filepath.Join(s.executionsDir(), id+".json")
}

func (s *FileStore) mutationsDir() string {
	return filepath.Join(s.root, "mutations")
}

func (s *FileStore) mutationPath(id string) string {
	return filepath.Join(s.mutationsDir(), id+".json")
}

func (s *FileStore) conversationsDir() string {
	return filepath.Join(s.root, "conversations")
}

func (s *FileStore) conversationPath(id string) string {
	return filepath.Join(s.conversationsDir(), id+".json")
}

func (s *FileStore) scratchDir() string {
	return filepath.Join(s.root, "scratch")
}

func (s *FileStore) scratchPath(id string) string {
	return filepath.Join(s.scratchDir(), id+".json")
}

func appendOrReplaceFact(items []domain.WorkspaceFact, fact domain.WorkspaceFact) []domain.WorkspaceFact {
	for idx := range items {
		if items[idx].ID != fact.ID {
			continue
		}
		items[idx] = fact
		return items
	}
	return append(items, fact)
}

func appendOrReplaceObservation(items []domain.ObservationSummary, summary domain.ObservationSummary) []domain.ObservationSummary {
	for idx := range items {
		if items[idx].ObservationID != summary.ObservationID {
			continue
		}
		items[idx] = summary
		return items
	}
	return append(items, summary)
}

func validObservationSummaries(summaries []domain.ObservationSummary, records []domain.ObservationRecord) []domain.ObservationSummary {
	if len(summaries) == 0 {
		return nil
	}
	recordsByID := make(map[string]domain.ObservationRecord, len(records))
	for _, record := range records {
		recordsByID[record.ID] = record
	}
	out := make([]domain.ObservationSummary, 0, len(summaries))
	for _, summary := range summaries {
		record, ok := recordsByID[summary.ObservationID]
		if !ok || record.Stale || !record.Reusable {
			continue
		}
		recordHash := record.IntegritySHA256
		if recordHash == "" {
			recordHash = observationIntegritySHA256(record)
		}
		if summary.IntegritySHA256 == "" || summary.IntegritySHA256 != recordHash {
			continue
		}
		out = append(out, summary)
	}
	return out
}

func observationIntegritySHA256(record domain.ObservationRecord) string {
	readSet := append([]string(nil), record.ReadSet...)
	sort.Strings(readSet)
	pathStates := append([]domain.WorkspacePathState(nil), record.PathStates...)
	sort.Slice(pathStates, func(i, j int) bool {
		return pathStates[i].Path < pathStates[j].Path
	})
	payload := struct {
		ID               string                      `json:"id"`
		SessionID        string                      `json:"session_id,omitempty"`
		ToolName         string                      `json:"tool_name"`
		SemanticKey      string                      `json:"semantic_key"`
		Summary          string                      `json:"summary,omitempty"`
		OutputArtifactID string                      `json:"output_artifact_id,omitempty"`
		ReadSet          []string                    `json:"read_set,omitempty"`
		PathStates       []domain.WorkspacePathState `json:"path_states,omitempty"`
		SnapshotRevision int64                       `json:"snapshot_revision"`
		Reusable         bool                        `json:"reusable"`
	}{record.ID, record.SessionID, record.ToolName, record.SemanticKey, record.Summary, record.OutputArtifactID, readSet, pathStates, record.SnapshotRevision, record.Reusable}
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func observationStale(items []domain.ObservationRecord, observationID string) bool {
	for _, item := range items {
		if item.ID == observationID {
			return item.Stale
		}
	}
	return false
}

func pathStatesFresh(snapshot *domain.WorkspaceSnapshot, recorded []domain.WorkspacePathState) bool {
	if len(recorded) == 0 {
		return true
	}
	if snapshot == nil {
		return false
	}
	for _, item := range recorded {
		current, ok := snapshot.Paths[item.Path]
		if !ok {
			return false
		}
		if current.Exists != item.Exists || current.IsDir != item.IsDir || current.Size != item.Size || current.ModTimeUnix != item.ModTimeUnix {
			return false
		}
	}
	return true
}

func sameStringSet(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	index := map[string]int{}
	for _, item := range left {
		index[item]++
	}
	for _, item := range right {
		index[item]--
		if index[item] < 0 {
			return false
		}
	}
	for _, value := range index {
		if value != 0 {
			return false
		}
	}
	return true
}

func pathsIntersect(left []string, right []string) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	for _, l := range left {
		for _, r := range right {
			if sameOrNestedPath(l, r) || sameOrNestedPath(r, l) {
				return true
			}
		}
	}
	return false
}

func sameOrNestedPath(base string, candidate string) bool {
	if base == candidate {
		return true
	}
	base = strings.TrimSuffix(base, string(filepath.Separator))
	candidate = strings.TrimSuffix(candidate, string(filepath.Separator))
	if base == "" || candidate == "" {
		return false
	}
	return strings.HasPrefix(candidate, base+string(filepath.Separator))
}

func sanitizeID(value string) string {
	replacer := strings.NewReplacer("/", "-", "\\", "-", " ", "-", ":", "-", "\n", "-")
	return replacer.Replace(value)
}
