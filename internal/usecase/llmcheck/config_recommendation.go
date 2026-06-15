package llmcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"yagent/internal/config"
)

type ConfigRecommendationPlan struct {
	ServerName string
	Config     config.Config
	Changes    []ConfigChange
	External   []ConfigChange
}

type ConfigChange struct {
	Setting     string `json:"setting"`
	Current     string `json:"current,omitempty"`
	Recommended string `json:"recommended"`
	Reason      string `json:"reason,omitempty"`
}

type ConfigWriteResult struct {
	Path    string
	Status  string
	Bytes   int
	Changes int
}

func BuildRecommendedConfig(cfg config.Config, result Result) ConfigRecommendationPlan {
	next := cloneConfig(cfg)
	serverIndex := resolvePlanServerIndex(next, result.ServerName)
	serverName := result.ServerName
	if serverIndex >= 0 && serverName == "" {
		serverName = next.Server.Servers[serverIndex].Name
	}
	plan := ConfigRecommendationPlan{
		ServerName: serverName,
		Config:     next,
	}
	if serverIndex < 0 {
		return plan
	}

	originalModel := plan.Config.Server.Servers[serverIndex].Model
	if result.ModelFound && result.MatchedModel != "" && result.MatchedModel != originalModel {
		plan.Config.Server.Servers[serverIndex].Model = result.MatchedModel
		plan.Changes = append(plan.Changes, ConfigChange{
			Setting:     fmt.Sprintf("server.servers[%q].model", plan.Config.Server.Servers[serverIndex].Name),
			Current:     originalModel,
			Recommended: result.MatchedModel,
			Reason:      "LM Studio /v1/models returned a fuzzy match; using the exact runtime model id avoids request mismatch",
		})
		plan.Changes = append(plan.Changes, updateModelReferences(&plan.Config, originalModel, result.MatchedModel)...)
	}

	for _, recommendation := range result.Recommendations {
		if recommendation.Area == "lmstudio" {
			plan.External = append(plan.External, ConfigChange{
				Setting:     recommendation.Setting,
				Current:     recommendation.Current,
				Recommended: recommendation.Recommended,
				Reason:      recommendation.Reason,
			})
			continue
		}
		if change, ok := applyConfigRecommendation(&plan.Config, serverIndex, recommendation); ok {
			plan.Changes = append(plan.Changes, change)
		}
	}
	return plan
}

func WriteRecommendedConfig(path string, plan ConfigRecommendationPlan, force bool) (ConfigWriteResult, error) {
	if strings.TrimSpace(path) == "" {
		return ConfigWriteResult{}, fmt.Errorf("recommended config path is empty")
	}
	cleanPath := filepath.Clean(path)
	if _, err := os.Stat(cleanPath); err == nil && !force {
		return ConfigWriteResult{}, fmt.Errorf("%s already exists; use --force-recommended-config to overwrite it", cleanPath)
	} else if err != nil && !os.IsNotExist(err) {
		return ConfigWriteResult{}, fmt.Errorf("%s の確認に失敗しました: %w", cleanPath, err)
	}

	data, err := config.Marshal(plan.Config)
	if err != nil {
		return ConfigWriteResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(cleanPath), 0o755); err != nil {
		return ConfigWriteResult{}, fmt.Errorf("%s のディレクトリ作成に失敗しました: %w", cleanPath, err)
	}
	status := "created"
	if _, err := os.Stat(cleanPath); err == nil {
		status = "overwritten"
	}
	if err := os.WriteFile(cleanPath, data, 0o644); err != nil {
		return ConfigWriteResult{}, fmt.Errorf("%s の書き込みに失敗しました: %w", cleanPath, err)
	}
	return ConfigWriteResult{
		Path:    cleanPath,
		Status:  status,
		Bytes:   len(data),
		Changes: len(plan.Changes),
	}, nil
}

func applyConfigRecommendation(cfg *config.Config, serverIndex int, recommendation Recommendation) (ConfigChange, bool) {
	switch recommendation.Setting {
	case "context.compact_after_est_tokens":
		value, ok := parseRecommendedInt(recommendation.Recommended)
		if !ok || cfg.Context.CompactAfterEstTokens == value {
			return ConfigChange{}, false
		}
		current := formatIntSetting(cfg.Context.CompactAfterEstTokens)
		cfg.Context.CompactAfterEstTokens = value
		return ConfigChange{
			Setting:     recommendation.Setting,
			Current:     current,
			Recommended: recommendation.Recommended,
			Reason:      recommendation.Reason,
		}, true
	case "server.servers[].generation.max_output_tokens":
		value, ok := parseRecommendedInt(recommendation.Recommended)
		if !ok || cfg.Server.Servers[serverIndex].Generation.MaxOutputTokens == value {
			return ConfigChange{}, false
		}
		current := formatIntSetting(cfg.Server.Servers[serverIndex].Generation.MaxOutputTokens)
		cfg.Server.Servers[serverIndex].Generation.MaxOutputTokens = value
		return generationChange(cfg, serverIndex, "max_output_tokens", current, recommendation), true
	case "server.servers[].generation.top_k":
		value, ok := parseRecommendedInt(recommendation.Recommended)
		if !ok || cfg.Server.Servers[serverIndex].Generation.TopK == value {
			return ConfigChange{}, false
		}
		current := formatIntSetting(cfg.Server.Servers[serverIndex].Generation.TopK)
		cfg.Server.Servers[serverIndex].Generation.TopK = value
		return generationChange(cfg, serverIndex, "top_k", current, recommendation), true
	case "server.servers[].generation.temperature":
		return applyFloatRecommendation(cfg, serverIndex, "temperature", recommendation, &cfg.Server.Servers[serverIndex].Generation.Temperature)
	case "server.servers[].generation.top_p":
		return applyFloatRecommendation(cfg, serverIndex, "top_p", recommendation, &cfg.Server.Servers[serverIndex].Generation.TopP)
	case "server.servers[].generation.min_p":
		return applyFloatRecommendation(cfg, serverIndex, "min_p", recommendation, &cfg.Server.Servers[serverIndex].Generation.MinP)
	case "server.servers[].generation.presence_penalty":
		return applyFloatRecommendation(cfg, serverIndex, "presence_penalty", recommendation, &cfg.Server.Servers[serverIndex].Generation.PresencePenalty)
	case "server.servers[].generation.repetition_penalty":
		return applyFloatRecommendation(cfg, serverIndex, "repetition_penalty", recommendation, &cfg.Server.Servers[serverIndex].Generation.RepetitionPenalty)
	default:
		return ConfigChange{}, false
	}
}

func applyFloatRecommendation(cfg *config.Config, serverIndex int, key string, recommendation Recommendation, target **float64) (ConfigChange, bool) {
	value, ok := parseRecommendedFloat(recommendation.Recommended)
	if !ok || (*target != nil && floatEqual(**target, value)) {
		return ConfigChange{}, false
	}
	current := formatFloatSetting(*target)
	*target = &value
	return generationChange(cfg, serverIndex, key, current, recommendation), true
}

func generationChange(cfg *config.Config, serverIndex int, key string, current string, recommendation Recommendation) ConfigChange {
	return ConfigChange{
		Setting:     fmt.Sprintf("server.servers[%q].generation.%s", cfg.Server.Servers[serverIndex].Name, key),
		Current:     current,
		Recommended: recommendation.Recommended,
		Reason:      recommendation.Reason,
	}
}

func updateModelReferences(cfg *config.Config, current string, recommended string) []ConfigChange {
	if current == "" || current == recommended {
		return nil
	}
	changes := []ConfigChange{}
	profileNames := make([]string, 0, len(cfg.Routing.Profiles))
	for name := range cfg.Routing.Profiles {
		profileNames = append(profileNames, name)
	}
	sort.Strings(profileNames)
	for _, name := range profileNames {
		profile := cfg.Routing.Profiles[name]
		if profile.Model == current {
			profile.Model = recommended
			cfg.Routing.Profiles[name] = profile
			changes = append(changes, ConfigChange{
				Setting:     fmt.Sprintf("routing.profiles.%s.model", name),
				Current:     current,
				Recommended: recommended,
				Reason:      "keep routing profile model in sync with the LM Studio exact model id",
			})
		}
		if profile.FallbackModel == current {
			profile.FallbackModel = recommended
			cfg.Routing.Profiles[name] = profile
			changes = append(changes, ConfigChange{
				Setting:     fmt.Sprintf("routing.profiles.%s.fallback_model", name),
				Current:     current,
				Recommended: recommended,
				Reason:      "keep routing fallback model in sync with the LM Studio exact model id",
			})
		}
	}
	agentNames := make([]string, 0, len(cfg.Agents))
	for name := range cfg.Agents {
		agentNames = append(agentNames, name)
	}
	sort.Strings(agentNames)
	for _, name := range agentNames {
		agent := cfg.Agents[name]
		if agent.Model != current {
			continue
		}
		agent.Model = recommended
		cfg.Agents[name] = agent
		changes = append(changes, ConfigChange{
			Setting:     fmt.Sprintf("agents.%s.model", name),
			Current:     current,
			Recommended: recommended,
			Reason:      "keep agent override model in sync with the LM Studio exact model id",
		})
	}
	return changes
}

func resolvePlanServerIndex(cfg config.Config, serverName string) int {
	target := serverName
	if target == "" {
		target = cfg.Server.Default
	}
	for i, server := range cfg.Server.Servers {
		if target == "" || server.Name == target {
			return i
		}
	}
	return -1
}

func parseRecommendedInt(value string) (int, bool) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func parseRecommendedFloat(value string) (float64, bool) {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func cloneConfig(cfg config.Config) config.Config {
	cfg.Server.Servers = append([]config.ServerTarget(nil), cfg.Server.Servers...)
	cfg.Permission.Rules = append([]config.PermissionRuleConfig(nil), cfg.Permission.Rules...)
	cfg.File.AllowPaths = append([]string(nil), cfg.File.AllowPaths...)
	cfg.AgentCatalog.Paths = append([]string(nil), cfg.AgentCatalog.Paths...)
	if cfg.Routing.Profiles != nil {
		profiles := make(map[string]config.RoutingProfileConfig, len(cfg.Routing.Profiles))
		for key, value := range cfg.Routing.Profiles {
			profiles[key] = value
		}
		cfg.Routing.Profiles = profiles
	}
	if cfg.Agents != nil {
		agents := make(map[string]config.AgentOverride, len(cfg.Agents))
		for key, value := range cfg.Agents {
			agents[key] = value
		}
		cfg.Agents = agents
	}
	return cfg
}
