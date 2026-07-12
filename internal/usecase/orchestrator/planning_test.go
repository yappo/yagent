package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"yagent/internal/domain"
)

func TestPlannerDecisionSchemaUsesPhaseConstrainedAgentEnums(t *testing.T) {
	inventory := plannerDecisionTestInventory()
	schema := plannerDecisionSchema(inventory)
	properties := schema["properties"].(map[string]any)
	primary := properties["primary_agent_id"].(map[string]any)["enum"].([]string)
	preparation := properties["preparation_agent_ids"].(map[string]any)["items"].(map[string]any)["enum"].([]string)

	if strings.Join(primary, ",") != "coder,manager,researcher" {
		t.Fatalf("primary enum = %v", primary)
	}
	if strings.Join(preparation, ",") != "researcher" {
		t.Fatalf("preparation enum = %v", preparation)
	}
}

func TestExecutionPlanFromPlannerDecisionDerivesRuntimeOwnedGraph(t *testing.T) {
	plan, err := executionPlanFromPlannerDecision(plannerDecision{
		TaskKind: domain.TaskKindMutate, PrimaryAgentID: "coder", PreparationAgentIDs: []string{"researcher"},
	}, plannerDecisionTestInventory())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Plan == nil || plan.Plan.AgentID != "planner" || plan.Primary.AgentID != "coder" {
		t.Fatalf("runtime assignments were not derived: %+v", plan)
	}
	if len(plan.Preparation) != 1 || plan.Preparation[0].AgentID != "researcher" || len(plan.Verify) != 1 || plan.Verify[0].AgentID != "tester" {
		t.Fatalf("preparation/verification were not derived: %+v", plan)
	}
	if plan.Recovery == nil || plan.Recovery.AgentID != "coder" || plan.Finalize == nil || plan.Finalize.AgentID != "manager" || len(plan.Steps) == 0 {
		t.Fatalf("recovery/finalize/steps were not derived: %+v", plan)
	}
}

func TestRunPlanPhaseRejectsInvalidOutputWithoutRepairCall(t *testing.T) {
	model := &fakeModelClient{responses: map[string][]domain.ModelResponse{
		"planner": {{Message: domain.Message{Role: domain.RoleAssistant, Content: `{"version":"v1","task_kind":"research","plan":{"agent_id":"researcher"}}`}}},
	}}
	service := newTestService(model, &fakeToolExecutor{}, fakeCatalog{agents: map[string]domain.AgentSpec{
		"manager": {ID: "manager", Mode: domain.AgentModeManager},
		"planner": {ID: "planner", Mode: domain.AgentModeTool, ReadOnly: true},
	}}, Config{})
	now := time.Now()
	run := &domain.RunState{ID: "run-plan-reject", RootRunID: "run-plan-reject", Status: domain.RunStatusRunning, Messages: []domain.Message{{Role: domain.RoleUser, Content: "inspect the repo"}}, CreatedAt: now, UpdatedAt: now}
	inventory := []domain.AgentInventoryEntry{
		{AgentID: "manager", PreferredPhases: []domain.RunPhase{domain.RunPhaseExecute, domain.RunPhaseFinalize}},
		{AgentID: "planner", ReadOnly: true, PreferredPhases: []domain.RunPhase{domain.RunPhasePlan}},
	}

	plan, events, err := service.runPlanPhase(context.Background(), run, domain.TurnRequest{}, inventory)
	if err != nil {
		t.Fatal(err)
	}
	if model.indexes["planner"] != 1 {
		t.Fatalf("planner calls = %d, want 1", model.indexes["planner"])
	}
	if plan.Source != "fallback" || !strings.Contains(plan.FallbackReason, "unknown field") {
		t.Fatalf("fallback plan = %+v", plan)
	}
	if !hasEventType(events, "planner_output_rejected") {
		t.Fatalf("missing planner_output_rejected event: %+v", events)
	}
}

func plannerDecisionTestInventory() []domain.AgentInventoryEntry {
	return []domain.AgentInventoryEntry{
		{AgentID: "manager", TaskKinds: []domain.TaskKind{domain.TaskKindMutate}, PreferredPhases: []domain.RunPhase{domain.RunPhaseExecute, domain.RunPhaseFinalize}},
		{AgentID: "planner", ReadOnly: true, PreferredPhases: []domain.RunPhase{domain.RunPhasePlan}},
		{AgentID: "researcher", ReadOnly: true, PreferredPhases: []domain.RunPhase{domain.RunPhaseExecute}},
		{AgentID: "coder", TaskKinds: []domain.TaskKind{domain.TaskKindMutate}, PreferredPhases: []domain.RunPhase{domain.RunPhaseExecute, domain.RunPhaseRecover}, VerificationPolicy: domain.VerificationPolicy{Required: true, MaxAttempts: 2}},
		{AgentID: "tester", ReadOnly: true, TaskKinds: []domain.TaskKind{domain.TaskKindTest, domain.TaskKindMutate}, PreferredPhases: []domain.RunPhase{domain.RunPhaseVerify}},
	}
}
