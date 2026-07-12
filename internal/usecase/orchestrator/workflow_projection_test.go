package orchestrator

import (
	"testing"
	"time"

	"yagent/internal/domain"
)

func TestBuildInitialWorkflowSnapshotAndProjectLifecycle(t *testing.T) {
	createdAt := time.Date(2026, time.July, 10, 17, 0, 0, 0, time.UTC)
	plan := &domain.ExecutionPlan{
		Version: "1", Mode: "direct", TaskKind: domain.TaskKindQuestion, Summary: "answer with evidence",
		Primary: domain.PlannedAgentAssignment{AgentID: "manager", Reason: "answer the user"},
	}
	run := &domain.RunState{
		ID: "run-1", RootRunID: "run-1", ConversationID: "conversation-1", ConversationTurnID: "turn-1", WorkflowID: "workflow-1",
		Status: domain.RunStatusRunning, CurrentPhase: domain.RunPhasePlan, Attempt: 1, UserGoal: "answer the question", Messages: []domain.Message{{Role: domain.RoleUser, Content: "answer the question"}}, ExecutionPlan: plan,
		Profile: "stored-profile", EnabledCapabilities: []string{"repo-read"},
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	run.Artifacts = []domain.RunArtifact{newExecutionPlanArtifact(run, domain.RunPhasePlan, "manager", plan)}
	run.WorkUnits = workUnitsFromExecutionPlan(run, plan)

	seed := durableRunSeed{messages: cloneMessages(run.Messages), model: "stored-model", profile: run.Profile, capabilities: append([]string(nil), run.EnabledCapabilities...), stream: true}
	snapshot, err := buildInitialWorkflowSnapshot(run, seed, createdAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("buildInitialWorkflowSnapshot() error = %v", err)
	}
	if snapshot.Workflow.Revision != 1 || snapshot.Workflow.UpdatedAt != createdAt.Add(time.Minute) || len(snapshot.Workflow.InputArtifactRefs) != 1 || len(snapshot.Workflow.GraphArtifactRefs) != 1 || len(snapshot.WorkUnits) != 1 {
		t.Fatalf("unexpected initial snapshot: %+v", snapshot)
	}
	run.Artifacts[0].Name = "changed after snapshot"
	if snapshot.Artifacts[0].Name == run.Artifacts[0].Name {
		t.Fatal("initial snapshot did not deep-copy artifacts")
	}

	lease := domain.DurableLease{OwnerID: "worker-1", Token: "lease-1", FencingToken: 1, ExpiresAt: createdAt.Add(10 * time.Minute)}
	snapshot, err = domain.ClaimReadyBatch(snapshot, domain.WorkflowBatchClaims{
		ExpectedRevision: snapshot.Workflow.Revision,
		Claims:           []domain.WorkUnitClaim{{UnitID: snapshot.WorkUnits[0].ID, Lease: lease}},
	}, createdAt.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = domain.StartClaimedBatch(snapshot, domain.WorkflowBatchCredentials{
		ExpectedRevision: snapshot.Workflow.Revision,
		Credentials:      []domain.WorkUnitCredential{{UnitID: snapshot.WorkUnits[0].ID, Credential: domain.LeaseCredential{Token: lease.Token, FencingToken: lease.FencingToken}}},
	}, createdAt.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = domain.FinishUnit(snapshot, domain.FinishUnitInput{
		ExpectedRevision: snapshot.Workflow.Revision,
		UnitID:           snapshot.WorkUnits[0].ID,
		Status:           domain.DurableWorkUnitStatusSucceeded,
		Outcome:          domain.DurableWorkUnitOutcome{},
		Credential:       domain.LeaseCredential{Token: lease.Token, FencingToken: lease.FencingToken},
		At:               createdAt.Add(4 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = domain.SettleWorkflow(snapshot, domain.SettleWorkflowInput{
		ExpectedRevision: snapshot.Workflow.Revision,
		At:               createdAt.Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}

	projected, err := projectRunState(snapshot)
	if err != nil {
		t.Fatalf("projectRunState() error = %v", err)
	}
	if projected.ID != string(snapshot.Workflow.ID) || projected.WorkflowRevision != 5 || projected.Status != domain.RunStatusCompleted || projected.CurrentPhase != domain.RunPhaseExecute {
		t.Fatalf("unexpected projected run: %+v", projected)
	}
	if projected.ExecutionPlan == nil || projected.ExecutionPlan.Summary != plan.Summary || len(projected.WorkUnits) != 1 || projected.WorkUnits[0].Status != "done" {
		t.Fatalf("projection did not rebuild plan/work units: %+v", projected)
	}
	if len(projected.Messages) != 1 || projected.Messages[0].Content != "answer the question" || projected.Profile != "stored-profile" || len(projected.EnabledCapabilities) != 1 {
		t.Fatalf("projection did not rebuild workflow input: %+v", projected)
	}
	if projected.WorkUnits[0].StartedAt.IsZero() || projected.WorkUnits[0].CompletedAt.IsZero() || projected.UpdatedAt != createdAt.Add(5*time.Minute) {
		t.Fatalf("projection did not preserve lifecycle timestamps: %+v", projected.WorkUnits[0])
	}
}

func TestBuildInitialWorkflowSnapshotRejectsMissingExecutionPlanArtifact(t *testing.T) {
	createdAt := time.Date(2026, time.July, 10, 17, 0, 0, 0, time.UTC)
	plan := &domain.ExecutionPlan{Version: "1", Mode: "direct", TaskKind: domain.TaskKindQuestion, Primary: domain.PlannedAgentAssignment{AgentID: "manager", Reason: "answer"}}
	run := &domain.RunState{
		ID: "run-1", RootRunID: "run-1", ConversationID: "conversation-1", ConversationTurnID: "turn-1", WorkflowID: "workflow-1",
		UserGoal: "answer", Messages: []domain.Message{{Role: domain.RoleUser, Content: "answer"}}, ExecutionPlan: plan, CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	run.WorkUnits = workUnitsFromExecutionPlan(run, plan)
	if _, err := buildInitialWorkflowSnapshot(run, durableRunSeed{messages: cloneMessages(run.Messages)}, createdAt.Add(time.Minute)); err == nil {
		t.Fatal("buildInitialWorkflowSnapshot() error = nil, want missing execution plan artifact rejection")
	}
}
