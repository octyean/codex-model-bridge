package config

import (
	"os"
	"strings"
	"testing"

	"codex-bridge/internal/upstreamprobe"
)

func TestVerifiedCapabilityRejectsCredentialAndProfileChanges(t *testing.T) {
	model := ModelConfig{Provider: "upstream", UpstreamModel: "test-model", Profile: "default"}
	provider := ProviderConfig{
		Type:     "openai_compatible",
		BaseURL:  "https://example.test/v1",
		APIKey:   "secret-a",
		Profile:  "default",
		Protocol: "responses",
	}
	cfg := Config{
		Providers: map[string]ProviderConfig{"upstream": provider},
		Models:    map[string]ModelConfig{"test-model": model},
	}
	cfg.UpdateVerifiedCapability("upstream", provider, upstreamprobe.Result{ProbeModel: model.UpstreamModel})

	if _, ok := cfg.VerifiedCapability(model, provider); !ok {
		t.Fatal("fresh capability should be accepted")
	}

	changedCredential := provider
	changedCredential.APIKey = "secret-b"
	if _, ok := cfg.VerifiedCapability(model, changedCredential); ok {
		t.Fatal("capability should be rejected after credential change")
	}

	changedProfile := model
	changedProfile.Profile = "kimi"
	cfg.Models["test-model"] = changedProfile
	if _, ok := cfg.VerifiedCapability(changedProfile, provider); ok {
		t.Fatal("capability should be rejected after profile change")
	}
}

func TestCapabilityCacheDoesNotPersistAPIKey(t *testing.T) {
	path := t.TempDir() + "/config.toml"
	provider := ProviderConfig{
		Type:    "openai_compatible",
		BaseURL: "https://example.test/v1",
		APIKey:  "secret-api-key",
		Profile: "default",
	}
	cfg := Config{
		Path:      path,
		Providers: map[string]ProviderConfig{"upstream": provider},
		Models: map[string]ModelConfig{
			"test-model": {Provider: "upstream", UpstreamModel: "test-model", Profile: "default"},
		},
	}
	cfg.UpdateVerifiedCapability("upstream", provider, upstreamprobe.Result{ProbeModel: "test-model"})
	if err := cfg.WriteCapabilityCache(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(cfg.CapabilityCachePath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), provider.APIKey) {
		t.Fatal("capability cache persisted the API key")
	}
}
