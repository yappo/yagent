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

func TestCheckWithOptionsUsesExactModelOverride(t *testing.T) {
	client := fakeHTTPClient(func(r *http.Request) (int, string) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		return http.StatusOK, `{"data":[{"id":"candidate-model"}]}`
	})

	result, err := New(client).CheckWithOptions(context.Background(), testConfig("http://lmstudio.test", "server-model"), CheckOptions{Model: "candidate-model"})
	if err != nil {
		t.Fatalf("CheckWithOptions returned error: %v", err)
	}
	if result.Model != "candidate-model" || result.MatchedModel != "candidate-model" || !result.ModelExactMatch || len(result.Problems) != 0 {
		t.Fatalf("expected exact model override, got %+v", result)
	}
}

func TestDefaultClientUsesConfiguredServerTimeout(t *testing.T) {
	service := New(nil)
	configured := service.forServer(config.ServerTarget{Timeout: config.Duration{Duration: 42 * time.Second}})
	if configured == service {
		t.Fatal("expected server-specific service for default client")
	}
	if configured.client.Timeout != 42*time.Second {
		t.Fatalf("configured timeout = %s, want %s", configured.client.Timeout, 42*time.Second)
	}
	if service.client.Timeout != 5*time.Second {
		t.Fatalf("default client timeout changed to %s", service.client.Timeout)
	}
}

func TestInjectedClientRetainsItsTimeout(t *testing.T) {
	client := fakeHTTPClient(func(*http.Request) (int, string) { return http.StatusOK, `{}` })
	service := New(client)
	configured := service.forServer(config.ServerTarget{Timeout: config.Duration{Duration: 42 * time.Second}})
	if configured != service {
		t.Fatal("injected client must not be replaced")
	}
	if configured.client.Timeout != time.Second {
		t.Fatalf("injected client timeout = %s, want %s", configured.client.Timeout, time.Second)
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
	var maxTokens int
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
			maxTokens = int(payload["max_tokens"].(float64))
			messages := payload["messages"].([]any)
			prompt := messages[0].(map[string]any)["content"].(string)
			if !strings.Contains(prompt, "local model health check") || strings.Contains(prompt, `{"ok":true`) {
				t.Fatalf("structured probe prompt is not task-oriented: %q", prompt)
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
	if !result.Probe.OK || !result.Probe.Structured || !sawStructuredFormat || maxTokens != defaultProbeMaxOutputTokens {
		t.Fatalf("expected successful structured probe, got %+v saw=%v", result.Probe, sawStructuredFormat)
	}
}

func TestProbeOutputTokensUsesConfiguredGenerationLimit(t *testing.T) {
	if got := probeOutputTokens(config.GenerationConfig{}); got != defaultProbeMaxOutputTokens {
		t.Fatalf("default probe output tokens = %d, want %d", got, defaultProbeMaxOutputTokens)
	}
	if got := probeOutputTokens(config.GenerationConfig{MaxOutputTokens: 2048}); got != 2048 {
		t.Fatalf("configured probe output tokens = %d, want 2048", got)
	}
}

func TestCheckProbeDiagnosesReasoningBudgetExhaustion(t *testing.T) {
	client := fakeHTTPClient(func(r *http.Request) (int, string) {
		switch r.URL.Path {
		case "/v1/models":
			return http.StatusOK, `{"data":[{"id":"gemma-4"}]}`
		case "/v1/chat/completions":
			return http.StatusOK, `{"choices":[{"finish_reason":"length","message":{"role":"assistant","content":"","reasoning_content":"still reasoning"}}]}`
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		return http.StatusNotFound, `not found`
	})
	result, err := New(client).CheckWithOptions(context.Background(), testConfig("http://lmstudio.test", "gemma-4"), CheckOptions{Probe: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Probe.OK || !strings.Contains(result.Probe.Error, "exhausted by reasoning") {
		t.Fatalf("unexpected probe diagnosis: %+v", result.Probe)
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
				"loaded_instances":[{"id":"Qwen/Qwen3.6-35B-A3B","config":{"context_length":8192,"parallel":4}}],
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
	if configs := result.Runtime.MatchedModel.LoadedInstanceConfigs; len(configs) != 1 || configs[0].ID != "Qwen/Qwen3.6-35B-A3B" || configs[0].Parallel == nil || *configs[0].Parallel != 4 {
		t.Fatalf("unexpected loaded instance configs: %+v", configs)
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

func TestCheckRuntimeMetadataReportsGemma4Settings(t *testing.T) {
	client := fakeHTTPClient(func(r *http.Request) (int, string) {
		switch r.URL.Path {
		case "/v1/models":
			return http.StatusOK, `{"data":[{"id":"google/gemma-4-26b-a4b"}]}`
		case "/api/v1/models":
			return http.StatusOK, `{"models":[{
				"type":"llm",
				"key":"google/gemma-4-26b-a4b",
				"display_name":"Gemma 4 26B A4B",
				"quantization":{"name":"Q4_K_M","bits_per_weight":4},
				"size_bytes":15600000000,
				"params_string":"26B-A4B",
				"loaded_instances":[{"id":"google/gemma-4-26b-a4b","config":{"context_length":32768}}],
				"max_context_length":262144,
				"format":"gguf",
				"capabilities":{"vision":true,"trained_for_tool_use":true,"reasoning":{"allowed_options":["on","off"],"default":"on"}}
			}]}`
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		return http.StatusNotFound, `not found`
	})

	cfg := testConfig("http://lmstudio.test", "google/gemma-4-26b-a4b")
	cfg.Server.Servers[0].Generation = config.GenerationConfig{
		TopK:              20,
		MinP:              floatTestPtr(0),
		PresencePenalty:   floatTestPtr(1.5),
		RepetitionPenalty: floatTestPtr(1),
	}
	result, err := New(client).CheckWithOptions(context.Background(), cfg, CheckOptions{Runtime: true})
	if err != nil {
		t.Fatalf("CheckWithOptions returned error: %v", err)
	}
	if configs := result.Runtime.MatchedModel.LoadedInstanceConfigs; len(configs) != 1 || configs[0].Parallel != nil {
		t.Fatalf("expected missing parallel to remain unavailable, got %+v", configs)
	}
	for setting, recommended := range map[string]string{
		"server.servers[].generation.temperature":        "1",
		"server.servers[].generation.top_p":              "0.95",
		"server.servers[].generation.top_k":              "64",
		"server.servers[].generation.min_p":              "(unset)",
		"server.servers[].generation.presence_penalty":   "(unset)",
		"server.servers[].generation.repetition_penalty": "(unset)",
	} {
		if !containsRecommendation(result.Recommendations, setting, recommended) {
			t.Fatalf("expected Gemma 4 recommendation %s=%s, got %+v", setting, recommended, result.Recommendations)
		}
	}

	plan := BuildRecommendedConfig(cfg, result)
	generation := plan.Config.Server.Servers[0].Generation
	if generation.TopK != 64 || generation.Temperature == nil || *generation.Temperature != 1 || generation.TopP == nil || *generation.TopP != 0.95 {
		t.Fatalf("unexpected Gemma 4 recommended generation config: %+v", generation)
	}
	if generation.MinP != nil || generation.PresencePenalty != nil || generation.RepetitionPenalty != nil {
		t.Fatalf("expected Qwen-only sampling settings to be removed: %+v", generation)
	}
}

func TestCheckRuntimeMetadataKeepsLoadedInstanceConfigsSeparate(t *testing.T) {
	client := fakeHTTPClient(func(r *http.Request) (int, string) {
		switch r.URL.Path {
		case "/v1/models":
			return http.StatusOK, `{"data":[{"id":"Qwen/Qwen3.6-35B-A3B"}]}`
		case "/api/v1/models":
			return http.StatusOK, `{"models":[{
				"key":"Qwen/Qwen3.6-35B-A3B",
				"loaded_instances":[
					{"id":"instance-small","config":{"context_length":8192,"parallel":1}},
					{"id":"instance-large","config":{"context_length":32768,"parallel":4}}
				],
				"max_context_length":131072
			}]}`
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		return http.StatusNotFound, `not found`
	})

	result, err := New(client).CheckWithOptions(context.Background(), testConfig("http://lmstudio.test", "Qwen/Qwen3.6-35B-A3B"), CheckOptions{Runtime: true})
	if err != nil {
		t.Fatalf("CheckWithOptions returned error: %v", err)
	}
	if result.Runtime.LoadedInstance != "instance-small" || result.Runtime.ContextLength != 8192 {
		t.Fatalf("selected instance metadata was mixed: %+v", result.Runtime)
	}
	configs := result.Runtime.MatchedModel.LoadedInstanceConfigs
	if len(configs) != 2 || configs[0].ContextLength != 8192 || configs[0].Parallel == nil || *configs[0].Parallel != 1 || configs[1].ContextLength != 32768 || configs[1].Parallel == nil || *configs[1].Parallel != 4 {
		t.Fatalf("loaded instance configs were not preserved independently: %+v", configs)
	}
}

func TestCheckReportsGemma4MissingModelSuggestions(t *testing.T) {
	client := fakeHTTPClient(func(r *http.Request) (int, string) {
		return http.StatusOK, `{"data":[{"id":"other-model"}]}`
	})

	result, err := New(client).Check(context.Background(), testConfig("http://lmstudio.test", "google/gemma-4-26b-a4b"), "")
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !containsWarning(result.Suggestions, "Gemma 4") {
		t.Fatalf("expected Gemma 4-specific suggestions, got %+v", result.Suggestions)
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
	parallel := 4
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
			MatchedModel: RuntimeModelSummary{LoadedInstanceConfigs: []RuntimeLoadedInstanceConfig{
				{ID: "instance-with-parallel", ContextLength: 32768, Parallel: &parallel},
				{ID: "instance-without-parallel", ContextLength: 16384},
			}},
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
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"parallel":4`) || strings.Count(string(data), `"parallel":`) != 1 {
		t.Fatalf("audit JSON did not preserve available parallel or omitted missing parallel: %s", data)
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

func floatTestPtr(value float64) *float64 {
	return &value
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
