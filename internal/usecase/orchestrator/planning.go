package orchestrator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"yagent/internal/domain"
)

func shouldBypassPlanner(prompt string) bool {
	return strings.TrimSpace(prompt) == ""
}

func (s *Service) buildAgentInventory() []domain.AgentInventoryEntry {
	agents := s.catalog.List()
	sort.Slice(agents, func(i, j int) bool {
		return agents[i].ID < agents[j].ID
	})
	inventory := make([]domain.AgentInventoryEntry, 0, len(agents))
	for _, agent := range agents {
		inventory = append(inventory, normalizedInventoryEntry(agent))
	}
	return inventory
}

func normalizedInventoryEntry(agent domain.AgentSpec) domain.AgentInventoryEntry {
	defaultTaskKinds, defaultCapabilities, defaultPreferredPhases, defaultScopeHints, verificationPolicy := builtInInventoryDefaults(agent)
	taskKinds := append([]domain.TaskKind(nil), agent.TaskKinds...)
	if len(taskKinds) == 0 {
		taskKinds = append(taskKinds, defaultTaskKinds...)
	}
	if len(taskKinds) == 0 {
		taskKinds = defaultInventoryTaskKinds(agent)
	}
	capabilities := append([]string(nil), agent.Capabilities...)
	if len(capabilities) == 0 {
		capabilities = append(capabilities, defaultCapabilities...)
	}
	if len(capabilities) == 0 {
		capabilities = defaultInventoryCapabilities(agent)
	}
	preferredPhases := append([]domain.RunPhase(nil), agent.PreferredPhases...)
	if len(preferredPhases) == 0 {
		preferredPhases = append(preferredPhases, defaultPreferredPhases...)
	}
	if len(preferredPhases) == 0 {
		preferredPhases = defaultInventoryPhases(agent, taskKinds)
	}
	scopeHints := append([]string(nil), agent.ScopeHints...)
	if len(scopeHints) == 0 {
		scopeHints = append(scopeHints, defaultScopeHints...)
	}
	if len(scopeHints) == 0 {
		scopeHints = defaultInventoryScopeHints(agent)
	}
	if agent.VerificationPolicy != (domain.VerificationPolicy{}) {
		verificationPolicy = agent.VerificationPolicy
	}
	if verificationPolicy == (domain.VerificationPolicy{}) {
		switch agent.ID {
		case "coder":
			verificationPolicy = domain.VerificationPolicy{Required: true, MaxAttempts: 2}
		}
	}
	return domain.AgentInventoryEntry{
		AgentID:            agent.ID,
		Name:               fallbackString(agent.Name, agent.ID),
		Description:        agent.Description,
		Mode:               agent.Mode,
		ReadOnly:           agent.ReadOnly,
		TaskKinds:          taskKinds,
		Capabilities:       capabilities,
		PreferredPhases:    preferredPhases,
		ScopeHints:         scopeHints,
		AllowedToolGroups:  inventoryToolGroups(agent),
		RoutingProfile:     agent.RoutingProfile,
		VerificationPolicy: verificationPolicy,
	}
}

func builtInInventoryDefaults(agent domain.AgentSpec) ([]domain.TaskKind, []string, []domain.RunPhase, []string, domain.VerificationPolicy) {
	switch agent.ID {
	case "manager":
		return []domain.TaskKind{domain.TaskKindQuestion, domain.TaskKindResearch, domain.TaskKindDocs, domain.TaskKindReview, domain.TaskKindTest, domain.TaskKindMutate},
			[]string{"coordination", "synthesis", "response"},
			[]domain.RunPhase{domain.RunPhaseExecute, domain.RunPhaseFinalize},
			[]string{"conversation", "handoff orchestration", "final response"},
			domain.VerificationPolicy{}
	case "planner":
		return []domain.TaskKind{domain.TaskKindQuestion, domain.TaskKindResearch, domain.TaskKindDocs, domain.TaskKindReview, domain.TaskKindTest, domain.TaskKindMutate},
			[]string{"planning", "decomposition"},
			[]domain.RunPhase{domain.RunPhasePlan},
			[]string{"execution planning", "agent selection"},
			domain.VerificationPolicy{}
	case "researcher":
		return []domain.TaskKind{domain.TaskKindQuestion, domain.TaskKindResearch, domain.TaskKindDocs},
			[]string{"inspection", "repository reading"},
			[]domain.RunPhase{domain.RunPhaseExecute},
			[]string{"file discovery", "focused context prep"},
			domain.VerificationPolicy{}
	case "coder":
		return []domain.TaskKind{domain.TaskKindMutate, domain.TaskKindDocs},
			[]string{"implementation", "workspace edits"},
			[]domain.RunPhase{domain.RunPhaseExecute, domain.RunPhaseRecover},
			[]string{"code changes", "repo updates"},
			domain.VerificationPolicy{Required: true, MaxAttempts: 2}
	case "tester":
		return []domain.TaskKind{domain.TaskKindTest, domain.TaskKindMutate},
			[]string{"verification", "task execution"},
			[]domain.RunPhase{domain.RunPhaseVerify},
			[]string{"regression checks", "validation"},
			domain.VerificationPolicy{}
	case "reviewer":
		return []domain.TaskKind{domain.TaskKindReview, domain.TaskKindMutate, domain.TaskKindDocs},
			[]string{"review", "risk assessment"},
			[]domain.RunPhase{domain.RunPhaseVerify},
			[]string{"bug finding", "regression review"},
			domain.VerificationPolicy{}
	default:
		return nil, nil, nil, nil, domain.VerificationPolicy{}
	}
}

func defaultInventoryTaskKinds(agent domain.AgentSpec) []domain.TaskKind {
	kinds := []domain.TaskKind{}
	switch agent.Mode {
	case domain.AgentModeManager:
		kinds = append(kinds, domain.TaskKindQuestion, domain.TaskKindResearch, domain.TaskKindDocs, domain.TaskKindReview, domain.TaskKindTest, domain.TaskKindMutate)
	case domain.AgentModeHandoff:
		kinds = append(kinds, domain.TaskKindMutate, domain.TaskKindDocs)
	default:
		if agent.ReadOnly {
			kinds = append(kinds, domain.TaskKindQuestion, domain.TaskKindResearch, domain.TaskKindDocs)
		} else {
			kinds = append(kinds, domain.TaskKindQuestion, domain.TaskKindResearch, domain.TaskKindDocs, domain.TaskKindMutate)
		}
	}
	for _, tag := range agent.Tags {
		switch strings.ToLower(tag) {
		case "docs", "readme", "documentation":
			kinds = append(kinds, domain.TaskKindDocs)
		case "review", "audit", "risk":
			kinds = append(kinds, domain.TaskKindReview)
		case "test", "verify", "validation", "regression":
			kinds = append(kinds, domain.TaskKindTest)
		case "research", "analysis", "inspect":
			kinds = append(kinds, domain.TaskKindResearch)
		case "code", "implement", "edit", "patch":
			kinds = append(kinds, domain.TaskKindMutate)
		}
	}
	return uniqueTaskKinds(kinds)
}

func defaultInventoryPhases(agent domain.AgentSpec, taskKinds []domain.TaskKind) []domain.RunPhase {
	phases := []domain.RunPhase{}
	switch agent.Mode {
	case domain.AgentModeManager:
		phases = append(phases, domain.RunPhaseExecute, domain.RunPhaseFinalize)
	case domain.AgentModeHandoff:
		phases = append(phases, domain.RunPhaseExecute, domain.RunPhaseRecover)
	default:
		phases = append(phases, domain.RunPhasePlan, domain.RunPhaseExecute, domain.RunPhaseVerify)
	}
	for _, kind := range taskKinds {
		switch kind {
		case domain.TaskKindReview, domain.TaskKindTest:
			phases = append(phases, domain.RunPhaseVerify)
		case domain.TaskKindMutate:
			phases = append(phases, domain.RunPhaseExecute, domain.RunPhaseRecover)
		case domain.TaskKindResearch, domain.TaskKindDocs, domain.TaskKindQuestion:
			phases = append(phases, domain.RunPhasePlan, domain.RunPhaseExecute)
		}
	}
	return uniqueRunPhases(phases)
}

func defaultInventoryCapabilities(agent domain.AgentSpec) []string {
	values := append([]string(nil), inventoryToolGroups(agent)...)
	switch agent.Mode {
	case domain.AgentModeManager:
		values = append(values, "coordination")
	case domain.AgentModeHandoff:
		values = append(values, "implementation")
	default:
		values = append(values, "analysis")
	}
	return uniqueStrings(values)
}

func defaultInventoryScopeHints(agent domain.AgentSpec) []string {
	values := append([]string(nil), agent.Tags...)
	if agent.ReadOnly {
		values = append(values, "read-only")
	}
	if agent.Mode == domain.AgentModeHandoff {
		values = append(values, "write-capable")
	}
	return uniqueStrings(values)
}

func inventoryToolGroups(agent domain.AgentSpec) []string {
	groups := []string{}
	for _, name := range agent.AllowedTools {
		switch {
		case strings.HasPrefix(name, "fs_"):
			groups = append(groups, "filesystem")
		case strings.HasPrefix(name, "git_"):
			groups = append(groups, "git")
		case strings.HasPrefix(name, "task_"):
			groups = append(groups, "task")
		case strings.HasPrefix(name, "mcp__"):
			groups = append(groups, "mcp")
		case name == "patch_apply":
			groups = append(groups, "patch")
		}
	}
	return uniqueStrings(groups)
}

func uniqueTaskKinds(values []domain.TaskKind) []domain.TaskKind {
	out := make([]domain.TaskKind, 0, len(values))
	seen := map[domain.TaskKind]struct{}{}
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func uniqueRunPhases(values []domain.RunPhase) []domain.RunPhase {
	out := make([]domain.RunPhase, 0, len(values))
	seen := map[domain.RunPhase]struct{}{}
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func inventoryIndex(inventory []domain.AgentInventoryEntry) map[string]domain.AgentInventoryEntry {
	index := make(map[string]domain.AgentInventoryEntry, len(inventory))
	for _, entry := range inventory {
		index[entry.AgentID] = entry
	}
	return index
}

func inventoryArtifactSummary(inventory []domain.AgentInventoryEntry) string {
	return prettyJSON(inventory)
}

func prettyJSON(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(data)
}

func plannerOutputContract() map[string]any {
	return map[string]any{
		"type":   "json_schema",
		"name":   "execution_plan",
		"schema": executionPlanSchema(),
		"strict": true,
		"notes": []string{
			"Return strict JSON only.",
			"Use agent ids exactly as listed in the inventory.",
			"Do not assign read-only agents as primary for mutate tasks.",
			"Keep reasons short and concrete.",
			"Use null for unused plan, recovery, or finalize assignments.",
			"Use empty arrays for unused preparation, verify, steps, or required_capabilities.",
		},
	}
}

func verificationOutputContract() map[string]any {
	return map[string]any{
		"type":   "json_schema",
		"name":   "verification_result",
		"schema": verificationSchema(),
		"strict": true,
	}
}

func finalResponseOutputContract() map[string]any {
	return map[string]any{
		"type":   "json_schema",
		"name":   "final_response",
		"schema": finalResponseSchema(),
		"strict": true,
		"notes": []string{
			"Return strict JSON only.",
			"The response field must be the complete user-facing answer.",
			"Use empty strings or empty arrays when a field does not apply.",
		},
	}
}

func repairOutputContract() map[string]any {
	return map[string]any{
		"goal": "Provide the repaired implementation result or precise remediation steps.",
	}
}

func executionPlanResponseFormat() *domain.ResponseFormat {
	return &domain.ResponseFormat{
		Type:   "json_schema",
		Name:   "execution_plan",
		Schema: executionPlanSchema(),
		Strict: true,
	}
}

func verificationResponseFormat() *domain.ResponseFormat {
	return &domain.ResponseFormat{
		Type:   "json_schema",
		Name:   "verification_result",
		Schema: verificationSchema(),
		Strict: true,
	}
}

func finalResponseResponseFormat() *domain.ResponseFormat {
	return &domain.ResponseFormat{
		Type:   "json_schema",
		Name:   "final_response",
		Schema: finalResponseSchema(),
		Strict: true,
	}
}

func executionPlanSchema() map[string]any {
	assignment := plannedAssignmentSchema()
	nullableAssignment := nullableObjectSchema(assignment)
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"version":               stringSchema("Execution plan schema version, usually v1."),
			"mode":                  enumSchema([]string{"direct", "assisted", "full"}, "Execution mode selected for the request."),
			"task_kind":             enumSchema([]string{"unknown", "casual", "question", "research", "docs", "review", "test", "mutate"}, "Classified task kind."),
			"summary":               stringSchema("Short summary of the routing decision."),
			"plan":                  nullableAssignment,
			"preparation":           arraySchema(plannedAssignmentSchema(), "Read-only preparation assignments."),
			"primary":               assignment,
			"verify":                arraySchema(plannedAssignmentSchema(), "Independent verification assignments."),
			"recovery":              nullableObjectSchema(plannedAssignmentSchema()),
			"finalize":              nullableObjectSchema(plannedAssignmentSchema()),
			"steps":                 arraySchema(plannedStepSchema(), "Ordered execution steps."),
			"required_capabilities": arraySchema(map[string]any{"type": "string"}, "Capability groups required by this plan."),
			"source":                stringSchema("Source of the plan, usually planner."),
			"fallback_reason":       stringSchema("Fallback reason, or empty string when not a fallback."),
		},
		"required": []string{
			"version",
			"mode",
			"task_kind",
			"summary",
			"plan",
			"preparation",
			"primary",
			"verify",
			"recovery",
			"finalize",
			"steps",
			"required_capabilities",
			"source",
			"fallback_reason",
		},
		"additionalProperties": false,
	}
}

func verificationSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status":       enumSchema([]string{"pass", "fail"}, "Overall verification status."),
			"summary":      stringSchema("One-sentence verification summary."),
			"repair_brief": stringSchema("Short actionable repair brief, or none."),
		},
		"required":             []string{"status", "summary", "repair_brief"},
		"additionalProperties": false,
	}
}

func finalResponseSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"response":             stringSchema("Complete user-facing final answer."),
			"summary":              stringSchema("One sentence summary of the outcome."),
			"verification_summary": stringSchema("Verification status or empty string."),
			"remaining_risks":      arraySchema(map[string]any{"type": "string"}, "Remaining risks, caveats, or blocked checks."),
			"next_steps":           arraySchema(map[string]any{"type": "string"}, "Concrete follow-up steps, or empty array."),
		},
		"required": []string{
			"response",
			"summary",
			"verification_summary",
			"remaining_risks",
			"next_steps",
		},
		"additionalProperties": false,
	}
}

func plannedAssignmentSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"agent_id": stringSchema("Agent id from the inventory."),
			"reason":   stringSchema("Short concrete reason for the assignment."),
		},
		"required":             []string{"agent_id", "reason"},
		"additionalProperties": false,
	}
}

func plannedStepSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":       stringSchema("Stable step id."),
			"title":    stringSchema("Short step title."),
			"phase":    enumSchema([]string{"intake", "plan", "execute", "verify", "recover", "finalize"}, "Execution phase."),
			"agent_id": stringSchema("Assigned agent id, or empty string when unassigned."),
		},
		"required":             []string{"id", "title", "phase", "agent_id"},
		"additionalProperties": false,
	}
}

func nullableObjectSchema(schema map[string]any) map[string]any {
	out := cloneSchemaMap(schema)
	out["type"] = []string{"object", "null"}
	return out
}

func arraySchema(items map[string]any, description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": description,
		"items":       items,
	}
}

func stringSchema(description string) map[string]any {
	return map[string]any{
		"type":        "string",
		"description": description,
	}
}

func enumSchema(values []string, description string) map[string]any {
	return map[string]any{
		"type":        "string",
		"description": description,
		"enum":        values,
	}
}

func cloneSchemaMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func buildInvocationInstructions(base string, context domain.RunContext) string {
	sections := []string{}
	if base = strings.TrimSpace(base); base != "" {
		sections = append(sections, base)
	}

	lines := []string{
		"Execution context",
		fmt.Sprintf("- current_phase: %s", fallbackString(string(context.CurrentPhase), "-")),
		fmt.Sprintf("- user_goal: %s", fallbackString(strings.TrimSpace(context.UserGoal), "-")),
		fmt.Sprintf("- task_brief: %s", fallbackString(strings.TrimSpace(context.TaskBrief), "-")),
	}
	if packetRole := strings.TrimSpace(context.PacketRole); packetRole != "" {
		lines = append(lines, "- packet_role: "+packetRole)
	}
	if packetKind := strings.TrimSpace(context.PacketKind); packetKind != "" {
		lines = append(lines, "- packet_kind: "+packetKind)
	}
	if context.PacketBudgetTokens > 0 {
		lines = append(lines, fmt.Sprintf("- packet_budget_tokens: %d", context.PacketBudgetTokens))
	}
	if context.PacketEstimatedTokens > 0 {
		lines = append(lines, fmt.Sprintf("- packet_estimated_tokens: %d", context.PacketEstimatedTokens))
	}
	if len(context.ScopedConstraints) > 0 {
		lines = append(lines, "- scoped_constraints: "+strings.Join(context.ScopedConstraints, "; "))
	}
	if len(context.KnownFailures) > 0 {
		lines = append(lines, "- known_failures: "+strings.Join(context.KnownFailures, " | "))
	}
	if len(context.RelevantFiles) > 0 {
		lines = append(lines, "- relevant_files: "+strings.Join(context.RelevantFiles, ", "))
	}
	if len(context.ArtifactRefs) > 0 {
		lines = append(lines, "- artifacts: "+strings.Join(context.ArtifactRefs, ", "))
	}
	if len(context.Artifacts) > 0 {
		lines = append(lines, "- artifact_refs: "+strings.Join(artifactReferenceNames(context.Artifacts), ", "))
	}
	if len(context.UnresolvedTODOs) > 0 {
		lines = append(lines, "- unresolved_todos: "+strings.Join(context.UnresolvedTODOs, "; "))
	}
	if len(context.RecentFailures) > 0 {
		lines = append(lines, "- recent_failures: "+strings.Join(context.RecentFailures, " | "))
	}
	if len(context.VerificationNotes) > 0 {
		lines = append(lines, "- verification_notes: "+strings.Join(context.VerificationNotes, " | "))
	}
	if len(context.StableFacts) > 0 {
		lines = append(lines, "- stable_facts: "+strings.Join(context.StableFacts, " | "))
	}
	if len(context.Observations) > 0 {
		lines = append(lines, "- reusable_observations: "+strings.Join(observationSummaries(context.Observations), " | "))
	}
	if len(context.AvailableToolNames) > 0 {
		lines = append(lines, "- available_tools: "+strings.Join(context.AvailableToolNames, ", "))
	}
	sections = append(sections, strings.Join(lines, "\n"))

	if context.ToolState.CurrentAgentID != "" {
		sections = append(sections, "Tool state:\n"+prettyJSON(context.ToolState))
	}
	if hints := toolWorkflowHints(context.ToolState); len(hints) > 0 {
		sections = append(sections, "Workflow hints:\n- "+strings.Join(hints, "\n- "))
	}
	if len(context.ExpectedOutput) > 0 {
		sections = append(sections, "Expected output contract:\n"+prettyJSON(context.ExpectedOutput))
	}
	if len(context.AgentInventory) > 0 {
		sections = append(sections, "Agent inventory:\n"+prettyJSON(context.AgentInventory))
	}
	return strings.Join(sections, "\n\n")
}

func toolWorkflowHints(state domain.ToolState) []string {
	hints := []string{}
	if state.TaskDiscoveryAvailable && state.MCPToolsLazyBind {
		hints = append(hints, `No visible mcp__* tools does not necessarily mean MCP is unavailable. If MCP seems relevant, call task_list, inspect kind="mcp_server", and bind a relevant unbound server before concluding MCP cannot be used.`)
	}
	if state.MCPBindingAvailable {
		hints = append(hints, "After task_bind succeeds, use the returned tool_names directly on your next tool call.")
	}
	if len(state.VisibleWriteTools) > 0 {
		hints = append(hints, "If file edits are needed and fs_write is visible, call fs_write directly. The approval dialog will be shown automatically when the write tool runs.")
	} else if state.WriteCapabilityAvailable && len(state.HiddenWriteCapabilities) > 0 {
		hints = append(hints, "This agent can write files, but the write tools are currently hidden behind capability gating. If edits are needed, call enable_capability with one of hidden_write_capabilities, then call fs_write or patch_apply directly.")
		hints = append(hints, "Do not ask the user in a normal assistant reply to grant file_write_allowed or write permission. Running the write tool should trigger the approval dialog automatically.")
	}
	if state.ReadOnly {
		hints = append(hints, "This agent is read-only. If file writes are required, delegate or handoff to a write-capable agent instead of saying the write tool does not exist.")
	}
	return hints
}

func parseExecutionPlan(content string) (*domain.ExecutionPlan, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, fmt.Errorf("execution plan JSON が空です")
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	var plan domain.ExecutionPlan
	if err := decoder.Decode(&plan); err != nil {
		return nil, err
	}
	if decoder.More() {
		return nil, fmt.Errorf("execution plan JSON の後ろに余分なデータがあります")
	}
	return &plan, nil
}

func validateAndNormalizeExecutionPlan(plan *domain.ExecutionPlan, inventory []domain.AgentInventoryEntry) error {
	if plan == nil {
		return fmt.Errorf("execution plan がありません")
	}
	index := inventoryIndex(inventory)
	if plan.Version == "" {
		plan.Version = "v1"
	}
	if plan.TaskKind == "" {
		return fmt.Errorf("task_kind が空です")
	}
	if plan.Plan == nil {
		if _, ok := index["planner"]; ok {
			plan.Plan = &domain.PlannedAgentAssignment{AgentID: "planner", Reason: "Build the execution plan."}
		}
	}
	plan.Preparation = compactAssignments(plan.Preparation)
	plan.Verify = compactAssignments(plan.Verify)
	plan.RequiredCapabilities = uniqueStrings(plan.RequiredCapabilities)

	if err := validateAssignment(plan.Plan, domain.RunPhasePlan, index); err != nil {
		return err
	}
	for _, item := range plan.Preparation {
		value := item
		if err := validateAssignment(&value, domain.RunPhaseExecute, index); err != nil {
			return err
		}
	}
	if strings.TrimSpace(plan.Primary.AgentID) == "" {
		return fmt.Errorf("primary agent が空です")
	}
	if err := validateAssignment(&plan.Primary, domain.RunPhaseExecute, index); err != nil {
		return err
	}
	if plan.TaskKind == domain.TaskKindMutate && index[plan.Primary.AgentID].ReadOnly {
		return fmt.Errorf("mutate task に read-only primary %q は使えません", plan.Primary.AgentID)
	}

	if len(plan.Verify) == 0 && requiresVerification(plan, index[plan.Primary.AgentID]) {
		plan.Verify = defaultVerifyAssignments(plan.TaskKind, inventory)
	}
	for _, item := range plan.Verify {
		value := item
		if err := validateAssignment(&value, domain.RunPhaseVerify, index); err != nil {
			return err
		}
	}
	if plan.Recovery == nil && len(plan.Verify) > 0 {
		plan.Recovery = defaultRecoveryAssignment(plan.Primary, inventory)
	}
	if err := validateAssignment(plan.Recovery, domain.RunPhaseRecover, index); err != nil {
		return err
	}
	if plan.Finalize == nil && plan.Primary.AgentID != "" && plan.Primary.AgentID != "manager" {
		if _, ok := index["manager"]; ok {
			plan.Finalize = &domain.PlannedAgentAssignment{AgentID: "manager", Reason: "Summarize the completed work for the user."}
		}
	}
	if err := validateAssignment(plan.Finalize, domain.RunPhaseFinalize, index); err != nil {
		return err
	}
	if len(plan.Steps) == 0 {
		plan.Steps = plannedStepsFromExecutionPlan(plan)
	}
	if len(plan.Steps) == 0 {
		return fmt.Errorf("execution steps が空です")
	}
	if err := validatePlannedSteps(plan.Steps, index); err != nil {
		return err
	}
	if plan.Mode == "" {
		plan.Mode = resolveExecutionPlanMode(plan)
	}
	if plan.Summary == "" {
		plan.Summary = summarizeExecutionPlan(plan)
	}
	if plan.Source == "" {
		plan.Source = "planner"
	}
	return nil
}

func compactAssignments(items []domain.PlannedAgentAssignment) []domain.PlannedAgentAssignment {
	out := make([]domain.PlannedAgentAssignment, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		item.AgentID = strings.TrimSpace(item.AgentID)
		if item.AgentID == "" {
			continue
		}
		if _, ok := seen[item.AgentID]; ok {
			continue
		}
		seen[item.AgentID] = struct{}{}
		out = append(out, item)
	}
	return out
}

func validateAssignment(item *domain.PlannedAgentAssignment, phase domain.RunPhase, index map[string]domain.AgentInventoryEntry) error {
	if item == nil {
		return nil
	}
	item.AgentID = strings.TrimSpace(item.AgentID)
	if item.AgentID == "" {
		return fmt.Errorf("%s assignment の agent_id が空です", phase)
	}
	entry, ok := index[item.AgentID]
	if !ok {
		return fmt.Errorf("%s assignment の agent %q が inventory にありません", phase, item.AgentID)
	}
	if len(entry.PreferredPhases) > 0 && !supportsPhase(entry, phase) {
		return fmt.Errorf("agent %q は phase %q に適していません", item.AgentID, phase)
	}
	return nil
}

func validatePlannedSteps(steps []domain.PlannedStep, index map[string]domain.AgentInventoryEntry) error {
	for _, step := range steps {
		if strings.TrimSpace(step.Title) == "" {
			return fmt.Errorf("execution step title が空です")
		}
		if step.AgentID == "" {
			continue
		}
		entry, ok := index[step.AgentID]
		if !ok {
			return fmt.Errorf("step agent %q が inventory にありません", step.AgentID)
		}
		if len(entry.PreferredPhases) > 0 && !supportsPhase(entry, step.Phase) {
			return fmt.Errorf("step agent %q は phase %q に適していません", step.AgentID, step.Phase)
		}
	}
	return nil
}

func requiresVerification(plan *domain.ExecutionPlan, primary domain.AgentInventoryEntry) bool {
	if plan == nil {
		return false
	}
	if plan.TaskKind == domain.TaskKindMutate {
		return true
	}
	if (plan.TaskKind == domain.TaskKindReview || plan.TaskKind == domain.TaskKindTest) && !supportsPhase(primary, domain.RunPhaseVerify) {
		return true
	}
	return primary.VerificationPolicy.Required
}

func defaultVerifyAssignments(kind domain.TaskKind, inventory []domain.AgentInventoryEntry) []domain.PlannedAgentAssignment {
	assignments := []domain.PlannedAgentAssignment{}
	switch kind {
	case domain.TaskKindReview:
		if agentID := selectFallbackAgent(inventory, domain.TaskKindReview, domain.RunPhaseVerify, false, "reviewer"); agentID != "" {
			assignments = append(assignments, domain.PlannedAgentAssignment{AgentID: agentID, Reason: "Review findings and regression risk."})
		}
	case domain.TaskKindTest:
		if agentID := selectFallbackAgent(inventory, domain.TaskKindTest, domain.RunPhaseVerify, false, "tester"); agentID != "" {
			assignments = append(assignments, domain.PlannedAgentAssignment{AgentID: agentID, Reason: "Verify behavior against the task."})
		}
	default:
		if agentID := selectFallbackAgent(inventory, domain.TaskKindTest, domain.RunPhaseVerify, false, "tester"); agentID != "" {
			assignments = append(assignments, domain.PlannedAgentAssignment{AgentID: agentID, Reason: "Run validation and regression checks."})
		}
		if agentID := selectFallbackAgent(inventory, domain.TaskKindReview, domain.RunPhaseVerify, false, "reviewer"); agentID != "" {
			assignments = append(assignments, domain.PlannedAgentAssignment{AgentID: agentID, Reason: "Review changes for bugs and risk."})
		}
	}
	return compactAssignments(assignments)
}

func defaultRecoveryAssignment(primary domain.PlannedAgentAssignment, inventory []domain.AgentInventoryEntry) *domain.PlannedAgentAssignment {
	if primary.AgentID != "" {
		if entry, ok := inventoryIndex(inventory)[primary.AgentID]; ok && !entry.ReadOnly {
			return &domain.PlannedAgentAssignment{AgentID: primary.AgentID, Reason: "Repair the implementation from the latest verification brief."}
		}
	}
	if agentID := selectFallbackAgent(inventory, domain.TaskKindMutate, domain.RunPhaseRecover, true, "coder"); agentID != "" {
		return &domain.PlannedAgentAssignment{AgentID: agentID, Reason: "Repair the implementation from the latest verification brief."}
	}
	if hasAgent(inventory, "manager") {
		return &domain.PlannedAgentAssignment{AgentID: "manager", Reason: "Fallback repair owner."}
	}
	return nil
}

func plannedStepsFromExecutionPlan(plan *domain.ExecutionPlan) []domain.PlannedStep {
	steps := []domain.PlannedStep{}
	appendStep := func(title string, phase domain.RunPhase, agentID string) {
		if agentID == "" {
			return
		}
		steps = append(steps, domain.PlannedStep{
			ID:      fmt.Sprintf("step-%d", len(steps)+1),
			Title:   title,
			Phase:   phase,
			AgentID: agentID,
		})
	}
	if plan.Plan != nil {
		appendStep("Create execution plan", domain.RunPhasePlan, plan.Plan.AgentID)
	}
	for _, item := range plan.Preparation {
		appendStep("Prepare focused context", domain.RunPhaseExecute, item.AgentID)
	}
	appendStep("Execute primary task", domain.RunPhaseExecute, plan.Primary.AgentID)
	for _, item := range plan.Verify {
		appendStep("Verify latest result", domain.RunPhaseVerify, item.AgentID)
	}
	if plan.Recovery != nil {
		appendStep("Repair from verification brief", domain.RunPhaseRecover, plan.Recovery.AgentID)
	}
	if plan.Finalize != nil {
		appendStep("Summarize the completed work", domain.RunPhaseFinalize, plan.Finalize.AgentID)
	}
	return steps
}

func resolveExecutionPlanMode(plan *domain.ExecutionPlan) string {
	if plan == nil {
		return "direct"
	}
	if plan.Plan == nil && len(plan.Preparation) == 0 && len(plan.Verify) == 0 && plan.Finalize == nil && plan.Primary.AgentID == "manager" {
		return "direct"
	}
	if plan.Plan != nil || len(plan.Verify) > 0 || plan.Recovery != nil || plan.Finalize != nil {
		return "full"
	}
	return "assisted"
}

func summarizeExecutionPlan(plan *domain.ExecutionPlan) string {
	if plan == nil {
		return ""
	}
	parts := []string{
		"mode=" + fallbackString(plan.Mode, resolveExecutionPlanMode(plan)),
		"task=" + fallbackString(string(plan.TaskKind), string(domain.TaskKindQuestion)),
		"primary=" + fallbackString(plan.Primary.AgentID, "manager"),
	}
	if plan.Plan != nil && plan.Plan.AgentID != "" {
		parts = append(parts, "plan="+plan.Plan.AgentID)
	}
	if len(plan.Preparation) > 0 {
		ids := make([]string, 0, len(plan.Preparation))
		for _, item := range plan.Preparation {
			ids = append(ids, item.AgentID)
		}
		parts = append(parts, "prep="+strings.Join(ids, ","))
	}
	if len(plan.Verify) > 0 {
		ids := make([]string, 0, len(plan.Verify))
		for _, item := range plan.Verify {
			ids = append(ids, item.AgentID)
		}
		parts = append(parts, "verify="+strings.Join(ids, ","))
	}
	return strings.Join(parts, " ")
}

func planNodesFromExecutionPlan(plan *domain.ExecutionPlan) []domain.PlanNode {
	if plan == nil || len(plan.Steps) == 0 {
		return nil
	}
	nodes := make([]domain.PlanNode, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		description := fallbackString(step.AgentID, "")
		nodes = append(nodes, domain.PlanNode{
			ID:          step.ID,
			Title:       step.Title,
			Description: description,
			Status:      "pending",
			CreatedAt:   time.Now(),
		})
	}
	return nodes
}

func markPlanNodeStatus(run *domain.RunState, phase domain.RunPhase, agentID string, status string) {
	if run == nil {
		return
	}
	for idx := range run.Plan {
		stepAgentID := strings.TrimSpace(run.Plan[idx].Description)
		if stepAgentID != "" && stepAgentID != agentID {
			continue
		}
		if run.ExecutionPlan != nil && idx < len(run.ExecutionPlan.Steps) && run.ExecutionPlan.Steps[idx].Phase != phase {
			continue
		}
		run.Plan[idx].Status = status
	}
}

func directConversationPlan(prompt string) *domain.ExecutionPlan {
	plan := &domain.ExecutionPlan{
		Version:  "v1",
		Mode:     "direct",
		TaskKind: domain.TaskKindCasual,
		Summary:  "Obvious casual turn; answer directly with manager.",
		Primary:  domain.PlannedAgentAssignment{AgentID: "manager", Reason: "Reply directly to the user."},
		Source:   "local_bypass",
	}
	plan.Steps = plannedStepsFromExecutionPlan(plan)
	return plan
}

func disabledHarnessExecutionPlan(prompt string) *domain.ExecutionPlan {
	kind := domain.TaskKindUnknown
	if strings.TrimSpace(prompt) == "" {
		kind = domain.TaskKindCasual
	}
	plan := &domain.ExecutionPlan{
		Version:  "v1",
		Mode:     "direct",
		TaskKind: kind,
		Summary:  "Phase harness disabled; manager handles the request directly.",
		Primary:  domain.PlannedAgentAssignment{AgentID: "manager", Reason: "Fallback direct execution while harness is disabled."},
		Source:   "disabled_harness",
	}
	plan.Steps = plannedStepsFromExecutionPlan(plan)
	return plan
}

func buildFallbackExecutionPlan(inventory []domain.AgentInventoryEntry, reason string) *domain.ExecutionPlan {
	plan := &domain.ExecutionPlan{
		Version:        "v1",
		TaskKind:       domain.TaskKindUnknown,
		Source:         "fallback",
		FallbackReason: strings.TrimSpace(reason),
		Primary:        domain.PlannedAgentAssignment{AgentID: fallbackString(selectKnownAgent(inventory, "manager"), "manager"), Reason: "Planner was unavailable, so answer conservatively without prompt classification."},
		Summary:        "Planner fallback: skip prompt-side classification and default to manager direct execution.",
	}
	plan.Mode = "direct"
	plan.Steps = plannedStepsFromExecutionPlan(plan)
	return plan
}

func optionalAssignment(agentID string, reason string) *domain.PlannedAgentAssignment {
	if strings.TrimSpace(agentID) == "" {
		return nil
	}
	return &domain.PlannedAgentAssignment{AgentID: agentID, Reason: reason}
}

func defaultFallbackPlanReason(err error) string {
	if err == nil {
		return "planner output was unavailable"
	}
	return err.Error()
}

func selectFallbackAgent(inventory []domain.AgentInventoryEntry, kind domain.TaskKind, phase domain.RunPhase, requireWrite bool, preferredIDs ...string) string {
	index := inventoryIndex(inventory)
	for _, id := range preferredIDs {
		entry, ok := index[id]
		if ok && compatibleInventoryEntry(entry, kind, phase, requireWrite) {
			return id
		}
	}
	type candidate struct {
		agentID string
		score   int
	}
	candidates := []candidate{}
	for _, entry := range inventory {
		if entry.AgentID == "manager" {
			continue
		}
		if !compatibleInventoryEntry(entry, kind, phase, requireWrite) {
			continue
		}
		score := 0
		if !isBuiltInAgentID(entry.AgentID) {
			score += 8
		}
		if supportsTaskKind(entry, kind) {
			score += 6
		}
		if supportsPhase(entry, phase) {
			score += 4
		}
		if requireWrite && !entry.ReadOnly {
			score += 4
		}
		if phase == domain.RunPhaseVerify && entry.ReadOnly {
			score += 2
		}
		candidates = append(candidates, candidate{agentID: entry.AgentID, score: score})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].agentID < candidates[j].agentID
		}
		return candidates[i].score > candidates[j].score
	})
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0].agentID
}

func selectKnownAgent(inventory []domain.AgentInventoryEntry, agentID string) string {
	if hasAgent(inventory, agentID) {
		return agentID
	}
	return ""
}

func compatibleInventoryEntry(entry domain.AgentInventoryEntry, kind domain.TaskKind, phase domain.RunPhase, requireWrite bool) bool {
	if requireWrite && entry.ReadOnly {
		return false
	}
	if kind != "" && !supportsTaskKind(entry, kind) {
		if !(kind == domain.TaskKindQuestion && supportsTaskKind(entry, domain.TaskKindResearch)) {
			return false
		}
	}
	if len(entry.PreferredPhases) > 0 && !supportsPhase(entry, phase) {
		return false
	}
	return true
}

func supportsTaskKind(entry domain.AgentInventoryEntry, kind domain.TaskKind) bool {
	if kind == "" {
		return true
	}
	for _, value := range entry.TaskKinds {
		if value == kind {
			return true
		}
	}
	return false
}

func supportsPhase(entry domain.AgentInventoryEntry, phase domain.RunPhase) bool {
	if phase == "" || len(entry.PreferredPhases) == 0 {
		return true
	}
	for _, value := range entry.PreferredPhases {
		if value == phase {
			return true
		}
	}
	return false
}

func hasAgent(inventory []domain.AgentInventoryEntry, agentID string) bool {
	for _, entry := range inventory {
		if entry.AgentID == agentID {
			return true
		}
	}
	return false
}

func isBuiltInAgentID(agentID string) bool {
	switch agentID {
	case "manager", "planner", "researcher", "coder", "tester", "reviewer":
		return true
	default:
		return false
	}
}

func repairPromptForPlan(raw string, parseErr error) string {
	var detail string
	if parseErr != nil {
		detail = parseErr.Error()
	}
	return strings.TrimSpace(strings.Join([]string{
		"Return a corrected execution plan as strict JSON only.",
		"The previous plan was invalid.",
		"Validation error: " + fallbackString(detail, "unknown"),
		"Previous output:",
		raw,
	}, "\n"))
}

func planArtifactSummary(plan *domain.ExecutionPlan) string {
	if plan == nil {
		return ""
	}
	return prettyJSON(plan)
}

func plannerMessages(base []domain.Message, inventory []domain.AgentInventoryEntry) []domain.Message {
	contract := prettyJSON(plannerOutputContract())
	return phaseMessages(base,
		"Generate a planner-owned execution plan for this request.",
		"Return strict JSON only. Do not wrap the JSON in markdown fences.",
		"ExecutionPlan contract:\n"+contract,
		"Available agent inventory:\n"+inventoryArtifactSummary(inventory),
	)
}

func artifactReferenceNames(items []domain.ArtifactReference) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		switch {
		case item.Name != "":
			out = append(out, item.Name)
		case item.Kind != "":
			out = append(out, item.Kind)
		default:
			out = append(out, item.ID)
		}
	}
	return out
}

func observationSummaries(items []domain.ObservationRecord) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item.Summary == "" {
			out = append(out, item.ToolName)
			continue
		}
		out = append(out, item.ToolName+": "+item.Summary)
	}
	return out
}

func countContextItems(messages []domain.Message, context domain.ContextPack) int {
	messageCount := len(messages)
	fileCount := len(uniqueStrings(append([]string(nil), context.RelevantFiles...)))
	artifactCount := len(uniqueStrings(append([]string(nil), context.ArtifactRefs...))) + len(context.Artifacts)
	inventoryCount := len(context.AgentInventory)
	observationCount := len(context.Observations)
	return messageCount + fileCount + artifactCount + inventoryCount + observationCount
}

func stablePlanJSON(plan *domain.ExecutionPlan) string {
	if plan == nil {
		return ""
	}
	buffer := bytes.NewBuffer(nil)
	encoder := json.NewEncoder(buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(plan); err != nil {
		return prettyJSON(plan)
	}
	return strings.TrimSpace(buffer.String())
}

func lastExecutionPlanPhase(plan *domain.ExecutionPlan) domain.RunPhase {
	if plan == nil {
		return domain.RunPhaseExecute
	}
	if plan.Finalize != nil {
		return domain.RunPhaseFinalize
	}
	if plan.Recovery != nil {
		return domain.RunPhaseRecover
	}
	if len(plan.Verify) > 0 {
		return domain.RunPhaseVerify
	}
	return domain.RunPhaseExecute
}

func planAgentID(plan *domain.ExecutionPlan) string {
	if plan == nil || plan.Plan == nil {
		return ""
	}
	return plan.Plan.AgentID
}
