package cli

import (
	"strings"
	"testing"

	"yagent/internal/config"
	benchmarkusecase "yagent/internal/usecase/benchmark"
)

func TestResolveBenchmarkProfileLegacy(t *testing.T) {
	profile, err := resolveBenchmarkProfile(config.FeaturesConfig{
		PhaseHarness:       true,
		AdaptiveCompaction: true,
		RoleRouting:        true,
		RepoMemory:         true,
	}, "legacy")
	if err != nil {
		t.Fatalf("resolveBenchmarkProfile returned error: %v", err)
	}
	if profile.Features.PhaseHarness || profile.Features.RoleRouting || profile.Features.RepoMemory {
		t.Fatalf("expected legacy flags to be disabled, got %+v", profile.Features)
	}
}

func TestRenderBenchmarkTableIncludesFeatureSummary(t *testing.T) {
	text := renderBenchmarkTable(benchmarkusecase.Report{
		Prompt: "hello",
		Runs:   1,
		Results: []benchmarkusecase.ProfileReport{{
			Profile: benchmarkusecase.Profile{
				Name: "current",
				Features: config.FeaturesConfig{
					PhaseHarness: true,
					RoleRouting:  true,
				},
			},
			Summary: benchmarkusecase.Summary{
				Runs:        1,
				Successes:   1,
				AvgDuration: 12,
			},
			Runs: []benchmarkusecase.RunReport{{
				Index:    1,
				Status:   "completed",
				Duration: 12,
				Phase:    "finalize",
			}},
		}},
	})

	if !strings.Contains(text, "harness,routing") {
		t.Fatalf("expected feature summary, got %q", text)
	}
}
