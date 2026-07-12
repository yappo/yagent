package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"yagent/internal/config"
	"yagent/internal/domain"
)

func TestRouterSelectsProfileByAgentAndPhase(t *testing.T) {
	type seenRequest struct {
		Model string `json:"model"`
	}
	requests := map[string][]seenRequest{}

	router, err := NewRouter(config.Config{
		Server: config.ServerConfig{
			Default: "fast",
			Servers: []config.ServerTarget{
				{Name: "fast", URL: "http://fast.test", Model: "gpt-fast", Timeout: config.Duration{Duration: time.Minute}},
				{Name: "strong", URL: "http://strong.test", Model: "gpt-strong", Timeout: config.Duration{Duration: time.Minute}},
				{Name: "summary", URL: "http://summary.test", Model: "gpt-summary", Timeout: config.Duration{Duration: time.Minute}},
			},
		},
		Features: config.FeaturesConfig{
			RoleRouting: true,
		},
		Routing: config.RoutingConfig{
			Profiles: map[string]config.RoutingProfileConfig{
				"default": {Server: "fast"},
				"fast":    {Server: "fast", Model: "gpt-fast"},
				"strong":  {Server: "strong", Model: "gpt-strong"},
				"summary": {Server: "summary", Model: "gpt-summary"},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewRouter returned error: %v", err)
	}
	for _, name := range []string{"fast", "strong", "summary"} {
		name := name
		attachFakeRouterClient(router, name, func(r *http.Request) (int, string) {
			var body seenRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			requests[name] = append(requests[name], body)
			return http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`
		})
	}

	cases := []domain.ModelRequest{
		{Agent: domain.AgentSpec{ID: "planner"}, Phase: domain.RunPhasePlan},
		{Agent: domain.AgentSpec{ID: "coder"}, Phase: domain.RunPhaseExecute},
		{Agent: domain.AgentSpec{ID: "manager"}, Phase: domain.RunPhaseFinalize},
	}
	for _, request := range cases {
		if _, err := router.Generate(context.Background(), request); err != nil {
			t.Fatalf("Generate returned error: %v", err)
		}
	}

	if got := len(requests["fast"]); got != 1 || requests["fast"][0].Model != "gpt-fast" {
		t.Fatalf("unexpected fast routing: %+v", requests["fast"])
	}
	if got := len(requests["strong"]); got != 1 || requests["strong"][0].Model != "gpt-strong" {
		t.Fatalf("unexpected strong routing: %+v", requests["strong"])
	}
	if got := len(requests["summary"]); got != 1 || requests["summary"][0].Model != "gpt-summary" {
		t.Fatalf("unexpected summary routing: %+v", requests["summary"])
	}
}

func TestRouterCanDisableRoleBasedRouting(t *testing.T) {
	type seenRequest struct {
		Model string `json:"model"`
	}
	requests := map[string][]seenRequest{}

	router, err := NewRouter(config.Config{
		Server: config.ServerConfig{
			Default: "fast",
			Servers: []config.ServerTarget{
				{Name: "fast", URL: "http://fast.test", Model: "gpt-fast", Timeout: config.Duration{Duration: time.Minute}},
				{Name: "strong", URL: "http://strong.test", Model: "gpt-strong", Timeout: config.Duration{Duration: time.Minute}},
			},
		},
		Features: config.FeaturesConfig{
			RoleRouting: false,
		},
		Routing: config.RoutingConfig{
			Profiles: map[string]config.RoutingProfileConfig{
				"default": {Server: "fast", Model: "gpt-fast"},
				"strong":  {Server: "strong", Model: "gpt-strong"},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewRouter returned error: %v", err)
	}
	for _, name := range []string{"fast", "strong"} {
		name := name
		attachFakeRouterClient(router, name, func(r *http.Request) (int, string) {
			var body seenRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			requests[name] = append(requests[name], body)
			return http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`
		})
	}

	if _, err := router.Generate(context.Background(), domain.ModelRequest{
		Agent: domain.AgentSpec{ID: "coder"},
		Phase: domain.RunPhaseExecute,
	}); err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	if got := len(requests["fast"]); got != 1 || requests["fast"][0].Model != "gpt-fast" {
		t.Fatalf("expected default profile routing, got %+v", requests)
	}
	if len(requests["strong"]) != 0 {
		t.Fatalf("expected strong profile to stay unused, got %+v", requests["strong"])
	}
}

func TestRouterMergesGenerationSettings(t *testing.T) {
	type seenRequest struct {
		Model           string   `json:"model"`
		ReasoningEffort string   `json:"reasoning_effort"`
		MaxTokens       int      `json:"max_tokens"`
		Temperature     *float64 `json:"temperature"`
	}
	var request seenRequest
	handler := func(r *http.Request) (int, string) {
		_ = json.NewDecoder(r.Body).Decode(&request)
		return http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`
	}

	temp := 0.8
	router, err := NewRouter(config.Config{
		Server: config.ServerConfig{
			Default: "local",
			Servers: []config.ServerTarget{
				{
					Name:  "local",
					URL:   "http://local.test",
					Model: "Qwen/Qwen3.6-35B-A3B",
					Generation: config.GenerationConfig{
						MaxOutputTokens: 4096,
						Temperature:     &temp,
					},
					Timeout: config.Duration{Duration: time.Minute},
				},
			},
		},
		Features: config.FeaturesConfig{RoleRouting: true},
		Routing: config.RoutingConfig{
			Profiles: map[string]config.RoutingProfileConfig{
				"default": {},
				"strong": {
					Server: "local",
					Model:  "gpt-5.5",
					Generation: config.GenerationConfig{
						ReasoningEffort: "high",
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewRouter returned error: %v", err)
	}
	attachFakeRouterClient(router, "local", handler)

	if _, err := router.Generate(context.Background(), domain.ModelRequest{
		Agent: domain.AgentSpec{ID: "coder"},
		Phase: domain.RunPhaseExecute,
	}); err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	if request.Model != "gpt-5.5" || request.MaxTokens != 4096 || request.ReasoningEffort != "high" {
		t.Fatalf("unexpected merged request: %+v", request)
	}
	if request.Temperature == nil || *request.Temperature != temp {
		t.Fatalf("unexpected temperature: %+v", request.Temperature)
	}
}

func TestRouterUsesTokenEnvForAuthorization(t *testing.T) {
	t.Setenv("YAGENT_TEST_OPENAI_KEY", "secret-from-env")

	var authorization string
	handler := func(r *http.Request) (int, string) {
		authorization = r.Header.Get("Authorization")
		return http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`
	}

	router, err := NewRouter(config.Config{
		Server: config.ServerConfig{
			Default: "openai",
			Servers: []config.ServerTarget{
				{
					Name:     "openai",
					URL:      "http://openai.test",
					TokenEnv: "YAGENT_TEST_OPENAI_KEY",
					Model:    "gpt-5.5",
					Timeout:  config.Duration{Duration: time.Minute},
				},
			},
		},
		Routing: config.RoutingConfig{Profiles: map[string]config.RoutingProfileConfig{"default": {}}},
	})
	if err != nil {
		t.Fatalf("NewRouter returned error: %v", err)
	}
	attachFakeRouterClient(router, "openai", handler)

	if _, err := router.Generate(context.Background(), domain.ModelRequest{}); err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if authorization != "Bearer secret-from-env" {
		t.Fatalf("expected Authorization header from token_env, got %q", authorization)
	}
}

func TestRouterSavesModelInvocationAudit(t *testing.T) {
	router, err := NewRouter(config.Config{
		Server: config.ServerConfig{
			Default: "local",
			Servers: []config.ServerTarget{{
				Name:    "local",
				URL:     "http://local.test",
				Model:   "Qwen/Qwen3.6-35B-A3B",
				API:     "chat_completions",
				Timeout: config.Duration{Duration: time.Minute},
			}},
		},
		Features: config.FeaturesConfig{RoleRouting: true},
		Routing:  config.RoutingConfig{Profiles: map[string]config.RoutingProfileConfig{"strong": {Server: "local"}}},
	})
	if err != nil {
		t.Fatalf("NewRouter returned error: %v", err)
	}
	store := &fakeModelAuditStore{}
	router.SetAuditStore(store)
	attachFakeRouterClient(router, "local", func(r *http.Request) (int, string) {
		return http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`
	})

	response, err := router.Generate(context.Background(), domain.ModelRequest{
		RunID:     "run-1",
		RootRunID: "root-1",
		Attempt:   2,
		Agent:     domain.AgentSpec{ID: "coder", RoutingProfile: "strong"},
		Phase:     domain.RunPhaseExecute,
		Messages:  []domain.Message{{Role: domain.RoleUser, Content: "hi"}},
		Tools:     []domain.ToolDefinition{{Name: "fs_read"}},
		ResponseFormat: &domain.ResponseFormat{
			Name: "execution_plan",
		},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if response.Invocation.ServerName != "local" || response.Invocation.Model != "Qwen/Qwen3.6-35B-A3B" || response.Invocation.ProfileName != "strong" || response.Invocation.API != "chat_completions" {
		t.Fatalf("unexpected response invocation metadata: %+v", response.Invocation)
	}
	if len(store.records) != 1 {
		t.Fatalf("expected one audit scratch record, got %+v", store.records)
	}
	if store.records[0].Kind != domain.ScratchKindModelInvocation || store.records[0].SessionID != "root-1" {
		t.Fatalf("unexpected scratch record: %+v", store.records[0])
	}
	var record domain.ModelInvocationRecord
	if err := json.Unmarshal(store.records[0].Payload, &record); err != nil {
		t.Fatalf("audit payload did not decode: %v", err)
	}
	if record.ServerName != "local" || record.Model != "Qwen/Qwen3.6-35B-A3B" || record.ProfileName != "strong" || !record.Success {
		t.Fatalf("unexpected model invocation record: %+v", record)
	}
	if record.AgentID != "coder" || record.Phase != domain.RunPhaseExecute || record.Attempt != 2 || record.Messages != 1 || record.Tools != 1 || record.ResponseFormat != "execution_plan" {
		t.Fatalf("unexpected model invocation metadata: %+v", record)
	}
}

func TestRouterAuditsFallbackModelInvocation(t *testing.T) {
	router, err := NewRouter(config.Config{
		Server: config.ServerConfig{
			Default: "local",
			Servers: []config.ServerTarget{
				{Name: "local", URL: "http://local.test", Model: "local-model", Timeout: config.Duration{Duration: time.Minute}},
				{Name: "openai", URL: "http://openai.test", Model: "gpt-5.5", API: "responses", Timeout: config.Duration{Duration: time.Minute}},
			},
		},
		Routing: config.RoutingConfig{Profiles: map[string]config.RoutingProfileConfig{
			"default": {Server: "local", FallbackServer: "openai", FallbackModel: "gpt-5.5"},
		}},
	})
	if err != nil {
		t.Fatalf("NewRouter returned error: %v", err)
	}
	store := &fakeModelAuditStore{}
	router.SetAuditStore(store)
	attachFakeRouterClient(router, "local", func(r *http.Request) (int, string) {
		return http.StatusInternalServerError, `bad`
	})
	attachFakeRouterClient(router, "openai", func(r *http.Request) (int, string) {
		return http.StatusOK, `{"id":"resp_1","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":13,"output_tokens":9,"total_tokens":22,"input_tokens_details":{"cached_tokens":4},"output_tokens_details":{"reasoning_tokens":6}}}`
	})

	response, err := router.Generate(context.Background(), domain.ModelRequest{})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if !response.Invocation.Fallback || response.Invocation.FallbackFromServer != "local" || response.Invocation.ServerName != "openai" || response.Invocation.API != "responses" || response.Invocation.Model != "gpt-5.5" {
		t.Fatalf("unexpected fallback response invocation metadata: %+v", response.Invocation)
	}
	if response.Invocation.Usage != (domain.ModelUsage{Available: true, InputTokens: 13, OutputTokens: 9, TotalTokens: 22, CachedInputTokens: 4, ReasoningTokens: 6}) {
		t.Fatalf("unexpected fallback usage: %+v", response.Invocation.Usage)
	}
	if len(response.Invocation.Attempts) != 2 {
		t.Fatalf("expected primary and fallback attempts, got %+v", response.Invocation.Attempts)
	}
	primaryAttempt, fallbackAttempt := response.Invocation.Attempts[0], response.Invocation.Attempts[1]
	if primaryAttempt.Success || primaryAttempt.Error == "" || primaryAttempt.ServerName != "local" || primaryAttempt.Fallback || primaryAttempt.Usage.Available {
		t.Fatalf("unexpected primary transport attempt: %+v", primaryAttempt)
	}
	if !fallbackAttempt.Success || fallbackAttempt.Error != "" || fallbackAttempt.ServerName != "openai" || !fallbackAttempt.Fallback || fallbackAttempt.FallbackFromServer != "local" || fallbackAttempt.Usage != response.Invocation.Usage {
		t.Fatalf("unexpected fallback transport attempt: %+v", fallbackAttempt)
	}
	if len(store.records) != 2 {
		t.Fatalf("expected primary and fallback audit records, got %+v", store.records)
	}
	var primary, fallback domain.ModelInvocationRecord
	if err := json.Unmarshal(store.records[0].Payload, &primary); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(store.records[1].Payload, &fallback); err != nil {
		t.Fatal(err)
	}
	if primary.Success || primary.ServerName != "local" || primary.Error == "" || primary.Usage.Available {
		t.Fatalf("unexpected primary audit: %+v", primary)
	}
	if !fallback.Success || !fallback.Fallback || fallback.FallbackFromServer != "local" || fallback.ServerName != "openai" || fallback.API != "responses" || fallback.Usage != response.Invocation.Usage {
		t.Fatalf("unexpected fallback audit: %+v", fallback)
	}
}

func TestRouterReturnsAllFailedTransportAttempts(t *testing.T) {
	router, err := NewRouter(config.Config{
		Server: config.ServerConfig{
			Default: "local",
			Servers: []config.ServerTarget{
				{Name: "local", URL: "http://local.test", Model: "local-model", Timeout: config.Duration{Duration: time.Minute}},
				{Name: "fallback", URL: "http://fallback.test", Model: "fallback-model", Timeout: config.Duration{Duration: time.Minute}},
			},
		},
		Routing: config.RoutingConfig{Profiles: map[string]config.RoutingProfileConfig{
			"default": {Server: "local", FallbackServer: "fallback", FallbackModel: "fallback-model"},
		}},
	})
	if err != nil {
		t.Fatalf("NewRouter returned error: %v", err)
	}
	attachFakeRouterClient(router, "local", func(*http.Request) (int, string) {
		return http.StatusInternalServerError, `primary failed`
	})
	attachFakeRouterClient(router, "fallback", func(*http.Request) (int, string) {
		return http.StatusBadGateway, `fallback failed`
	})

	response, err := router.Generate(context.Background(), domain.ModelRequest{})
	if err == nil {
		t.Fatal("expected fallback failure")
	}
	if !response.Invocation.Fallback || response.Invocation.ServerName != "fallback" || response.Invocation.Model != "fallback-model" {
		t.Fatalf("expected final fallback metadata, got %+v", response.Invocation)
	}
	if len(response.Invocation.Attempts) != 2 {
		t.Fatalf("expected two failed attempts, got %+v", response.Invocation.Attempts)
	}
	primary, fallback := response.Invocation.Attempts[0], response.Invocation.Attempts[1]
	if primary.Success || primary.Error == "" || primary.ServerName != "local" || primary.Fallback || primary.DurationMS < 0 || primary.Usage.Available {
		t.Fatalf("unexpected failed primary attempt: %+v", primary)
	}
	if fallback.Success || fallback.Error == "" || fallback.ServerName != "fallback" || !fallback.Fallback || fallback.FallbackFromServer != "local" || fallback.DurationMS < 0 || fallback.Usage.Available {
		t.Fatalf("unexpected failed fallback attempt: %+v", fallback)
	}
}

func attachFakeRouterClient(router *Router, name string, handler func(*http.Request) (int, string)) {
	router.clients[name].httpClient = fakeHTTPClient(handler)
}

type fakeModelAuditStore struct {
	records []domain.ScratchRecord
}

func (s *fakeModelAuditStore) SaveScratch(_ context.Context, record domain.ScratchRecord) error {
	s.records = append(s.records, record)
	return nil
}
