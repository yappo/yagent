package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	newServer := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body seenRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			requests[name] = append(requests[name], body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
		}))
	}

	fast := newServer("fast")
	defer fast.Close()
	strong := newServer("strong")
	defer strong.Close()
	summary := newServer("summary")
	defer summary.Close()

	router, err := NewRouter(config.Config{
		Server: config.ServerConfig{
			Default: "fast",
			Servers: []config.ServerTarget{
				{Name: "fast", URL: fast.URL, Model: "gpt-fast", Timeout: config.Duration{Duration: time.Minute}},
				{Name: "strong", URL: strong.URL, Model: "gpt-strong", Timeout: config.Duration{Duration: time.Minute}},
				{Name: "summary", URL: summary.URL, Model: "gpt-summary", Timeout: config.Duration{Duration: time.Minute}},
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
	newServer := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body seenRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			requests[name] = append(requests[name], body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
		}))
	}

	fast := newServer("fast")
	defer fast.Close()
	strong := newServer("strong")
	defer strong.Close()

	router, err := NewRouter(config.Config{
		Server: config.ServerConfig{
			Default: "fast",
			Servers: []config.ServerTarget{
				{Name: "fast", URL: fast.URL, Model: "gpt-fast", Timeout: config.Duration{Duration: time.Minute}},
				{Name: "strong", URL: strong.URL, Model: "gpt-strong", Timeout: config.Duration{Duration: time.Minute}},
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
