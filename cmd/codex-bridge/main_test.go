package main

import (
	"bytes"
	"strings"
	"testing"

	"codex-bridge/internal/config"
	"codex-bridge/internal/upstreamprobe"
)

func TestWriteConfigSummarySortsModelsAndHidesCredentials(t *testing.T) {
	provider := config.ProviderConfig{
		Type:     "openai_compatible",
		BaseURL:  "https://example.test/v1",
		APIKey:   "secret-api-key",
		Profile:  "default",
		Protocol: "auto",
	}
	cfg := &config.Config{
		Providers: map[string]config.ProviderConfig{"upstream": provider},
		Models: map[string]config.ModelConfig{
			"z-model": {
				DisplayName:   "Z Model",
				Provider:      "upstream",
				Profile:       "mimo",
				UpstreamModel: "mimo-v2.5",
			},
			"a-model": {
				DisplayName:   "A Model",
				Provider:      "upstream",
				Profile:       "kimi",
				UpstreamModel: "kimi-for-coding",
				ExecutionMode: config.ExecutionModeProjectedResponses,
			},
			"openai-model": {
				DisplayName:   "OpenAI Model",
				Provider:      "upstream",
				Profile:       "openai",
				UpstreamModel: "gpt-5.6-sol",
			},
		},
	}
	if !cfg.UpdateVerifiedCapability("upstream", provider, upstreamprobe.Result{
		ProbeModel:          "kimi-for-coding",
		RecommendedProtocol: "responses",
	}) {
		t.Fatal("capability was not cached")
	}

	var output bytes.Buffer
	if err := writeConfigSummary(&output, cfg); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if strings.Index(text, "a-model") > strings.Index(text, "z-model") {
		t.Fatalf("models are not sorted:\n%s", text)
	}
	for _, expected := range []string{"DISPLAY NAME", "UPSTREAM MODEL", "EXECUTION MODE", "a-model", "A Model", "projected_responses", "verified", "z-model", "verification_required", "openai-model", "not_required"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("summary missing %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, provider.APIKey) || strings.Contains(text, provider.BaseURL) {
		t.Fatalf("summary exposed provider secrets or endpoint:\n%s", text)
	}
}

func TestResolveUpstreamAPIKeyPrefersFlag(t *testing.T) {
	t.Setenv("CODEX_BRIDGE_API_KEY", "env-key")
	if got := resolveUpstreamAPIKey("flag-key"); got != "flag-key" {
		t.Fatalf("resolved API key = %q, want flag-key", got)
	}
	if got := resolveUpstreamAPIKey(""); got != "env-key" {
		t.Fatalf("resolved API key = %q, want env-key", got)
	}
}

func TestSummarizeSkippedModelsBoundsOutput(t *testing.T) {
	models, omitted := summarizeSkippedModels([]string{"a", "b", "c"}, 2)
	if strings.Join(models, ",") != "a,b" || omitted != 1 {
		t.Fatalf("summary = %v, omitted = %d", models, omitted)
	}
}
