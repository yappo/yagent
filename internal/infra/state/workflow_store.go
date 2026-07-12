package state

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"yagent/internal/domain"
)

const (
	durableWorkflowDirectory = "durable-workflows"
	workflowManifestVersion  = 2
	workflowInitializedName  = "INITIALIZED"
)

var errWorkflowStoreCorrupt = errors.New("durable workflow store is corrupt")

var _ domain.DurableWorkflowStore = (*FileStore)(nil)

type workflowManifest struct {
	Version    int                              `json:"version"`
	Generation string                           `json:"generation"`
	Workflows  map[string]workflowManifestIndex `json:"workflows"`
}

type workflowManifestIndex struct {
	Workflow      string            `json:"workflow"`
	WorkUnits     map[string]string `json:"work_units"`
	Actions       map[string]string `json:"actions"`
	ActionOrder   []string          `json:"action_order"`
	Artifacts     map[string]string `json:"artifacts"`
	ArtifactOrder []string          `json:"artifact_order"`
}

// workflowStoreOptions is deliberately package-private. It permits fault
// injection immediately before publication without expanding the production
// DurableWorkflowStore API.
type workflowStoreOptions struct {
	beforeHeadPublication func() error
}

// CommitWorkflowSnapshot atomically publishes every durable record for one
// workflow. Immutable objects that are not reachable from HEAD are never read
// as committed state.
func (s *FileStore) CommitWorkflowSnapshot(ctx context.Context, expectedWorkflowRevision int64, snapshot domain.WorkflowSnapshot) error {
	return s.commitWorkflowSnapshot(ctx, expectedWorkflowRevision, snapshot, workflowStoreOptions{})
}

func (s *FileStore) commitWorkflowSnapshot(ctx context.Context, expectedWorkflowRevision int64, snapshot domain.WorkflowSnapshot, options workflowStoreOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if expectedWorkflowRevision < 0 {
		return fmt.Errorf("expected workflow revision must not be negative")
	}
	if err := s.ensureWorkflowStoreLayout(); err != nil {
		return err
	}

	unlock, err := acquireWorkflowLock(ctx, s.workflowLockPath())
	if err != nil {
		return err
	}
	defer func() { _ = unlock() }()

	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.cleanWorkflowStaging(); err != nil {
		return err
	}

	current, err := s.loadWorkflowManifest()
	if err != nil {
		return err
	}
	actualRevision := int64(0)
	if current != nil {
		if index, exists := current.Workflows[string(snapshot.Workflow.ID)]; exists {
			workflow, err := s.readWorkflowObject(index.Workflow)
			if err != nil {
				return err
			}
			actualRevision = workflow.Revision
		}
	}
	if expectedWorkflowRevision != actualRevision {
		return &domain.WorkflowRevisionConflictError{
			WorkflowID: snapshot.Workflow.ID,
			Expected:   expectedWorkflowRevision,
			Actual:     actualRevision,
		}
	}
	if snapshot.Workflow.Revision != expectedWorkflowRevision+1 {
		return fmt.Errorf("%w: workflow revision=%d, expected next revision=%d", domain.ErrInvalidWorkflow, snapshot.Workflow.Revision, expectedWorkflowRevision+1)
	}
	if err := domain.ValidateWorkflowSnapshot(snapshot); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	generation, err := newWorkflowGenerationID()
	if err != nil {
		return err
	}
	stageDir := filepath.Join(s.workflowStagingDir(), generation)
	stageObjectsDir := filepath.Join(stageDir, "objects")
	if err := os.MkdirAll(stageObjectsDir, 0o755); err != nil {
		return fmt.Errorf("create workflow staging generation: %w", err)
	}

	index := workflowManifestIndex{
		WorkUnits:     make(map[string]string, len(snapshot.WorkUnits)),
		Actions:       make(map[string]string, len(snapshot.Actions)),
		ActionOrder:   make([]string, 0, len(snapshot.Actions)),
		Artifacts:     make(map[string]string, len(snapshot.Artifacts)),
		ArtifactOrder: make([]string, 0, len(snapshot.Artifacts)),
	}
	objectData := make(map[string][]byte, 1+len(snapshot.WorkUnits)+len(snapshot.Actions)+len(snapshot.Artifacts))
	workflowHash, workflowJSON, err := marshalWorkflowObject(snapshot.Workflow)
	if err != nil {
		return err
	}
	index.Workflow = workflowHash
	objectData[workflowHash] = workflowJSON
	for _, unit := range snapshot.WorkUnits {
		hash, data, err := marshalWorkflowObject(unit)
		if err != nil {
			return err
		}
		index.WorkUnits[string(unit.ID)] = hash
		objectData[hash] = data
	}
	for _, action := range snapshot.Actions {
		hash, data, err := marshalWorkflowObject(action)
		if err != nil {
			return err
		}
		index.Actions[string(action.ID)] = hash
		index.ActionOrder = append(index.ActionOrder, string(action.ID))
		objectData[hash] = data
	}
	for _, artifact := range snapshot.Artifacts {
		hash, data, err := marshalWorkflowObject(artifact)
		if err != nil {
			return err
		}
		index.Artifacts[artifact.ID] = hash
		index.ArtifactOrder = append(index.ArtifactOrder, artifact.ID)
		objectData[hash] = data
	}
	for hash, data := range objectData {
		if err := writeWorkflowFile(filepath.Join(stageObjectsDir, hash+".json"), data); err != nil {
			return fmt.Errorf("stage workflow object: %w", err)
		}
	}
	if err := syncDirectory(stageObjectsDir); err != nil {
		return err
	}

	next := cloneWorkflowManifest(current, generation)
	next.Workflows[string(snapshot.Workflow.ID)] = index
	manifestData, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("marshal workflow manifest: %w", err)
	}
	stageManifestPath := filepath.Join(stageDir, "manifest.json")
	if err := writeWorkflowFile(stageManifestPath, manifestData); err != nil {
		return fmt.Errorf("stage workflow manifest: %w", err)
	}
	if err := syncDirectory(stageDir); err != nil {
		return err
	}

	for hash, data := range objectData {
		if err := s.publishWorkflowObject(hash, filepath.Join(stageObjectsDir, hash+".json"), data); err != nil {
			return err
		}
	}
	if err := syncDirectory(s.workflowObjectsDir()); err != nil {
		return err
	}
	manifestPath := s.workflowManifestPath(generation)
	if _, err := os.Stat(manifestPath); err == nil {
		return corruptWorkflowStore("refusing to overwrite immutable manifest %s", generation)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect workflow manifest destination: %w", err)
	}
	if err := os.Rename(stageManifestPath, manifestPath); err != nil {
		return fmt.Errorf("publish workflow manifest: %w", err)
	}
	if err := syncDirectory(s.workflowManifestsDir()); err != nil {
		return err
	}
	if err := os.RemoveAll(stageDir); err != nil {
		return fmt.Errorf("remove published workflow staging generation: %w", err)
	}
	if err := syncDirectory(s.workflowStagingDir()); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	beforeHeadReplacement := func() error {
		if options.beforeHeadPublication != nil {
			if err := options.beforeHeadPublication(); err != nil {
				return err
			}
		}
		return ctx.Err()
	}
	if err := writeWorkflowFileBeforeReplace(s.workflowHeadPath(), []byte(generation+"\n"), beforeHeadReplacement); err != nil {
		return fmt.Errorf("publish workflow HEAD: %w", err)
	}
	if err := writeWorkflowFile(s.workflowInitializedPath(), []byte(workflowInitializedName+"\n")); err != nil {
		return fmt.Errorf("publish workflow initialization marker: %w", err)
	}
	return syncDirectory(s.workflowStoreRoot())
}

// LoadWorkflowSnapshot reads exactly the generation named by one HEAD read.
func (s *FileStore) LoadWorkflowSnapshot(ctx context.Context, workflowID domain.WorkflowID) (domain.WorkflowSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return domain.WorkflowSnapshot{}, err
	}
	if strings.TrimSpace(string(workflowID)) == "" {
		return domain.WorkflowSnapshot{}, fmt.Errorf("%w: workflow id is required", domain.ErrInvalidWorkflow)
	}
	manifest, err := s.loadWorkflowManifest()
	if err != nil {
		return domain.WorkflowSnapshot{}, err
	}
	if manifest == nil {
		return domain.WorkflowSnapshot{}, &domain.WorkflowNotFoundError{WorkflowID: workflowID}
	}
	index, exists := manifest.Workflows[string(workflowID)]
	if !exists {
		return domain.WorkflowSnapshot{}, &domain.WorkflowNotFoundError{WorkflowID: workflowID}
	}

	workflow, err := s.readWorkflowObject(index.Workflow)
	if err != nil {
		return domain.WorkflowSnapshot{}, err
	}
	if workflow.ID != workflowID {
		return domain.WorkflowSnapshot{}, corruptWorkflowStore("workflow index points to %q, not %q", workflow.ID, workflowID)
	}
	if err := validateWorkflowWorkUnitIndex(workflowID, workflow.WorkUnitIDs, index.WorkUnits); err != nil {
		return domain.WorkflowSnapshot{}, err
	}
	snapshot := domain.WorkflowSnapshot{Workflow: workflow}
	for _, unitID := range workflow.WorkUnitIDs {
		id := string(unitID)
		hash := index.WorkUnits[id]
		unit, err := s.readWorkUnitObject(hash)
		if err != nil {
			return domain.WorkflowSnapshot{}, err
		}
		if string(unit.ID) != id {
			return domain.WorkflowSnapshot{}, corruptWorkflowStore("work unit index key %q does not match object id %q", id, unit.ID)
		}
		snapshot.WorkUnits = append(snapshot.WorkUnits, unit)
	}
	for _, id := range index.ActionOrder {
		hash, exists := index.Actions[id]
		if !exists {
			return domain.WorkflowSnapshot{}, corruptWorkflowStore("action order references missing action %q", id)
		}
		action, err := s.readActionObject(hash)
		if err != nil {
			return domain.WorkflowSnapshot{}, err
		}
		if string(action.ID) != id {
			return domain.WorkflowSnapshot{}, corruptWorkflowStore("action index key %q does not match object id %q", id, action.ID)
		}
		snapshot.Actions = append(snapshot.Actions, action)
	}
	for _, id := range index.ArtifactOrder {
		hash, exists := index.Artifacts[id]
		if !exists {
			return domain.WorkflowSnapshot{}, corruptWorkflowStore("artifact order references missing artifact %q", id)
		}
		artifact, err := s.readArtifactObject(hash)
		if err != nil {
			return domain.WorkflowSnapshot{}, err
		}
		if artifact.ID != id {
			return domain.WorkflowSnapshot{}, corruptWorkflowStore("artifact index key %q does not match object id %q", id, artifact.ID)
		}
		snapshot.Artifacts = append(snapshot.Artifacts, artifact)
	}
	if err := domain.ValidateWorkflowSnapshot(snapshot); err != nil {
		return domain.WorkflowSnapshot{}, corruptWorkflowStore("snapshot validation failed: %v", err)
	}
	return snapshot, nil
}

func validateWorkflowWorkUnitIndex(workflowID domain.WorkflowID, declared []domain.DurableWorkUnitID, index map[string]string) error {
	if len(index) != len(declared) {
		return corruptWorkflowStore("workflow %q work unit index cardinality=%d, declared=%d", workflowID, len(index), len(declared))
	}
	seen := make(map[string]struct{}, len(declared))
	for _, unitID := range declared {
		id := string(unitID)
		if _, duplicate := seen[id]; duplicate {
			return corruptWorkflowStore("workflow %q declares duplicate work unit %q", workflowID, id)
		}
		seen[id] = struct{}{}
		if _, exists := index[id]; !exists {
			return corruptWorkflowStore("workflow %q work unit %q is missing from manifest", workflowID, id)
		}
	}
	return nil
}

func (s *FileStore) ensureWorkflowStoreLayout() error {
	for _, dir := range []string{s.workflowStoreRoot(), s.workflowObjectsDir(), s.workflowManifestsDir(), s.workflowStagingDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create durable workflow storage: %w", err)
		}
	}
	return nil
}

func (s *FileStore) cleanWorkflowStaging() error {
	entries, err := os.ReadDir(s.workflowStagingDir())
	if err != nil {
		return fmt.Errorf("read workflow staging directory: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || !validWorkflowGenerationID(entry.Name()) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(s.workflowStagingDir(), entry.Name())); err != nil {
			return fmt.Errorf("clean workflow staging generation: %w", err)
		}
	}
	return syncDirectory(s.workflowStagingDir())
}

func (s *FileStore) loadWorkflowManifest() (*workflowManifest, error) {
	head, err := os.ReadFile(s.workflowHeadPath())
	if errors.Is(err, os.ErrNotExist) {
		return s.loadAbsentWorkflowHead()
	}
	if err != nil {
		return nil, corruptWorkflowStore("read HEAD: %v", err)
	}
	generation := strings.TrimSpace(string(head))
	if !validWorkflowGenerationID(generation) {
		return nil, corruptWorkflowStore("invalid HEAD generation")
	}
	data, err := os.ReadFile(s.workflowManifestPath(generation))
	if err != nil {
		return nil, corruptWorkflowStore("read manifest for HEAD generation: %v", err)
	}
	var manifest workflowManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, corruptWorkflowStore("decode manifest: %v", err)
	}
	if err := validateWorkflowManifest(manifest, generation); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func (s *FileStore) loadAbsentWorkflowHead() (*workflowManifest, error) {
	if _, err := os.Stat(s.workflowInitializedPath()); err == nil {
		return nil, corruptWorkflowStore("HEAD is missing from an initialized store")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, corruptWorkflowStore("inspect initialization marker while HEAD is absent: %v", err)
	}
	return nil, nil
}

func validateWorkflowManifest(manifest workflowManifest, generation string) error {
	if manifest.Version != workflowManifestVersion || manifest.Generation != generation || !validWorkflowGenerationID(manifest.Generation) || len(manifest.Workflows) == 0 {
		return corruptWorkflowStore("invalid manifest header")
	}
	for workflowID, index := range manifest.Workflows {
		if strings.TrimSpace(workflowID) == "" || !validWorkflowObjectHash(index.Workflow) || index.WorkUnits == nil || index.Actions == nil || index.Artifacts == nil {
			return corruptWorkflowStore("invalid manifest workflow index")
		}
		for id, hash := range index.WorkUnits {
			if strings.TrimSpace(id) == "" || !validWorkflowObjectHash(hash) {
				return corruptWorkflowStore("invalid work unit index")
			}
		}
		for id, hash := range index.Actions {
			if strings.TrimSpace(id) == "" || !validWorkflowObjectHash(hash) {
				return corruptWorkflowStore("invalid action index")
			}
		}
		if err := validateWorkflowManifestOrder("action", index.ActionOrder, index.Actions); err != nil {
			return err
		}
		for id, hash := range index.Artifacts {
			if strings.TrimSpace(id) == "" || !validWorkflowObjectHash(hash) {
				return corruptWorkflowStore("invalid artifact index")
			}
		}
		if err := validateWorkflowManifestOrder("artifact", index.ArtifactOrder, index.Artifacts); err != nil {
			return err
		}
	}
	return nil
}

func validateWorkflowManifestOrder(kind string, order []string, index map[string]string) error {
	if len(order) != len(index) {
		return corruptWorkflowStore("invalid %s order", kind)
	}
	seen := make(map[string]struct{}, len(order))
	for _, id := range order {
		if strings.TrimSpace(id) == "" {
			return corruptWorkflowStore("invalid %s order", kind)
		}
		if _, exists := index[id]; !exists {
			return corruptWorkflowStore("%s order references missing index entry %q", kind, id)
		}
		if _, exists := seen[id]; exists {
			return corruptWorkflowStore("duplicate %s order entry %q", kind, id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func cloneWorkflowManifest(current *workflowManifest, generation string) workflowManifest {
	next := workflowManifest{Version: workflowManifestVersion, Generation: generation, Workflows: make(map[string]workflowManifestIndex)}
	if current == nil {
		return next
	}
	for workflowID, index := range current.Workflows {
		clone := workflowManifestIndex{
			Workflow:      index.Workflow,
			WorkUnits:     make(map[string]string, len(index.WorkUnits)),
			Actions:       make(map[string]string, len(index.Actions)),
			ActionOrder:   append([]string(nil), index.ActionOrder...),
			Artifacts:     make(map[string]string, len(index.Artifacts)),
			ArtifactOrder: append([]string(nil), index.ArtifactOrder...),
		}
		for id, hash := range index.WorkUnits {
			clone.WorkUnits[id] = hash
		}
		for id, hash := range index.Actions {
			clone.Actions[id] = hash
		}
		for id, hash := range index.Artifacts {
			clone.Artifacts[id] = hash
		}
		next.Workflows[workflowID] = clone
	}
	return next
}

func (s *FileStore) publishWorkflowObject(hash, stagedPath string, expected []byte) error {
	destination := s.workflowObjectPath(hash)
	if _, err := os.Stat(destination); err == nil {
		data, err := readAndVerifyWorkflowObject(destination, hash)
		if err != nil {
			return err
		}
		if string(data) != string(expected) {
			return corruptWorkflowStore("object %s hash collision", hash)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect workflow object: %w", err)
	}
	if err := os.Rename(stagedPath, destination); err != nil {
		return fmt.Errorf("publish workflow object: %w", err)
	}
	return nil
}

func (s *FileStore) readWorkflowObject(hash string) (domain.Workflow, error) {
	var workflow domain.Workflow
	if err := s.readVerifiedWorkflowObject(hash, &workflow); err != nil {
		return domain.Workflow{}, err
	}
	return workflow, nil
}

func (s *FileStore) readWorkUnitObject(hash string) (domain.DurableWorkUnit, error) {
	var unit domain.DurableWorkUnit
	if err := s.readVerifiedWorkflowObject(hash, &unit); err != nil {
		return domain.DurableWorkUnit{}, err
	}
	return unit, nil
}

func (s *FileStore) readActionObject(hash string) (domain.DurableAction, error) {
	var action domain.DurableAction
	if err := s.readVerifiedWorkflowObject(hash, &action); err != nil {
		return domain.DurableAction{}, err
	}
	return action, nil
}

func (s *FileStore) readArtifactObject(hash string) (domain.RunArtifact, error) {
	var artifact domain.RunArtifact
	if err := s.readVerifiedWorkflowObject(hash, &artifact); err != nil {
		return domain.RunArtifact{}, err
	}
	return artifact, nil
}

func (s *FileStore) readVerifiedWorkflowObject(hash string, destination any) error {
	if !validWorkflowObjectHash(hash) {
		return corruptWorkflowStore("invalid object hash")
	}
	data, err := readAndVerifyWorkflowObject(s.workflowObjectPath(hash), hash)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, destination); err != nil {
		return corruptWorkflowStore("decode object %s: %v", hash, err)
	}
	return nil
}

func readAndVerifyWorkflowObject(path, hash string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, corruptWorkflowStore("read object %s: %v", hash, err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != hash {
		return nil, corruptWorkflowStore("object %s hash mismatch", hash)
	}
	return data, nil
}

func marshalWorkflowObject(value any) (string, []byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", nil, fmt.Errorf("marshal workflow object: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), data, nil
}

func writeWorkflowFile(path string, data []byte) error {
	return writeWorkflowFileBeforeReplace(path, data, nil)
}

func writeWorkflowFileBeforeReplace(path string, data []byte, beforeReplace func() error) error {
	dir := filepath.Dir(path)
	temporary, err := os.CreateTemp(dir, ".workflow-tmp-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o644); err != nil {
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if beforeReplace != nil {
		if err := beforeReplace(); err != nil {
			return err
		}
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return syncDirectory(dir)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func newWorkflowGenerationID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, bytes); err != nil {
		return "", fmt.Errorf("generate workflow generation id: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

func validWorkflowGenerationID(value string) bool {
	return len(value) == 32 && validLowerHex(value)
}

func validWorkflowObjectHash(value string) bool {
	return len(value) == sha256.Size*2 && validLowerHex(value)
}

func validLowerHex(value string) bool {
	for _, character := range value {
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func corruptWorkflowStore(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errWorkflowStoreCorrupt, fmt.Sprintf(format, args...))
}

func (s *FileStore) workflowStoreRoot() string {
	return filepath.Join(s.root, durableWorkflowDirectory)
}

func (s *FileStore) workflowObjectsDir() string {
	return filepath.Join(s.workflowStoreRoot(), "objects")
}

func (s *FileStore) workflowManifestsDir() string {
	return filepath.Join(s.workflowStoreRoot(), "manifests")
}

func (s *FileStore) workflowStagingDir() string {
	return filepath.Join(s.workflowStoreRoot(), "staging")
}

func (s *FileStore) workflowLockPath() string {
	return filepath.Join(s.workflowStoreRoot(), "LOCK")
}

func (s *FileStore) workflowHeadPath() string {
	return filepath.Join(s.workflowStoreRoot(), "HEAD")
}

func (s *FileStore) workflowInitializedPath() string {
	return filepath.Join(s.workflowStoreRoot(), workflowInitializedName)
}

func (s *FileStore) workflowManifestPath(generation string) string {
	return filepath.Join(s.workflowManifestsDir(), generation+".json")
}

func (s *FileStore) workflowObjectPath(hash string) string {
	return filepath.Join(s.workflowObjectsDir(), hash+".json")
}
