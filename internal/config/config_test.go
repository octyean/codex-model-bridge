package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"codex-bridge/internal/adapters"
)

func TestCheckUnknownFieldsRejectsUnknownSetting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("read_top_k = 3\n[server]\nlisten = \"127.0.0.1:8787\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := CheckUnknownFields(path)
	if err == nil || !strings.Contains(err.Error(), "read_top_k") {
		t.Fatalf("CheckUnknownFields() error = %v, want read_top_k", err)
	}
}

func TestK3ReasoningUsesGenericDefaultsWithoutModelNameHardCoding(t *testing.T) {
	model := ModelConfig{UpstreamModel: "k3"}
	if got := defaultReasoningLevel(model, true); got != "high" {
		t.Fatalf("default reasoning level = %q, want high", got)
	}

	levels := reasoningLevelsForModel(model, true)
	want := []ReasoningEffortPreset{
		{Effort: "low", Description: "Fast responses with lighter reasoning"},
		{Effort: "medium", Description: "Balanced reasoning for coding tasks"},
		{Effort: "high", Description: "Deeper reasoning for complex changes"},
	}
	if !reflect.DeepEqual(levels, want) {
		t.Fatalf("reasoning levels = %#v, want %#v", levels, want)
	}
}

func TestReasoningLevelsUseModelConfiguration(t *testing.T) {
	model := ModelConfig{
		UpstreamModel:            "k3",
		DefaultReasoningLevel:    "high",
		SupportedReasoningLevels: []string{"high", "max", "ultra"},
	}
	if got := defaultReasoningLevel(model, true); got != "high" {
		t.Fatalf("default reasoning level = %q, want high", got)
	}

	levels := reasoningLevelsForModel(model, true)
	want := []ReasoningEffortPreset{
		{Effort: "high", Description: "Deeper reasoning for complex changes"},
		{Effort: "max", Description: "Maximum provider-supported reasoning"},
		{Effort: "ultra", Description: "Maximum reasoning with automatic task delegation"},
	}
	if !reflect.DeepEqual(levels, want) {
		t.Fatalf("reasoning levels = %#v, want %#v", levels, want)
	}
}

func TestValidateRejectsReasoningDefaultOutsideSupportedLevels(t *testing.T) {
	cfg := Config{
		Server: ServerConfig{Listen: "127.0.0.1:8787"},
		Codex: CodexConfig{
			ModelCatalogPath: "/tmp/models.json",
			DefaultModel:     "test-model",
			LocalToken:       "local-token",
		},
		Providers: map[string]ProviderConfig{
			"test": {
				Type:    "openai_compatible",
				BaseURL: "https://example.com/v1",
				APIKey:  "test-key",
				Profile: "default",
			},
		},
		Models: map[string]ModelConfig{
			"test-model": {
				DisplayName:              "Test Model",
				Provider:                 "test",
				Profile:                  "default",
				UpstreamModel:            "test-model",
				DefaultReasoningLevel:    "max",
				SupportedReasoningLevels: []string{"high"},
				ContextWindow:            64000,
				ApplyPatchToolType:       "freeform",
			},
		},
	}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "default_reasoning_level") {
		t.Fatalf("Validate() error = %v, want default_reasoning_level error", err)
	}
}

func TestShutdownTimeoutDefaultsToThirtySeconds(t *testing.T) {
	cfg := Config{}
	if got := cfg.ShutdownTimeout(); got != 30*time.Second {
		t.Fatalf("shutdown timeout = %s, want 30s", got)
	}
	cfg.Server.ShutdownTimeoutSeconds = 45
	if got := cfg.ShutdownTimeout(); got != 45*time.Second {
		t.Fatalf("shutdown timeout = %s, want 45s", got)
	}
}

func TestValidateRejectsNegativeShutdownTimeout(t *testing.T) {
	cfg := Config{Server: ServerConfig{
		Listen:                 "127.0.0.1:8787",
		ShutdownTimeoutSeconds: -1,
	}}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "shutdown_timeout_seconds") {
		t.Fatalf("Validate() error = %v, want shutdown_timeout_seconds error", err)
	}
}

func TestExecutionPlanUsesExplicitProjectedResponsesMode(t *testing.T) {
	supportsOptions := false
	cfg := Config{}
	model := ModelConfig{
		Profile:                  "default",
		UpstreamModel:            "third-party-model",
		ExecutionMode:            ExecutionModeProjectedResponses,
		SupportsResponsesOptions: &supportsOptions,
	}
	plan := cfg.ExecutionPlan(model, ProviderConfig{Protocol: "chat_completions"})

	if plan.Mode != ExecutionModeProjectedResponses {
		t.Fatalf("mode = %q, want %q", plan.Mode, ExecutionModeProjectedResponses)
	}
	if plan.Protocol != "responses" {
		t.Fatalf("protocol = %q, want responses", plan.Protocol)
	}
	if plan.SupportsResponsesOptions {
		t.Fatalf("supports responses options = true, want false")
	}
}

func TestExecutionPlanKeepsVerifiedProjectedProfilesResponsesOptions(t *testing.T) {
	cfg := Config{}
	plan := cfg.ExecutionPlan(
		ModelConfig{Profile: "kimi", UpstreamModel: "kimi-for-coding"},
		ProviderConfig{Protocol: "responses"},
	)

	if plan.Mode != ExecutionModeProjectedResponses {
		t.Fatalf("mode = %q, want %q", plan.Mode, ExecutionModeProjectedResponses)
	}
	if !plan.SupportsResponsesOptions {
		t.Fatalf("supports responses options = false, want true")
	}
	if !plan.SupportsResponsesStructuredOutput {
		t.Fatalf("supports structured output = false, want true")
	}
}

func TestExecutionPlanUsesMimoStructuredOutputFallbackByDefault(t *testing.T) {
	cfg := Config{}
	plan := cfg.ExecutionPlan(
		ModelConfig{Profile: "mimo", UpstreamModel: "mimo-v2.5-pro"},
		ProviderConfig{Protocol: "responses"},
	)

	if !plan.SupportsResponsesOptions {
		t.Fatalf("supports responses options = false, want true")
	}
	if plan.SupportsResponsesStructuredOutput {
		t.Fatalf("supports structured output = true, want false")
	}
}

func TestDesktopModelSlugUsesStableCompatibilitySlots(t *testing.T) {
	cases := map[string]string{
		"kimi-for-coding": "gpt-5.3-codex",
		"mimo-v2.5-pro":   "gpt-5.2",
		"mimo-v2.5":       "gpt-5.4-mini",
		"gpt-5.4":         "gpt-5.4",
		"gpt-5.6-sol":     "gpt-5.6-sol",
		"gpt-5.6-terra":   "gpt-5.6-terra",
		"gpt-5.6-luna":    "gpt-5.6-luna",
		"other-model":     "",
	}
	for model, want := range cases {
		if got := DesktopModelSlug(model); got != want {
			t.Fatalf("DesktopModelSlug(%q) = %q, want %q", model, got, want)
		}
	}
}

func TestOpenAIReasoningModelsUseNativeResponses(t *testing.T) {
	cfg := Config{}
	model := ModelConfig{UpstreamModel: "o1"}
	provider := ProviderConfig{Type: "openai_compatible", Protocol: "auto"}

	if got := cfg.ProfileName(model, provider); got != adapters.OpenAIName {
		t.Fatalf("profile = %q, want openai", got)
	}
	if got := cfg.UpstreamProtocol(model, provider); got != "responses" {
		t.Fatalf("protocol = %q, want responses", got)
	}
}

func TestAddDiscoveredModelsAssignsOnlyStableDesktopSlots(t *testing.T) {
	cfg := Config{
		ModelDiscovery: ModelDiscoveryConfig{Enabled: true, Mode: "merge"},
		Providers: map[string]ProviderConfig{
			"upstream": {Profile: "default"},
		},
	}

	cfg.AddDiscoveredModels("upstream", []string{
		"zeta-model",
		"mimo-v2.5-pro",
		"kimi-for-coding",
		"alpha-model",
	})

	want := map[string]string{
		"gpt-5.3-codex": "kimi-for-coding",
		"gpt-5.2":       "mimo-v2.5-pro",
		"gpt-5.4-mini":  "alpha-model",
		"gpt-5.5":       "zeta-model",
	}
	if len(cfg.Models) != len(want) {
		t.Fatalf("models = %#v", cfg.Models)
	}
	for slug, upstreamModel := range want {
		if got := cfg.Models[slug].UpstreamModel; got != upstreamModel {
			t.Fatalf("models.%s.upstream_model = %q, want %q", slug, got, upstreamModel)
		}
	}
}

func TestAddDiscoveredModelsKeepsExplicitModelProfileInMergeMode(t *testing.T) {
	cfg := Config{
		ModelDiscovery: ModelDiscoveryConfig{Enabled: true, Mode: "merge"},
		Providers: map[string]ProviderConfig{
			"upstream": {Profile: "default"},
		},
		Models: map[string]ModelConfig{
			"gpt-5.3-codex": {
				DisplayName:        "Kimi for Coding",
				Provider:           "upstream",
				Profile:            "kimi",
				UpstreamModel:      "kimi-for-coding",
				ContextWindow:      192000,
				ApplyPatchToolType: "freeform",
			},
		},
		discoveryAssignments: modelDiscoveryAssignments{
			Version: 1,
			Providers: map[string]map[string]string{
				"upstream": {"kimi-for-coding": "gpt-5.3-codex"},
			},
		},
	}

	report := cfg.AddDiscoveredModelsReport("upstream", []string{"kimi-for-coding"})

	if report.Added != 0 {
		t.Fatalf("added = %d, want 0", report.Added)
	}
	if got := cfg.Models["gpt-5.3-codex"].Profile; got != "kimi" {
		t.Fatalf("profile = %q, want kimi", got)
	}
}
