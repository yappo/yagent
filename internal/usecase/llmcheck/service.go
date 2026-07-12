package llmcheck

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"yagent/internal/config"
	"yagent/internal/usecase/localmodel"
)

type Service struct {
	client            *http.Client
	usesDefaultClient bool
}

type Result struct {
	ServerName      string           `json:"server_name"`
	URL             string           `json:"url"`
	API             string           `json:"api"`
	Model           string           `json:"model"`
	Endpoint        string           `json:"endpoint"`
	Models          []string         `json:"models,omitempty"`
	ModelFound      bool             `json:"model_found"`
	ModelExactMatch bool             `json:"model_exact_match"`
	MatchedModel    string           `json:"matched_model,omitempty"`
	Warnings        []string         `json:"warnings,omitempty"`
	Problems        []string         `json:"problems,omitempty"`
	Suggestions     []string         `json:"suggestions,omitempty"`
	Recommendations []Recommendation `json:"recommendations,omitempty"`
	Probe           ProbeResult      `json:"probe,omitempty"`
	Runtime         RuntimeResult    `json:"runtime,omitempty"`
}

type Recommendation struct {
	Area        string `json:"area"`
	Setting     string `json:"setting"`
	Current     string `json:"current,omitempty"`
	Recommended string `json:"recommended"`
	Reason      string `json:"reason"`
}

type CheckOptions struct {
	ServerName      string
	Model           string
	Probe           bool
	ProbeStructured bool
	Runtime         bool
}

type ProbeResult struct {
	Requested  bool   `json:"requested"`
	Structured bool   `json:"structured"`
	Endpoint   string `json:"endpoint,omitempty"`
	Model      string `json:"model,omitempty"`
	OK         bool   `json:"ok"`
	Output     string `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
}

type RuntimeResult struct {
	Requested        bool                  `json:"requested"`
	Endpoint         string                `json:"endpoint,omitempty"`
	Models           []RuntimeModelSummary `json:"models,omitempty"`
	ModelFound       bool                  `json:"model_found"`
	MatchedModel     RuntimeModelSummary   `json:"matched_model,omitempty"`
	Loaded           bool                  `json:"loaded"`
	LoadedInstance   string                `json:"loaded_instance,omitempty"`
	ContextLength    int                   `json:"context_length,omitempty"`
	MaxContextLength int                   `json:"max_context_length,omitempty"`
	Error            string                `json:"error,omitempty"`
}

type RuntimeModelSummary struct {
	ID                    string                        `json:"id,omitempty"`
	DisplayName           string                        `json:"display_name,omitempty"`
	Loaded                bool                          `json:"loaded"`
	LoadedInstances       []string                      `json:"loaded_instances,omitempty"`
	LoadedInstanceConfigs []RuntimeLoadedInstanceConfig `json:"loaded_instance_configs,omitempty"`
	ContextLength         int                           `json:"context_length,omitempty"`
	MaxContextLength      int                           `json:"max_context_length,omitempty"`
	Quantization          string                        `json:"quantization,omitempty"`
	Format                string                        `json:"format,omitempty"`
	Params                string                        `json:"params,omitempty"`
	SizeBytes             int64                         `json:"size_bytes,omitempty"`
	TrainedForToolUse     *bool                         `json:"trained_for_tool_use,omitempty"`
	Vision                *bool                         `json:"vision,omitempty"`
	ReasoningAllowed      []string                      `json:"reasoning_allowed,omitempty"`
	ReasoningDefault      string                        `json:"reasoning_default,omitempty"`
	Variants              []string                      `json:"variants,omitempty"`
	SelectedVariant       string                        `json:"selected_variant,omitempty"`
}

type RuntimeLoadedInstanceConfig struct {
	ID            string `json:"id"`
	ContextLength int    `json:"context_length,omitempty"`
	Parallel      *int   `json:"parallel,omitempty"`
}

func New(client *http.Client) *Service {
	if client == nil {
		return &Service{
			client:            &http.Client{Timeout: 5 * time.Second},
			usesDefaultClient: true,
		}
	}
	return &Service{client: client}
}

func (s *Service) Check(ctx context.Context, cfg config.Config, serverName string) (Result, error) {
	return s.CheckWithOptions(ctx, cfg, CheckOptions{ServerName: serverName})
}

func (s *Service) CheckWithOptions(ctx context.Context, cfg config.Config, options CheckOptions) (Result, error) {
	if options.ProbeStructured {
		options.Probe = true
	}
	server, err := resolveServer(cfg, options.ServerName)
	if err != nil {
		return Result{}, err
	}
	checker := s.forServer(server)
	result := Result{
		ServerName: server.Name,
		URL:        strings.TrimRight(server.URL, "/"),
		API:        fallback(server.API, "chat_completions"),
		Model:      firstNonEmpty(options.Model, server.Model),
	}
	result.Endpoint = result.URL + "/v1/models"

	models, err := checker.listModels(ctx, server)
	if err != nil {
		result.Problems = append(result.Problems, err.Error())
		result.Suggestions = append(result.Suggestions, lmStudioServerSuggestions(result.URL)...)
		return result, nil
	}

	result.Models = models
	if result.Model == "" {
		result.Problems = append(result.Problems, "server model is not configured")
		result.Suggestions = append(result.Suggestions, "config の server.servers[].model に LM Studio の model identifier を設定してください")
		return result, nil
	}
	if matched, ok := matchModel(result.Model, models); ok {
		result.ModelFound = true
		result.MatchedModel = matched
		result.ModelExactMatch = matched == result.Model
		if options.Runtime {
			result.Runtime = checker.checkRuntime(ctx, server, matched)
			appendRuntimeDiagnostics(&result)
			appendRuntimeRecommendations(&result, cfg, server)
		}
		if !result.ModelExactMatch {
			result.Warnings = append(result.Warnings, fmt.Sprintf("configured model %q fuzzy-matched LM Studio model %q, but app requests should use the exact LM Studio model id", result.Model, matched))
			result.Suggestions = append(result.Suggestions, fmt.Sprintf("config の server.servers[].model を %q に合わせると実行時の model mismatch を避けられます", matched))
		}
		if options.Probe {
			result.Probe = checker.runProbe(ctx, server, matched, options.ProbeStructured, probeOutputTokens(server.Generation))
			if !result.Probe.OK {
				result.Problems = append(result.Problems, result.Probe.Error)
				result.Suggestions = append(result.Suggestions, probeSuggestions(result.API, result.URL)...)
			}
		}
		return result, nil
	}

	result.Problems = append(result.Problems, fmt.Sprintf("configured model %q was not found in LM Studio /v1/models", result.Model))
	result.Suggestions = append(result.Suggestions, modelNotFoundSuggestions(result.Model)...)
	return result, nil
}

func (s *Service) forServer(server config.ServerTarget) *Service {
	if !s.usesDefaultClient || server.Timeout.Duration <= 0 {
		return s
	}
	client := *s.client
	client.Timeout = server.Timeout.Duration
	return &Service{client: &client}
}

func (s *Service) checkRuntime(ctx context.Context, server config.ServerTarget, model string) RuntimeResult {
	endpoint := strings.TrimRight(server.URL, "/") + "/api/v1/models"
	result := RuntimeResult{Requested: true, Endpoint: endpoint}
	models, err := s.listRuntimeModels(ctx, server)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Models = models
	matched, ok := matchRuntimeModel(model, models)
	if !ok {
		result.Error = fmt.Sprintf("configured model %q was not found in LM Studio /api/v1/models", model)
		return result
	}
	result.ModelFound = true
	result.MatchedModel = matched
	result.Loaded = matched.Loaded
	result.MaxContextLength = matched.MaxContextLength
	if len(matched.LoadedInstanceConfigs) > 0 {
		selected := matched.LoadedInstanceConfigs[0]
		result.LoadedInstance = selected.ID
		result.ContextLength = selected.ContextLength
	} else {
		result.ContextLength = matched.ContextLength
		if len(matched.LoadedInstances) > 0 {
			result.LoadedInstance = matched.LoadedInstances[0]
		}
	}
	return result
}

func appendRuntimeDiagnostics(result *Result) {
	runtime := result.Runtime
	if !runtime.Requested {
		return
	}
	if runtime.Error != "" {
		result.Warnings = append(result.Warnings, "LM Studio runtime metadata を取得できませんでした: "+runtime.Error)
		result.Suggestions = append(result.Suggestions, "runtime 詳細が必要な場合は LM Studio の REST API が有効か確認してください")
		return
	}
	if !runtime.ModelFound {
		return
	}
	model := runtime.MatchedModel
	if !runtime.Loaded {
		result.Warnings = append(result.Warnings, fmt.Sprintf("LM Studio runtime model %q is not loaded", model.ID))
		result.Suggestions = append(result.Suggestions, "LM Studio で対象 model を load してから yagent doctor --runtime --probe-structured を再実行してください")
	}
	if runtime.ContextLength > 0 && runtime.ContextLength < 25000 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("loaded context_length is %d tokens; coding-agent workflows often need around 25k+ tokens", runtime.ContextLength))
		if runtime.MaxContextLength > runtime.ContextLength {
			result.Suggestions = append(result.Suggestions, "メモリに余裕がある場合は LM Studio の loaded context length を 25k 以上へ上げてください")
		}
	}
	if model.TrainedForToolUse != nil && !*model.TrainedForToolUse {
		result.Warnings = append(result.Warnings, fmt.Sprintf("LM Studio capabilities do not mark %q as trained_for_tool_use", model.ID))
		result.Suggestions = append(result.Suggestions, "tool-heavy coding task では tool-use 対応 model または structured probe の成功を優先して確認してください")
	}
	if result.API == "responses" && !containsString(model.ReasoningAllowed, "high") && !containsString(model.ReasoningAllowed, "on") && len(model.ReasoningAllowed) > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("runtime reasoning options are %s; configured Responses reasoning may need adjustment", strings.Join(model.ReasoningAllowed, ",")))
	}
}

func appendRuntimeRecommendations(result *Result, cfg config.Config, server config.ServerTarget) {
	runtime := result.Runtime
	if !runtime.Requested || runtime.Error != "" || !runtime.ModelFound {
		return
	}
	if target := recommendedLoadedContext(runtime); target > 0 && runtime.ContextLength < target {
		current := "unloaded"
		if runtime.ContextLength > 0 {
			current = fmt.Sprintf("%d", runtime.ContextLength)
		}
		result.Recommendations = append(result.Recommendations, Recommendation{
			Area:        "lmstudio",
			Setting:     "loaded context_length",
			Current:     current,
			Recommended: fmt.Sprintf("%d", target),
			Reason:      "coding-agent workflows consume context quickly; raise the loaded context when the local machine can handle it",
		})
	}
	if target := recommendedCompactAfterTokens(runtime.ContextLength); target > 0 && cfg.Context.CompactAfterEstTokens > target {
		result.Recommendations = append(result.Recommendations, Recommendation{
			Area:        "context",
			Setting:     "context.compact_after_est_tokens",
			Current:     fmt.Sprintf("%d", cfg.Context.CompactAfterEstTokens),
			Recommended: fmt.Sprintf("%d", target),
			Reason:      "keep yagent compaction below roughly half of the loaded local context",
		})
	}
	if target := recommendedMaxOutputTokens(runtime.ContextLength); target > 0 {
		current := server.Generation.MaxOutputTokens
		if current == 0 || current > target || current < 2048 {
			result.Recommendations = append(result.Recommendations, Recommendation{
				Area:        "generation",
				Setting:     "server.servers[].generation.max_output_tokens",
				Current:     formatIntSetting(current),
				Recommended: fmt.Sprintf("%d", target),
				Reason:      "reserve enough output room for planning and final reports without exhausting a small local context",
			})
		}
	}
	modelID := firstNonEmpty(result.MatchedModel, result.Model, runtime.MatchedModel.ID)
	if localmodel.IsQwen36(modelID) {
		appendQwenGenerationRecommendations(result, server.Generation)
	}
	if localmodel.IsGemma4(modelID) {
		appendGemma4GenerationRecommendations(result, server.Generation)
	}
}

func appendQwenGenerationRecommendations(result *Result, generation config.GenerationConfig) {
	addFloatRecommendation := func(setting string, current *float64, target float64, reason string) {
		if current != nil && floatEqual(*current, target) {
			return
		}
		result.Recommendations = append(result.Recommendations, Recommendation{
			Area:        "generation",
			Setting:     setting,
			Current:     formatFloatSetting(current),
			Recommended: formatFloat(target),
			Reason:      reason,
		})
	}
	addFloatRecommendation("server.servers[].generation.temperature", generation.Temperature, 1.0, "Qwen3.6 thinking mode general tasks use temperature=1.0")
	addFloatRecommendation("server.servers[].generation.top_p", generation.TopP, 0.95, "Qwen3.6 thinking mode general tasks use top_p=0.95")
	addFloatRecommendation("server.servers[].generation.min_p", generation.MinP, 0.0, "Qwen3.6 thinking mode general tasks use min_p=0.0")
	addFloatRecommendation("server.servers[].generation.presence_penalty", generation.PresencePenalty, 1.5, "Qwen3.6 thinking mode general tasks use presence_penalty=1.5")
	addFloatRecommendation("server.servers[].generation.repetition_penalty", generation.RepetitionPenalty, 1.0, "Qwen3.6 thinking mode general tasks use repetition_penalty=1.0")
	if generation.TopK != 20 {
		result.Recommendations = append(result.Recommendations, Recommendation{
			Area:        "generation",
			Setting:     "server.servers[].generation.top_k",
			Current:     formatIntSetting(generation.TopK),
			Recommended: "20",
			Reason:      "Qwen3.6 thinking mode general tasks use top_k=20",
		})
	}
}

func appendGemma4GenerationRecommendations(result *Result, generation config.GenerationConfig) {
	addFloatRecommendation := func(setting string, current *float64, target float64, reason string) {
		if current != nil && floatEqual(*current, target) {
			return
		}
		result.Recommendations = append(result.Recommendations, Recommendation{
			Area:        "generation",
			Setting:     setting,
			Current:     formatFloatSetting(current),
			Recommended: formatFloat(target),
			Reason:      reason,
		})
	}
	addFloatRecommendation("server.servers[].generation.temperature", generation.Temperature, 1.0, "Gemma 4 official best practices use temperature=1.0")
	addFloatRecommendation("server.servers[].generation.top_p", generation.TopP, 0.95, "Gemma 4 official best practices use top_p=0.95")
	appendUnsetFloatRecommendation(result, "server.servers[].generation.min_p", generation.MinP, "Gemma 4 official sampling guidance does not include min_p")
	appendUnsetFloatRecommendation(result, "server.servers[].generation.presence_penalty", generation.PresencePenalty, "Gemma 4 official sampling guidance does not include presence_penalty")
	appendUnsetFloatRecommendation(result, "server.servers[].generation.repetition_penalty", generation.RepetitionPenalty, "Gemma 4 official sampling guidance does not include repetition_penalty")
	if generation.TopK != 64 {
		result.Recommendations = append(result.Recommendations, Recommendation{
			Area:        "generation",
			Setting:     "server.servers[].generation.top_k",
			Current:     formatIntSetting(generation.TopK),
			Recommended: "64",
			Reason:      "Gemma 4 official best practices use top_k=64",
		})
	}
}

func appendUnsetFloatRecommendation(result *Result, setting string, current *float64, reason string) {
	if current == nil {
		return
	}
	result.Recommendations = append(result.Recommendations, Recommendation{
		Area:        "generation",
		Setting:     setting,
		Current:     formatFloatSetting(current),
		Recommended: "(unset)",
		Reason:      reason,
	})
}

func modelNotFoundSuggestions(model string) []string {
	suggestions := []string{
		"LM Studio の model identifier が config の model と違う場合は、config 側を /v1/models に出ている名前へ合わせてください",
	}
	switch localmodel.Detect(model) {
	case localmodel.PresetGemma4:
		return append([]string{
			"LM Studio で Gemma 4 の instruction-tuned GGUF または MLX model を download/load してください",
			"MacBook Air M4 32GB では Gemma 4 26B A4B または 12B を 16k-32k context から試すのが現実的です",
		}, suggestions...)
	case localmodel.PresetQwen36:
		return append([]string{
			"LM Studio で Qwen/Qwen3.6-35B-A3B の量子化モデルを download/load してください",
			"MacBook Air M4 32GB では最初は text-only / 16k-32k context / Q4 系 quantization から始めるのが現実的です",
		}, suggestions...)
	default:
		return append([]string{
			"LM Studio で config に指定した local model を download/load してください",
		}, suggestions...)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (s *Service) runProbe(ctx context.Context, server config.ServerTarget, model string, structured bool, maxOutputTokens int) ProbeResult {
	api := fallback(server.API, "chat_completions")
	switch api {
	case "responses":
		return s.runResponsesProbe(ctx, server, model, structured, maxOutputTokens)
	default:
		return s.runChatProbe(ctx, server, model, structured, maxOutputTokens)
	}
}

const defaultProbeMaxOutputTokens = 512

func probeOutputTokens(generation config.GenerationConfig) int {
	if generation.MaxOutputTokens > 0 {
		return generation.MaxOutputTokens
	}
	return defaultProbeMaxOutputTokens
}

func (s *Service) runChatProbe(ctx context.Context, server config.ServerTarget, model string, structured bool, maxOutputTokens int) ProbeResult {
	endpoint := strings.TrimRight(server.URL, "/") + "/v1/chat/completions"
	result := ProbeResult{Requested: true, Structured: structured, Endpoint: endpoint, Model: model}
	payload := map[string]any{
		"model":       model,
		"max_tokens":  maxOutputTokens,
		"temperature": 0,
		"messages": []map[string]string{
			{"role": "user", "content": probePrompt(structured)},
		},
	}
	if structured {
		payload["response_format"] = map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "yagent_doctor_probe",
				"schema": probeSchema(),
				"strict": true,
			},
		}
	}
	body, err := s.postJSON(ctx, server, endpoint, payload)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	var decoded struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		result.Error = fmt.Sprintf("failed to decode chat completion probe response: %v", err)
		return result
	}
	if len(decoded.Choices) == 0 {
		result.Error = "chat completion probe returned no choices"
		return result
	}
	if strings.TrimSpace(decoded.Choices[0].Message.Content) == "" && decoded.Choices[0].FinishReason == "length" && strings.TrimSpace(decoded.Choices[0].Message.ReasoningContent) != "" {
		result.Error = "probe output budget was exhausted by reasoning before final content"
		return result
	}
	return validateProbeOutput(result, decoded.Choices[0].Message.Content)
}

func (s *Service) runResponsesProbe(ctx context.Context, server config.ServerTarget, model string, structured bool, maxOutputTokens int) ProbeResult {
	endpoint := strings.TrimRight(server.URL, "/") + "/v1/responses"
	result := ProbeResult{Requested: true, Structured: structured, Endpoint: endpoint, Model: model}
	payload := map[string]any{
		"model":             model,
		"max_output_tokens": maxOutputTokens,
		"input": []map[string]string{
			{"role": "user", "content": probePrompt(structured)},
		},
	}
	if structured {
		payload["text"] = map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   "yagent_doctor_probe",
				"schema": probeSchema(),
				"strict": true,
			},
		}
	}
	body, err := s.postJSON(ctx, server, endpoint, payload)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	output, err := parseResponsesProbeOutput(body)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	return validateProbeOutput(result, output)
}

func (s *Service) postJSON(ctx context.Context, server config.ServerTarget, endpoint string, payload any) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to encode probe request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create probe request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token := server.ResolvedToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to run probe against %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned HTTP %d during probe: %s", endpoint, resp.StatusCode, string(body))
	}
	if readErr != nil {
		return nil, fmt.Errorf("failed to read probe response: %w", readErr)
	}
	return body, nil
}

func parseResponsesProbeOutput(body []byte) (string, error) {
	var decoded struct {
		Output []json.RawMessage `json:"output"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", fmt.Errorf("failed to decode responses probe response: %w", err)
	}
	if decoded.Error != nil {
		return "", fmt.Errorf("responses probe returned error: %s", decoded.Error.Message)
	}
	for _, raw := range decoded.Output {
		var item struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		if item.Type != "message" {
			continue
		}
		parts := []string{}
		for _, content := range item.Content {
			if content.Text != "" {
				parts = append(parts, content.Text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n"), nil
		}
	}
	return "", fmt.Errorf("responses probe returned no output text")
}

func validateProbeOutput(result ProbeResult, output string) ProbeResult {
	result.Output = strings.TrimSpace(output)
	if result.Output == "" {
		result.Error = "probe returned empty output"
		return result
	}
	if !result.Structured {
		result.OK = true
		return result
	}
	var decoded struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}
	decoder := json.NewDecoder(strings.NewReader(result.Output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		result.Error = fmt.Sprintf("structured probe output was not valid JSON schema output: %v", err)
		return result
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		result.Error = "structured probe output contained trailing JSON data"
		return result
	}
	if !decoded.OK {
		result.Error = "structured probe JSON returned ok=false"
		return result
	}
	result.OK = true
	return result
}

func probePrompt(structured bool) string {
	if structured {
		return "This is a local model health check with no external action. If you understand this request and can follow the supplied JSON schema, set ok to true and message to yagent-ok."
	}
	return "This is a local model health check with no external action. Reply with a short confirmation that says yagent-ok."
}

func probeSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok": map[string]any{
				"type": "boolean",
			},
			"message": map[string]any{
				"type": "string",
			},
		},
		"required":             []string{"ok", "message"},
		"additionalProperties": false,
	}
}

func (s *Service) listModels(ctx context.Context, server config.ServerTarget) ([]string, error) {
	endpoint := strings.TrimRight(server.URL, "/") + "/v1/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create /v1/models request: %w", err)
	}
	if token := server.ResolvedToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned HTTP %d", endpoint, resp.StatusCode)
	}

	var decoded struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("failed to decode /v1/models response: %w", err)
	}
	models := make([]string, 0, len(decoded.Data))
	for _, item := range decoded.Data {
		if strings.TrimSpace(item.ID) == "" {
			continue
		}
		models = append(models, item.ID)
	}
	return models, nil
}

func (s *Service) listRuntimeModels(ctx context.Context, server config.ServerTarget) ([]RuntimeModelSummary, error) {
	endpoint := strings.TrimRight(server.URL, "/") + "/api/v1/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create /api/v1/models request: %w", err)
	}
	if token := server.ResolvedToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned HTTP %d", endpoint, resp.StatusCode)
	}

	var decoded struct {
		Models []runtimeModelDTO `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("failed to decode /api/v1/models response: %w", err)
	}
	models := make([]RuntimeModelSummary, 0, len(decoded.Models))
	for _, item := range decoded.Models {
		summary := runtimeModelSummaryFromDTO(item)
		if strings.TrimSpace(summary.ID) == "" {
			continue
		}
		models = append(models, summary)
	}
	return models, nil
}

type runtimeModelDTO struct {
	Key             string `json:"key"`
	DisplayName     string `json:"display_name"`
	LoadedInstances []struct {
		ID     string `json:"id"`
		Config struct {
			ContextLength int  `json:"context_length"`
			Parallel      *int `json:"parallel"`
		} `json:"config"`
	} `json:"loaded_instances"`
	MaxContextLength int `json:"max_context_length"`
	Quantization     *struct {
		Name          string   `json:"name"`
		BitsPerWeight *float64 `json:"bits_per_weight"`
	} `json:"quantization"`
	Format       string `json:"format"`
	ParamsString string `json:"params_string"`
	SizeBytes    int64  `json:"size_bytes"`
	Capabilities *struct {
		Vision            *bool `json:"vision"`
		TrainedForToolUse *bool `json:"trained_for_tool_use"`
		Reasoning         *struct {
			AllowedOptions []string `json:"allowed_options"`
			Default        string   `json:"default"`
		} `json:"reasoning"`
	} `json:"capabilities"`
	Variants        []string `json:"variants"`
	SelectedVariant string   `json:"selected_variant"`
}

func runtimeModelSummaryFromDTO(item runtimeModelDTO) RuntimeModelSummary {
	summary := RuntimeModelSummary{
		ID:               item.Key,
		DisplayName:      item.DisplayName,
		MaxContextLength: item.MaxContextLength,
		Format:           item.Format,
		Params:           item.ParamsString,
		SizeBytes:        item.SizeBytes,
		Variants:         append([]string(nil), item.Variants...),
		SelectedVariant:  item.SelectedVariant,
	}
	if item.Quantization != nil {
		summary.Quantization = item.Quantization.Name
		if summary.Quantization == "" && item.Quantization.BitsPerWeight != nil {
			summary.Quantization = fmt.Sprintf("%.0fbit", *item.Quantization.BitsPerWeight)
		}
	}
	for _, instance := range item.LoadedInstances {
		if strings.TrimSpace(instance.ID) == "" {
			continue
		}
		summary.Loaded = true
		summary.LoadedInstances = append(summary.LoadedInstances, instance.ID)
		loadedConfig := RuntimeLoadedInstanceConfig{
			ID:            instance.ID,
			ContextLength: instance.Config.ContextLength,
		}
		if instance.Config.Parallel != nil {
			parallel := *instance.Config.Parallel
			loadedConfig.Parallel = &parallel
		}
		summary.LoadedInstanceConfigs = append(summary.LoadedInstanceConfigs, loadedConfig)
		if instance.Config.ContextLength > summary.ContextLength {
			summary.ContextLength = instance.Config.ContextLength
		}
	}
	if item.Capabilities != nil {
		summary.TrainedForToolUse = item.Capabilities.TrainedForToolUse
		summary.Vision = item.Capabilities.Vision
		if item.Capabilities.Reasoning != nil {
			summary.ReasoningAllowed = append([]string(nil), item.Capabilities.Reasoning.AllowedOptions...)
			summary.ReasoningDefault = item.Capabilities.Reasoning.Default
		}
	}
	return summary
}

func resolveServer(cfg config.Config, serverName string) (config.ServerTarget, error) {
	if serverName == "" {
		return cfg.ResolveServer()
	}
	for _, server := range cfg.Server.Servers {
		if server.Name == serverName {
			return server, nil
		}
	}
	return config.ServerTarget{}, fmt.Errorf("server %q was not found", serverName)
}

func matchModel(configured string, models []string) (string, bool) {
	for _, model := range models {
		if model == configured {
			return model, true
		}
	}
	candidates := normalizedModelCandidates(configured)
	for _, model := range models {
		modelCandidates := normalizedModelCandidates(model)
		for _, left := range candidates {
			for _, right := range modelCandidates {
				if left == right || strings.Contains(right, left) || strings.Contains(left, right) {
					return model, true
				}
			}
		}
	}
	return "", false
}

func matchRuntimeModel(configured string, models []RuntimeModelSummary) (RuntimeModelSummary, bool) {
	configuredCandidates := normalizedModelCandidates(configured)
	for _, model := range models {
		for _, alias := range runtimeModelAliases(model) {
			if alias == configured {
				return model, true
			}
		}
	}
	for _, model := range models {
		modelCandidates := []string{}
		for _, alias := range runtimeModelAliases(model) {
			modelCandidates = append(modelCandidates, normalizedModelCandidates(alias)...)
		}
		for _, left := range configuredCandidates {
			for _, right := range modelCandidates {
				if left == right || strings.Contains(right, left) || strings.Contains(left, right) {
					return model, true
				}
			}
		}
	}
	return RuntimeModelSummary{}, false
}

func runtimeModelAliases(model RuntimeModelSummary) []string {
	aliases := []string{model.ID, model.DisplayName, model.SelectedVariant}
	aliases = append(aliases, model.LoadedInstances...)
	aliases = append(aliases, model.Variants...)
	out := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias != "" {
			out = append(out, alias)
		}
	}
	return out
}

func normalizedModelCandidates(value string) []string {
	items := []string{normalizeModelID(value)}
	if idx := strings.LastIndex(value, "/"); idx >= 0 && idx+1 < len(value) {
		items = append(items, normalizeModelID(value[idx+1:]))
	}
	return items
}

func normalizeModelID(value string) string {
	value = strings.ToLower(value)
	replacer := strings.NewReplacer("/", "", "-", "", "_", "", ".", "", ":", "", " ", "")
	return replacer.Replace(value)
}

func lmStudioServerSuggestions(url string) []string {
	return []string{
		"LM Studio の Developer tab で Start server を有効にしてください",
		"CLI を使う場合は `lms server start --port 1234` を実行してください",
		fmt.Sprintf("yagent の base URL は /v1 を付けずに %q のように設定してください", url),
	}
}

func probeSuggestions(api string, url string) []string {
	suggestions := []string{
		"LM Studio で対象 model が load 済みか確認してください",
		"モデル identifier が /v1/models と config で完全一致しているか確認してください",
	}
	if api == "responses" {
		suggestions = append(suggestions, "LM Studio 側の Responses endpoint が有効か確認し、未対応なら server.servers[].api = \"chat_completions\" にしてください")
	} else {
		suggestions = append(suggestions, fmt.Sprintf("OpenAI-compatible chat endpoint %s/v1/chat/completions が使えるか確認してください", strings.TrimRight(url, "/")))
	}
	return suggestions
}

func containsString(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func recommendedLoadedContext(runtime RuntimeResult) int {
	if runtime.MaxContextLength >= 32768 {
		return 32768
	}
	if runtime.MaxContextLength >= 25000 {
		return runtime.MaxContextLength
	}
	return 0
}

func recommendedCompactAfterTokens(contextLength int) int {
	if contextLength <= 0 {
		return 0
	}
	target := contextLength * 45 / 100
	if target > 12000 {
		target = 12000
	}
	if target < 2000 {
		target = 2000
	}
	return target
}

func recommendedMaxOutputTokens(contextLength int) int {
	if contextLength <= 0 {
		return 8192
	}
	target := contextLength / 4
	if target > 8192 {
		target = 8192
	}
	if target < 1024 {
		target = 1024
	}
	return target
}

func formatIntSetting(value int) string {
	if value == 0 {
		return "(unset)"
	}
	return fmt.Sprintf("%d", value)
}

func formatFloatSetting(value *float64) string {
	if value == nil {
		return "(unset)"
	}
	return formatFloat(*value)
}

func formatFloat(value float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", value), "0"), ".")
}

func floatEqual(left float64, right float64) bool {
	diff := left - right
	if diff < 0 {
		diff = -diff
	}
	return diff < 0.000001
}

func fallback(value string, fallbackValue string) string {
	if value != "" {
		return value
	}
	return fallbackValue
}
