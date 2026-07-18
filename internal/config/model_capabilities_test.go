package config

import (
	"os"
	"strings"
	"testing"
	"time"

	"codex-bridge/internal/upstreamprobe"
)

func TestCapabilityWarningsReportsMissingThirdPartyVerification(t *testing.T) {
	cfg := Config{
		Providers: map[string]ProviderConfig{
			"upstream": {Profile: "default"},
		},
		Models: map[string]ModelConfig{
			"gpt-5.3-codex": {
				Provider:      "upstream",
				Profile:       "kimi",
				UpstreamModel: "kimi-for-coding",
			},
		},
	}

	warnings := cfg.CapabilityWarnings(time.Now())
	if len(warnings) != 1 || !strings.Contains(warnings[0], "upstream/kimi-for-coding has no capability verification") {
		t.Fatalf("warnings = %#v", warnings)
	}
}

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

func TestUpdateVerifiedCapabilityPreservesCacheAfterInconclusiveProbe(t *testing.T) {
	provider := ProviderConfig{
		Type:    "openai_compatible",
		BaseURL: "https://example.test/v1",
		APIKey:  "secret-api-key",
		Profile: "kimi",
	}
	model := ModelConfig{Provider: "upstream", UpstreamModel: "test-model", Profile: "kimi"}
	cfg := Config{
		Providers: map[string]ProviderConfig{"upstream": provider},
		Models:    map[string]ModelConfig{"test-model": model},
	}
	initial := upstreamprobe.Result{
		ProbeModel:                  model.UpstreamModel,
		ResponsesStreamOK:           true,
		ResponsesToolsOK:            true,
		ResponsesToolStreamOK:       true,
		ResponsesToolContinuationOK: true,
		RecommendedProtocol:         "responses",
	}
	if !cfg.UpdateVerifiedCapability("upstream", provider, initial) {
		t.Fatal("initial capability was not cached")
	}
	before, ok := cfg.VerifiedCapability(model, provider)
	if !ok {
		t.Fatal("initial capability is unavailable")
	}

	inconclusive := upstreamprobe.Result{
		ProbeModel:   model.UpstreamModel,
		Inconclusive: map[string]string{"responses_stream": "upstream status 429"},
	}
	if cfg.UpdateVerifiedCapability("upstream", provider, inconclusive) {
		t.Fatal("inconclusive capability replaced the cache")
	}
	after, ok := cfg.VerifiedCapability(model, provider)
	if !ok || after.VerifiedAt != before.VerifiedAt || after.RecommendedProtocol != "responses" {
		t.Fatalf("cached capability changed: before=%#v after=%#v ok=%v", before, after, ok)
	}
}
