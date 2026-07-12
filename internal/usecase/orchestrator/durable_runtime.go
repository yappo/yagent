package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"yagent/internal/domain"
)

// durableRunSeed keeps request-scoped inputs outside the workflow snapshot. The
// workflow remains authoritative for mutable execution state and artifacts.
type durableRunSeed struct {
	messages     []domain.Message
	model        string
	profile      string
	capabilities []string
	stream       bool
}

func (s *Service) runDurableWorkGraph(ctx context.Context, run *domain.RunState, plan *domain.ExecutionPlan, request domain.TurnRequest) (domain.AgentResult, []domain.ExecutionEvent, error) {
	if run == nil || run.WorkflowID == "" {
		return domain.AgentResult{}, nil, fmt.Errorf("execution graph の workflow identity がありません")
	}

	snapshot, err := s.ensureWorkflowIntent(ctx, run, request)
	if err != nil {
		return domain.AgentResult{}, nil, err
	}
	allEvents := []domain.ExecutionEvent{}
	if workflowIntentOnly(snapshot) && plan != nil {
		if len(run.WorkUnits) == 0 {
			run.WorkUnits = workUnitsFromExecutionPlan(run, plan)
		}
		snapshot, err = s.attachWorkflowPlan(ctx, snapshot, run, time.Now())
		if err != nil {
			return domain.AgentResult{}, allEvents, err
		}
	}
	seed, err := durableRunSeedFromSnapshot(snapshot)
	if err != nil {
		return domain.AgentResult{}, allEvents, err
	}
	projected, err := durableProjectedRun(snapshot, seed)
	if err != nil {
		return domain.AgentResult{}, allEvents, err
	}
	*run = *projected
	plan = run.ExecutionPlan
	request = domain.TurnRequest{Model: seed.model, Profile: seed.profile, Stream: seed.stream}
	if workflowIntentOnly(snapshot) {
		inventory := s.buildAgentInventory()
		planned, events, planErr := s.runPlanPhase(ctx, run, request, inventory)
		allEvents = append(allEvents, events...)
		if planErr != nil {
			return domain.AgentResult{}, allEvents, planErr
		}
		snapshot, err = s.attachWorkflowPlan(ctx, snapshot, run, time.Now())
		if err != nil {
			return domain.AgentResult{}, allEvents, err
		}
		projected, err = durableProjectedRun(snapshot, seed)
		if err != nil {
			return domain.AgentResult{}, allEvents, err
		}
		*run = *projected
		plan = planned
	}
	if plan == nil {
		return domain.AgentResult{}, allEvents, fmt.Errorf("workflow %q has no projected execution plan", snapshot.Workflow.ID)
	}

	state := durableGraphExecutionState(snapshot)

workflowLoop:
	for {
		if durableWorkflowTerminal(snapshot) {
			break
		}

		batch, err := s.claimAndStartDurableBatch(ctx, snapshot.Workflow.ID, time.Now())
		if err != nil {
			return domain.AgentResult{}, allEvents, err
		}
		snapshot = batch.Snapshot
		if len(batch.Units) == 0 {
			if err := s.settleDurableWorkflow(ctx, snapshot); err != nil {
				return domain.AgentResult{}, allEvents, err
			}
			snapshot, err = s.config.WorkflowStore.LoadWorkflowSnapshot(ctx, snapshot.Workflow.ID)
			if err != nil {
				return domain.AgentResult{}, allEvents, err
			}
			continue
		}

		batchRun, err := durableProjectedRun(snapshot, seed)
		if err != nil {
			return domain.AgentResult{}, allEvents, err
		}
		durableHydrateExecutionState(snapshot, &state)
		outcomes := make([]workUnitOutcome, len(batch.Units))
		group, groupCtx := errgroup.WithContext(ctx)
		for index, durableUnit := range batch.Units {
			index, durableUnit := index, durableUnit
			group.Go(func() error {
				unit := durableWorkUnitProjection(durableUnit)
				outcomes[index] = s.executeWorkUnit(groupCtx, batchRun, plan, request, unit, state, batch.Credentials[durableUnit.ID])
				return nil // Every result is committed as a terminal durable outcome below.
			})
		}
		stopHeartbeat := s.startDurableLeaseHeartbeat(ctx, snapshot.Workflow.ID, batch.Credentials)
		_ = group.Wait()
		heartbeatErr := stopHeartbeat()
		for _, outcome := range outcomes {
			allEvents = append(allEvents, outcome.result.Events...)
		}
		if heartbeatErr != nil {
			snapshot, err = s.recoverLostDurableBatch(ctx, snapshot.Workflow.ID, heartbeatErr, time.Now())
			if err != nil {
				return domain.AgentResult{}, allEvents, err
			}
			continue
		}

		for _, outcome := range outcomes {
			committed, err := s.finishDurableWorkUnit(ctx, snapshot.Workflow.ID, outcome, batch.Credentials[domain.DurableWorkUnitID(outcome.unit.ID)], plan, seed, &state)
			if err != nil {
				if errors.Is(err, domain.ErrLeaseExpired) || errors.Is(err, domain.ErrLeaseMismatch) {
					snapshot, err = s.recoverLostDurableBatch(ctx, snapshot.Workflow.ID, err, time.Now())
					if err != nil {
						return domain.AgentResult{}, allEvents, err
					}
					continue workflowLoop
				}
				return domain.AgentResult{}, allEvents, err
			}
			snapshot = committed
		}
	}

	projected, err = durableProjectedRun(snapshot, seed)
	if err != nil {
		return domain.AgentResult{}, allEvents, err
	}
	*run = *projected
	final := durableFinalResult(snapshot)
	if snapshot.Workflow.Status == domain.WorkflowStatusFailed {
		return final, allEvents, fmt.Errorf("durable workflow %q failed: %s", snapshot.Workflow.ID, durableWorkflowFailureReason(snapshot))
	}
	if snapshot.Workflow.Status == domain.WorkflowStatusNeedsAttention && final.Message.Content == "" {
		return final, allEvents, fmt.Errorf("durable workflow %q needs attention: %s", snapshot.Workflow.ID, durableWorkflowFailureReason(snapshot))
	}
	return final, allEvents, nil
}

func (s *Service) ensureWorkflowIntent(ctx context.Context, run *domain.RunState, request domain.TurnRequest) (domain.WorkflowSnapshot, error) {
	snapshot, err := s.config.WorkflowStore.LoadWorkflowSnapshot(ctx, run.WorkflowID)
	if err == nil {
		return snapshot, nil
	}
	if !errors.Is(err, domain.ErrWorkflowNotFound) {
		return domain.WorkflowSnapshot{}, err
	}
	seed := durableRunSeed{
		messages: cloneMessages(run.Messages), model: request.Model, profile: fallbackString(request.Profile, run.Profile),
		capabilities: append([]string(nil), run.EnabledCapabilities...), stream: request.Stream,
	}
	intent, err := buildWorkflowIntentSnapshot(run, seed, time.Now())
	if err != nil {
		return domain.WorkflowSnapshot{}, err
	}
	return s.createWorkflowSnapshot(ctx, intent)
}

func (s *Service) attachWorkflowPlan(ctx context.Context, snapshot domain.WorkflowSnapshot, run *domain.RunState, at time.Time) (domain.WorkflowSnapshot, error) {
	attachment, err := buildWorkflowPlanAttachment(snapshot, run, at)
	if err != nil {
		return domain.WorkflowSnapshot{}, err
	}
	return s.commitWorkflowTransition(ctx, snapshot.Workflow.ID, func(current domain.WorkflowSnapshot) (domain.WorkflowSnapshot, error) {
		attachment.ExpectedRevision = current.Workflow.Revision
		attachment.At = maxWorkflowTransitionTime(at, current.Workflow.UpdatedAt)
		return domain.AttachWorkflowPlan(current, attachment)
	}, func(current domain.WorkflowSnapshot) bool {
		return workflowPlanAttachmentApplied(current, attachment)
	})
}

func workflowIntentOnly(snapshot domain.WorkflowSnapshot) bool {
	return snapshot.Workflow.Status == domain.WorkflowStatusPending && len(snapshot.Workflow.GraphArtifactRefs) == 0 && len(snapshot.Workflow.WorkUnitIDs) == 0 && len(snapshot.WorkUnits) == 0 && len(snapshot.Actions) == 0
}

func workflowPlanAttachmentApplied(snapshot domain.WorkflowSnapshot, attachment domain.AttachWorkflowPlanInput) bool {
	if !reflect.DeepEqual(snapshot.Workflow.GraphArtifactRefs, attachment.GraphArtifactRefs) || len(snapshot.WorkUnits) != len(attachment.WorkUnits) {
		return false
	}
	for index, unit := range attachment.WorkUnits {
		if !reflect.DeepEqual(snapshot.WorkUnits[index], unit) {
			return false
		}
	}
	artifacts := make(map[string]domain.RunArtifact, len(snapshot.Artifacts))
	for _, artifact := range snapshot.Artifacts {
		artifacts[artifact.ID] = artifact
	}
	for _, artifact := range attachment.Artifacts {
		if existing, ok := artifacts[artifact.ID]; !ok || !reflect.DeepEqual(existing, artifact) {
			return false
		}
	}
	return len(attachment.GraphArtifactRefs) == 1 && len(attachment.WorkUnits) > 0
}

func maxWorkflowTransitionTime(candidate time.Time, minimum time.Time) time.Time {
	if candidate.Before(minimum) {
		return minimum
	}
	return candidate
}

// recoverLostDurableBatch discards outcomes produced under stale fencing. A
// suspended local process can miss every heartbeat while the Mac is asleep;
// recovery must go through the same durable reconciliation used by a takeover.
func (s *Service) recoverLostDurableBatch(ctx context.Context, workflowID domain.WorkflowID, heartbeatErr error, at time.Time) (domain.WorkflowSnapshot, error) {
	if !errors.Is(heartbeatErr, domain.ErrLeaseExpired) && !errors.Is(heartbeatErr, domain.ErrLeaseMismatch) {
		return domain.WorkflowSnapshot{}, heartbeatErr
	}
	current, err := s.config.WorkflowStore.LoadWorkflowSnapshot(ctx, workflowID)
	if err != nil {
		return domain.WorkflowSnapshot{}, errors.Join(heartbeatErr, err)
	}
	if !hasExpiredDurableWorkUnits(current.WorkUnits, at) {
		return current, nil
	}
	reconciled, err := s.reconcileExpiredDurableWorkUnits(ctx, current, at)
	if err != nil {
		return domain.WorkflowSnapshot{}, errors.Join(heartbeatErr, err)
	}
	return reconciled, nil
}

func (s *Service) startDurableLeaseHeartbeat(ctx context.Context, workflowID domain.WorkflowID, credentials map[domain.DurableWorkUnitID]domain.LeaseCredential) func() error {
	heartbeatCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	interval := s.config.WorkflowLeaseDuration / 3
	if interval <= 0 {
		interval = time.Nanosecond
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				done <- nil
				return
			case at := <-ticker.C:
				if err := s.renewDurableWorkBatch(heartbeatCtx, workflowID, credentials, at); err != nil {
					done <- fmt.Errorf("renew durable work leases: %w", err)
					return
				}
			}
		}
	}()
	return func() error {
		cancel()
		return <-done
	}
}

func durableWorkflowFailureReason(snapshot domain.WorkflowSnapshot) string {
	for index := len(snapshot.WorkUnits) - 1; index >= 0; index-- {
		unit := snapshot.WorkUnits[index]
		if unit.Outcome == nil || (unit.Status != domain.DurableWorkUnitStatusFailed && unit.Status != domain.DurableWorkUnitStatusNeedsAttention) {
			continue
		}
		if reason := strings.TrimSpace(unit.Outcome.Reason); reason != "" {
			return reason
		}
	}
	return "no terminal outcome was produced"
}

func (s *Service) finishDurableWorkUnit(ctx context.Context, workflowID domain.WorkflowID, outcome workUnitOutcome, credential domain.LeaseCredential, plan *domain.ExecutionPlan, seed durableRunSeed, state *graphExecutionState) (domain.WorkflowSnapshot, error) {
	unitID := domain.DurableWorkUnitID(outcome.unit.ID)
	if unitID == "" {
		return domain.WorkflowSnapshot{}, fmt.Errorf("durable work outcome is missing a work unit id")
	}
	return s.commitWorkflowTransition(ctx, workflowID, func(current domain.WorkflowSnapshot) (domain.WorkflowSnapshot, error) {
		status, result := durableOutcome(current, unitID, outcome)
		at := workflowActionTransitionTime(current)
		artifacts := append([]domain.RunArtifact(nil), outcome.artifacts...)
		newUnits, extraArtifacts, err := s.durableDynamicDiff(ctx, current, outcome, plan, seed, state)
		if err != nil {
			return domain.WorkflowSnapshot{}, err
		}
		artifacts = append(artifacts, extraArtifacts...)
		result.ArtifactRefs = durableArtifactRefs(artifacts)
		result.ActionIDs = durableOutcomeActionIDs(current, unitID)
		return domain.FinishUnit(current, domain.FinishUnitInput{
			ExpectedRevision: current.Workflow.Revision,
			UnitID:           unitID,
			Status:           status,
			Outcome:          result,
			NewUnits:         newUnits,
			Artifacts:        artifacts,
			Credential:       credential,
			At:               at,
		})
	}, func(snapshot domain.WorkflowSnapshot) bool {
		unit, ok := durableSnapshotUnit(snapshot, unitID)
		return ok && (unit.Status == domain.DurableWorkUnitStatusSucceeded || unit.Status == domain.DurableWorkUnitStatusFailed || unit.Status == domain.DurableWorkUnitStatusNeedsAttention || unit.Status == domain.DurableWorkUnitStatusSkipped)
	})
}

func (s *Service) durableDynamicDiff(ctx context.Context, snapshot domain.WorkflowSnapshot, outcome workUnitOutcome, plan *domain.ExecutionPlan, seed durableRunSeed, state *graphExecutionState) ([]domain.DurableWorkUnit, []domain.RunArtifact, error) {
	if outcome.err != nil || outcome.skipped {
		return nil, nil, nil
	}
	run, err := durableProjectedRun(snapshot, seed)
	if err != nil {
		return nil, nil, err
	}
	beforeUnits := len(run.WorkUnits)
	markWorkUnitStatus(run, outcome.unit.ID, "done")
	run.Artifacts = append(run.Artifacts, outcome.artifacts...)
	afterOutcomeArtifacts := len(run.Artifacts)
	if outcome.verification != nil {
		run.Verification = append(run.Verification, *outcome.verification)
	}
	if outcome.appendMessage {
		run.Messages = evidenceMessages(run.Messages, agentResultEvidence(outcome.unit, outcome.result.Message))
	}
	durableHydrateExecutionState(snapshot, state)

	switch outcome.unit.Kind {
	case "primary":
		if len(plan.Verify) == 0 && plan.Finalize == nil {
			if artifact := newFinalResponseArtifact(run, outcome.unit.Phase, outcome.result.Message); artifact.ID != "" {
				run.Artifacts = append(run.Artifacts, artifact)
			}
		}
	case "preparation":
		s.ensurePreparationEvidence(run)
	case "verification":
		if err := s.resolveVerificationAttempt(ctx, run, plan, maxInt(1, outcome.unit.Attempt), state); err != nil {
			return nil, nil, err
		}
		if len(run.WorkUnits) == beforeUnits && plan.Finalize == nil {
			execution := latestExecutionForAttempt(state.latestExecution, maxInt(1, outcome.unit.Attempt))
			if artifact := newFinalResponseArtifact(run, outcome.unit.Phase, execution.Message); artifact.ID != "" {
				run.Artifacts = append(run.Artifacts, artifact)
			}
		}
	case "recovery":
		attempt := maxInt(1, outcome.unit.Attempt)
		if len(plan.Verify) > 0 {
			s.appendVerificationUnits(run, plan, attempt, []string{outcome.unit.ID})
		} else {
			s.appendFinalizeUnit(run, plan, []string{outcome.unit.ID}, attempt, state)
			if plan.Finalize == nil {
				if artifact := newFinalResponseArtifact(run, outcome.unit.Phase, outcome.result.Message); artifact.ID != "" {
					run.Artifacts = append(run.Artifacts, artifact)
				}
			}
		}
	case "finalize":
		if artifact := newFinalResponseArtifact(run, outcome.unit.Phase, outcome.result.Message); artifact.ID != "" {
			run.Artifacts = append(run.Artifacts, artifact)
		}
	}

	units, err := durableWorkUnitDiff(snapshot.Workflow.ID, run.WorkUnits[beforeUnits:])
	if err != nil {
		return nil, nil, err
	}
	return units, run.Artifacts[afterOutcomeArtifacts:], nil
}

func durableOutcome(snapshot domain.WorkflowSnapshot, unitID domain.DurableWorkUnitID, outcome workUnitOutcome) (domain.DurableWorkUnitStatus, domain.DurableWorkUnitOutcome) {
	for _, action := range snapshot.Actions {
		if action.WorkUnitID == unitID && action.Status == domain.ActionStatusAmbiguous && action.SideEffectClass != domain.SideEffectNone {
			reason := strings.TrimSpace(action.Reason)
			if reason == "" {
				reason = "mutating action outcome is ambiguous"
			}
			return domain.DurableWorkUnitStatusNeedsAttention, domain.DurableWorkUnitOutcome{Reason: reason}
		}
	}
	if outcome.err != nil {
		return domain.DurableWorkUnitStatusFailed, domain.DurableWorkUnitOutcome{Reason: outcome.err.Error()}
	}
	if reason := strings.TrimSpace(outcome.needsAttention); reason != "" {
		return domain.DurableWorkUnitStatusNeedsAttention, domain.DurableWorkUnitOutcome{Reason: reason}
	}
	if outcome.skipped {
		reason := strings.TrimSpace(outcome.skippedSummary)
		if reason == "" {
			reason = "work unit did not execute"
		}
		return domain.DurableWorkUnitStatusFailed, domain.DurableWorkUnitOutcome{Reason: reason}
	}
	return domain.DurableWorkUnitStatusSucceeded, domain.DurableWorkUnitOutcome{}
}

func durableWorkUnitDiff(workflowID domain.WorkflowID, items []domain.WorkUnit) ([]domain.DurableWorkUnit, error) {
	units := make([]domain.DurableWorkUnit, 0, len(items))
	for _, item := range items {
		unit, err := domain.NewDurableWorkUnit(domain.DurableWorkUnitInput{
			ID:                domain.DurableWorkUnitID(item.ID),
			WorkflowID:        workflowID,
			Kind:              item.Kind,
			Phase:             item.Phase,
			Role:              item.Role,
			Task:              item.Task,
			Attempt:           item.Attempt,
			Source:            item.Source,
			SourceLimit:       sourceLimit(item.Source),
			SideEffectClass:   item.SideEffectClass,
			DuplicateKey:      item.DuplicateKey,
			InputArtifactRefs: item.ArtifactRefs,
			ReadSet:           item.ReadSet,
			WriteSet:          item.WriteSet,
			Dependencies:      durableDependencyIDs(item.DependsOn),
		})
		if err != nil {
			return nil, fmt.Errorf("dynamic work unit %q: %w", item.ID, err)
		}
		units = append(units, unit)
	}
	return units, nil
}

func durableProjectedRun(snapshot domain.WorkflowSnapshot, seed durableRunSeed) (*domain.RunState, error) {
	run, err := projectRunState(snapshot)
	if err != nil {
		return nil, err
	}
	run.Messages = cloneMessages(seed.messages)
	run.Profile = seed.profile
	run.EnabledCapabilities = append([]string(nil), seed.capabilities...)
	durableProjectReviewFindings(snapshot, run)
	for _, artifact := range snapshot.Artifacts {
		if artifact.Kind == "execution" || artifact.Kind == "recovery" {
			run.Messages = evidenceMessages(run.Messages, strings.Join([]string{
				"Agent result:",
				"agent_id: " + fallbackString(artifact.AgentID, "unknown"),
				"phase: " + string(artifact.Phase),
				"content:",
				artifact.Text,
			}, "\n"))
		}
	}
	return run, nil
}

func durableHydrateExecutionState(snapshot domain.WorkflowSnapshot, state *graphExecutionState) {
	if state == nil {
		return
	}
	if state.latestExecution == nil {
		state.latestExecution = map[int]domain.AgentResult{}
	}
	if state.mergedVerification == nil {
		state.mergedVerification = map[int]domain.VerificationResult{}
	}
	artifacts := make(map[string]domain.RunArtifact, len(snapshot.Artifacts))
	for _, artifact := range snapshot.Artifacts {
		artifacts[artifact.ID] = artifact
	}
	for _, unit := range snapshot.WorkUnits {
		if unit.Outcome == nil {
			continue
		}
		for _, ref := range unit.Outcome.ArtifactRefs {
			artifact, ok := artifacts[ref.ID]
			if !ok {
				continue
			}
			switch artifact.Kind {
			case "execution", "recovery":
				state.latestExecution[entryAttempt(unit.Attempt)] = domain.AgentResult{Message: domain.Message{Role: domain.RoleAssistant, AgentID: artifact.AgentID, Content: artifact.Text}}
			case "review_findings":
				var payload domain.ReviewFindingsArtifactPayload
				attempt := entryAttempt(unit.Attempt)
				if json.Unmarshal(artifact.Payload, &payload) == nil && !state.reportedAttempts[attempt] {
					state.mergedVerification[attempt] = payload.Result
				}
			}
		}
	}
}

func durableGraphExecutionState(snapshot domain.WorkflowSnapshot) graphExecutionState {
	state := graphExecutionState{latestExecution: map[int]domain.AgentResult{}, mergedVerification: map[int]domain.VerificationResult{}, reportedAttempts: map[int]bool{}}
	for _, artifact := range snapshot.Artifacts {
		if artifact.Kind != "test_report" {
			continue
		}
		var payload domain.TestReportArtifactPayload
		if json.Unmarshal(artifact.Payload, &payload) == nil {
			attempt := maxInt(1, payload.Attempt)
			state.reportedAttempts[attempt] = true
			results := make([]domain.VerificationResult, 0, len(payload.Entries))
			for _, entry := range payload.Entries {
				results = append(results, domain.VerificationResult{Attempt: attempt, SourceAgent: entry.AgentID, Status: entry.Status, Summary: entry.Summary, RepairBrief: entry.RepairBrief, ArtifactID: entry.ArtifactID, CreatedAt: artifact.CreatedAt})
			}
			if len(results) > 0 {
				state.mergedVerification[attempt] = mergeVerification(results, attempt)
			}
		}
	}
	durableHydrateExecutionState(snapshot, &state)
	return state
}

func durableProjectReviewFindings(snapshot domain.WorkflowSnapshot, run *domain.RunState) {
	if run == nil {
		return
	}
	known := make(map[string]struct{}, len(run.Verification))
	for _, result := range run.Verification {
		if result.ArtifactID != "" {
			known[result.ArtifactID] = struct{}{}
		}
	}
	artifacts := make(map[string]domain.RunArtifact, len(snapshot.Artifacts))
	for _, artifact := range snapshot.Artifacts {
		artifacts[artifact.ID] = artifact
	}
	for _, unit := range snapshot.WorkUnits {
		if unit.Outcome == nil {
			continue
		}
		for _, ref := range unit.Outcome.ArtifactRefs {
			artifact, ok := artifacts[ref.ID]
			if !ok || artifact.Kind != "review_findings" {
				continue
			}
			if _, exists := known[artifact.ID]; exists {
				continue
			}
			var payload domain.ReviewFindingsArtifactPayload
			if json.Unmarshal(artifact.Payload, &payload) != nil {
				continue
			}
			payload.Result.Attempt = entryAttempt(unit.Attempt)
			payload.Result.ArtifactID = artifact.ID
			if payload.Result.CreatedAt.IsZero() {
				payload.Result.CreatedAt = artifact.CreatedAt
			}
			run.Verification = append(run.Verification, payload.Result)
			known[artifact.ID] = struct{}{}
		}
	}
}

func durableWorkUnitProjection(unit domain.DurableWorkUnit) domain.WorkUnit {
	return domain.WorkUnit{
		ID: string(unit.ID), Kind: unit.Kind, Role: unit.Role, Phase: unit.Phase, Attempt: entryAttempt(unit.Attempt), Task: unit.Task,
		Status: projectedWorkUnitStatus(unit.Status), DependsOn: stringDependencyIDs(unit.Dependencies), ReadSet: append([]string(nil), unit.ReadSet...),
		WriteSet: append([]string(nil), unit.WriteSet...), Source: unit.Source, SideEffectClass: unit.SideEffectClass, DuplicateKey: unit.DuplicateKey,
		ArtifactRefs: projectedWorkUnitArtifactRefs(unit), StartedAt: unit.StartedAt, CompletedAt: unit.CompletedAt,
	}
}

func durableArtifactRefs(artifacts []domain.RunArtifact) []domain.ArtifactReference {
	refs := make([]domain.ArtifactReference, 0, len(artifacts))
	for _, artifact := range artifacts {
		if artifact.ID != "" {
			refs = append(refs, domain.ArtifactReference{ID: artifact.ID, Kind: artifact.Kind, Name: artifact.Name})
		}
	}
	return refs
}

func durableOutcomeActionIDs(snapshot domain.WorkflowSnapshot, unitID domain.DurableWorkUnitID) []domain.ActionID {
	ids := []domain.ActionID{}
	for _, action := range snapshot.Actions {
		if action.WorkUnitID == unitID {
			ids = append(ids, action.ID)
		}
	}
	return ids
}

func durableWorkflowTerminal(snapshot domain.WorkflowSnapshot) bool {
	return snapshot.Workflow.Status == domain.WorkflowStatusSucceeded || snapshot.Workflow.Status == domain.WorkflowStatusFailed || snapshot.Workflow.Status == domain.WorkflowStatusNeedsAttention
}

func (s *Service) settleDurableWorkflow(ctx context.Context, snapshot domain.WorkflowSnapshot) error {
	if durableWorkflowTerminal(snapshot) {
		return nil
	}
	_, err := s.commitWorkflowTransition(ctx, snapshot.Workflow.ID, func(current domain.WorkflowSnapshot) (domain.WorkflowSnapshot, error) {
		return domain.SettleWorkflow(current, domain.SettleWorkflowInput{
			ExpectedRevision: current.Workflow.Revision,
			FinalOutcomeRefs: durableFinalOutcomeRefs(current),
			At:               workflowActionTransitionTime(current),
		})
	}, durableWorkflowTerminal)
	return err
}

func durableFinalOutcomeRefs(snapshot domain.WorkflowSnapshot) []domain.ArtifactReference {
	for index := len(snapshot.WorkUnits) - 1; index >= 0; index-- {
		unit := snapshot.WorkUnits[index]
		if unit.Outcome == nil {
			continue
		}
		for _, ref := range unit.Outcome.ArtifactRefs {
			if ref.Kind == "final_response" {
				return []domain.ArtifactReference{ref}
			}
		}
	}
	return nil
}

func durableFinalResult(snapshot domain.WorkflowSnapshot) domain.AgentResult {
	for index := len(snapshot.WorkUnits) - 1; index >= 0; index-- {
		unit := snapshot.WorkUnits[index]
		if unit.Outcome == nil {
			continue
		}
		for _, ref := range unit.Outcome.ArtifactRefs {
			if ref.Kind != "final_response" {
				continue
			}
			for _, artifact := range snapshot.Artifacts {
				if artifact.ID == ref.ID {
					return domain.AgentResult{Status: "completed", Message: domain.Message{Role: domain.RoleAssistant, AgentID: artifact.AgentID, Content: artifact.Text}}
				}
			}
		}
	}
	return domain.AgentResult{}
}
