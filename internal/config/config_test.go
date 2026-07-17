package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestReasoningLevelsDoNotDependOnUpstreamModelName(t *testing.T) {
	levels := reasoningLevelsForModel(ModelConfig{UpstreamModel: "gpt-5.6-sol"}, true)
	efforts := make([]string, 0, len(levels))
	for _, level := range levels {
		efforts = append(efforts, level.Effort)
	}

	want := []string{"low", "medium", "high"}
	if !reflect.DeepEqual(efforts, want) {
		t.Fatalf("reasoning efforts = %v, want %v", efforts, want)
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
		SupportedReasoningLevels: []string{"high", "max"},
	}
	if got := defaultReasoningLevel(model, true); got != "high" {
		t.Fatalf("default reasoning level = %q, want high", got)
	}

	levels := reasoningLevelsForModel(model, true)
	want := []ReasoningEffortPreset{
		{Effort: "high", Description: "Deeper reasoning for complex changes"},
		{Effort: "max", Description: "Maximum provider-supported reasoning"},
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
		"other-model":     "",
	}
	for model, want := range cases {
		if got := DesktopModelSlug(model); got != want {
			t.Fatalf("DesktopModelSlug(%q) = %q, want %q", model, got, want)
		}
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
