package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/sync/errgroup"

	"yagent/internal/domain"
)

type graphExecutionState struct {
	latestExecution    map[int]domain.AgentResult
	mergedVerification map[int]domain.VerificationResult
	reportedAttempts   map[int]bool
	finalizeAdded      bool
	prepBundled        bool
}

type workUnitOutcome struct {
	unit             domain.WorkUnit
	result           domain.AgentResult
	verification     *domain.VerificationResult
	artifacts        []domain.RunArtifact
	appendMessage    bool
	checkpoint       string
	skipped          bool
	skippedSummary   string
	markPlanComplete bool
	err              error
}

func (s *Service) runWorkGraph(ctx context.Context, run *domain.RunState, plan *domain.ExecutionPlan, request domain.TurnRequest) (domain.AgentResult, []domain.ExecutionEvent, error) {
	if run == nil || plan == nil {
		return domain.AgentResult{}, nil, fmt.Errorf("execution graph の実行に必要な state がありません")
	}
	if len(run.WorkUnits) == 0 {
		run.WorkUnits = workUnitsFromExecutionPlan(run, plan)
	}
	state := graphExecutionState{
		latestExecution:    map[int]domain.AgentResult{},
		mergedVerification: map[int]domain.VerificationResult{},
		reportedAttempts:   map[int]bool{},
	}
	allEvents := []domain.ExecutionEvent{}
	s.refreshWorkUnits(run)
	_ = s.saveRun(ctx, run)

	scheduler := newRuntimeScheduler(s.config.MaxParallelAgents)
	for {
		if allTerminalWorkUnits(run.WorkUnits) {
			break
		}

		specs := scheduleSpecsFromWorkUnits(run.WorkUnits)
		completed := completedWorkUnits(run.WorkUnits)
		batch := scheduler.nextBatch(specs, completed)
		if len(batch) == 0 {
			return domain.AgentResult{}, allEvents, fmt.Errorf("work graph が進行不能になりました")
		}

		results := make([]workUnitOutcome, len(batch))
		for _, idx := range batch {
			unit := run.WorkUnits[idx]
			run.CurrentPhase = unit.Phase
			run.Attempt = maxInt(1, unit.Attempt)
			markWorkUnitStatus(run, unit.ID, "running")
			markPlanNodeStatus(run, unit.Phase, unit.Role, "running")
		}
		s.refreshWorkUnits(run)
		_ = s.saveRun(ctx, run)

		group, groupCtx := errgroup.WithContext(ctx)
		for batchIdx, workIdx := range batch {
			batchIdx := batchIdx
			workIdx := workIdx
			group.Go(func() error {
				results[batchIdx] = s.executeWorkUnit(groupCtx, run, plan, request, run.WorkUnits[workIdx], state)
				return results[batchIdx].err
			})
		}
		if err := group.Wait(); err != nil {
			for _, outcome := range results {
				if outcome.unit.ID == "" {
					continue
				}
				markWorkUnitStatus(run, outcome.unit.ID, "failed")
			}
			_ = s.saveRun(ctx, run)
			return domain.AgentResult{}, append(allEvents, collectWorkUnitEvents(results)...), err
		}

		completedPrep := false
		completedRecoveryAttempts := map[int]struct{}{}
		verifyAttempts := map[int]struct{}{}
		for _, outcome := range results {
			allEvents = append(allEvents, outcome.result.Events...)
			if outcome.skipped {
				markWorkUnitStatus(run, outcome.unit.ID, "skipped")
				if outcome.skippedSummary != "" {
					run.Checkpoints = append(run.Checkpoints, checkpoint(run, outcome.unit.Phase, outcome.skippedSummary))
				}
				continue
			}

			markWorkUnitStatus(run, outcome.unit.ID, "done")
			markPlanNodeStatus(run, outcome.unit.Phase, outcome.unit.Role, "done")
			for _, artifact := range outcome.artifacts {
				if artifact.ID == "" {
					continue
				}
				run.Artifacts = append(run.Artifacts, artifact)
			}
			if outcome.appendMessage {
				run.Messages = append(run.Messages, domain.Message{
					Role:    domain.RoleAssistant,
					AgentID: outcome.result.Message.AgentID,
					Content: outcome.result.Message.Content,
				})
			}
			if outcome.checkpoint != "" {
				run.Checkpoints = append(run.Checkpoints, checkpoint(run, outcome.unit.Phase, outcome.checkpoint))
			}
			switch outcome.unit.Kind {
			case "preparation":
				completedPrep = true
			case "primary", "recovery":
				state.latestExecution[maxInt(1, outcome.unit.Attempt)] = outcome.result
				if outcome.unit.Kind == "recovery" {
					completedRecoveryAttempts[maxInt(1, outcome.unit.Attempt)] = struct{}{}
				}
			case "verification":
				if outcome.verification != nil {
					run.Verification = append(run.Verification, *outcome.verification)
					verifyAttempts[maxInt(1, outcome.unit.Attempt)] = struct{}{}
				}
			case "finalize":
				state.latestExecution[maxInt(1, outcome.unit.Attempt)] = outcome.result
			}
		}

		if completedPrep {
			s.ensurePreparationEvidence(run)
		}
		for attempt := range verifyAttempts {
			if err := s.resolveVerificationAttempt(ctx, run, plan, attempt, &state); err != nil {
				return domain.AgentResult{}, allEvents, err
			}
		}
		for attempt := range completedRecoveryAttempts {
			if len(plan.Verify) > 0 {
				s.appendVerificationUnits(run, plan, attempt, []string{recoveryUnitID(plan, attempt)})
			} else {
				s.appendFinalizeUnit(run, plan, []string{recoveryUnitID(plan, attempt)}, attempt, &state)
			}
		}

		s.refreshWorkUnits(run)
		s.maybeCompactRun(run)
		_ = s.saveRun(ctx, run)
	}

	final := latestExecutionResult(state.latestExecution)
	if plan.Finalize != nil && plan.Finalize.AgentID != "" {
		if finalized, ok := state.latestExecution[maxAttemptFromResults(state.latestExecution)]; ok {
			final = finalized
		}
	}
	return final, allEvents, nil
}

func (s *Service) executeWorkUnit(ctx context.Context, run *domain.RunState, plan *domain.ExecutionPlan, request domain.TurnRequest, unit domain.WorkUnit, state graphExecutionState) workUnitOutcome {
	switch unit.Kind {
	case "preparation":
		return s.executePreparationUnit(ctx, run, plan, request, unit)
	case "primary":
		return s.executePrimaryUnit(ctx, run, plan, request, unit)
	case "verification":
		return s.executeVerificationUnit(ctx, run, plan, request, unit, state)
	case "recovery":
		return s.executeRecoveryUnit(ctx, run, plan, request, unit, state)
	case "finalize":
		return s.executeFinalizeUnit(ctx, run, plan, request, unit, state)
	default:
		return workUnitOutcome{unit: unit, skipped: true}
	}
}

func (s *Service) executePreparationUnit(ctx context.Context, run *domain.RunState, plan *domain.ExecutionPlan, request domain.TurnRequest, unit domain.WorkUnit) workUnitOutcome {
	agent, ok := s.catalog.Resolve(unit.Role)
	if !ok {
		return workUnitOutcome{unit: unit, skipped: true, skippedSummary: "preparation agent unavailable"}
	}
	messages := phaseMessages(run.Messages, "Execution plan:\n"+stablePlanJSON(plan))
	taskBrief := fallbackString(strings.TrimSpace(unit.Task), fmt.Sprintf("Prepare focused context for the primary agent %s. Return only findings that materially help the task.", fallbackString(plan.Primary.AgentID, "manager")))
	invocation := s.phaseInvocation(run, agent, request, domain.RunPhaseExecute, maxInt(1, unit.Attempt), messages, taskBrief)
	invocation.Context.ExpectedOutput = map[string]any{
		"goal": "Return only the findings that materially help the primary agent.",
	}
	result, err := s.runAgent(ctx, invocation, 0)
	if err != nil {
		return workUnitOutcome{unit: unit, err: err}
	}
	return workUnitOutcome{
		unit:             unit,
		result:           result,
		artifacts:        []domain.RunArtifact{newAgentMessageArtifact(run, domain.RunPhaseExecute, agent.ID, agent.Name+" evidence", "evidence_bundle", result.Message.Content, nil)},
		markPlanComplete: true,
	}
}

func (s *Service) executePrimaryUnit(ctx context.Context, run *domain.RunState, plan *domain.ExecutionPlan, request domain.TurnRequest, unit domain.WorkUnit) workUnitOutcome {
	agentID := fallbackString(plan.Primary.AgentID, unit.Role)
	primary, ok := s.catalog.Resolve(agentID)
	if !ok {
		return workUnitOutcome{unit: unit, err: fmt.Errorf("primary agent %q が見つかりません", agentID)}
	}
	messages := phaseMessages(cloneMessages(run.Messages), "Execution plan:\n"+stablePlanJSON(plan))
	if bundle, ok := latestArtifactOfKind(run.Artifacts, "evidence_bundle"); ok {
		messages = phaseMessages(messages, "Evidence bundle:\n"+bundle.Summary)
	}
	taskBrief := fallbackString(strings.TrimSpace(unit.Task), "Handle the request directly using the prepared context and available tools.")
	if primary.ID == "manager" {
		taskBrief = fallbackString(strings.TrimSpace(unit.Task), "Coordinate execution using the prepared context and produce the implementation result.")
	}
	result, err := s.runAgent(ctx, s.phaseInvocation(run, primary, request, domain.RunPhaseExecute, maxInt(1, unit.Attempt), messages, taskBrief), 0)
	if err != nil {
		return workUnitOutcome{unit: unit, err: err}
	}
	artifacts := []domain.RunArtifact{
		newAgentMessageArtifact(run, domain.RunPhaseExecute, result.Message.AgentID, "Execution result", "execution", result.Message.Content, nil),
	}
	if changeSet := s.buildChangeSetArtifact(ctx, run, domain.RunPhaseExecute, primary.ID, unit.StartedAt); changeSet.ID != "" {
		artifacts = append(artifacts, changeSet)
	}
	return workUnitOutcome{
		unit:          unit,
		result:        result,
		artifacts:     artifacts,
		appendMessage: true,
		checkpoint:    result.Message.Content,
	}
}

func (s *Service) executeVerificationUnit(ctx context.Context, run *domain.RunState, plan *domain.ExecutionPlan, request domain.TurnRequest, unit domain.WorkUnit, state graphExecutionState) workUnitOutcome {
	agent, ok := s.catalog.Resolve(unit.Role)
	if !ok {
		return workUnitOutcome{unit: unit, skipped: true, skippedSummary: "verification agent unavailable"}
	}
	agent = withVerificationInstruction(agent)
	execution := latestExecutionForAttempt(state.latestExecution, maxInt(1, unit.Attempt))
	input := phaseMessages(run.Messages, "Execution summary:\n"+execution.Message.Content, "Execution plan:\n"+stablePlanJSON(plan))
	taskBrief := fallbackString(strings.TrimSpace(unit.Task), "Verify the latest implementation. Return VERIFICATION_STATUS, SUMMARY, and REPAIR_BRIEF.")
	invocation := s.phaseInvocation(run, agent, request, domain.RunPhaseVerify, maxInt(1, unit.Attempt), input, taskBrief)
	invocation.Context.ExpectedOutput = verificationOutputContract()
	result, err := s.runAgent(ctx, invocation, 0)
	if err != nil {
		return workUnitOutcome{unit: unit, err: err}
	}
	parsed := parseVerification(result.Message.Content, agent.ID, maxInt(1, unit.Attempt))
	artifact := newVerificationArtifact(run, domain.RunPhaseVerify, agent.ID, agent.Name+" verification", result.Message.Content, parsed)
	parsed.ArtifactID = artifact.ID
	return workUnitOutcome{
		unit:         unit,
		result:       result,
		verification: &parsed,
		artifacts:    []domain.RunArtifact{artifact},
	}
}

func (s *Service) executeRecoveryUnit(ctx context.Context, run *domain.RunState, plan *domain.ExecutionPlan, request domain.TurnRequest, unit domain.WorkUnit, state graphExecutionState) workUnitOutcome {
	verification, ok := state.mergedVerification[maxInt(1, unit.Attempt)-1]
	if !ok || strings.EqualFold(verification.Status, "pass") {
		return workUnitOutcome{unit: unit, skipped: true, skippedSummary: "recovery skipped because verification passed"}
	}
	coder, ok := s.catalog.Resolve(unit.Role)
	if !ok {
		return workUnitOutcome{unit: unit, err: fmt.Errorf("recovery agent %q が見つかりません", unit.Role)}
	}
	repairPrompt := strings.TrimSpace("Repair the implementation using this brief:\n" + verification.RepairBrief)
	taskBrief := fallbackString(strings.TrimSpace(unit.Task), repairPrompt)
	input := phaseMessages(run.Messages, repairPrompt, "Execution plan:\n"+stablePlanJSON(plan))
	result, err := s.runAgent(ctx, s.phaseInvocation(run, coder, request, domain.RunPhaseRecover, maxInt(1, unit.Attempt), input, taskBrief), 0)
	if err != nil {
		return workUnitOutcome{unit: unit, err: err}
	}
	artifacts := []domain.RunArtifact{
		newAgentMessageArtifact(run, domain.RunPhaseRecover, coder.ID, "Recovery result", "recovery", result.Message.Content, nil),
	}
	if changeSet := s.buildChangeSetArtifact(ctx, run, domain.RunPhaseRecover, coder.ID, unit.StartedAt); changeSet.ID != "" {
		artifacts = append(artifacts, changeSet)
	}
	return workUnitOutcome{
		unit:          unit,
		result:        result,
		artifacts:     artifacts,
		appendMessage: true,
		checkpoint:    result.Message.Content,
	}
}

func (s *Service) executeFinalizeUnit(ctx context.Context, run *domain.RunState, plan *domain.ExecutionPlan, request domain.TurnRequest, unit domain.WorkUnit, state graphExecutionState) workUnitOutcome {
	if plan.Finalize == nil || plan.Finalize.AgentID == "" {
		return workUnitOutcome{unit: unit, skipped: true}
	}
	manager, ok := s.catalog.Resolve(plan.Finalize.AgentID)
	if !ok {
		return workUnitOutcome{unit: unit, err: fmt.Errorf("finalize agent %q が見つかりません", plan.Finalize.AgentID)}
	}
	execution := latestExecutionForAttempt(state.latestExecution, maxInt(1, unit.Attempt))
	verification := latestVerificationForAttempt(state.mergedVerification, maxInt(1, unit.Attempt))
	input := phaseMessages(run.Messages,
		"Execution summary:\n"+execution.Message.Content,
		"Verification summary:\n"+verification.Summary,
		"Execution plan:\n"+stablePlanJSON(plan),
	)
	taskBrief := fallbackString(strings.TrimSpace(unit.Task), "Summarize the completed work, verification status, and remaining risks.")
	result, err := s.runAgent(ctx, s.phaseInvocation(run, manager, request, domain.RunPhaseFinalize, maxInt(1, unit.Attempt), input, taskBrief), 0)
	if err != nil {
		return workUnitOutcome{unit: unit, err: err}
	}
	return workUnitOutcome{unit: unit, result: result}
}

func (s *Service) resolveVerificationAttempt(ctx context.Context, run *domain.RunState, plan *domain.ExecutionPlan, attempt int, state *graphExecutionState) error {
	if state.reportedAttempts[attempt] || !verificationAttemptComplete(run.WorkUnits, attempt) {
		return nil
	}
	results := verificationResultsForAttempt(run.Verification, attempt)
	if len(results) == 0 {
		return nil
	}
	merged := mergeVerification(results, attempt)
	run.Verification = append(run.Verification, merged)
	state.mergedVerification[attempt] = merged
	if report := s.buildTestReportArtifact(run, domain.RunPhaseVerify, attempt, merged.Status, run.Verification); report.ID != "" {
		run.Artifacts = append(run.Artifacts, report)
	}
	run.Checkpoints = append(run.Checkpoints, checkpoint(run, domain.RunPhaseVerify, merged.Summary))
	if strings.EqualFold(merged.Status, "fail") {
		run.KnownFailures = appendUnique(run.KnownFailures, merged.Summary)
	}
	state.reportedAttempts[attempt] = true

	if strings.EqualFold(merged.Status, "fail") && attempt < s.config.MaxVerificationAttempts && plan.Recovery != nil && plan.Recovery.AgentID != "" {
		s.appendRecoveryUnit(run, plan, attempt+1, verifyUnitIDs(run.WorkUnits, attempt))
		return nil
	}
	s.appendFinalizeUnit(run, plan, verifyUnitIDs(run.WorkUnits, attempt), attempt, state)
	return nil
}

func (s *Service) ensurePreparationEvidence(run *domain.RunState) {
	prepArtifacts := make([]domain.RunArtifact, 0, len(run.Artifacts))
	for _, artifact := range run.Artifacts {
		if artifact.Kind != "evidence_bundle" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(artifact.Name), "evidence bundle") {
			continue
		}
		prepArtifacts = append(prepArtifacts, artifact)
	}
	if len(prepArtifacts) == 0 {
		return
	}
	if _, ok := latestArtifactOfKind(run.Artifacts, "evidence_bundle"); ok {
		last, _ := latestArtifactOfKind(run.Artifacts, "evidence_bundle")
		if strings.EqualFold(last.Name, "Evidence bundle") {
			return
		}
	}
	if bundle := buildEvidenceBundleArtifact(run, prepArtifacts); bundle.ID != "" {
		run.Artifacts = append(run.Artifacts, bundle)
	}
}

func (s *Service) appendRecoveryUnit(run *domain.RunState, plan *domain.ExecutionPlan, attempt int, dependsOn []string) {
	if run == nil || plan == nil || plan.Recovery == nil || plan.Recovery.AgentID == "" {
		return
	}
	id := recoveryUnitID(plan, attempt)
	if hasWorkUnit(run.WorkUnits, id) {
		return
	}
	run.WorkUnits = append(run.WorkUnits, domain.WorkUnit{
		ID:              id,
		Kind:            "recovery",
		Role:            plan.Recovery.AgentID,
		Phase:           domain.RunPhaseRecover,
		Attempt:         attempt,
		Task:            plan.Recovery.Reason,
		Status:          "pending",
		DependsOn:       append([]string(nil), dependsOn...),
		Source:          plan.Recovery.AgentID,
		SideEffectClass: workUnitSideEffect(plan.TaskKind, "recovery"),
		WriteSet:        workUnitWriteSet(plan.TaskKind, "recovery"),
	})
}

func (s *Service) appendVerificationUnits(run *domain.RunState, plan *domain.ExecutionPlan, attempt int, dependsOn []string) {
	if run == nil || plan == nil || len(plan.Verify) == 0 {
		return
	}
	for _, item := range plan.Verify {
		id := verifyUnitID(item.AgentID, attempt)
		if hasWorkUnit(run.WorkUnits, id) {
			continue
		}
		run.WorkUnits = append(run.WorkUnits, domain.WorkUnit{
			ID:        id,
			Kind:      "verification",
			Role:      item.AgentID,
			Phase:     domain.RunPhaseVerify,
			Attempt:   attempt,
			Task:      item.Reason,
			Status:    "pending",
			DependsOn: append([]string(nil), dependsOn...),
			Source:    item.AgentID,
		})
	}
}

func (s *Service) appendFinalizeUnit(run *domain.RunState, plan *domain.ExecutionPlan, dependsOn []string, attempt int, state *graphExecutionState) {
	if run == nil || plan == nil || plan.Finalize == nil || plan.Finalize.AgentID == "" || state.finalizeAdded {
		return
	}
	id := finalizeUnitID(plan, attempt)
	if hasWorkUnit(run.WorkUnits, id) {
		state.finalizeAdded = true
		return
	}
	run.WorkUnits = append(run.WorkUnits, domain.WorkUnit{
		ID:        id,
		Kind:      "finalize",
		Role:      plan.Finalize.AgentID,
		Phase:     domain.RunPhaseFinalize,
		Attempt:   attempt,
		Task:      plan.Finalize.Reason,
		Status:    "pending",
		DependsOn: append([]string(nil), dependsOn...),
		Source:    plan.Finalize.AgentID,
	})
	state.finalizeAdded = true
}

func (s *Service) refreshWorkUnits(run *domain.RunState) {
	if run == nil {
		return
	}
	refs := recentArtifactReferences(lastArtifacts(run.Artifacts, 8), 8)
	failures := append([]string(nil), run.KnownFailures...)
	for idx := range run.WorkUnits {
		run.WorkUnits[idx].ArtifactRefs = append([]domain.ArtifactReference(nil), refs...)
		run.WorkUnits[idx].KnownFailureRefs = append([]string(nil), failures...)
	}
}

func scheduleSpecsFromWorkUnits(units []domain.WorkUnit) []scheduleSpec {
	specs := make([]scheduleSpec, 0, len(units))
	for _, unit := range units {
		specs = append(specs, scheduleSpec{
			ID:              unit.ID,
			DependsOn:       append([]string(nil), unit.DependsOn...),
			ReadSet:         append([]string(nil), unit.ReadSet...),
			WriteSet:        append([]string(nil), unit.WriteSet...),
			SideEffectClass: unit.SideEffectClass,
			DuplicateKey:    unit.DuplicateKey,
			Source:          unit.Source,
			SourceLimit:     1,
		})
	}
	return specs
}

func collectWorkUnitEvents(items []workUnitOutcome) []domain.ExecutionEvent {
	events := []domain.ExecutionEvent{}
	for _, item := range items {
		events = append(events, item.result.Events...)
	}
	return events
}

func completedWorkUnits(units []domain.WorkUnit) map[string]bool {
	completed := map[string]bool{}
	for _, unit := range units {
		if terminalWorkUnit(unit.Status) {
			completed[unit.ID] = true
		}
	}
	return completed
}

func allTerminalWorkUnits(units []domain.WorkUnit) bool {
	if len(units) == 0 {
		return true
	}
	for _, unit := range units {
		if !terminalWorkUnit(unit.Status) {
			return false
		}
	}
	return true
}

func terminalWorkUnit(status string) bool {
	switch status {
	case "done", "skipped", "failed":
		return true
	default:
		return false
	}
}

func verificationAttemptComplete(units []domain.WorkUnit, attempt int) bool {
	found := false
	for _, unit := range units {
		if unit.Kind != "verification" || maxInt(1, unit.Attempt) != attempt {
			continue
		}
		found = true
		if !terminalWorkUnit(unit.Status) {
			return false
		}
	}
	return found
}

func verificationResultsForAttempt(results []domain.VerificationResult, attempt int) []domain.VerificationResult {
	items := make([]domain.VerificationResult, 0, len(results))
	for _, result := range results {
		if result.Attempt != attempt || result.SourceAgent == "verification" {
			continue
		}
		items = append(items, result)
	}
	return items
}

func verifyUnitIDs(units []domain.WorkUnit, attempt int) []string {
	ids := []string{}
	for _, unit := range units {
		if unit.Kind == "verification" && maxInt(1, unit.Attempt) == attempt {
			ids = append(ids, unit.ID)
		}
	}
	return ids
}

func hasWorkUnit(units []domain.WorkUnit, id string) bool {
	for _, unit := range units {
		if unit.ID == id {
			return true
		}
	}
	return false
}

func latestArtifactOfKind(artifacts []domain.RunArtifact, kind string) (domain.RunArtifact, bool) {
	for idx := len(artifacts) - 1; idx >= 0; idx-- {
		if artifacts[idx].Kind == kind {
			return artifacts[idx], true
		}
	}
	return domain.RunArtifact{}, false
}

func latestExecutionForAttempt(results map[int]domain.AgentResult, attempt int) domain.AgentResult {
	if result, ok := results[attempt]; ok {
		return result
	}
	return latestExecutionResult(results)
}

func latestVerificationForAttempt(results map[int]domain.VerificationResult, attempt int) domain.VerificationResult {
	if result, ok := results[attempt]; ok {
		return result
	}
	maxAttempt := 0
	var latest domain.VerificationResult
	for current, result := range results {
		if current > maxAttempt {
			maxAttempt = current
			latest = result
		}
	}
	return latest
}

func latestExecutionResult(results map[int]domain.AgentResult) domain.AgentResult {
	maxAttempt := 0
	var latest domain.AgentResult
	for attempt, result := range results {
		if attempt > maxAttempt {
			maxAttempt = attempt
			latest = result
		}
	}
	return latest
}

func maxAttemptFromResults(results map[int]domain.AgentResult) int {
	maxAttempt := 0
	for attempt := range results {
		if attempt > maxAttempt {
			maxAttempt = attempt
		}
	}
	return maxAttempt
}

func verifyUnitID(agentID string, attempt int) string {
	if attempt <= 1 {
		return "verify:" + agentID
	}
	return fmt.Sprintf("verify:%d:%s", attempt, agentID)
}

func recoveryUnitID(plan *domain.ExecutionPlan, attempt int) string {
	if plan == nil || plan.Recovery == nil {
		return ""
	}
	if attempt <= 1 {
		return "recover:" + plan.Recovery.AgentID
	}
	return fmt.Sprintf("recover:%d:%s", attempt, plan.Recovery.AgentID)
}

func finalizeUnitID(plan *domain.ExecutionPlan, attempt int) string {
	if plan == nil || plan.Finalize == nil {
		return ""
	}
	if attempt <= 1 {
		return "finalize:" + plan.Finalize.AgentID
	}
	return fmt.Sprintf("finalize:%d:%s", attempt, plan.Finalize.AgentID)
}

func workUnitSideEffect(kind domain.TaskKind, unitKind string) domain.SideEffectClass {
	if kind == domain.TaskKindMutate && (unitKind == "primary" || unitKind == "recovery") {
		return domain.SideEffectWorkspace
	}
	return domain.SideEffectNone
}

func workUnitWriteSet(kind domain.TaskKind, unitKind string) []string {
	if kind == domain.TaskKindMutate && (unitKind == "primary" || unitKind == "recovery") {
		return []string{"workspace"}
	}
	return nil
}

func maxInt(left int, right int) int {
	if left > right {
		return left
	}
	return right
}
