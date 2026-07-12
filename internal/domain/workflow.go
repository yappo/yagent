package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type WorkflowID string
type ConversationID string
type ConversationTurnID string
type DurableWorkUnitID string
type ActionID string
type LeaseToken string

type WorkflowStatus string

const (
	WorkflowStatusPending        WorkflowStatus = "pending"
	WorkflowStatusRunning        WorkflowStatus = "running"
	WorkflowStatusSucceeded      WorkflowStatus = "succeeded"
	WorkflowStatusFailed         WorkflowStatus = "failed"
	WorkflowStatusNeedsAttention WorkflowStatus = "needs_attention"
)

type DurableWorkUnitStatus string

const (
	DurableWorkUnitStatusPending        DurableWorkUnitStatus = "pending"
	DurableWorkUnitStatusLeased         DurableWorkUnitStatus = "leased"
	DurableWorkUnitStatusExecuting      DurableWorkUnitStatus = "executing"
	DurableWorkUnitStatusSucceeded      DurableWorkUnitStatus = "succeeded"
	DurableWorkUnitStatusSkipped        DurableWorkUnitStatus = "skipped"
	DurableWorkUnitStatusBlocked        DurableWorkUnitStatus = "blocked"
	DurableWorkUnitStatusFailed         DurableWorkUnitStatus = "failed"
	DurableWorkUnitStatusNeedsAttention DurableWorkUnitStatus = "needs_attention"
)

type ActionStatus string

const (
	ActionStatusPrepared  ActionStatus = "prepared"
	ActionStatusExecuting ActionStatus = "executing"
	ActionStatusSucceeded ActionStatus = "succeeded"
	ActionStatusFailed    ActionStatus = "failed"
	ActionStatusAmbiguous ActionStatus = "ambiguous"
	ActionStatusAbandoned ActionStatus = "abandoned"
)

var (
	ErrInvalidWorkflow         = errors.New("invalid workflow")
	ErrInvalidDurableWorkUnit  = errors.New("invalid durable work unit")
	ErrInvalidAction           = errors.New("invalid action")
	ErrInvalidTransition       = errors.New("invalid workflow transition")
	ErrStaleWorkflowRevision   = errors.New("stale workflow revision")
	ErrLeaseMismatch           = errors.New("lease token or fencing token does not match")
	ErrLeaseExpired            = errors.New("lease has expired")
	ErrAmbiguousMutatingAction = errors.New("ambiguous mutating action cannot be retried blindly")
)

// ConversationReference links a workflow to the immutable conversation turn that started it.
// It deliberately does not model conversation history.
type ConversationReference struct {
	ConversationID ConversationID     `json:"conversation_id"`
	TurnID         ConversationTurnID `json:"turn_id"`
}

type Workflow struct {
	ID                WorkflowID            `json:"id"`
	Conversation      ConversationReference `json:"conversation"`
	RootGoal          string                `json:"root_goal"`
	InputArtifactRefs []ArtifactReference   `json:"input_artifact_refs,omitempty"`
	GraphArtifactRefs []ArtifactReference   `json:"graph_artifact_refs,omitempty"`
	Revision          int64                 `json:"revision"`
	Status            WorkflowStatus        `json:"status"`
	CreatedAt         time.Time             `json:"created_at"`
	UpdatedAt         time.Time             `json:"updated_at"`
	CompletedAt       time.Time             `json:"completed_at,omitempty"`
	WorkUnitIDs       []DurableWorkUnitID   `json:"work_unit_ids,omitempty"`
	FinalOutcomeRefs  []ArtifactReference   `json:"final_outcome_refs,omitempty"`
}

type WorkflowInput struct {
	ID                WorkflowID
	Conversation      ConversationReference
	RootGoal          string
	CreatedAt         time.Time
	InputArtifactRefs []ArtifactReference
	GraphArtifactRefs []ArtifactReference
	WorkUnitIDs       []DurableWorkUnitID
}

type DurableLease struct {
	OwnerID      string     `json:"owner_id"`
	Token        LeaseToken `json:"token"`
	FencingToken uint64     `json:"fencing_token"`
	ExpiresAt    time.Time  `json:"expires_at"`
}

type LeaseCredential struct {
	Token        LeaseToken
	FencingToken uint64
}

type DurableWorkUnitOutcome struct {
	ArtifactRefs []ArtifactReference `json:"artifact_refs,omitempty"`
	ActionIDs    []ActionID          `json:"action_ids,omitempty"`
	Reason       string              `json:"reason,omitempty"`
}

// DurableWorkUnit is separate from WorkUnit, the current in-memory execution-plan type.
// Its input and access sets are frozen at creation so retries cannot silently change scope.
type DurableWorkUnit struct {
	ID                DurableWorkUnitID       `json:"id"`
	WorkflowID        WorkflowID              `json:"workflow_id"`
	Kind              string                  `json:"kind"`
	Phase             RunPhase                `json:"phase"`
	Role              string                  `json:"role"`
	Task              string                  `json:"task"`
	Source            string                  `json:"source,omitempty"`
	SourceLimit       int                     `json:"source_limit"`
	SideEffectClass   SideEffectClass         `json:"side_effect_class"`
	DuplicateKey      string                  `json:"duplicate_key,omitempty"`
	Attempt           int                     `json:"attempt"`
	InputArtifactRefs []ArtifactReference     `json:"input_artifact_refs,omitempty"`
	ReadSet           []string                `json:"read_set,omitempty"`
	WriteSet          []string                `json:"write_set,omitempty"`
	Dependencies      []DurableWorkUnitID     `json:"dependencies,omitempty"`
	Status            DurableWorkUnitStatus   `json:"status"`
	ClaimedAt         time.Time               `json:"claimed_at,omitempty"`
	StartedAt         time.Time               `json:"started_at,omitempty"`
	CompletedAt       time.Time               `json:"completed_at,omitempty"`
	Lease             *DurableLease           `json:"lease,omitempty"`
	LastFencingToken  uint64                  `json:"last_fencing_token"`
	Outcome           *DurableWorkUnitOutcome `json:"outcome,omitempty"`
}

type DurableWorkUnitInput struct {
	ID                DurableWorkUnitID
	WorkflowID        WorkflowID
	Kind              string
	Phase             RunPhase
	Role              string
	Task              string
	Attempt           int
	Source            string
	SourceLimit       int
	SideEffectClass   SideEffectClass
	DuplicateKey      string
	InputArtifactRefs []ArtifactReference
	ReadSet           []string
	WriteSet          []string
	Dependencies      []DurableWorkUnitID
}

// DurableAction records durable action intent and observed outcome. It does not
// provide exactly-once semantics for filesystem, process, MCP, or external effects.
type DurableAction struct {
	ID                       ActionID            `json:"id"`
	WorkflowID               WorkflowID          `json:"workflow_id"`
	WorkUnitID               DurableWorkUnitID   `json:"work_unit_id"`
	Attempt                  int                 `json:"attempt"`
	Kind                     string              `json:"kind"`
	Target                   string              `json:"target"`
	IdempotencyKey           string              `json:"idempotency_key"`
	LeaseToken               LeaseToken          `json:"lease_token"`
	FencingToken             uint64              `json:"fencing_token"`
	NormalizedArguments      string              `json:"normalized_arguments"`
	ReadSet                  []string            `json:"read_set,omitempty"`
	WriteSet                 []string            `json:"write_set,omitempty"`
	SideEffectClass          SideEffectClass     `json:"side_effect_class"`
	PreconditionFingerprint  string              `json:"precondition_fingerprint"`
	Status                   ActionStatus        `json:"status"`
	ResultArtifactRefs       []ArtifactReference `json:"result_artifact_refs,omitempty"`
	ExecutionRef             string              `json:"execution_ref,omitempty"`
	MutationRefs             []string            `json:"mutation_refs,omitempty"`
	PostconditionFingerprint string              `json:"postcondition_fingerprint,omitempty"`
	Reason                   string              `json:"reason,omitempty"`
	StartedAt                time.Time           `json:"started_at,omitempty"`
	CompletedAt              time.Time           `json:"completed_at,omitempty"`
}

type DurableActionInput struct {
	ID                      ActionID
	WorkflowID              WorkflowID
	WorkUnitID              DurableWorkUnitID
	Attempt                 int
	Kind                    string
	Target                  string
	IdempotencyKey          string
	Lease                   LeaseCredential
	NormalizedArguments     string
	ReadSet                 []string
	WriteSet                []string
	SideEffectClass         SideEffectClass
	PreconditionFingerprint string
}

type DurableActionResult struct {
	ResultArtifactRefs       []ArtifactReference
	ExecutionRef             string
	MutationRefs             []string
	PostconditionFingerprint string
}

func NewWorkflow(input WorkflowInput) (Workflow, error) {
	workflow := Workflow{
		ID:                input.ID,
		Conversation:      input.Conversation,
		RootGoal:          strings.TrimSpace(input.RootGoal),
		InputArtifactRefs: cloneArtifactReferences(input.InputArtifactRefs),
		GraphArtifactRefs: cloneArtifactReferences(input.GraphArtifactRefs),
		Revision:          1,
		Status:            WorkflowStatusPending,
		CreatedAt:         input.CreatedAt,
		UpdatedAt:         input.CreatedAt,
		WorkUnitIDs:       cloneDurableWorkUnitIDs(input.WorkUnitIDs),
	}
	if err := ValidateWorkflow(workflow); err != nil {
		return Workflow{}, err
	}
	return workflow, nil
}

func NewDurableWorkUnit(input DurableWorkUnitInput) (DurableWorkUnit, error) {
	sideEffectClass := input.SideEffectClass
	if sideEffectClass == "" {
		sideEffectClass = SideEffectNone
	}
	attempt := input.Attempt
	if attempt < 1 {
		attempt = 1
	}
	unit := DurableWorkUnit{
		ID:                input.ID,
		WorkflowID:        input.WorkflowID,
		Kind:              strings.TrimSpace(input.Kind),
		Phase:             input.Phase,
		Role:              strings.TrimSpace(input.Role),
		Task:              strings.TrimSpace(input.Task),
		Source:            strings.TrimSpace(input.Source),
		SourceLimit:       input.SourceLimit,
		SideEffectClass:   sideEffectClass,
		DuplicateKey:      strings.TrimSpace(input.DuplicateKey),
		Attempt:           attempt,
		InputArtifactRefs: cloneArtifactReferences(input.InputArtifactRefs),
		ReadSet:           cloneStrings(input.ReadSet),
		WriteSet:          cloneStrings(input.WriteSet),
		Dependencies:      cloneDurableWorkUnitIDs(input.Dependencies),
		Status:            DurableWorkUnitStatusPending,
	}
	if err := ValidateDurableWorkUnit(unit); err != nil {
		return DurableWorkUnit{}, err
	}
	return unit, nil
}

func NewDurableAction(input DurableActionInput) (DurableAction, error) {
	sideEffectClass := input.SideEffectClass
	if sideEffectClass == "" {
		sideEffectClass = SideEffectNone
	}
	action := DurableAction{
		ID:                      input.ID,
		WorkflowID:              input.WorkflowID,
		WorkUnitID:              input.WorkUnitID,
		Attempt:                 input.Attempt,
		Kind:                    strings.TrimSpace(input.Kind),
		Target:                  strings.TrimSpace(input.Target),
		IdempotencyKey:          strings.TrimSpace(input.IdempotencyKey),
		LeaseToken:              input.Lease.Token,
		FencingToken:            input.Lease.FencingToken,
		NormalizedArguments:     input.NormalizedArguments,
		ReadSet:                 cloneStrings(input.ReadSet),
		WriteSet:                cloneStrings(input.WriteSet),
		SideEffectClass:         sideEffectClass,
		PreconditionFingerprint: strings.TrimSpace(input.PreconditionFingerprint),
		Status:                  ActionStatusPrepared,
	}
	if err := ValidateDurableAction(action); err != nil {
		return DurableAction{}, err
	}
	return action, nil
}

func ValidateWorkflow(workflow Workflow) error {
	if strings.TrimSpace(string(workflow.ID)) == "" || strings.TrimSpace(string(workflow.Conversation.ConversationID)) == "" || strings.TrimSpace(string(workflow.Conversation.TurnID)) == "" || strings.TrimSpace(workflow.RootGoal) == "" || workflow.Revision < 1 || !validWorkflowStatus(workflow.Status) || workflow.CreatedAt.IsZero() || workflow.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: id, conversation reference, root goal, revision, status, creation time, and update time are required", ErrInvalidWorkflow)
	}
	if workflow.UpdatedAt.Before(workflow.CreatedAt) {
		return fmt.Errorf("%w: update time precedes creation time", ErrInvalidWorkflow)
	}
	if workflowTerminal(workflow.Status) {
		if workflow.CompletedAt.IsZero() || workflow.CompletedAt.Before(workflow.UpdatedAt) {
			return fmt.Errorf("%w: terminal workflow requires a completion time no earlier than its update time", ErrInvalidWorkflow)
		}
	} else if !workflow.CompletedAt.IsZero() {
		return fmt.Errorf("%w: nonterminal workflow cannot have a completion time", ErrInvalidWorkflow)
	}
	if err := validateArtifactReferences(workflow.GraphArtifactRefs); err != nil {
		return fmt.Errorf("%w: graph artifact refs: %v", ErrInvalidWorkflow, err)
	}
	if err := validateArtifactReferences(workflow.InputArtifactRefs); err != nil {
		return fmt.Errorf("%w: input artifact refs: %v", ErrInvalidWorkflow, err)
	}
	if err := validateArtifactReferences(workflow.FinalOutcomeRefs); err != nil {
		return fmt.Errorf("%w: final outcome refs: %v", ErrInvalidWorkflow, err)
	}
	for _, id := range workflow.WorkUnitIDs {
		if strings.TrimSpace(string(id)) == "" {
			return fmt.Errorf("%w: empty work unit id", ErrInvalidWorkflow)
		}
	}
	return nil
}

func ValidateDurableWorkUnit(unit DurableWorkUnit) error {
	if strings.TrimSpace(string(unit.ID)) == "" || strings.TrimSpace(string(unit.WorkflowID)) == "" || strings.TrimSpace(unit.Kind) == "" || unit.Phase == "" || strings.TrimSpace(unit.Role) == "" || strings.TrimSpace(unit.Task) == "" || unit.SourceLimit < 0 || !validSideEffectClass(unit.SideEffectClass) || unit.Attempt < 1 || !validDurableWorkUnitStatus(unit.Status) {
		return fmt.Errorf("%w: id, workflow id, kind, phase, role, task, nonnegative source limit, side effect class, attempt, and status are required", ErrInvalidDurableWorkUnit)
	}
	if err := validateArtifactReferences(unit.InputArtifactRefs); err != nil {
		return fmt.Errorf("%w: input artifact refs: %v", ErrInvalidDurableWorkUnit, err)
	}
	if err := validateStrings(unit.ReadSet); err != nil {
		return fmt.Errorf("%w: read set: %v", ErrInvalidDurableWorkUnit, err)
	}
	if err := validateStrings(unit.WriteSet); err != nil {
		return fmt.Errorf("%w: write set: %v", ErrInvalidDurableWorkUnit, err)
	}
	for _, dependency := range unit.Dependencies {
		if strings.TrimSpace(string(dependency)) == "" {
			return fmt.Errorf("%w: empty dependency id", ErrInvalidDurableWorkUnit)
		}
	}
	if unit.Lease == nil && (unit.Status == DurableWorkUnitStatusLeased || unit.Status == DurableWorkUnitStatusExecuting) {
		return fmt.Errorf("%w: active status requires lease", ErrInvalidDurableWorkUnit)
	}
	if unit.Lease != nil && unit.Status != DurableWorkUnitStatusLeased && unit.Status != DurableWorkUnitStatusExecuting {
		return fmt.Errorf("%w: inactive status cannot retain a lease", ErrInvalidDurableWorkUnit)
	}
	if unit.Lease != nil {
		if err := validateLease(*unit.Lease, time.Time{}, 0, false); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidDurableWorkUnit, err)
		}
		if unit.Lease.FencingToken != unit.LastFencingToken {
			return fmt.Errorf("%w: lease fencing token must match last fencing token", ErrInvalidDurableWorkUnit)
		}
	}
	if err := validateDurableWorkUnitOutcomeState(unit.Status, unit.Outcome); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidDurableWorkUnit, err)
	}
	if err := validateDurableWorkUnitTimestamps(unit); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidDurableWorkUnit, err)
	}
	return nil
}

func validateDurableWorkUnitTimestamps(unit DurableWorkUnit) error {
	switch unit.Status {
	case DurableWorkUnitStatusPending:
		if !unit.ClaimedAt.IsZero() || !unit.StartedAt.IsZero() || !unit.CompletedAt.IsZero() {
			return errors.New("pending unit cannot have lifecycle timestamps")
		}
	case DurableWorkUnitStatusLeased:
		if unit.ClaimedAt.IsZero() || !unit.StartedAt.IsZero() || !unit.CompletedAt.IsZero() {
			return errors.New("leased unit requires only a claim time")
		}
	case DurableWorkUnitStatusExecuting:
		if unit.ClaimedAt.IsZero() || unit.StartedAt.IsZero() || !unit.CompletedAt.IsZero() {
			return errors.New("executing unit requires claim and start times only")
		}
		if unit.StartedAt.Before(unit.ClaimedAt) {
			return errors.New("unit start time precedes claim time")
		}
	case DurableWorkUnitStatusSucceeded, DurableWorkUnitStatusFailed, DurableWorkUnitStatusNeedsAttention:
		if unit.ClaimedAt.IsZero() || unit.StartedAt.IsZero() || unit.CompletedAt.IsZero() {
			return errors.New("terminal executed unit requires claim, start, and completion times")
		}
		if unit.StartedAt.Before(unit.ClaimedAt) || unit.CompletedAt.Before(unit.StartedAt) {
			return errors.New("unit lifecycle timestamps are out of order")
		}
	case DurableWorkUnitStatusSkipped, DurableWorkUnitStatusBlocked:
		if !unit.ClaimedAt.IsZero() || !unit.StartedAt.IsZero() || unit.CompletedAt.IsZero() {
			return fmt.Errorf("%s unit requires only a completion time", unit.Status)
		}
	}
	return nil
}

func ValidateDurableAction(action DurableAction) error {
	if strings.TrimSpace(string(action.ID)) == "" || strings.TrimSpace(string(action.WorkflowID)) == "" || strings.TrimSpace(string(action.WorkUnitID)) == "" || action.Attempt < 1 || strings.TrimSpace(action.Kind) == "" || strings.TrimSpace(action.Target) == "" || strings.TrimSpace(action.IdempotencyKey) == "" || strings.TrimSpace(string(action.LeaseToken)) == "" || action.FencingToken == 0 || !validActionStatus(action.Status) {
		return fmt.Errorf("%w: id, linkage, attempt, kind, target, idempotency key, lease credential, and status are required", ErrInvalidAction)
	}
	if err := validateStrings(action.ReadSet); err != nil {
		return fmt.Errorf("%w: read set: %v", ErrInvalidAction, err)
	}
	if err := validateStrings(action.WriteSet); err != nil {
		return fmt.Errorf("%w: write set: %v", ErrInvalidAction, err)
	}
	if err := validateArtifactReferences(action.ResultArtifactRefs); err != nil {
		return fmt.Errorf("%w: result artifact refs: %v", ErrInvalidAction, err)
	}
	if err := validateStrings(action.MutationRefs); err != nil {
		return fmt.Errorf("%w: mutation refs: %v", ErrInvalidAction, err)
	}
	if !validSideEffectClass(action.SideEffectClass) {
		return fmt.Errorf("%w: invalid side effect class", ErrInvalidAction)
	}
	if action.SideEffectClass != SideEffectNone && strings.TrimSpace(action.PreconditionFingerprint) == "" {
		return fmt.Errorf("%w: mutating action requires a precondition fingerprint", ErrInvalidAction)
	}
	if err := validateActionState(action); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidAction, err)
	}
	return nil
}

// ValidateActionRetry rejects a blind retry after an ambiguous action with a
// non-read-only side effect. Callers must reconcile its postcondition first.
func ValidateActionRetry(action DurableAction) error {
	if err := ValidateDurableAction(action); err != nil {
		return err
	}
	if action.Status == ActionStatusAmbiguous && action.SideEffectClass != SideEffectNone {
		return ErrAmbiguousMutatingAction
	}
	return nil
}

func validateLease(lease DurableLease, at time.Time, lastFencingToken uint64, requireFutureExpiry bool) error {
	if strings.TrimSpace(lease.OwnerID) == "" || strings.TrimSpace(string(lease.Token)) == "" || lease.FencingToken == 0 || lease.ExpiresAt.IsZero() {
		return fmt.Errorf("%w: owner id, token, fencing token, and expiry are required", ErrInvalidTransition)
	}
	if lease.FencingToken <= lastFencingToken {
		return fmt.Errorf("%w: fencing token must increase", ErrInvalidTransition)
	}
	if requireFutureExpiry && !lease.ExpiresAt.After(at) {
		return ErrLeaseExpired
	}
	return nil
}

func validateActionState(action DurableAction) error {
	switch action.Status {
	case ActionStatusPrepared:
		if !action.StartedAt.IsZero() || !action.CompletedAt.IsZero() || strings.TrimSpace(action.Reason) != "" {
			return errors.New("prepared action cannot have lifecycle outcome fields")
		}
	case ActionStatusExecuting:
		if action.StartedAt.IsZero() || !action.CompletedAt.IsZero() || strings.TrimSpace(action.Reason) != "" {
			return errors.New("executing action requires only a start time")
		}
	case ActionStatusSucceeded:
		if action.StartedAt.IsZero() || action.CompletedAt.IsZero() || strings.TrimSpace(action.Reason) != "" {
			return errors.New("succeeded action requires lifecycle times and no failure reason")
		}
		if action.SideEffectClass != SideEffectNone && (strings.TrimSpace(action.PostconditionFingerprint) == "" || len(action.ResultArtifactRefs) == 0) {
			return errors.New("succeeded mutating action requires a postcondition fingerprint and result artifact references")
		}
	case ActionStatusFailed, ActionStatusAmbiguous:
		if action.StartedAt.IsZero() || action.CompletedAt.IsZero() || strings.TrimSpace(action.Reason) == "" {
			return errors.New("failed or ambiguous action requires lifecycle times and a reason")
		}
	case ActionStatusAbandoned:
		if !action.StartedAt.IsZero() || action.CompletedAt.IsZero() || strings.TrimSpace(action.Reason) == "" {
			return errors.New("abandoned action requires only a completion time and reason")
		}
	}
	if !action.StartedAt.IsZero() && !action.CompletedAt.IsZero() && action.CompletedAt.Before(action.StartedAt) {
		return errors.New("completion time precedes start time")
	}
	return nil
}

func validateActionResult(result DurableActionResult) error {
	if err := validateArtifactReferences(result.ResultArtifactRefs); err != nil {
		return err
	}
	return validateStrings(result.MutationRefs)
}

func validateOutcome(outcome DurableWorkUnitOutcome) error {
	if err := validateArtifactReferences(outcome.ArtifactRefs); err != nil {
		return err
	}
	for _, id := range outcome.ActionIDs {
		if strings.TrimSpace(string(id)) == "" {
			return errors.New("empty action id")
		}
	}
	return nil
}

func validateDurableWorkUnitOutcomeState(status DurableWorkUnitStatus, outcome *DurableWorkUnitOutcome) error {
	switch status {
	case DurableWorkUnitStatusPending, DurableWorkUnitStatusLeased, DurableWorkUnitStatusExecuting:
		if outcome != nil {
			return errors.New("nonterminal work unit cannot carry an outcome")
		}
		return nil
	case DurableWorkUnitStatusSucceeded, DurableWorkUnitStatusSkipped, DurableWorkUnitStatusBlocked, DurableWorkUnitStatusFailed, DurableWorkUnitStatusNeedsAttention:
		if outcome == nil {
			return errors.New("terminal work unit requires an outcome")
		}
	default:
		return errors.New("unknown work unit status")
	}
	if err := validateOutcome(*outcome); err != nil {
		return fmt.Errorf("outcome: %v", err)
	}
	reason := strings.TrimSpace(outcome.Reason)
	if status == DurableWorkUnitStatusSucceeded && reason != "" {
		return errors.New("succeeded work unit cannot have an outcome reason")
	}
	if status != DurableWorkUnitStatusSucceeded && reason == "" {
		return fmt.Errorf("%s work unit requires an outcome reason", status)
	}
	return nil
}

func validateArtifactReferences(refs []ArtifactReference) error {
	for _, ref := range refs {
		if strings.TrimSpace(ref.ID) == "" {
			return errors.New("empty artifact id")
		}
	}
	return nil
}

func validateStrings(values []string) error {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return errors.New("empty value")
		}
	}
	return nil
}

func validWorkflowStatus(status WorkflowStatus) bool {
	switch status {
	case WorkflowStatusPending, WorkflowStatusRunning, WorkflowStatusSucceeded, WorkflowStatusFailed, WorkflowStatusNeedsAttention:
		return true
	default:
		return false
	}
}

func validDurableWorkUnitStatus(status DurableWorkUnitStatus) bool {
	switch status {
	case DurableWorkUnitStatusPending, DurableWorkUnitStatusLeased, DurableWorkUnitStatusExecuting, DurableWorkUnitStatusSucceeded, DurableWorkUnitStatusSkipped, DurableWorkUnitStatusBlocked, DurableWorkUnitStatusFailed, DurableWorkUnitStatusNeedsAttention:
		return true
	default:
		return false
	}
}

func validSideEffectClass(class SideEffectClass) bool {
	switch class {
	case SideEffectNone, SideEffectWorkspace, SideEffectProcess, SideEffectNetwork, SideEffectExternal:
		return true
	default:
		return false
	}
}

func validActionStatus(status ActionStatus) bool {
	switch status {
	case ActionStatusPrepared, ActionStatusExecuting, ActionStatusSucceeded, ActionStatusFailed, ActionStatusAmbiguous, ActionStatusAbandoned:
		return true
	default:
		return false
	}
}

func cloneDurableWorkUnit(unit DurableWorkUnit) DurableWorkUnit {
	unit.InputArtifactRefs = cloneArtifactReferences(unit.InputArtifactRefs)
	unit.ReadSet = cloneStrings(unit.ReadSet)
	unit.WriteSet = cloneStrings(unit.WriteSet)
	unit.Dependencies = cloneDurableWorkUnitIDs(unit.Dependencies)
	unit.Lease = cloneLease(unit.Lease)
	unit.Outcome = cloneOutcome(unit.Outcome)
	return unit
}

func cloneLease(lease *DurableLease) *DurableLease {
	if lease == nil {
		return nil
	}
	copy := *lease
	return &copy
}

func cloneOutcome(outcome *DurableWorkUnitOutcome) *DurableWorkUnitOutcome {
	if outcome == nil {
		return nil
	}
	copy := *outcome
	copy.ArtifactRefs = cloneArtifactReferences(outcome.ArtifactRefs)
	copy.ActionIDs = cloneActionIDs(outcome.ActionIDs)
	return &copy
}

func cloneArtifactReferences(refs []ArtifactReference) []ArtifactReference {
	return append([]ArtifactReference(nil), refs...)
}

func cloneDurableWorkUnitIDs(ids []DurableWorkUnitID) []DurableWorkUnitID {
	return append([]DurableWorkUnitID(nil), ids...)
}

func cloneActionIDs(ids []ActionID) []ActionID {
	return append([]ActionID(nil), ids...)
}

func cloneStrings(values []string) []string {
	return append([]string(nil), values...)
}
