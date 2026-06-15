package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"yagent/internal/domain"
	"yagent/internal/usecase/llmcheck"
)

func TestRenderPermissionAuditText(t *testing.T) {
	rendered := renderPermissionAuditText([]domain.PermissionDecisionRecord{{
		CreatedAt:    time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC),
		Decision:     domain.PermissionAllowOnce,
		ToolName:     "fs_write",
		Resource:     "/tmp/a.txt",
		AgentID:      "coder",
		Phase:        domain.RunPhaseExecute,
		Risk:         "high",
		Scope:        "/tmp/a.txt",
		SideEffects:  []string{"filesystem_write"},
		PreviewKind:  "diff",
		PreviewLines: 3,
		ChangeFiles:  1,
		Additions:    1,
		Deletions:    1,
	}})

	for _, want := range []string{
		"Permission decisions",
		"count: 1",
		"allow_once fs_write /tmp/a.txt",
		"agent=coder",
		"phase=execute",
		"effects=filesystem_write",
		"preview=diff:3",
		"changes=files=1 +1 -1",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in rendered audit, got %q", want, rendered)
		}
	}
}

func TestAuditCommandIncludesRunStateSubcommands(t *testing.T) {
	var configPath string
	command := newAuditCommand(&configPath)
	for _, name := range []string{"runs", "artifacts", "observations", "mutations", "bundle", "trace", "permissions", "executions", "conversations", "runtime", "models", "search"} {
		child, _, err := command.Find([]string{name})
		if err != nil {
			t.Fatalf("Find(%s) returned error: %v", name, err)
		}
		if child == nil || child.Name() != name {
			t.Fatalf("expected %s subcommand, got %+v", name, child)
		}
	}
}

func TestRenderConversationAuditText(t *testing.T) {
	rendered := renderConversationAuditText([]conversationAuditSummary{{
		ID:                  "turn-run-1-1",
		RunID:               "run-1",
		Status:              domain.RunStatusCompleted,
		CurrentPhase:        domain.RunPhaseFinalize,
		Attempt:             2,
		Profile:             "fast",
		UserGoal:            "inspect",
		RequestMessageCount: 1,
		ContextMessageCount: 3,
		OutputSummary:       "done",
		EventCount:          4,
		ToolCallCount:       1,
		ToolFailureCount:    0,
		ModelCallCount:      2,
		CacheHitCount:       1,
		ArtifactCount:       5,
		CompletedAt:         time.Date(2026, 6, 13, 12, 4, 0, 0, time.UTC),
	}})

	for _, want := range []string{
		"Conversations",
		"count: 1",
		"turn-run-1-1 completed phase=finalize attempt=2",
		"run=run-1",
		"profile=fast",
		"request_messages=1 context_messages=3 events=4 tools=1 tool_failures=0 models=2 cache_hits=1 artifacts=5",
		"output=done",
		"goal=inspect",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in rendered conversations, got %q", want, rendered)
		}
	}
}

func TestLoadConversationAuditFiltersAndIncludesMessages(t *testing.T) {
	store := fakeConversationAuditStore{items: []domain.ConversationTurnRecord{{
		ID:              "turn-1",
		RunID:           "run-1",
		RootRunID:       "root-1",
		Status:          domain.RunStatusCompleted,
		RequestMessages: []domain.Message{{Role: domain.RoleUser, Content: "hello"}},
		ContextMessages: []domain.Message{{Role: domain.RoleUser, Content: "hello"}},
		OutputMessage:   domain.Message{Role: domain.RoleAssistant, Content: "done"},
		ArtifactRefs:    []domain.ArtifactReference{{ID: "artifact-1", Kind: "final_response"}},
	}, {
		ID:    "turn-2",
		RunID: "run-2",
	}}}

	records, err := loadConversationAudit(context.Background(), store, "root-1", 10, true)
	if err != nil {
		t.Fatalf("loadConversationAudit returned error: %v", err)
	}
	if len(records) != 1 || records[0].ID != "turn-1" {
		t.Fatalf("unexpected conversation records: %+v", records)
	}
	if len(records[0].RequestMessages) != 1 || records[0].OutputMessage == nil || records[0].OutputMessage.Content != "done" || len(records[0].ArtifactRefs) != 1 {
		t.Fatalf("expected full message fields, got %+v", records[0])
	}

	records, err = loadConversationAudit(context.Background(), store, "run-1", 10, false)
	if err != nil {
		t.Fatalf("loadConversationAudit returned error: %v", err)
	}
	if len(records) != 1 || len(records[0].RequestMessages) != 0 || records[0].OutputMessage != nil {
		t.Fatalf("expected summarized message fields, got %+v", records)
	}
}

func TestSearchAuditRecordsFindsAcrossAuditStores(t *testing.T) {
	model := domain.ModelInvocationRecord{
		ID:          "model-1",
		RunID:       "run-1",
		RootRunID:   "run-1",
		AgentID:     "coder",
		Phase:       domain.RunPhaseExecute,
		ServerName:  "openai",
		Model:       "gpt-5.5",
		ProfileName: "strong",
		Fallback:    true,
		Success:     true,
		CreatedAt:   time.Date(2026, 6, 13, 12, 3, 0, 0, time.UTC),
	}
	modelPayload, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	store := fakeAuditBundleStore{
		fakeAuditRunStore: fakeAuditRunStore{
			ids: []string{"run-1"},
			runs: map[string]*domain.RunState{
				"run-1": {
					ID:           "run-1",
					RootRunID:    "run-1",
					Status:       domain.RunStatusFailed,
					CurrentPhase: domain.RunPhaseExecute,
					UserGoal:     "fix permission flow",
					Artifacts: []domain.RunArtifact{{
						ID:        "artifact-1",
						Kind:      "final_response",
						Name:      "Final response",
						Summary:   "permission repair failed",
						CreatedAt: time.Date(2026, 6, 13, 12, 4, 0, 0, time.UTC),
					}},
					CreatedAt: time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC),
					UpdatedAt: time.Date(2026, 6, 13, 12, 5, 0, 0, time.UTC),
				},
			},
		},
		executions: []domain.ToolExecutionRecord{{
			ID:             "exec-1",
			SessionID:      "run-1",
			ToolName:       "fs_write",
			AgentID:        "coder",
			Failure:        "permission denied",
			Success:        false,
			UpdatedAt:      time.Date(2026, 6, 13, 12, 2, 0, 0, time.UTC),
			WriteSet:       []string{"/repo/README.md"},
			ToolClass:      domain.ToolClassMutate,
			Source:         "fs",
			NormalizedArgs: `{"path":"/repo/README.md"}`,
		}},
		conversations: []domain.ConversationTurnRecord{{
			ID:               "turn-1",
			RunID:            "run-1",
			RootRunID:        "run-1",
			Status:           domain.RunStatusFailed,
			CurrentPhase:     domain.RunPhaseExecute,
			UserGoal:         "fix permission flow",
			OutputMessage:    domain.Message{Role: domain.RoleAssistant, Content: "permission denied while writing"},
			CompletedAt:      time.Date(2026, 6, 13, 12, 3, 30, 0, time.UTC),
			ToolFailureCount: 1,
		}},
		scratch: []domain.ScratchRecord{{
			ID:        "model-1",
			Kind:      domain.ScratchKindModelInvocation,
			SessionID: "run-1",
			Payload:   modelPayload,
			CreatedAt: model.CreatedAt,
		}},
	}

	results, err := searchAuditRecords(context.Background(), store, auditSearchOptions{
		Query: "permission denied",
		RunID: "run-1",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("searchAuditRecords returned error: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected search results")
	}
	foundToolFailure := false
	for _, result := range results {
		if result.Kind == "tool" && result.ID == "exec-1" && containsString(result.MatchedFields, "failure") {
			foundToolFailure = true
			break
		}
	}
	if !foundToolFailure {
		t.Fatalf("expected tool failure search result, got %+v", results)
	}

	modelResults, err := searchAuditRecords(context.Background(), store, auditSearchOptions{
		Query:   "gpt-5.5 fallback",
		Kind:    "model",
		AgentID: "coder",
		Phase:   domain.RunPhaseExecute,
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("model search returned error: %v", err)
	}
	if len(modelResults) != 1 || modelResults[0].Kind != "model" || modelResults[0].ID != "model-1" {
		t.Fatalf("unexpected model search results: %+v", modelResults)
	}
}

func TestRenderAuditSearchText(t *testing.T) {
	rendered := renderAuditSearchText("permission", []auditSearchResult{{
		Kind:          "tool",
		ID:            "exec-1",
		RunID:         "run-1",
		AgentID:       "coder",
		Phase:         domain.RunPhaseExecute,
		Status:        "fail",
		Name:          "fs_write",
		Summary:       "permission denied",
		MatchedFields: []string{"failure"},
		Timestamp:     time.Date(2026, 6, 13, 12, 2, 0, 0, time.UTC),
	}})
	for _, want := range []string{
		"Audit search",
		"query: permission",
		"count: 1",
		"tool exec-1",
		"run=run-1",
		"agent=coder",
		"phase=execute",
		"matches=failure",
		"summary=permission denied",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in rendered search, got %q", want, rendered)
		}
	}
}

func TestAuditModelsCommandIncludesSummaryFlag(t *testing.T) {
	var configPath string
	command := newAuditModelsCommand(&configPath)
	if command.Flags().Lookup("summary") == nil {
		t.Fatalf("expected summary flag")
	}
}

func TestModelInvocationRecordsFromScratch(t *testing.T) {
	record := domain.ModelInvocationRecord{
		ID:         "model-1",
		RunID:      "run-1",
		RootRunID:  "root-1",
		ServerName: "local",
		AgentID:    "coder",
		Model:      "qwen",
		Success:    true,
	}
	payload, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	input := []domain.ScratchRecord{{
		ID:        "model-1",
		Kind:      domain.ScratchKindModelInvocation,
		SessionID: "root-1",
		Payload:   payload,
	}, {
		ID:   "other",
		Kind: "permission_decision",
	}}

	records := modelInvocationRecordsFromScratch(input, modelInvocationFilter{RunID: "root-1", ServerName: "local", AgentID: "coder"})
	if len(records) != 1 || records[0].ID != "model-1" {
		t.Fatalf("unexpected model records: %+v", records)
	}
	if got := modelInvocationRecordsFromScratch(input, modelInvocationFilter{ServerName: "openai"}); len(got) != 0 {
		t.Fatalf("expected server filter to omit record, got %+v", got)
	}
}

func TestSummarizeModelInvocations(t *testing.T) {
	report := summarizeModelInvocations([]domain.ModelInvocationRecord{
		{
			ServerName:  "local",
			Model:       "qwen",
			API:         "chat_completions",
			ProfileName: "fast",
			AgentID:     "planner",
			Phase:       domain.RunPhasePlan,
			DurationMS:  100,
			Success:     true,
		},
		{
			ServerName:  "local",
			Model:       "qwen",
			API:         "chat_completions",
			ProfileName: "fast",
			AgentID:     "coder",
			Phase:       domain.RunPhaseExecute,
			DurationMS:  300,
			Success:     false,
		},
		{
			ServerName:  "openai",
			Model:       "gpt-5.5",
			API:         "responses",
			ProfileName: "strong",
			AgentID:     "coder",
			Phase:       domain.RunPhaseExecute,
			DurationMS:  200,
			Success:     true,
			Fallback:    true,
		},
	})
	if report.Records != 3 || report.Successes != 2 || report.Failures != 1 || report.Fallbacks != 1 || report.AvgDuration != 200 {
		t.Fatalf("unexpected summary: %+v", report)
	}
	if len(report.Groups) != 2 {
		t.Fatalf("expected two groups, got %+v", report.Groups)
	}
	var local modelInvocationGroupSummary
	for _, group := range report.Groups {
		if group.ServerName == "local" {
			local = group
			break
		}
	}
	if local.Records != 2 || local.Successes != 1 || local.Failures != 1 || local.AvgDuration != 200 || local.MaxDuration != 300 {
		t.Fatalf("unexpected local group: %+v", local)
	}
	if strings.Join(local.Agents, ",") != "coder,planner" || strings.Join(local.Phases, ",") != "execute,plan" {
		t.Fatalf("unexpected local group sets: %+v", local)
	}
}

func TestRenderModelInvocationSummaryText(t *testing.T) {
	rendered := renderModelInvocationSummaryText(modelInvocationSummaryReport{
		Records:     2,
		Successes:   1,
		Failures:    1,
		Fallbacks:   1,
		AvgDuration: 150,
		Groups: []modelInvocationGroupSummary{{
			ServerName:  "local",
			Model:       "qwen",
			API:         "chat_completions",
			ProfileName: "fast",
			Records:     2,
			Successes:   1,
			Failures:    1,
			Fallbacks:   1,
			AvgDuration: 150,
			MaxDuration: 200,
			Agents:      []string{"coder", "planner"},
			Phases:      []string{"execute", "plan"},
		}},
	})
	for _, want := range []string{
		"Model invocation summary",
		"records: 2",
		"success: 1",
		"failure: 1",
		"fallback: 1",
		"avg_duration: 150.0ms",
		"local qwen api=chat_completions profile=fast calls=2 success=1 failure=1 fallback=1 avg=150.0ms max=200ms",
		"agents=coder,planner",
		"phases=execute,plan",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in rendered summary, got %q", want, rendered)
		}
	}
}

func TestRenderModelInvocationAuditText(t *testing.T) {
	rendered := renderModelInvocationAuditText([]domain.ModelInvocationRecord{{
		CreatedAt:          time.Date(2026, 6, 14, 12, 2, 0, 0, time.UTC),
		Success:            true,
		ServerName:         "openai",
		Model:              "gpt-5.5",
		ProfileName:        "strong",
		AgentID:            "coder",
		Phase:              domain.RunPhaseExecute,
		API:                "responses",
		ResponseFormat:     "execution_plan",
		Fallback:           true,
		FallbackFromServer: "local",
		Messages:           3,
		Tools:              7,
		DurationMS:         1234,
		FinishReason:       "completed",
	}})
	for _, want := range []string{
		"Model invocations",
		"count: 1",
		"ok openai gpt-5.5",
		"profile=strong",
		"agent=coder",
		"phase=execute",
		"api=responses",
		"format=execution_plan",
		"fallback=true",
		"from=local",
		"messages=3 tools=7 duration=1234ms",
		"finish=completed",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in rendered model audit, got %q", want, rendered)
		}
	}
}

func TestDoctorAuditRecordsFromScratch(t *testing.T) {
	record := llmcheck.NewAuditRecord(llmcheck.Result{
		ServerName:   "local",
		Model:        "Qwen/Qwen3.6-35B-A3B",
		MatchedModel: "qwen3.6-35b-a3b-q4_k_m",
	}, time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC))
	payload, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	input := []domain.ScratchRecord{{
		ID:        "scratch-1",
		Kind:      llmcheck.AuditScratchKind,
		Payload:   payload,
		CreatedAt: record.CreatedAt,
	}, {
		ID:   "scratch-2",
		Kind: "permission_decision",
	}}

	records := doctorAuditRecordsFromScratch(input, "local")
	if len(records) != 1 || records[0].MatchedModel != "qwen3.6-35b-a3b-q4_k_m" {
		t.Fatalf("unexpected doctor audit records: %+v", records)
	}
	if got := doctorAuditRecordsFromScratch(input, "openai"); len(got) != 0 {
		t.Fatalf("expected server filter to omit local record, got %+v", got)
	}
}

func TestRenderDoctorRuntimeAuditText(t *testing.T) {
	falseValue := false
	rendered := renderDoctorRuntimeAuditText([]llmcheck.AuditRecord{{
		ID:              "llm-doctor-1",
		ServerName:      "local",
		API:             "chat_completions",
		Model:           "Qwen/Qwen3.6-35B-A3B",
		MatchedModel:    "qwen3.6-35b-a3b-q4_k_m",
		Warnings:        []string{"context small"},
		Recommendations: []llmcheck.Recommendation{{Setting: "loaded context_length"}},
		Runtime: llmcheck.RuntimeResult{
			Requested:        true,
			Loaded:           true,
			ContextLength:    32768,
			MaxContextLength: 131072,
			MatchedModel: llmcheck.RuntimeModelSummary{
				Quantization:      "Q4_K_M",
				TrainedForToolUse: &falseValue,
			},
		},
		Probe: llmcheck.ProbeResult{
			Requested:  true,
			Structured: true,
			OK:         true,
		},
		CreatedAt: time.Date(2026, 6, 14, 12, 1, 0, 0, time.UTC),
	}})

	for _, want := range []string{
		"LLM runtime audits",
		"count: 1",
		"warning server=local model=Qwen/Qwen3.6-35B-A3B",
		"matched=qwen3.6-35b-a3b-q4_k_m",
		"api=chat_completions",
		"runtime_loaded=true",
		"context=32768/131072",
		"quant=Q4_K_M",
		"probe=ok",
		"probe_format=json_schema",
		"warnings=1",
		"recommendations=1",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in runtime audit output, got %q", want, rendered)
		}
	}
}

func TestFilterObservationRecordsOmitsStaleByDefault(t *testing.T) {
	input := []domain.ObservationRecord{{
		ID:        "obs-1",
		SessionID: "run-1",
		ToolName:  "fs_read",
		Reusable:  true,
	}, {
		ID:        "obs-2",
		SessionID: "run-1",
		ToolName:  "fs_read",
		Stale:     true,
	}, {
		ID:        "obs-3",
		SessionID: "run-2",
		ToolName:  "git_diff",
	}}

	records := filterObservationRecords(input, "run-1", "fs_read", false)
	if len(records) != 1 || records[0].ID != "obs-1" {
		t.Fatalf("expected only fresh run-1 fs_read observation, got %+v", records)
	}

	withStale := filterObservationRecords(input, "run-1", "fs_read", true)
	if len(withStale) != 2 {
		t.Fatalf("expected stale observation when requested, got %+v", withStale)
	}
}

func TestRenderObservationAuditText(t *testing.T) {
	rendered := renderObservationAuditText([]domain.ObservationRecord{{
		ID:               "obs-1",
		SessionID:        "run-1",
		ToolName:         "fs_read",
		SemanticKey:      "fs_read:abc",
		Summary:          "README contents",
		OutputArtifactID: "artifact-1",
		ReadSet:          []string{"/repo/README.md"},
		SnapshotRevision: 7,
		Reusable:         true,
		UpdatedAt:        time.Date(2026, 6, 13, 12, 4, 0, 0, time.UTC),
	}})

	for _, want := range []string{
		"Observations",
		"count: 1",
		"obs-1 fs_read fs_read:abc",
		"session=run-1",
		"artifact=artifact-1",
		"reads=/repo/README.md",
		"snapshot=7",
		"reusable=true",
		"summary=README contents",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in rendered observations, got %q", want, rendered)
		}
	}
}

func TestFilterMutationRecords(t *testing.T) {
	input := []domain.MutationRecord{{
		ID:        "mut-1",
		SessionID: "run-1",
		ToolName:  "fs_write",
	}, {
		ID:        "mut-2",
		SessionID: "run-2",
		ToolName:  "patch_apply",
	}}

	records := filterMutationRecords(input, "run-2", "patch_apply")
	if len(records) != 1 || records[0].ID != "mut-2" {
		t.Fatalf("unexpected filtered mutations: %+v", records)
	}
}

func TestRenderMutationAuditText(t *testing.T) {
	rendered := renderMutationAuditText([]domain.MutationRecord{{
		ID:                  "mut-1",
		SessionID:           "run-1",
		AgentID:             "coder",
		ExecutionID:         "exec-1",
		ToolName:            "fs_write",
		WriteSet:            []string{"/repo/main.go"},
		MutationFingerprint: "fingerprint",
		BeforeRevision:      2,
		AfterRevision:       3,
		CreatedAt:           time.Date(2026, 6, 13, 12, 5, 0, 0, time.UTC),
	}})

	for _, want := range []string{
		"Mutations",
		"count: 1",
		"mut-1 fs_write",
		"session=run-1",
		"agent=coder",
		"execution=exec-1",
		"writes=/repo/main.go",
		"fingerprint=fingerprint",
		"revision=2->3",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in rendered mutations, got %q", want, rendered)
		}
	}
}

func TestBuildAuditBundleCollectsRunRecords(t *testing.T) {
	store := fakeAuditBundleStore{
		fakeAuditRunStore: fakeAuditRunStore{
			runs: map[string]*domain.RunState{
				"run-1": {
					ID:           "run-1",
					RootRunID:    "run-1",
					Status:       domain.RunStatusCompleted,
					CurrentPhase: domain.RunPhaseFinalize,
					UserGoal:     "ship harness",
					Artifacts: []domain.RunArtifact{{
						ID:      "artifact-1",
						Name:    "Final",
						Kind:    "final_response",
						Text:    "secret text",
						Payload: json.RawMessage(`{"secret":true}`),
					}},
				},
			},
			latest: "run-1",
		},
		executions: []domain.ToolExecutionRecord{{
			ID:        "exec-1",
			SessionID: "run-1",
			ToolName:  "fs_read",
			Output:    "secret output",
			Success:   true,
		}, {
			ID:        "exec-2",
			SessionID: "other-run",
			ToolName:  "fs_read",
		}},
		observations: []domain.ObservationRecord{{
			ID:        "obs-1",
			SessionID: "run-1",
			ToolName:  "fs_read",
			Reusable:  true,
		}, {
			ID:        "obs-2",
			SessionID: "run-1",
			Stale:     true,
		}},
		mutations: []domain.MutationRecord{{
			ID:        "mut-1",
			SessionID: "run-1",
			ToolName:  "fs_write",
		}},
		scratch: []domain.ScratchRecord{{
			ID:        "scratch-1",
			Kind:      "permission_decision",
			SessionID: "run-1",
			Payload:   json.RawMessage(`{"run_id":"run-1","tool_name":"fs_write","decision":"allow_once"}`),
		}, {
			ID:        "model-1",
			Kind:      domain.ScratchKindModelInvocation,
			SessionID: "run-1",
			Payload:   json.RawMessage(`{"id":"model-1","root_run_id":"run-1","server_name":"local","model":"qwen","success":true}`),
		}},
	}

	bundle, err := buildAuditBundle(context.Background(), store, auditBundleOptions{RunID: "latest", Limit: 100})
	if err != nil {
		t.Fatalf("buildAuditBundle returned error: %v", err)
	}
	if bundle.Run.ID != "run-1" {
		t.Fatalf("unexpected bundle run: %+v", bundle.Run)
	}
	if len(bundle.Artifacts) != 1 || bundle.Artifacts[0].Text != "" || len(bundle.Artifacts[0].Payload) != 0 {
		t.Fatalf("expected artifact body to be omitted by default, got %+v", bundle.Artifacts)
	}
	if len(bundle.Executions) != 1 || bundle.Executions[0].ID != "exec-1" || bundle.Executions[0].Output != "" {
		t.Fatalf("expected run execution without output, got %+v", bundle.Executions)
	}
	if len(bundle.Observations) != 1 || bundle.Observations[0].ID != "obs-1" {
		t.Fatalf("expected fresh observation only, got %+v", bundle.Observations)
	}
	if len(bundle.Mutations) != 1 || bundle.Mutations[0].ID != "mut-1" {
		t.Fatalf("expected run mutation, got %+v", bundle.Mutations)
	}
	if len(bundle.Permissions) != 1 || bundle.Permissions[0].ToolName != "fs_write" {
		t.Fatalf("expected permission record, got %+v", bundle.Permissions)
	}
	if len(bundle.Models) != 1 || bundle.Models[0].ID != "model-1" {
		t.Fatalf("expected model invocation record, got %+v", bundle.Models)
	}
	if bundle.FullRun != nil {
		t.Fatalf("did not expect full run by default")
	}
}

func TestBuildAuditBundleCanIncludeFullBodies(t *testing.T) {
	store := fakeAuditBundleStore{
		fakeAuditRunStore: fakeAuditRunStore{
			runs: map[string]*domain.RunState{
				"run-1": {
					ID: "run-1",
					Artifacts: []domain.RunArtifact{{
						ID:      "artifact-1",
						Text:    "artifact text",
						Payload: json.RawMessage(`{"ok":true}`),
					}},
				},
			},
		},
		executions: []domain.ToolExecutionRecord{{
			ID:        "exec-1",
			SessionID: "run-1",
			Output:    "tool output",
		}},
		observations: []domain.ObservationRecord{{
			ID:        "obs-1",
			SessionID: "run-1",
			Stale:     true,
		}},
	}

	bundle, err := buildAuditBundle(context.Background(), store, auditBundleOptions{
		RunID:                  "run-1",
		Limit:                  100,
		IncludeOutput:          true,
		IncludeArtifactContent: true,
		IncludeArtifactPayload: true,
		IncludeStale:           true,
		FullRun:                true,
	})
	if err != nil {
		t.Fatalf("buildAuditBundle returned error: %v", err)
	}
	if len(bundle.Artifacts) != 1 || bundle.Artifacts[0].Text != "artifact text" || len(bundle.Artifacts[0].Payload) == 0 {
		t.Fatalf("expected artifact body, got %+v", bundle.Artifacts)
	}
	if len(bundle.Executions) != 1 || bundle.Executions[0].Output != "tool output" {
		t.Fatalf("expected execution output, got %+v", bundle.Executions)
	}
	if len(bundle.Observations) != 1 || bundle.Observations[0].ID != "obs-1" {
		t.Fatalf("expected stale observation when requested, got %+v", bundle.Observations)
	}
	if bundle.FullRun == nil || bundle.FullRun.ID != "run-1" {
		t.Fatalf("expected full run, got %+v", bundle.FullRun)
	}
}

func TestRenderAuditBundleText(t *testing.T) {
	rendered := renderAuditBundleText(auditBundle{
		Run: runAuditSummary{
			ID:           "run-1",
			Status:       domain.RunStatusCompleted,
			CurrentPhase: domain.RunPhaseFinalize,
			Attempt:      2,
			UserGoal:     "ship harness",
		},
		Artifacts:    []artifactAuditSummary{{ID: "artifact-1"}},
		Executions:   []domain.ToolExecutionRecord{{ID: "exec-1"}},
		Models:       []domain.ModelInvocationRecord{{ID: "model-1"}},
		Observations: []domain.ObservationRecord{{ID: "obs-1"}},
		Mutations:    []domain.MutationRecord{{ID: "mut-1"}},
		Permissions:  []domain.PermissionDecisionRecord{{ToolName: "fs_write"}},
	})

	for _, want := range []string{
		"Audit bundle",
		"run: run-1 status=completed phase=finalize attempt=2",
		"goal: ship harness",
		"artifacts: 1",
		"executions: 1",
		"models: 1",
		"observations: 1",
		"mutations: 1",
		"permissions: 1",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in rendered bundle, got %q", want, rendered)
		}
	}
}

func TestBuildAuditTraceCollectsChronologicalSpans(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	modelPayload, err := json.Marshal(domain.ModelInvocationRecord{
		ID:                 "model-1",
		RunID:              "coder-1",
		RootRunID:          "run-1",
		AgentID:            "coder",
		Phase:              domain.RunPhaseExecute,
		Attempt:            2,
		ServerName:         "openai",
		Model:              "gpt-5.5",
		API:                "responses",
		ProfileName:        "strong",
		Fallback:           true,
		FallbackFromServer: "local",
		Messages:           3,
		Tools:              4,
		DurationMS:         750,
		Success:            true,
		CreatedAt:          now.Add(1500 * time.Millisecond),
	})
	if err != nil {
		t.Fatal(err)
	}
	permissionPayload, err := json.Marshal(domain.PermissionDecisionRecord{
		RunID:       "coder-1",
		RootRunID:   "run-1",
		AgentID:     "coder",
		Phase:       domain.RunPhaseExecute,
		Attempt:     2,
		ToolName:    "fs_write",
		Resource:    "/repo/main.go",
		Risk:        "high",
		Decision:    domain.PermissionAllowOnce,
		PreviewKind: "diff",
		CreatedAt:   now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	store := fakeAuditBundleStore{
		fakeAuditRunStore: fakeAuditRunStore{
			runs: map[string]*domain.RunState{
				"run-1": {
					ID:           "run-1",
					RootRunID:    "run-1",
					Status:       domain.RunStatusCompleted,
					CurrentPhase: domain.RunPhaseFinalize,
					Attempt:      2,
					UserGoal:     "ship trace",
					CreatedAt:    now,
					UpdatedAt:    now.Add(5 * time.Second),
					WorkUnits: []domain.WorkUnit{{
						ID:          "unit-1",
						Kind:        "implementation",
						Role:        "coder",
						Phase:       domain.RunPhaseExecute,
						Attempt:     2,
						Task:        "edit files",
						Status:      "done",
						StartedAt:   now.Add(time.Second),
						CompletedAt: now.Add(3 * time.Second),
					}},
					Artifacts: []domain.RunArtifact{{
						ID:        "artifact-1",
						Name:      "Final",
						Kind:      "final_response",
						Phase:     domain.RunPhaseFinalize,
						AgentID:   "manager",
						Summary:   "done",
						CreatedAt: now.Add(4 * time.Second),
					}},
				},
			},
			latest: "run-1",
		},
		executions: []domain.ToolExecutionRecord{{
			ID:        "exec-1",
			SessionID: "run-1",
			AgentID:   "coder",
			ToolName:  "fs_write",
			Success:   true,
			WriteSet:  []string{"/repo/main.go"},
			CreatedAt: now.Add(2500 * time.Millisecond),
			UpdatedAt: now.Add(2600 * time.Millisecond),
		}},
		mutations: []domain.MutationRecord{{
			ID:        "mut-1",
			SessionID: "run-1",
			AgentID:   "coder",
			ToolName:  "fs_write",
			WriteSet:  []string{"/repo/main.go"},
			CreatedAt: now.Add(2700 * time.Millisecond),
		}},
		scratch: []domain.ScratchRecord{{
			ID:        "model-1",
			Kind:      domain.ScratchKindModelInvocation,
			SessionID: "run-1",
			Payload:   modelPayload,
		}, {
			ID:        "permission-1",
			Kind:      "permission_decision",
			SessionID: "run-1",
			Payload:   permissionPayload,
		}},
	}

	trace, err := buildAuditTrace(context.Background(), store, auditTraceOptions{RunID: "latest", Limit: 100})
	if err != nil {
		t.Fatalf("buildAuditTrace returned error: %v", err)
	}
	if trace.Run.ID != "run-1" || trace.Summary.Models != 1 || trace.Summary.Tools != 1 || trace.Summary.Permissions != 1 || trace.Summary.Mutations != 1 || trace.Summary.Artifacts != 1 || trace.Summary.Fallbacks != 1 {
		t.Fatalf("unexpected trace summary: %+v", trace)
	}
	if len(trace.Spans) == 0 || trace.Spans[0].ID != "run:run-1" {
		t.Fatalf("expected run span first, got %+v", trace.Spans)
	}
	filtered, err := buildAuditTrace(context.Background(), store, auditTraceOptions{RunID: "run-1", Limit: 100, Kind: "model"})
	if err != nil {
		t.Fatalf("buildAuditTrace filtered returned error: %v", err)
	}
	if len(filtered.Spans) != 1 || filtered.Spans[0].ID != "model:model-1" || filtered.Summary.Fallbacks != 1 {
		t.Fatalf("unexpected filtered trace: %+v", filtered)
	}
}

func TestRenderAuditTraceText(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	rendered := renderAuditTraceText(auditTrace{
		Run: runAuditSummary{
			ID:           "run-1",
			Status:       domain.RunStatusCompleted,
			CurrentPhase: domain.RunPhaseFinalize,
			Attempt:      2,
			UserGoal:     "ship harness",
		},
		Summary: auditTraceSummary{
			Spans:      2,
			Models:     1,
			Tools:      1,
			Fallbacks:  1,
			DurationMS: 1234,
		},
		Spans: []auditTraceSpan{{
			ID:         "model:model-1",
			Kind:       "model",
			Name:       "openai/gpt-5.5",
			Status:     "ok",
			AgentID:    "coder",
			Phase:      domain.RunPhaseExecute,
			Attempt:    2,
			StartedAt:  &now,
			DurationMS: 1234,
			Details: map[string]any{
				"server":   "openai",
				"model":    "gpt-5.5",
				"api":      "responses",
				"profile":  "strong",
				"fallback": true,
				"tools":    4,
			},
		}},
	})

	for _, want := range []string{
		"Trace",
		"run: run-1 status=completed phase=finalize attempt=2",
		"goal: ship harness",
		"spans: 2 models=1 tools=1",
		"fallbacks=1 duration=1234ms",
		"model model:model-1 status=ok name=openai/gpt-5.5 agent=coder phase=execute attempt=2 duration=1234ms",
		"api=responses",
		"fallback=true",
		"server=openai",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in rendered trace, got %q", want, rendered)
		}
	}
}

func TestRenderRunAuditText(t *testing.T) {
	rendered := renderRunAuditText([]runAuditSummary{{
		ID:                "run-1",
		RootRunID:         "root-1",
		Status:            domain.RunStatusCompleted,
		CurrentPhase:      domain.RunPhaseFinalize,
		Attempt:           2,
		Profile:           "strong",
		UserGoal:          "inspect run state",
		MessageCount:      3,
		PlanCount:         4,
		WorkUnitCount:     5,
		ArtifactCount:     6,
		CheckpointCount:   7,
		VerificationCount: 8,
		UpdatedAt:         time.Date(2026, 6, 13, 12, 2, 0, 0, time.UTC),
	}})

	for _, want := range []string{
		"Runs",
		"count: 1",
		"run-1 completed phase=finalize attempt=2",
		"root=root-1",
		"profile=strong",
		"messages=3 plan=4 work_units=5 artifacts=6 checkpoints=7 verification=8",
		"goal=inspect run state",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in rendered runs, got %q", want, rendered)
		}
	}
}

func TestLoadAuditRunLatest(t *testing.T) {
	store := fakeAuditRunStore{
		runs: map[string]*domain.RunState{
			"run-1": {
				ID: "run-1",
			},
		},
		latest: "run-1",
	}

	run, err := loadAuditRun(context.Background(), store, "latest")
	if err != nil {
		t.Fatalf("loadAuditRun returned error: %v", err)
	}
	if run == nil || run.ID != "run-1" {
		t.Fatalf("expected latest run, got %+v", run)
	}
}

func TestFilterArtifactSummariesOmitsContentAndPayloadByDefault(t *testing.T) {
	artifacts := []domain.RunArtifact{{
		ID:        "a-1",
		Name:      "Final response",
		Kind:      "final_response",
		Phase:     domain.RunPhaseFinalize,
		AgentID:   "manager",
		Summary:   "done",
		Text:      "secret text",
		Content:   "secret content",
		Payload:   json.RawMessage(`{"response":"secret"}`),
		CreatedAt: time.Date(2026, 6, 13, 12, 3, 0, 0, time.UTC),
	}, {
		ID:        "a-2",
		Name:      "Plan",
		Kind:      "execution_plan",
		CreatedAt: time.Date(2026, 6, 13, 12, 2, 0, 0, time.UTC),
	}}

	summaries := filterArtifactSummaries("run-1", artifacts, "", "final_response", false, false)
	if len(summaries) != 1 || summaries[0].ID != "a-1" {
		t.Fatalf("unexpected artifact summaries: %+v", summaries)
	}
	if summaries[0].Text != "" || summaries[0].Content != "" || len(summaries[0].Payload) != 0 {
		t.Fatalf("expected content and payload to be omitted by default, got %+v", summaries[0])
	}

	withBody := filterArtifactSummaries("run-1", artifacts, "a-1", "", true, true)
	if len(withBody) != 1 || withBody[0].Text != "secret text" || len(withBody[0].Payload) == 0 {
		t.Fatalf("expected selected artifact body and payload, got %+v", withBody)
	}
}

func TestRenderArtifactAuditText(t *testing.T) {
	rendered := renderArtifactAuditText([]artifactAuditSummary{{
		ID:        "a-1",
		RunID:     "run-1",
		Name:      "Final response",
		Kind:      "final_response",
		Phase:     domain.RunPhaseFinalize,
		AgentID:   "manager",
		Summary:   "done",
		Text:      "hello",
		Payload:   json.RawMessage(`{"response":"hello"}`),
		CreatedAt: time.Date(2026, 6, 13, 12, 3, 0, 0, time.UTC),
		References: []domain.ArtifactReference{{
			ID: "a-plan",
		}},
	}})

	for _, want := range []string{
		"Artifacts",
		"count: 1",
		"a-1 final_response Final response phase=finalize",
		"agent=manager",
		"run=run-1",
		"summary=done",
		"text=hello",
		"payload={\"response\":\"hello\"}",
		"refs=a-plan",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in rendered artifacts, got %q", want, rendered)
		}
	}
}

func TestLoadAuditRunsSortsByUpdatedAt(t *testing.T) {
	store := fakeAuditRunStore{
		runs: map[string]*domain.RunState{
			"old": {
				ID:        "old",
				Status:    domain.RunStatusCompleted,
				UpdatedAt: time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC),
			},
			"new": {
				ID:        "new",
				Status:    domain.RunStatusRunning,
				UpdatedAt: time.Date(2026, 6, 13, 12, 1, 0, 0, time.UTC),
			},
		},
		ids: []string{"old", "new"},
	}

	runs, err := loadAuditRuns(context.Background(), store, "", 1)
	if err != nil {
		t.Fatalf("loadAuditRuns returned error: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != "new" {
		t.Fatalf("expected newest run first with limit, got %+v", runs)
	}
}

type fakeAuditRunStore struct {
	ids    []string
	runs   map[string]*domain.RunState
	latest string
}

func (s fakeAuditRunStore) ListRuns() ([]string, error) {
	return append([]string(nil), s.ids...), nil
}

func (s fakeAuditRunStore) LoadRun(_ context.Context, id string) (*domain.RunState, error) {
	return s.runs[id], nil
}

func (s fakeAuditRunStore) LoadLatestRun(_ context.Context) (*domain.RunState, error) {
	if s.latest == "" {
		return nil, nil
	}
	return s.runs[s.latest], nil
}

type fakeAuditBundleStore struct {
	fakeAuditRunStore
	executions    []domain.ToolExecutionRecord
	observations  []domain.ObservationRecord
	mutations     []domain.MutationRecord
	scratch       []domain.ScratchRecord
	conversations []domain.ConversationTurnRecord
}

type fakeConversationAuditStore struct {
	items []domain.ConversationTurnRecord
}

func (s fakeConversationAuditStore) ListConversationTurns(_ context.Context, _ int) ([]domain.ConversationTurnRecord, error) {
	return append([]domain.ConversationTurnRecord(nil), s.items...), nil
}

func (s fakeAuditBundleStore) ListExecutions(_ context.Context, _ int) ([]domain.ToolExecutionRecord, error) {
	return append([]domain.ToolExecutionRecord(nil), s.executions...), nil
}

func (s fakeAuditBundleStore) ListObservations(_ context.Context, _ int) ([]domain.ObservationRecord, error) {
	return append([]domain.ObservationRecord(nil), s.observations...), nil
}

func (s fakeAuditBundleStore) ListMutations(_ context.Context, _ int) ([]domain.MutationRecord, error) {
	return append([]domain.MutationRecord(nil), s.mutations...), nil
}

func (s fakeAuditBundleStore) ListScratch(_ context.Context, _ int) ([]domain.ScratchRecord, error) {
	return append([]domain.ScratchRecord(nil), s.scratch...), nil
}

func (s fakeAuditBundleStore) ListConversationTurns(_ context.Context, _ int) ([]domain.ConversationTurnRecord, error) {
	return append([]domain.ConversationTurnRecord(nil), s.conversations...), nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestFilterExecutionRecordsOmitsOutputByDefault(t *testing.T) {
	input := []domain.ToolExecutionRecord{{
		ID:        "exec-1",
		SessionID: "run-1",
		ToolName:  "fs_read",
		Output:    "secret output",
	}, {
		ID:        "exec-2",
		SessionID: "run-2",
		ToolName:  "fs_write",
		Output:    "other output",
	}}
	records := filterExecutionRecords(input, "run-1", false)

	if len(records) != 1 || records[0].ID != "exec-1" {
		t.Fatalf("unexpected filtered records: %+v", records)
	}
	if records[0].Output != "" {
		t.Fatalf("expected output to be omitted, got %+v", records[0])
	}

	withOutput := filterExecutionRecords(input, "run-1", true)
	if len(withOutput) != 1 || withOutput[0].Output != "secret output" {
		t.Fatalf("unexpected records with output: %+v", withOutput)
	}
}

func TestRenderExecutionAuditText(t *testing.T) {
	rendered := renderExecutionAuditText([]domain.ToolExecutionRecord{{
		UpdatedAt:           time.Date(2026, 6, 13, 12, 1, 0, 0, time.UTC),
		SessionID:           "run-1",
		ToolName:            "fs_write",
		ToolClass:           domain.ToolClassMutate,
		AgentID:             "coder",
		SemanticKey:         "fs_write:abc",
		Success:             true,
		Source:              "fs",
		SideEffectClass:     domain.SideEffectWorkspace,
		WriteSet:            []string{"/tmp/a.txt"},
		MutationID:          "mut-1",
		MutationFingerprint: "fingerprint",
		Reusable:            false,
	}})

	for _, want := range []string{
		"Tool executions",
		"count: 1",
		"ok fs_write fs_write:abc",
		"agent=coder",
		"session=run-1",
		"class=mutate",
		"side_effect=workspace",
		"writes=/tmp/a.txt",
		"mutation=mut-1",
		"mutation_fingerprint=fingerprint",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in rendered execution audit, got %q", want, rendered)
		}
	}
}
