package llm

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"yagent/internal/config"
	"yagent/internal/domain"
)

type modelAuditStore interface {
	SaveScratch(context.Context, domain.ScratchRecord) error
}

type Router struct {
	servers        map[string]config.ServerTarget
	clients        map[string]*Client
	profiles       map[string]config.RoutingProfileConfig
	roleRouting    bool
	defaultServer  string
	defaultModel   string
	defaultTimeout time.Duration
	auditStore     modelAuditStore
}

func NewRouter(cfg config.Config) (*Router, error) {
	servers := map[string]config.ServerTarget{}
	clients := map[string]*Client{}
	for _, server := range cfg.Server.Servers {
		api, err := normalizeAPI(server.API)
		if err != nil {
			return nil, err
		}
		servers[server.Name] = server
		clients[server.Name] = NewClient(server.URL, server.ResolvedToken(), server.Timeout.Duration, api)
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

func (r *Router) SetAuditStore(store modelAuditStore) {
	r.auditStore = store
}

func (r *Router) Generate(ctx context.Context, request domain.ModelRequest) (domain.ModelResponse, error) {
	profileName := r.resolveProfile(request)
	profile := r.profiles[profileName]
	serverName := profile.Server
	if serverName == "" {
		serverName = r.defaultServer
	}
	serverTarget, ok := r.servers[serverName]
	if !ok {
		return domain.ModelResponse{}, fmt.Errorf("routing server %q が見つかりません", serverName)
	}
	client, ok := r.clients[serverName]
	if !ok {
		return domain.ModelResponse{}, fmt.Errorf("routing server %q が見つかりません", serverName)
	}
	resolved := request
	if resolved.Model == "" {
		if profile.Model != "" {
			resolved.Model = profile.Model
		} else {
			resolved.Model = fallbackString(serverTarget.Model, r.defaultModel)
		}
	}
	resolved.Settings = mergeModelSettings(
		modelSettingsFromConfig(serverTarget.Generation),
		modelSettingsFromConfig(profile.Generation),
		resolved.Settings,
	)

	response, err := r.generateWithAudit(ctx, client, resolved, modelInvocationMeta{
		profileName: profileName,
		serverName:  serverName,
		server:      serverTarget,
	})
	if err == nil || profile.FallbackServer == "" {
		return response, err
	}
	fallback, ok := r.clients[profile.FallbackServer]
	if !ok {
		return domain.ModelResponse{}, err
	}
	fallbackTarget := r.servers[profile.FallbackServer]
	if profile.FallbackModel != "" {
		resolved.Model = profile.FallbackModel
	}
	return r.generateWithAudit(ctx, fallback, resolved, modelInvocationMeta{
		profileName:        profileName,
		serverName:         profile.FallbackServer,
		server:             fallbackTarget,
		fallback:           true,
		fallbackFromServer: serverName,
	})
}

type modelInvocationMeta struct {
	profileName        string
	serverName         string
	server             config.ServerTarget
	fallback           bool
	fallbackFromServer string
}

func (r *Router) generateWithAudit(ctx context.Context, client *Client, request domain.ModelRequest, meta modelInvocationMeta) (domain.ModelResponse, error) {
	start := time.Now()
	response, err := client.Generate(ctx, request)
	duration := time.Since(start)
	response.Invocation = modelInvocationMetadata(request, meta, duration)
	r.saveModelInvocation(ctx, request, response, err, meta, duration, start)
	return response, err
}

func modelInvocationMetadata(request domain.ModelRequest, meta modelInvocationMeta, duration time.Duration) domain.ModelInvocationMetadata {
	return domain.ModelInvocationMetadata{
		ServerName:         meta.serverName,
		Fallback:           meta.fallback,
		FallbackFromServer: meta.fallbackFromServer,
		API:                fallbackString(meta.server.API, apiChatCompletions),
		Model:              request.Model,
		ProfileName:        meta.profileName,
		DurationMS:         duration.Milliseconds(),
	}
}

func (r *Router) saveModelInvocation(ctx context.Context, request domain.ModelRequest, response domain.ModelResponse, callErr error, meta modelInvocationMeta, duration time.Duration, createdAt time.Time) {
	if r.auditStore == nil {
		return
	}
	record := domain.ModelInvocationRecord{
		ID:                 modelInvocationID(request, meta, createdAt),
		RunID:              request.RunID,
		RootRunID:          request.RootRunID,
		AgentID:            request.Agent.ID,
		Phase:              request.Phase,
		Attempt:            request.Attempt,
		ProfileName:        meta.profileName,
		ServerName:         meta.serverName,
		Fallback:           meta.fallback,
		FallbackFromServer: meta.fallbackFromServer,
		URL:                strings.TrimRight(meta.server.URL, "/"),
		API:                fallbackString(meta.server.API, apiChatCompletions),
		Model:              request.Model,
		ResponseFormat:     responseFormatName(request.ResponseFormat),
		Messages:           len(request.Messages),
		Tools:              len(request.Tools),
		Settings:           modelInvocationSettings(request.Settings),
		DurationMS:         duration.Milliseconds(),
		Success:            callErr == nil,
		FinishReason:       response.FinishReason,
		CreatedAt:          createdAt,
	}
	if callErr != nil {
		record.Error = callErr.Error()
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return
	}
	_ = r.auditStore.SaveScratch(ctx, domain.ScratchRecord{
		ID:        record.ID,
		Kind:      domain.ScratchKindModelInvocation,
		SessionID: fallbackString(record.RootRunID, record.RunID),
		Summary:   modelInvocationSummary(record),
		Payload:   payload,
		CreatedAt: record.CreatedAt,
	})
}

func modelInvocationID(request domain.ModelRequest, meta modelInvocationMeta, createdAt time.Time) string {
	sum := sha1.Sum([]byte(strings.Join([]string{
		request.RootRunID,
		request.RunID,
		request.Agent.ID,
		string(request.Phase),
		meta.profileName,
		meta.serverName,
		request.Model,
		createdAt.Format(time.RFC3339Nano),
	}, "\x00")))
	return "model-" + hex.EncodeToString(sum[:])
}

func modelInvocationSummary(record domain.ModelInvocationRecord) string {
	status := "failed"
	if record.Success {
		status = "ok"
	}
	return strings.TrimSpace(strings.Join([]string{
		status,
		record.ServerName,
		record.Model,
		record.AgentID,
		string(record.Phase),
	}, " "))
}

func responseFormatName(format *domain.ResponseFormat) string {
	if format == nil {
		return ""
	}
	if format.Name != "" {
		return format.Name
	}
	return format.Type
}

func modelInvocationSettings(settings domain.ModelSettings) domain.ModelInvocationSettings {
	return domain.ModelInvocationSettings{
		MaxOutputTokens:   settings.MaxOutputTokens,
		Temperature:       settings.Temperature,
		TopP:              settings.TopP,
		TopK:              settings.TopK,
		MinP:              settings.MinP,
		PresencePenalty:   settings.PresencePenalty,
		RepetitionPenalty: settings.RepetitionPenalty,
		ReasoningEffort:   settings.ReasoningEffort,
		TextVerbosity:     settings.TextVerbosity,
		ParallelToolCalls: settings.ParallelToolCalls,
		Store:             settings.Store,
	}
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

func modelSettingsFromConfig(cfg config.GenerationConfig) domain.ModelSettings {
	return domain.ModelSettings{
		MaxOutputTokens:   cfg.MaxOutputTokens,
		Temperature:       cfg.Temperature,
		TopP:              cfg.TopP,
		TopK:              cfg.TopK,
		MinP:              cfg.MinP,
		PresencePenalty:   cfg.PresencePenalty,
		RepetitionPenalty: cfg.RepetitionPenalty,
		ReasoningEffort:   cfg.ReasoningEffort,
		TextVerbosity:     cfg.TextVerbosity,
		ParallelToolCalls: cfg.ParallelToolCalls,
		Store:             cfg.Store,
	}
}

func mergeModelSettings(items ...domain.ModelSettings) domain.ModelSettings {
	var out domain.ModelSettings
	for _, item := range items {
		if item.MaxOutputTokens > 0 {
			out.MaxOutputTokens = item.MaxOutputTokens
		}
		if item.Temperature != nil {
			out.Temperature = item.Temperature
		}
		if item.TopP != nil {
			out.TopP = item.TopP
		}
		if item.TopK > 0 {
			out.TopK = item.TopK
		}
		if item.MinP != nil {
			out.MinP = item.MinP
		}
		if item.PresencePenalty != nil {
			out.PresencePenalty = item.PresencePenalty
		}
		if item.RepetitionPenalty != nil {
			out.RepetitionPenalty = item.RepetitionPenalty
		}
		if item.ReasoningEffort != "" {
			out.ReasoningEffort = item.ReasoningEffort
		}
		if item.TextVerbosity != "" {
			out.TextVerbosity = item.TextVerbosity
		}
		if item.ParallelToolCalls != nil {
			out.ParallelToolCalls = item.ParallelToolCalls
		}
		if item.Store != nil {
			out.Store = item.Store
		}
	}
	return out
}

func fallbackString(value string, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func normalizeAPI(api string) (string, error) {
	switch api {
	case "":
		return apiChatCompletions, nil
	case apiChatCompletions, apiResponses:
		return api, nil
	default:
		return "", fmt.Errorf("unsupported model api %q", api)
	}
}
