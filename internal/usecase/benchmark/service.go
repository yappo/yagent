package benchmark

import (
	"context"
	"fmt"
	"strings"
	"time"

	"yagent/internal/config"
	"yagent/internal/domain"
)

type Profile struct {
	Name           string
	Description    string
	RoutingProfile string
	Features       config.FeaturesConfig
}

type Request struct {
	Prompt         string
	Model          string
	ResumeID       string
	Runs           int
	RoutingProfile string
	Profiles       []Profile
}

type RunnerFactory func(config.Config) (domain.Orchestrator, func(), error)

type Report struct {
	GeneratedAt time.Time
	Prompt      string
	Runs        int
	Results     []ProfileReport
}

type ProfileReport struct {
	Profile Profile
	Runs    []RunReport
	Summary Summary
}

type RunReport struct {
	Index                int
	Duration             time.Duration
	Status               domain.RunStatus
	Phase                domain.RunPhase
	Attempt              int
	Events               int
	ToolCalls            int
	AgentStarts          int
	FailedEvents         int
	VerificationFailures int
	Artifacts            int
	PlanNodes            int
	Message              string
}

type Summary struct {
	Runs                 int
	Successes            int
	AvgDuration          time.Duration
	AvgEvents            float64
	AvgAttempt           float64
	AvgArtifacts         float64
	VerificationFailures int
}

func Execute(ctx context.Context, base config.Config, request Request, factory RunnerFactory) (Report, error) {
	if strings.TrimSpace(request.Prompt) == "" {
		return Report{}, fmt.Errorf("prompt が必要です")
	}
	if request.Runs < 1 {
		return Report{}, fmt.Errorf("runs は 1 以上である必要があります")
	}
	if len(request.Profiles) == 0 {
		return Report{}, fmt.Errorf("benchmark profile が必要です")
	}

	report := Report{
		GeneratedAt: time.Now(),
		Prompt:      request.Prompt,
		Runs:        request.Runs,
		Results:     make([]ProfileReport, 0, len(request.Profiles)),
	}

	for _, profile := range request.Profiles {
		result := ProfileReport{Profile: profile}
		for idx := 0; idx < request.Runs; idx++ {
			cfg := base
			cfg.Features = profile.Features

			runner, closeFn, err := factory(cfg)
			if err != nil {
				return Report{}, err
			}

			turnProfile := request.RoutingProfile
			if turnProfile == "" {
				turnProfile = profile.RoutingProfile
			}

			started := time.Now()
			turn, err := runner.RunTurn(ctx, domain.TurnRequest{
				Messages: []domain.Message{{Role: domain.RoleUser, Content: request.Prompt}},
				Model:    request.Model,
				Profile:  turnProfile,
				ResumeID: request.ResumeID,
			})
			elapsed := time.Since(started)
			if closeFn != nil {
				closeFn()
			}
			if err != nil {
				return Report{}, err
			}

			result.Runs = append(result.Runs, runReportFromTurn(idx+1, elapsed, turn))
		}
		result.Summary = summarize(result.Runs)
		report.Results = append(report.Results, result)
	}

	return report, nil
}

func runReportFromTurn(index int, elapsed time.Duration, turn domain.TurnResult) RunReport {
	runReport := RunReport{
		Index:    index,
		Duration: elapsed,
		Events:   len(turn.Events),
		Message:  strings.TrimSpace(turn.Message.Content),
	}
	for _, event := range turn.Events {
		switch event.Type {
		case "tool_called", "tool_failed":
			runReport.ToolCalls++
		case "agent_started":
			runReport.AgentStarts++
		}
		if event.Status == "failed" || event.Type == "agent_failed" || event.Type == "tool_failed" {
			runReport.FailedEvents++
		}
	}
	if turn.Run != nil {
		runReport.Status = turn.Run.Status
		runReport.Phase = turn.Run.CurrentPhase
		runReport.Attempt = turn.Run.Attempt
		runReport.Artifacts = len(turn.Run.Artifacts)
		runReport.PlanNodes = len(turn.Run.Plan)
		for _, item := range turn.Run.Verification {
			if strings.EqualFold(item.Status, "fail") {
				runReport.VerificationFailures++
			}
		}
	}
	if runReport.Message == "" {
		runReport.Message = "(empty response)"
	}
	return runReport
}

func summarize(runs []RunReport) Summary {
	summary := Summary{Runs: len(runs)}
	if len(runs) == 0 {
		return summary
	}

	var totalDuration time.Duration
	var totalEvents int
	var totalAttempts int
	var totalArtifacts int
	for _, run := range runs {
		totalDuration += run.Duration
		totalEvents += run.Events
		totalAttempts += run.Attempt
		totalArtifacts += run.Artifacts
		summary.VerificationFailures += run.VerificationFailures
		if run.Status == domain.RunStatusCompleted {
			summary.Successes++
		}
	}
	summary.AvgDuration = totalDuration / time.Duration(len(runs))
	summary.AvgEvents = float64(totalEvents) / float64(len(runs))
	summary.AvgAttempt = float64(totalAttempts) / float64(len(runs))
	summary.AvgArtifacts = float64(totalArtifacts) / float64(len(runs))
	return summary
}
