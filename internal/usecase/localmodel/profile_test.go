package localmodel

import "testing"

func TestResolveGemma4Preset(t *testing.T) {
	profile, err := Resolve(PresetGemma4, "")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if profile.DefaultModel != DefaultGemma4Model || profile.Generation.TopK != 64 {
		t.Fatalf("unexpected Gemma 4 profile: %+v", profile)
	}
	if profile.Generation.Temperature == nil || *profile.Generation.Temperature != 1.0 {
		t.Fatalf("expected Gemma 4 temperature 1.0, got %+v", profile.Generation)
	}
}

func TestResolveAutoDetectsGemma4(t *testing.T) {
	profile, err := Resolve(PresetAuto, "google/gemma-4-12b")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if profile.Preset != PresetGemma4 {
		t.Fatalf("expected Gemma 4 preset, got %+v", profile)
	}
}

func TestResolveAutoKeepsQwenDefault(t *testing.T) {
	profile, err := Resolve(PresetAuto, "")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if profile.Preset != PresetQwen36 || profile.DefaultModel != DefaultQwen36Model {
		t.Fatalf("expected Qwen default profile, got %+v", profile)
	}
}

func TestResolveRejectsUnknownPreset(t *testing.T) {
	if _, err := Resolve("missing", "model"); err == nil {
		t.Fatal("expected unknown preset error")
	}
}
