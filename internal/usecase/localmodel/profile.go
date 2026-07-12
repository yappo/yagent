package localmodel

import (
	"fmt"
	"strings"

	"yagent/internal/config"
)

const (
	PresetAuto    = "auto"
	PresetQwen36  = "qwen36"
	PresetGemma4  = "gemma4"
	PresetGeneric = "generic"

	DefaultQwen36Model = "Qwen/Qwen3.6-35B-A3B"
	DefaultGemma4Model = "google/gemma-4-26b-a4b"
)

type Profile struct {
	Preset       string
	Label        string
	DefaultModel string
	Generation   config.GenerationConfig
}

func Resolve(preset string, model string) (Profile, error) {
	preset = normalizePreset(preset)
	if preset == PresetAuto {
		preset = Detect(model)
		if preset == PresetGeneric && strings.TrimSpace(model) == "" {
			preset = PresetQwen36
		}
	}

	profile, ok := profileForPreset(preset)
	if !ok {
		return Profile{}, fmt.Errorf("unknown local model preset %q; use auto, qwen36, gemma4, or generic", preset)
	}
	if profile.Preset == PresetGeneric && strings.TrimSpace(model) == "" {
		return Profile{}, fmt.Errorf("local model preset %q requires --local-model", PresetGeneric)
	}
	return profile, nil
}

func Detect(model string) string {
	normalized := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.Contains(normalized, "gemma-4"), strings.Contains(normalized, "gemma4"):
		return PresetGemma4
	case strings.Contains(normalized, "qwen3.6"), strings.Contains(normalized, "qwen-3.6"):
		return PresetQwen36
	default:
		return PresetGeneric
	}
}

func IsGemma4(model string) bool {
	return Detect(model) == PresetGemma4
}

func IsQwen36(model string) bool {
	return Detect(model) == PresetQwen36
}

func normalizePreset(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return PresetAuto
	case "qwen", "qwen36", "qwen3.6":
		return PresetQwen36
	case "gemma", "gemma4", "gemma-4":
		return PresetGemma4
	case "generic", "custom":
		return PresetGeneric
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func profileForPreset(preset string) (Profile, bool) {
	switch preset {
	case PresetQwen36:
		return Profile{
			Preset:       PresetQwen36,
			Label:        "Qwen 3.6",
			DefaultModel: DefaultQwen36Model,
			Generation: config.GenerationConfig{
				MaxOutputTokens:   8192,
				Temperature:       floatPtr(1.0),
				TopP:              floatPtr(0.95),
				TopK:              20,
				MinP:              floatPtr(0.0),
				PresencePenalty:   floatPtr(1.5),
				RepetitionPenalty: floatPtr(1.0),
			},
		}, true
	case PresetGemma4:
		return Profile{
			Preset:       PresetGemma4,
			Label:        "Gemma 4",
			DefaultModel: DefaultGemma4Model,
			Generation: config.GenerationConfig{
				MaxOutputTokens: 8192,
				Temperature:     floatPtr(1.0),
				TopP:            floatPtr(0.95),
				TopK:            64,
			},
		}, true
	case PresetGeneric:
		return Profile{
			Preset: PresetGeneric,
			Label:  "generic local model",
			Generation: config.GenerationConfig{
				MaxOutputTokens: 8192,
			},
		}, true
	default:
		return Profile{}, false
	}
}

func floatPtr(value float64) *float64 {
	return &value
}
