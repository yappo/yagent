package llmcheck

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yagent/internal/config"
)

func TestCheckFindsConfiguredModel(t *testing.T) {
	client := fakeHTTPClient(func(r *http.Request) (int, string) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		return http.StatusOK, `{"data":[{"id":"Qwen/Qwen3.6-35B-A3B"}]}`
	})

	result, err := New(client).Check(context.Background(), testConfig("http://lmstudio.test", "Qwen/Qwen3.6-35B-A3B"), "")
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if len(result.Problems) != 0 {
		t.Fatalf("unexpected problems: %+v", result.Problems)
	}
	if !result.ModelFound || result.MatchedModel != "Qwen/Qwen3.6-35B-A3B" {
		t.Fatalf("unexpected model match: %+v", result)
	}
}

func TestCheckUsesTokenEnvForModelList(t *testing.T) {
	t.Setenv("YAGENT_TEST_OPENAI_KEY", "secret-from-env")
	client := fakeHTTPClient(func(r *http.Request) (int, string) {
		if r.Header.Get("Authorization") != "Bearer secret-from-env" {
			t.Fatalf("expected Authorization header from token_env, got %q", r.Header.Get("Authorization"))
		}
		return http.StatusOK, `{"data":[{"id":"gpt-5.5"}]}`
	})

	cfg := testConfig("http://openai.test", "gpt-5.5")
	cfg.Server.Servers[0].TokenEnv = "YAGENT_TEST_OPENAI_KEY"
	result, err := New(client).Check(context.Background(), cfg, "")
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if len(result.Problems) != 0 || !result.ModelFound {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCheckAcceptsLMStudioQuantizedModelName(t *testing.T) {
	client := fakeHTTPClient(func(r *http.Request) (int, string) {
		return http.StatusOK, `{"data":[{"id":"qwen3.6-35b-a3b-q4_k_m"}]}`
	})

	result, err := New(client).Check(context.Background(), testConfig("http://lmstudio.test", "Qwen/Qwen3.6-35B-A3B"), "")
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !result.ModelFound || result.MatchedModel != "qwen3.6-35b-a3b-q4_k_m" {
		t.Fatalf("expected fuzzy model match, got %+v", result)
	}
	if len(result.Warnings) == 0 {
		t.Fatalf("expected fuzzy match warning")
	}
}

func TestCheckReportsMissingModel(t *testing.T) {
	client := fakeHTTPClient(func(r *http.Request) (int, string) {
		return http.StatusOK, `{"data":[{"id":"other-model"}]}`
	})

	result, err := New(client).Check(context.Background(), testConfig("http://lmstudio.test", "Qwen/Qwen3.6-35B-A3B"), "")
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if len(result.Problems) != 1 {
		t.Fatalf("expected one problem, got %+v", result.Problems)
	}
	if result.ModelFound {
		t.Fatalf("did not expect model match: %+v", result)
	}
}

func TestCheckReportsModelEndpointFailure(t *testing.T) {
	client := fakeHTTPClient(func(r *http.Request) (int, string) {
		return http.StatusNotFound, `no`
	})

	result, err := New(client).Check(context.Background(), testConfig("http://lmstudio.test", "Qwen/Qwen3.6-35B-A3B"), "")
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if len(result.Problems) != 1 {
		t.Fatalf("expected one problem, got %+v", result.Problems)
	}
	if len(result.Suggestions) == 0 {
		t.Fatalf("expected suggestions")
	}
}

func TestCheckProbeChatCompletionsStructuredOutput(t *testing.T) {
	var sawStructuredFormat bool
	client := fakeHTTPClient(func(r *http.Request) (int, string) {
		switch r.URL.Path {
		case "/v1/models":
			return http.StatusOK, `{"data":[{"id":"Qwen/Qwen3.6-35B-A3B"}]}`
		case "/v1/chat/completions":
			var payload map[string]any
			if err := jsonNewDecoder(r).Decode(&payload); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if _, ok := payload["response_format"].(map[string]any); ok {
				sawStructuredFormat = true
			}
			return http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"{\"ok\":true,\"message\":\"yagent-ok\"}"}}]}`
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		return http.StatusNotFound, `not found`
	})

	result, err := New(client).CheckWithOptions(context.Background(), testConfig("http://lmstudio.test", "Qwen/Qwen3.6-35B-A3B"), CheckOptions{
		ProbeStructured: true,
	})
	if err != nil {
		t.Fatalf("CheckWithOptions returned error: %v", err)
	}
	if len(result.Problems) != 0 {
		t.Fatalf("unexpected problems: %+v", result.Problems)
	}
	if !result.Probe.OK || !result.Probe.Structured || !sawStructuredFormat {
		t.Fatalf("expected successful structured probe, got %+v saw=%v", result.Probe, sawStructuredFormat)
	}
}

func TestCheckRuntimeMetadataReportsLocalModelSettings(t *testing.T) {
	client := fakeHTTPClient(func(r *http.Request) (int, string) {
		switch r.URL.Path {
		case "/v1/models":
			return http.StatusOK, `{"data":[{"id":"Qwen/Qwen3.6-35B-A3B"}]}`
		case "/api/v1/models":
			return http.StatusOK, `{"models":[{
				"type":"llm",
				"key":"Qwen/Qwen3.6-35B-A3B",
				"display_name":"Qwen 3.6 35B A3B",
				"quantization":{"name":"Q4_K_M","bits_per_weight":4},
				"size_bytes":21000000000,
				"params_string":"35B-A3B",
				"loaded_instances":[{"id":"Qwen/Qwen3.6-35B-A3B","config":{"context_length":8192}}],
				"max_context_length":131072,
				"format":"gguf",
				"capabilities":{"vision":false,"trained_for_tool_use":false,"reasoning":{"allowed_options":["on","off"],"default":"on"}},
				"variants":["Qwen/Qwen3.6-35B-A3B@q4_k_m"],
				"selected_variant":"Qwen/Qwen3.6-35B-A3B@q4_k_m"
			}]}`
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		return http.StatusNotFound, `not found`
	})

	result, err := New(client).CheckWithOptions(context.Background(), testConfig("http://lmstudio.test", "Qwen/Qwen3.6-35B-A3B"), CheckOptions{
		Runtime: true,
	})
	if err != nil {
		t.Fatalf("CheckWithOptions returned error: %v", err)
	}
	if len(result.Problems) != 0 {
		t.Fatalf("unexpected problems: %+v", result.Problems)
	}
	if !result.Runtime.ModelFound || !result.Runtime.Loaded || result.Runtime.ContextLength != 8192 || result.Runtime.MatchedModel.Quantization != "Q4_K_M" {
		t.Fatalf("unexpected runtime metadata: %+v", result.Runtime)
	}
	if !containsWarning(result.Warnings, "context_length") || !containsWarning(result.Warnings, "trained_for_tool_use") {
		t.Fatalf("expected runtime warnings, got %+v", result.Warnings)
	}
	if !containsRecommendation(result.Recommendations, "loaded context_length", "32768") ||
		!containsRecommendation(result.Recommendations, "server.servers[].generation.max_output_tokens", "2048") ||
		!containsRecommendation(result.Recommendations, "server.servers[].generation.temperature", "1") {
		t.Fatalf("expected runtime recommendations, got %+v", result.Recommendations)
	}
}

func TestCheckRuntimeMetadataFailureIsWarning(t *testing.T) {
	client := fakeHTTPClient(func(r *http.Request) (int, string) {
		switch r.URL.Path {
		case "/v1/models":
			return http.StatusOK, `{"data":[{"id":"Qwen/Qwen3.6-35B-A3B"}]}`
		case "/api/v1/models":
			return http.StatusNotFound, `not found`
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		return http.StatusNotFound, `not found`
	})

	result, err := New(client).CheckWithOptions(context.Background(), testConfig("http://lmstudio.test", "Qwen/Qwen3.6-35B-A3B"), CheckOptions{
		Runtime: true,
	})
	if err != nil {
		t.Fatalf("CheckWithOptions returned error: %v", err)
	}
	if len(result.Problems) != 0 {
		t.Fatalf("unexpected problems: %+v", result.Problems)
	}
	if result.Runtime.Error == "" || !containsWarning(result.Warnings, "runtime metadata") {
		t.Fatalf("expected runtime warning, got result=%+v warnings=%+v", result.Runtime, result.Warnings)
	}
}

func TestCheckProbeResponsesEndpoint(t *testing.T) {
	client := fakeHTTPClient(func(r *http.Request) (int, string) {
		switch r.URL.Path {
		case "/v1/models":
			return http.StatusOK, `{"data":[{"id":"gpt-5.5"}]}`
		case "/v1/responses":
			return http.StatusOK, `{"id":"resp_1","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"yagent-ok"}]}]}`
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		return http.StatusNotFound, `not found`
	})

	cfg := testConfig("http://lmstudio.test", "gpt-5.5")
	cfg.Server.Servers[0].API = "responses"
	result, err := New(client).CheckWithOptions(context.Background(), cfg, CheckOptions{Probe: true})
	if err != nil {
		t.Fatalf("CheckWithOptions returned error: %v", err)
	}
	if len(result.Problems) != 0 {
		t.Fatalf("unexpected problems: %+v", result.Problems)
	}
	if !result.Probe.OK || !strings.HasSuffix(result.Probe.Endpoint, "/v1/responses") {
		t.Fatalf("expected responses probe, got %+v", result.Probe)
	}
}

func TestCheckProbeReportsStructuredFailure(t *testing.T) {
	client := fakeHTTPClient(func(r *http.Request) (int, string) {
		switch r.URL.Path {
		case "/v1/models":
			return http.StatusOK, `{"data":[{"id":"Qwen/Qwen3.6-35B-A3B"}]}`
		case "/v1/chat/completions":
			return http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"not json"}}]}`
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		return http.StatusNotFound, `not found`
	})

	result, err := New(client).CheckWithOptions(context.Background(), testConfig("http://lmstudio.test", "Qwen/Qwen3.6-35B-A3B"), CheckOptions{
		ProbeStructured: true,
	})
	if err != nil {
		t.Fatalf("CheckWithOptions returned error: %v", err)
	}
	if result.Probe.OK {
		t.Fatalf("expected probe failure, got %+v", result.Probe)
	}
	if len(result.Problems) == 0 || !strings.Contains(result.Problems[0], "structured probe output") {
		t.Fatalf("expected structured probe problem, got %+v", result.Problems)
	}
}

func TestBuildRecommendedConfigAppliesYAgentSettings(t *testing.T) {
	cfg := testConfig("http://lmstudio.test", "Qwen/Qwen3.6-35B-A3B")
	cfg.Context.CompactAfterEstTokens = 12000
	cfg.Routing.Profiles = map[string]config.RoutingProfileConfig{
		"fast": {Server: "local", Model: "Qwen/Qwen3.6-35B-A3B"},
	}
	cfg.Agents = map[string]config.AgentOverride{
		"coder": {Model: "Qwen/Qwen3.6-35B-A3B"},
	}
	result := Result{
		ServerName:      "local",
		Model:           "Qwen/Qwen3.6-35B-A3B",
		ModelFound:      true,
		ModelExactMatch: false,
		MatchedModel:    "qwen3.6-35b-a3b-q4_k_m",
		Recommendations: []Recommendation{
			{
				Area:        "lmstudio",
				Setting:     "loaded context_length",
				Current:     "8192",
				Recommended: "32768",
				Reason:      "raise loaded context",
			},
			{
				Area:        "context",
				Setting:     "context.compact_after_est_tokens",
				Current:     "12000",
				Recommended: "4096",
				Reason:      "keep compaction below local context",
			},
			{
				Area:        "generation",
				Setting:     "server.servers[].generation.max_output_tokens",
				Current:     "(unset)",
				Recommended: "2048",
				Reason:      "reserve output room",
			},
			{
				Area:        "generation",
				Setting:     "server.servers[].generation.temperature",
				Current:     "(unset)",
				Recommended: "1",
				Reason:      "Qwen thinking mode",
			},
			{
				Area:        "generation",
				Setting:     "server.servers[].generation.top_k",
				Current:     "(unset)",
				Recommended: "20",
				Reason:      "Qwen thinking mode",
			},
		},
	}

	plan := BuildRecommendedConfig(cfg, result)
	server := plan.Config.Server.Servers[0]
	if server.Model != "qwen3.6-35b-a3b-q4_k_m" {
		t.Fatalf("expected exact LM Studio model id, got %+v", server)
	}
	if plan.Config.Routing.Profiles["fast"].Model != "qwen3.6-35b-a3b-q4_k_m" || plan.Config.Agents["coder"].Model != "qwen3.6-35b-a3b-q4_k_m" {
		t.Fatalf("expected model references to be synchronized, profiles=%+v agents=%+v", plan.Config.Routing.Profiles, plan.Config.Agents)
	}
	if plan.Config.Context.CompactAfterEstTokens != 4096 {
		t.Fatalf("expected context recommendation, got %+v", plan.Config.Context)
	}
	if server.Generation.MaxOutputTokens != 2048 || server.Generation.TopK != 20 {
		t.Fatalf("expected generation recommendation, got %+v", server.Generation)
	}
	if server.Generation.Temperature == nil || *server.Generation.Temperature != 1.0 {
		t.Fatalf("expected temperature recommendation, got %+v", server.Generation.Temperature)
	}
	if len(plan.External) != 1 || plan.External[0].Setting != "loaded context_length" {
		t.Fatalf("expected LM Studio runtime change to stay external, got %+v", plan.External)
	}
	if len(plan.Changes) < 5 {
		t.Fatalf("expected applied config changes, got %+v", plan.Changes)
	}
	if cfg.Server.Servers[0].Model != "Qwen/Qwen3.6-35B-A3B" {
		t.Fatalf("BuildRecommendedConfig mutated input config: %+v", cfg.Server.Servers[0])
	}
}

func TestWriteRecommendedConfigWritesParseableConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "recommended.toml")
	cfg := testConfig("http://lmstudio.test", "Qwen/Qwen3.6-35B-A3B")
	cfg.Execution.MaxParallelAgents = 1
	cfg.Execution.MaxHandoffDepth = 1
	cfg.Harness.MaxVerificationAttempts = 1
	cfg.Context.MaxRecentMessages = 1
	cfg.Memory.MaxRuns = 1
	cfg.Memory.MaxFacts = 1
	cfg.Benchmark.DefaultRuns = 1
	plan := BuildRecommendedConfig(cfg, Result{
		ServerName:   "local",
		Model:        "Qwen/Qwen3.6-35B-A3B",
		ModelFound:   true,
		MatchedModel: "qwen3.6-35b-a3b-q4_k_m",
	})

	result, err := WriteRecommendedConfig(path, plan, false)
	if err != nil {
		t.Fatalf("WriteRecommendedConfig returned error: %v", err)
	}
	if result.Status != "created" || result.Bytes == 0 {
		t.Fatalf("unexpected write result: %+v", result)
	}
	if _, err := config.Load(path); err != nil {
		data, _ := os.ReadFile(path)
		t.Fatalf("recommended config did not parse: %v\n%s", err, string(data))
	}
	if _, err := WriteRecommendedConfig(path, plan, false); err == nil {
		t.Fatalf("expected existing file without force to fail")
	}
	result, err = WriteRecommendedConfig(path, plan, true)
	if err != nil {
		t.Fatalf("forced WriteRecommendedConfig returned error: %v", err)
	}
	if result.Status != "overwritten" {
		t.Fatalf("expected overwritten status, got %+v", result)
	}
}

func TestNewAuditRecordCopiesDoctorResult(t *testing.T) {
	createdAt := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	record := NewAuditRecord(Result{
		ServerName:   "local",
		URL:          "http://127.0.0.1:1234",
		API:          "chat_completions",
		Model:        "Qwen/Qwen3.6-35B-A3B",
		ModelFound:   true,
		MatchedModel: "qwen3.6-35b-a3b-q4_k_m",
		Warnings:     []string{"warn"},
		Runtime: RuntimeResult{
			Requested:        true,
			Loaded:           true,
			ContextLength:    32768,
			MaxContextLength: 131072,
		},
		Probe: ProbeResult{
			Requested:  true,
			Structured: true,
			OK:         true,
		},
	}, createdAt)

	if record.ID == "" || !strings.HasPrefix(record.ID, "llm-doctor-") {
		t.Fatalf("unexpected audit id: %q", record.ID)
	}
	if record.CreatedAt != createdAt || record.Runtime.ContextLength != 32768 || !record.Probe.OK {
		t.Fatalf("unexpected audit record: %+v", record)
	}
	if summary := AuditSummary(record); !strings.Contains(summary, "warning") || !strings.Contains(summary, "local") || !strings.Contains(summary, "qwen3.6-35b-a3b-q4_k_m") {
		t.Fatalf("unexpected audit summary: %q", summary)
	}
}

func containsWarning(warnings []string, needle string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, needle) {
			return true
		}
	}
	return false
}

func containsRecommendation(recommendations []Recommendation, setting string, recommended string) bool {
	for _, item := range recommendations {
		if item.Setting == setting && item.Recommended == recommended {
			return true
		}
	}
	return false
}

func testConfig(url string, model string) config.Config {
	return config.Config{
		Server: config.ServerConfig{
			Default: "local",
			Servers: []config.ServerTarget{
				{
					Name:    "local",
					URL:     url,
					Model:   model,
					API:     "chat_completions",
					Timeout: config.Duration{Duration: time.Minute},
				},
			},
		},
	}
}

func jsonNewDecoder(r *http.Request) *json.Decoder {
	return json.NewDecoder(r.Body)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func fakeHTTPClient(handler func(*http.Request) (int, string)) *http.Client {
	return &http.Client{
		Timeout: time.Second,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			status, body := handler(r)
			return &http.Response{
				StatusCode: status,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    r,
			}, nil
		}),
	}
}
