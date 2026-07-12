package benchmark

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"yagent/internal/domain"
)

func BuildArtifactRun(report Report) (*domain.RunState, domain.RunArtifact, error) {
	generatedAt := report.GeneratedAt
	if generatedAt.IsZero() {
		generatedAt = time.Now()
		report.GeneratedAt = generatedAt
	}
	records := FlattenRecords(report)
	if len(records) == 0 {
		return nil, domain.RunArtifact{}, fmt.Errorf("benchmark artifact に保存する record がありません")
	}
	reportJSON, err := json.Marshal(report)
	if err != nil {
		return nil, domain.RunArtifact{}, fmt.Errorf("benchmark report の JSON 変換に失敗しました: %w", err)
	}
	recordsJSON, err := json.Marshal(records)
	if err != nil {
		return nil, domain.RunArtifact{}, fmt.Errorf("benchmark record の JSON 変換に失敗しました: %w", err)
	}

	profiles := reportProfileNames(report)
	cases := reportCaseIDs(report)
	passed := 0
	for _, record := range records {
		if record.Passed {
			passed++
		}
	}
	preflightDoctor := false
	for _, result := range report.Results {
		if result.Preflight != nil && result.Preflight.Doctor != nil {
			preflightDoctor = true
			break
		}
	}
	runs := report.Runs
	if runs < 1 {
		runs = 1
	}
	payload := domain.BenchmarkReportArtifactPayload{
		Prompt:           strings.TrimSpace(report.Prompt),
		Runs:             runs,
		Profiles:         profiles,
		Cases:            cases,
		RecordCount:      len(records),
		EvaluationPasses: passed,
		EvaluationFailed: len(records) - passed,
		PreflightDoctor:  preflightDoctor,
		Report:           reportJSON,
		Records:          recordsJSON,
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, domain.RunArtifact{}, fmt.Errorf("benchmark artifact payload の JSON 変換に失敗しました: %w", err)
	}

	runID := fmt.Sprintf("benchmark-%d", generatedAt.UnixNano())
	summary := benchmarkArtifactSummary(payload)
	artifact := domain.RunArtifact{
		ID:            runID + "-report",
		Name:          "Benchmark report",
		Kind:          "benchmark_report",
		SchemaVersion: "benchmark_report.v1",
		Phase:         domain.RunPhaseFinalize,
		AgentID:       "benchmark",
		Summary:       summary,
		Text:          benchmarkArtifactText(payload),
		Content:       benchmarkArtifactText(payload),
		Payload:       payloadJSON,
		CreatedAt:     generatedAt,
	}
	run := &domain.RunState{
		ID:           runID,
		RootRunID:    runID,
		Status:       domain.RunStatusCompleted,
		CurrentPhase: domain.RunPhaseFinalize,
		Attempt:      1,
		UserGoal:     summary,
		Messages: []domain.Message{{
			Role:    domain.RoleUser,
			Content: summary,
		}},
		Artifacts: []domain.RunArtifact{artifact},
		Checkpoints: []domain.RunCheckpoint{{
			ID:        runID + "-checkpoint",
			Phase:     domain.RunPhaseFinalize,
			Status:    domain.RunStatusCompleted,
			Attempt:   1,
			Summary:   summary,
			CreatedAt: generatedAt,
		}},
		CreatedAt: generatedAt,
		UpdatedAt: generatedAt,
	}
	return run, artifact, nil
}

func reportProfileNames(report Report) []string {
	names := make([]string, 0, len(report.Results))
	for _, result := range report.Results {
		names = appendUnique(names, result.Profile.Name)
	}
	return names
}

func reportCaseIDs(report Report) []string {
	ids := make([]string, 0, len(report.Cases))
	for _, item := range report.Cases {
		ids = appendUnique(ids, item.ID)
	}
	return ids
}

func benchmarkArtifactSummary(payload domain.BenchmarkReportArtifactPayload) string {
	return fmt.Sprintf(
		"benchmark profiles=%s cases=%s records=%d passed=%d failed=%d",
		fallbackString(strings.Join(payload.Profiles, ","), "-"),
		fallbackString(strings.Join(payload.Cases, ","), "-"),
		payload.RecordCount,
		payload.EvaluationPasses,
		payload.EvaluationFailed,
	)
}

func benchmarkArtifactText(payload domain.BenchmarkReportArtifactPayload) string {
	lines := []string{
		"Benchmark report",
		"profiles: " + fallbackString(strings.Join(payload.Profiles, ", "), "-"),
		"cases: " + fallbackString(strings.Join(payload.Cases, ", "), "-"),
		fmt.Sprintf("records: %d", payload.RecordCount),
		fmt.Sprintf("passed: %d", payload.EvaluationPasses),
		fmt.Sprintf("failed: %d", payload.EvaluationFailed),
	}
	if payload.PreflightDoctor {
		lines = append(lines, "preflight_doctor: true")
	}
	return strings.Join(lines, "\n")
}
