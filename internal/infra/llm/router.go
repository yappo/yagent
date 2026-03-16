package llm

import (
	"context"
	"fmt"
	"time"

	"yagent/internal/config"
	"yagent/internal/domain"
)

type Router struct {
	servers        map[string]config.ServerTarget
	clients        map[string]*Client
	profiles       map[string]config.RoutingProfileConfig
	roleRouting    bool
	defaultServer  string
	defaultModel   string
	defaultTimeout time.Duration
}

func NewRouter(cfg config.Config) (*Router, error) {
	servers := map[string]config.ServerTarget{}
	clients := map[string]*Client{}
	for _, server := range cfg.Server.Servers {
		servers[server.Name] = server
		clients[server.Name] = NewClient(server.URL, server.Token, server.Timeout.Duration)
	}
	defaultServer := cfg.Server.Default
	if defaultServer == "" && len(cfg.Server.Servers) > 0 {
		defaultServer = cfg.Server.Servers[0].Name
	}
	if defaultServer == "" {
		return nil, fmt.Errorf("server.default が必要です")
	}
	defaultTarget, ok := servers[defaultServer]
	if !ok {
		return nil, fmt.Errorf("default server %q が見つかりません", defaultServer)
	}
	return &Router{
		servers:        servers,
		clients:        clients,
		profiles:       cfg.Routing.Profiles,
		roleRouting:    cfg.Features.RoleRouting,
		defaultServer:  defaultServer,
		defaultModel:   defaultTarget.Model,
		defaultTimeout: defaultTarget.Timeout.Duration,
	}, nil
}

func (r *Router) Generate(ctx context.Context, request domain.ModelRequest) (domain.ModelResponse, error) {
	profileName := r.resolveProfile(request)
	profile := r.profiles[profileName]
	serverName := profile.Server
	if serverName == "" {
		serverName = r.defaultServer
	}
	client, ok := r.clients[serverName]
	if !ok {
		return domain.ModelResponse{}, fmt.Errorf("routing server %q が見つかりません", serverName)
	}
	resolved := request
	if resolved.Model == "" {
		if profile.Model != "" {
			resolved.Model = profile.Model
		} else if target, ok := r.servers[serverName]; ok {
			resolved.Model = target.Model
		} else {
			resolved.Model = r.defaultModel
		}
	}

	response, err := client.Generate(ctx, resolved)
	if err == nil || profile.FallbackServer == "" {
		return response, err
	}
	fallback, ok := r.clients[profile.FallbackServer]
	if !ok {
		return domain.ModelResponse{}, err
	}
	if profile.FallbackModel != "" {
		resolved.Model = profile.FallbackModel
	}
	return fallback.Generate(ctx, resolved)
}

func (r *Router) resolveProfile(request domain.ModelRequest) string {
	if request.Agent.RoutingProfile != "" {
		return request.Agent.RoutingProfile
	}
	if !r.roleRouting {
		if _, ok := r.profiles["default"]; ok {
			return "default"
		}
		return ""
	}
	switch {
	case request.Phase == domain.RunPhaseFinalize:
		if _, ok := r.profiles["summary"]; ok {
			return "summary"
		}
	case request.Agent.ID == "planner" || request.Agent.ID == "researcher" || request.Agent.ID == "tester":
		if _, ok := r.profiles["fast"]; ok {
			return "fast"
		}
	case request.Agent.ID == "coder" || request.Agent.ID == "reviewer":
		if _, ok := r.profiles["strong"]; ok {
			return "strong"
		}
	}
	if _, ok := r.profiles["default"]; ok {
		return "default"
	}
	return ""
}
