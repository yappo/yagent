package orchestrator

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"yagent/internal/domain"
)

func buildWorkflowIntentSnapshot(run *domain.RunState, seed durableRunSeed, at time.Time) (domain.WorkflowSnapshot, error) {
	if run == nil || run.WorkflowID == "" || run.ConversationID == "" || run.ConversationTurnID == "" {
		return domain.WorkflowSnapshot{}, fmt.Errorf("run identity is incomplete")
	}
	if run.CreatedAt.IsZero() || at.IsZero() || at.Before(run.CreatedAt) {
		return domain.WorkflowSnapshot{}, fmt.Errorf("valid workflow lifecycle times are required")
	}
	intentArtifacts := make([]domain.RunArtifact, 0, len(run.Artifacts)+1)
	for _, artifact := range run.Artifacts {
		// A caller may already hold an in-memory plan. It is intentionally not
		// persisted with the intent; the plan transition owns that publication.
		if artifact.Kind != "execution_plan" {
			intentArtifacts = append(intentArtifacts, artifact)
		}
	}
	artifacts, err := cloneWorkflowArtifacts(intentArtifacts)
	if err != nil {
		return domain.WorkflowSnapshot{}, err
	}
	inputArtifact := newWorkflowInputArtifact(run, domain.WorkflowInputArtifactPayload{
		Messages:            cloneMessages(seed.messages),
		Model:               seed.model,
		Profile:             seed.profile,
		EnabledCapabilities: append([]string(nil), seed.capabilities...),
		Stream:              seed.stream,
	})
	if err := domain.ValidateArtifactPayload(inputArtifact); err != nil {
		return domain.WorkflowSnapshot{}, err
	}
	artifacts = append(artifacts, inputArtifact)
	artifacts, err = cloneWorkflowArtifacts(artifacts)
	if err != nil {
		return domain.WorkflowSnapshot{}, err
	}
	workflow, err := domain.NewWorkflow(domain.WorkflowInput{
		ID:                run.WorkflowID,
		Conversation:      domain.ConversationReference{ConversationID: run.ConversationID, TurnID: run.ConversationTurnID},
		RootGoal:          run.UserGoal,
		InputArtifactRefs: []domain.ArtifactReference{{ID: inputArtifact.ID, Kind: inputArtifact.Kind, Name: inputArtifact.Name}},
		CreatedAt:         run.CreatedAt,
	})
	if err != nil {
		return domain.WorkflowSnapshot{}, err
	}
	workflow.UpdatedAt = at
	snapshot := domain.WorkflowSnapshot{Workflow: workflow, Artifacts: artifacts}
	if err := domain.ValidateWorkflowSnapshot(snapshot); err != nil {
		return domain.WorkflowSnapshot{}, err
	}
	return snapshot, nil
}

func buildWorkflowPlanAttachment(snapshot domain.WorkflowSnapshot, run *domain.RunState, at time.Time) (domain.AttachWorkflowPlanInput, error) {
	if run == nil || run.ExecutionPlan == nil || len(run.WorkUnits) == 0 {
		return domain.AttachWorkflowPlanInput{}, fmt.Errorf("planned run and work units are required")
	}
	if at.IsZero() || at.Before(snapshot.Workflow.UpdatedAt) {
		return domain.AttachWorkflowPlanInput{}, fmt.Errorf("plan attachment time is invalid")
	}
	graphRefs := latestWorkflowArtifactReference(run.Artifacts, "execution_plan")
	if len(graphRefs) != 1 {
		return domain.AttachWorkflowPlanInput{}, fmt.Errorf("execution plan artifact is required")
	}
	knownArtifacts := make(map[string]domain.RunArtifact, len(snapshot.Artifacts))
	for _, artifact := range snapshot.Artifacts {
		knownArtifacts[artifact.ID] = artifact
	}
	runArtifacts, err := cloneWorkflowArtifacts(run.Artifacts)
	if err != nil {
		return domain.AttachWorkflowPlanInput{}, err
	}
	artifacts := make([]domain.RunArtifact, 0)
	for _, artifact := range runArtifacts {
		if existing, ok := knownArtifacts[artifact.ID]; ok {
			if !reflect.DeepEqual(existing, artifact) {
				return domain.AttachWorkflowPlanInput{}, fmt.Errorf("artifact %q changed after workflow intent commit", artifact.ID)
			}
			continue
		}
		artifacts = append(artifacts, artifact)
	}
	units := make([]domain.DurableWorkUnit, 0, len(run.WorkUnits))
	seen := make(map[domain.DurableWorkUnitID]struct{}, len(run.WorkUnits))
	for _, item := range run.WorkUnits {
		if item.Status != "pending" {
			return domain.AttachWorkflowPlanInput{}, fmt.Errorf("planned work unit %q must be pending, got %q", item.ID, item.Status)
		}
		id := domain.DurableWorkUnitID(item.ID)
		if _, exists := seen[id]; exists {
			return domain.AttachWorkflowPlanInput{}, fmt.Errorf("duplicate planned work unit %q", id)
		}
		seen[id] = struct{}{}
		unit, err := domain.NewDurableWorkUnit(domain.DurableWorkUnitInput{
			ID: id, WorkflowID: snapshot.Workflow.ID, Kind: item.Kind, Phase: item.Phase, Role: item.Role, Task: item.Task,
			Attempt: item.Attempt, Source: item.Source, SourceLimit: sourceLimit(item.Source), SideEffectClass: item.SideEffectClass,
			DuplicateKey: item.DuplicateKey, InputArtifactRefs: item.ArtifactRefs, ReadSet: item.ReadSet, WriteSet: item.WriteSet,
			Dependencies: durableDependencyIDs(item.DependsOn),
		})
		if err != nil {
			return domain.AttachWorkflowPlanInput{}, fmt.Errorf("planned work unit %q: %w", item.ID, err)
		}
		units = append(units, unit)
	}
	return domain.AttachWorkflowPlanInput{
		ExpectedRevision: snapshot.Workflow.Revision, GraphArtifactRefs: graphRefs, WorkUnits: units, Artifacts: artifacts, At: at,
	}, nil
}

func buildInitialWorkflowSnapshot(run *domain.RunState, seed durableRunSeed, at time.Time) (domain.WorkflowSnapshot, error) {
	if run == nil || run.WorkflowID == "" || run.ConversationID == "" || run.ConversationTurnID == "" {
		return domain.WorkflowSnapshot{}, fmt.Errorf("run identity is incomplete")
	}
	if run.ExecutionPlan == nil || len(run.WorkUnits) == 0 || run.CreatedAt.IsZero() || at.IsZero() || at.Before(run.CreatedAt) {
		return domain.WorkflowSnapshot{}, fmt.Errorf("planned run, work units, and valid lifecycle times are required")
	}

	artifacts, err := cloneWorkflowArtifacts(run.Artifacts)
	if err != nil {
		return domain.WorkflowSnapshot{}, err
	}
	graphRefs := latestWorkflowArtifactReference(artifacts, "execution_plan")
	if len(graphRefs) == 0 {
		return domain.WorkflowSnapshot{}, fmt.Errorf("execution plan artifact is required")
	}
	inputArtifact := newWorkflowInputArtifact(run, domain.WorkflowInputArtifactPayload{
		Messages:            cloneMessages(seed.messages),
		Model:               seed.model,
		Profile:             seed.profile,
		EnabledCapabilities: append([]string(nil), seed.capabilities...),
		Stream:              seed.stream,
	})
	if err := domain.ValidateArtifactPayload(inputArtifact); err != nil {
		return domain.WorkflowSnapshot{}, err
	}
	artifacts = append(artifacts, inputArtifact)
	inputRefs := []domain.ArtifactReference{{ID: inputArtifact.ID, Kind: inputArtifact.Kind, Name: inputArtifact.Name}}

	unitIDs := make([]domain.DurableWorkUnitID, 0, len(run.WorkUnits))
	units := make([]domain.DurableWorkUnit, 0, len(run.WorkUnits))
	seen := make(map[domain.DurableWorkUnitID]struct{}, len(run.WorkUnits))
	for _, item := range run.WorkUnits {
		if item.Status != "pending" {
			return domain.WorkflowSnapshot{}, fmt.Errorf("initial work unit %q must be pending, got %q", item.ID, item.Status)
		}
		id := domain.DurableWorkUnitID(item.ID)
		if _, exists := seen[id]; exists {
			return domain.WorkflowSnapshot{}, fmt.Errorf("duplicate initial work unit %q", id)
		}
		seen[id] = struct{}{}
		unit, err := domain.NewDurableWorkUnit(domain.DurableWorkUnitInput{
			ID:                id,
			WorkflowID:        run.WorkflowID,
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
			return domain.WorkflowSnapshot{}, fmt.Errorf("initial work unit %q: %w", item.ID, err)
		}
		unitIDs = append(unitIDs, id)
		units = append(units, unit)
	}

	workflow, err := domain.NewWorkflow(domain.WorkflowInput{
		ID:                run.WorkflowID,
		Conversation:      domain.ConversationReference{ConversationID: run.ConversationID, TurnID: run.ConversationTurnID},
		RootGoal:          run.UserGoal,
		InputArtifactRefs: inputRefs,
		GraphArtifactRefs: graphRefs,
		WorkUnitIDs:       unitIDs,
		CreatedAt:         run.CreatedAt,
	})
	if err != nil {
		return domain.WorkflowSnapshot{}, err
	}
	workflow.UpdatedAt = at
	snapshot := domain.WorkflowSnapshot{Workflow: workflow, WorkUnits: units, Artifacts: artifacts}
	if err := domain.ValidateWorkflowSnapshot(snapshot); err != nil {
		return domain.WorkflowSnapshot{}, err
	}
	return snapshot, nil
}

func durableRunSeedFromSnapshot(snapshot domain.WorkflowSnapshot) (durableRunSeed, error) {
	artifacts := make(map[string]domain.RunArtifact, len(snapshot.Artifacts))
	for _, artifact := range snapshot.Artifacts {
		artifacts[artifact.ID] = artifact
	}
	for _, ref := range snapshot.Workflow.InputArtifactRefs {
		artifact, ok := artifacts[ref.ID]
		if !ok || artifact.Kind != "workflow_input" {
			continue
		}
		var payload domain.WorkflowInputArtifactPayload
		if err := json.Unmarshal(artifact.Payload, &payload); err != nil {
			return durableRunSeed{}, fmt.Errorf("decode workflow input artifact %q: %w", artifact.ID, err)
		}
		if len(payload.Messages) == 0 {
			return durableRunSeed{}, fmt.Errorf("workflow input artifact %q has no messages", artifact.ID)
		}
		return durableRunSeed{
			messages:     cloneMessages(payload.Messages),
			model:        payload.Model,
			profile:      payload.Profile,
			capabilities: append([]string(nil), payload.EnabledCapabilities...),
			stream:       payload.Stream,
		}, nil
	}
	return durableRunSeed{}, fmt.Errorf("workflow %q has no referenced workflow_input artifact", snapshot.Workflow.ID)
}

func projectRunState(snapshot domain.WorkflowSnapshot) (*domain.RunState, error) {
	if err := domain.ValidateWorkflowSnapshot(snapshot); err != nil {
		return nil, err
	}
	artifacts, err := cloneWorkflowArtifacts(snapshot.Artifacts)
	if err != nil {
		return nil, err
	}
	phase, attempt := projectedWorkflowPosition(snapshot)
	run := &domain.RunState{
		ID:                 string(snapshot.Workflow.ID),
		RootRunID:          string(snapshot.Workflow.ID),
		ConversationID:     snapshot.Workflow.Conversation.ConversationID,
		ConversationTurnID: snapshot.Workflow.Conversation.TurnID,
		WorkflowID:         snapshot.Workflow.ID,
		WorkflowRevision:   snapshot.Workflow.Revision,
		Status:             projectedRunStatus(snapshot.Workflow.Status),
		CurrentPhase:       phase,
		Attempt:            attempt,
		UserGoal:           snapshot.Workflow.RootGoal,
		Artifacts:          artifacts,
		CreatedAt:          snapshot.Workflow.CreatedAt,
		UpdatedAt:          snapshot.Workflow.UpdatedAt,
	}
	for _, item := range snapshot.WorkUnits {
		run.WorkUnits = append(run.WorkUnits, domain.WorkUnit{
			ID:              string(item.ID),
			Kind:            item.Kind,
			Role:            item.Role,
			Phase:           item.Phase,
			Attempt:         item.Attempt,
			Task:            item.Task,
			Status:          projectedWorkUnitStatus(item.Status),
			DependsOn:       stringDependencyIDs(item.Dependencies),
			ReadSet:         append([]string(nil), item.ReadSet...),
			WriteSet:        append([]string(nil), item.WriteSet...),
			Source:          item.Source,
			SideEffectClass: item.SideEffectClass,
			DuplicateKey:    item.DuplicateKey,
			ArtifactRefs:    projectedWorkUnitArtifactRefs(item),
			StartedAt:       item.StartedAt,
			CompletedAt:     item.CompletedAt,
		})
		if item.Outcome != nil && strings.TrimSpace(item.Outcome.Reason) != "" && (item.Status == domain.DurableWorkUnitStatusFailed || item.Status == domain.DurableWorkUnitStatusNeedsAttention) {
			run.KnownFailures = appendUnique(run.KnownFailures, item.Outcome.Reason)
		}
	}
	projectWorkflowArtifacts(run)
	run.Checkpoints = []domain.RunCheckpoint{{
		ID:        fmt.Sprintf("workflow-revision-%d", snapshot.Workflow.Revision),
		Phase:     run.CurrentPhase,
		Status:    run.Status,
		Attempt:   run.Attempt,
		Summary:   snapshot.Workflow.RootGoal,
		CreatedAt: snapshot.Workflow.UpdatedAt,
	}}
	return run, nil
}

func projectWorkflowArtifacts(run *domain.RunState) {
	for _, artifact := range run.Artifacts {
		switch artifact.Kind {
		case "workflow_input":
			var payload domain.WorkflowInputArtifactPayload
			if json.Unmarshal(artifact.Payload, &payload) == nil {
				run.Messages = cloneMessages(payload.Messages)
				run.Profile = payload.Profile
				run.EnabledCapabilities = append([]string(nil), payload.EnabledCapabilities...)
			}
		case "execution_plan":
			var payload domain.ExecutionPlanArtifactPayload
			if json.Unmarshal(artifact.Payload, &payload) == nil && payload.Plan != nil {
				run.ExecutionPlan = payload.Plan
				run.Plan = planNodesFromExecutionPlan(payload.Plan)
			}
		case "test_report":
			var payload domain.TestReportArtifactPayload
			if json.Unmarshal(artifact.Payload, &payload) != nil {
				continue
			}
			for _, entry := range payload.Entries {
				run.Verification = append(run.Verification, domain.VerificationResult{
					Attempt: entryAttempt(payload.Attempt), SourceAgent: entry.AgentID, Status: entry.Status,
					Summary: entry.Summary, RepairBrief: entry.RepairBrief, ArtifactID: fallbackString(entry.ArtifactID, artifact.ID), CreatedAt: artifact.CreatedAt,
				})
			}
			for _, failure := range payload.KnownFail {
				run.KnownFailures = appendUnique(run.KnownFailures, failure)
			}
		}
	}
}

func cloneWorkflowArtifacts(items []domain.RunArtifact) ([]domain.RunArtifact, error) {
	data, err := json.Marshal(items)
	if err != nil {
		return nil, fmt.Errorf("clone workflow artifacts: %w", err)
	}
	var cloned []domain.RunArtifact
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil, fmt.Errorf("clone workflow artifacts: %w", err)
	}
	return cloned, nil
}

func latestWorkflowArtifactReference(artifacts []domain.RunArtifact, kind string) []domain.ArtifactReference {
	for index := len(artifacts) - 1; index >= 0; index-- {
		if artifacts[index].Kind == kind && artifacts[index].ID != "" {
			return []domain.ArtifactReference{{ID: artifacts[index].ID, Kind: artifacts[index].Kind, Name: artifacts[index].Name}}
		}
	}
	return nil
}

func projectedWorkUnitArtifactRefs(unit domain.DurableWorkUnit) []domain.ArtifactReference {
	refs := append([]domain.ArtifactReference(nil), unit.InputArtifactRefs...)
	if unit.Outcome != nil {
		for _, ref := range unit.Outcome.ArtifactRefs {
			refs = appendArtifactRef(refs, ref)
		}
	}
	return refs
}

func projectedWorkflowPosition(snapshot domain.WorkflowSnapshot) (domain.RunPhase, int) {
	for _, unit := range snapshot.WorkUnits {
		if unit.Status == domain.DurableWorkUnitStatusLeased || unit.Status == domain.DurableWorkUnitStatusExecuting {
			return unit.Phase, entryAttempt(unit.Attempt)
		}
	}
	statuses := make(map[domain.DurableWorkUnitID]domain.DurableWorkUnitStatus, len(snapshot.WorkUnits))
	for _, unit := range snapshot.WorkUnits {
		statuses[unit.ID] = unit.Status
	}
	for _, unit := range snapshot.WorkUnits {
		if unit.Status != domain.DurableWorkUnitStatusPending {
			continue
		}
		ready := true
		for _, dependencyID := range unit.Dependencies {
			status := statuses[dependencyID]
			if status != domain.DurableWorkUnitStatusSucceeded && status != domain.DurableWorkUnitStatusSkipped {
				ready = false
				break
			}
		}
		if ready {
			return unit.Phase, entryAttempt(unit.Attempt)
		}
	}
	for index := len(snapshot.WorkUnits) - 1; index >= 0; index-- {
		unit := snapshot.WorkUnits[index]
		if unit.Status != domain.DurableWorkUnitStatusPending {
			return unit.Phase, entryAttempt(unit.Attempt)
		}
	}
	return domain.RunPhaseIntake, 1
}

func projectedRunStatus(status domain.WorkflowStatus) domain.RunStatus {
	switch status {
	case domain.WorkflowStatusSucceeded:
		return domain.RunStatusCompleted
	case domain.WorkflowStatusFailed:
		return domain.RunStatusFailed
	case domain.WorkflowStatusNeedsAttention:
		return domain.RunStatusNeedsAttention
	default:
		return domain.RunStatusRunning
	}
}

func projectedWorkUnitStatus(status domain.DurableWorkUnitStatus) string {
	switch status {
	case domain.DurableWorkUnitStatusLeased, domain.DurableWorkUnitStatusExecuting:
		return "running"
	case domain.DurableWorkUnitStatusSucceeded:
		return "done"
	case domain.DurableWorkUnitStatusSkipped:
		return "skipped"
	case domain.DurableWorkUnitStatusBlocked:
		return "blocked"
	case domain.DurableWorkUnitStatusFailed, domain.DurableWorkUnitStatusNeedsAttention:
		return "failed"
	default:
		return "pending"
	}
}

func durableDependencyIDs(items []string) []domain.DurableWorkUnitID {
	ids := make([]domain.DurableWorkUnitID, 0, len(items))
	for _, item := range items {
		ids = append(ids, domain.DurableWorkUnitID(item))
	}
	return ids
}

func stringDependencyIDs(items []domain.DurableWorkUnitID) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, string(item))
	}
	return ids
}

func sourceLimit(source string) int {
	if strings.TrimSpace(source) == "" {
		return 0
	}
	return 1
}

func entryAttempt(attempt int) int {
	if attempt < 1 {
		return 1
	}
	return attempt
}
