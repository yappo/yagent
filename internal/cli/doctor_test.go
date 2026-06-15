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

func TestRenderDoctorResultIncludesProbeAndWarnings(t *testing.T) {
	text := renderDoctorResult(llmcheck.Result{
		ServerName:      "local",
		URL:             "http://127.0.0.1:1234",
		API:             "chat_completions",
		Model:           "Qwen/Qwen3.6-35B-A3B",
		Endpoint:        "http://127.0.0.1:1234/v1/models",
		ModelFound:      true,
		ModelExactMatch: false,
		MatchedModel:    "qwen3.6-35b-a3b-q4_k_m",
		Warnings:        []string{"model id differs"},
		Recommendations: []llmcheck.Recommendation{{
			Area:        "generation",
			Setting:     "server.servers[].generation.max_output_tokens",
			Current:     "(unset)",
			Recommended: "8192",
			Reason:      "reserve enough output room",
		}},
		Runtime: llmcheck.RuntimeResult{
			Requested:        true,
			Endpoint:         "http://127.0.0.1:1234/api/v1/models",
			ModelFound:       true,
			Loaded:           true,
			LoadedInstance:   "qwen3.6-35b-a3b-q4_k_m",
			ContextLength:    32768,
			MaxContextLength: 131072,
			Models:           []llmcheck.RuntimeModelSummary{{ID: "qwen3.6-35b-a3b-q4_k_m"}},
			MatchedModel: llmcheck.RuntimeModelSummary{
				ID:               "qwen3.6-35b-a3b-q4_k_m",
				Params:           "35B-A3B",
				Quantization:     "Q4_K_M",
				Format:           "gguf",
				ReasoningAllowed: []string{"on", "off"},
				ReasoningDefault: "on",
			},
		},
		Probe: llmcheck.ProbeResult{
			Requested:  true,
			Structured: true,
			Endpoint:   "http://127.0.0.1:1234/v1/chat/completions",
			Model:      "qwen3.6-35b-a3b-q4_k_m",
			OK:         true,
			Output:     `{"ok":true,"message":"yagent-ok"}`,
		},
	})
	for _, fragment := range []string{
		"match:  qwen3.6-35b-a3b-q4_k_m (fuzzy)",
		"probe_status: ok",
		"probe_format: json_schema",
		"runtime_status: ok",
		"runtime_context: 32768/131072 tokens",
		"runtime_model: 35B-A3B Q4_K_M gguf",
		"Recommendations:",
		"server.servers[].generation.max_output_tokens: (unset) -> 8192",
		"Warnings:",
		"model id differs",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("expected doctor output to contain %q, got %s", fragment, text)
		}
	}
}

func TestDoctorResultJSONUsesStableKeys(t *testing.T) {
	data, err := json.Marshal(llmcheck.Result{
		ServerName:      "local",
		URL:             "http://127.0.0.1:1234",
		API:             "chat_completions",
		Model:           "Qwen/Qwen3.6-35B-A3B",
		Endpoint:        "http://127.0.0.1:1234/v1/models",
		ModelFound:      true,
		ModelExactMatch: true,
		Recommendations: []llmcheck.Recommendation{{
			Area:        "lmstudio",
			Setting:     "loaded context_length",
			Current:     "8192",
			Recommended: "32768",
			Reason:      "raise context",
		}},
		Runtime: llmcheck.RuntimeResult{
			Requested:        true,
			Endpoint:         "http://127.0.0.1:1234/api/v1/models",
			ModelFound:       true,
			Loaded:           true,
			ContextLength:    8192,
			MaxContextLength: 131072,
		},
	})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	text := string(data)
	for _, want := range []string{`"server_name"`, `"model_found"`, `"recommendations"`, `"context_length"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected JSON to contain %s, got %s", want, text)
		}
	}
}

func TestDoctorGateError(t *testing.T) {
	if err := doctorGateError(llmcheck.Result{Problems: []string{"bad"}}, doctorGateOptions{}); err == nil {
		t.Fatalf("expected problems to fail")
	}
	if err := doctorGateError(llmcheck.Result{Warnings: []string{"warn"}}, doctorGateOptions{}); err != nil {
		t.Fatalf("did not expect warning to fail by default: %v", err)
	}
	if err := doctorGateError(llmcheck.Result{Warnings: []string{"warn"}}, doctorGateOptions{FailOnWarning: true}); err == nil {
		t.Fatalf("expected fail-on-warning to fail")
	}
	if err := doctorGateError(llmcheck.Result{Recommendations: []llmcheck.Recommendation{{Setting: "x"}}}, doctorGateOptions{FailOnRecommendation: true}); err == nil {
		t.Fatalf("expected fail-on-recommendation to fail")
	}
}

func TestDoctorCommandIncludesRecommendedConfigFlags(t *testing.T) {
	configPath := ""
	command := newDoctorCommand(&configPath)
	for _, name := range []string{"write-recommended-config", "force-recommended-config", "save-audit"} {
		if command.Flags().Lookup(name) == nil {
			t.Fatalf("expected doctor flag %s", name)
		}
	}
}

func TestSaveDoctorAuditWritesScratchRecord(t *testing.T) {
	store := &fakeDoctorAuditStore{}
	record := llmcheck.NewAuditRecord(llmcheck.Result{
		ServerName:   "local",
		URL:          "http://127.0.0.1:1234",
		API:          "chat_completions",
		Model:        "Qwen/Qwen3.6-35B-A3B",
		ModelFound:   true,
		MatchedModel: "qwen3.6-35b-a3b-q4_k_m",
		Runtime: llmcheck.RuntimeResult{
			Requested:     true,
			Loaded:        true,
			ContextLength: 32768,
		},
	}, time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC))

	if err := saveDoctorAudit(context.Background(), store, record); err != nil {
		t.Fatalf("saveDoctorAudit returned error: %v", err)
	}
	if len(store.records) != 1 {
		t.Fatalf("expected one scratch record, got %+v", store.records)
	}
	scratch := store.records[0]
	if scratch.Kind != llmcheck.AuditScratchKind || scratch.ID != record.ID {
		t.Fatalf("unexpected scratch record: %+v", scratch)
	}
	if !strings.Contains(scratch.Summary, "local") || !strings.Contains(scratch.Summary, "qwen3.6-35b-a3b-q4_k_m") {
		t.Fatalf("unexpected summary: %q", scratch.Summary)
	}
	var decoded llmcheck.AuditRecord
	if err := json.Unmarshal(scratch.Payload, &decoded); err != nil {
		t.Fatalf("payload did not decode: %v", err)
	}
	if decoded.Runtime.ContextLength != 32768 {
		t.Fatalf("unexpected payload: %+v", decoded)
	}
}

func TestRenderDoctorAuditSavedResult(t *testing.T) {
	text := renderDoctorAuditSavedResult(llmcheck.AuditRecord{ID: "llm-doctor-1"})
	for _, want := range []string{"Runtime audit", "status: saved", "llm-doctor-1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in audit save output, got %q", want, text)
		}
	}
}

type fakeDoctorAuditStore struct {
	records []domain.ScratchRecord
}

func (s *fakeDoctorAuditStore) SaveScratch(_ context.Context, record domain.ScratchRecord) error {
	s.records = append(s.records, record)
	return nil
}

func TestRenderDoctorRecommendedConfigResult(t *testing.T) {
	text := renderDoctorRecommendedConfigResult(llmcheck.ConfigRecommendationPlan{
		Changes: []llmcheck.ConfigChange{{
			Setting:     `server.servers["local"].generation.max_output_tokens`,
			Current:     "(unset)",
			Recommended: "2048",
		}},
		External: []llmcheck.ConfigChange{{
			Setting:     "loaded context_length",
			Current:     "8192",
			Recommended: "32768",
		}},
	}, llmcheck.ConfigWriteResult{
		Path:    "/tmp/recommended.toml",
		Status:  "created",
		Bytes:   123,
		Changes: 1,
	})
	for _, want := range []string{
		"Recommended config",
		"status:  created",
		"/tmp/recommended.toml",
		`server.servers["local"].generation.max_output_tokens: (unset) -> 2048`,
		"loaded context_length: 8192 -> 32768",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in recommended config output, got %q", want, text)
		}
	}
}
