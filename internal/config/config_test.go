package config

import (
	"os"
	"testing"
)

func TestCatalogIncludesCodexModelFields(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{Listen: "127.0.0.1:8787"},
		Codex:  CodexConfig{DefaultModel: "deepseek-v4-flash", ModelCatalogPath: "/tmp/models.json", LocalToken: "token"},
		Providers: map[string]ProviderConfig{
			"sub2api": {Type: "openai_chat_compatible", BaseURL: "https://example.test/v1", APIKey: "sk-test"},
		},
		Models: map[string]ModelConfig{
			"deepseek-v4-flash": {
				DisplayName: "DeepSeek V4 Flash", Provider: "sub2api", UpstreamModel: "deepseek-v4-flash",
				ContextWindow: 1000000, SupportsParallelToolCalls: true, ApplyPatchToolType: "freeform",
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate config: %v", err)
	}
	catalog := cfg.Catalog()
	if len(catalog.Models) != 1 {
		t.Fatalf("models len = %d", len(catalog.Models))
	}
	model := catalog.Models[0]
	if model.Slug != "deepseek-v4-flash" {
		t.Fatalf("slug = %q", model.Slug)
	}
	if model.DisplayName != "DeepSeek V4 Flash" {
		t.Fatalf("display_name = %q", model.DisplayName)
	}
	if model.ApplyPatchToolType != "freeform" {
		t.Fatalf("apply_patch_tool_type = %q", model.ApplyPatchToolType)
	}
	if model.TruncationPolicy.Limit != 950000 {
		t.Fatalf("truncation limit = %d", model.TruncationPolicy.Limit)
	}
}

func TestCatalogIncludesXHighForOpenAINativeModels(t *testing.T) {
	cfg := validTestConfig()
	cfg.Providers["p"] = ProviderConfig{Type: "openai_compatible", BaseURL: "https://example.test/v1", APIKey: "sk-test", Profile: "default"}
	cfg.Models["m"] = ModelConfig{
		DisplayName: "GPT", Provider: "p", UpstreamModel: "gpt-5.4",
		ContextWindow: 1000000, SupportsParallelToolCalls: true, ApplyPatchToolType: "freeform",
	}
	catalog := cfg.Catalog()
	if !hasReasoningEffort(catalog.Models[0].SupportedReasoningLevels, "xhigh") {
		t.Fatalf("reasoning levels = %#v", catalog.Models[0].SupportedReasoningLevels)
	}
}

func TestOpenAINativeModelDefaultsToImageInput(t *testing.T) {
	cfg := validTestConfig()
	cfg.Providers["p"] = ProviderConfig{Type: "openai_compatible", BaseURL: "https://example.test/v1", APIKey: "sk-test", Profile: "default"}
	cfg.Models["m"] = ModelConfig{
		DisplayName: "GPT", Provider: "p", UpstreamModel: "gpt-5.4",
		ContextWindow: 1000000, SupportsParallelToolCalls: true, ApplyPatchToolType: "freeform",
	}
	catalog := cfg.Catalog()
	if got := cfg.ProfileName(cfg.Models["m"], cfg.Providers["p"]); got != "openai" {
		t.Fatalf("profile = %q", got)
	}
	if !containsString(catalog.Models[0].InputModalities, "image") {
		t.Fatalf("input modalities = %#v", catalog.Models[0].InputModalities)
	}
	if !catalog.Models[0].SupportsImageDetailOriginal {
		t.Fatalf("expected original image detail support")
	}
}

func TestOpenAINativeModelProfileCanBeOverriddenAtModelLevel(t *testing.T) {
	cfg := validTestConfig()
	cfg.Providers["p"] = ProviderConfig{Type: "openai_compatible", BaseURL: "https://example.test/v1", APIKey: "sk-test", Profile: "default"}
	cfg.Models["m"] = ModelConfig{
		DisplayName: "GPT", Provider: "p", UpstreamModel: "gpt-5.4", Profile: "mimo",
		ContextWindow: 1000000, SupportsParallelToolCalls: true, ApplyPatchToolType: "freeform",
	}
	if got := cfg.ProfileName(cfg.Models["m"], cfg.Providers["p"]); got != "mimo" {
		t.Fatalf("profile = %q", got)
	}
}

func TestDeepSeekModelStaysTextOnly(t *testing.T) {
	cfg := validTestConfig()
	cfg.Providers["p"] = ProviderConfig{Type: "openai_compatible", BaseURL: "https://example.test/v1", APIKey: "sk-test", Profile: "deepseek"}
	cfg.Models["m"] = ModelConfig{
		DisplayName: "DeepSeek", Provider: "p", UpstreamModel: "deepseek-v4-flash",
		ContextWindow: 64000, SupportsParallelToolCalls: true, ApplyPatchToolType: "freeform",
	}
	catalog := cfg.Catalog()
	if containsString(catalog.Models[0].InputModalities, "image") {
		t.Fatalf("deepseek should stay text-only: %#v", catalog.Models[0].InputModalities)
	}
}

func hasReasoningEffort(levels []ReasoningEffortPreset, effort string) bool {
	for _, level := range levels {
		if level.Effort == effort {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestProfileNameUsesModelThenProvider(t *testing.T) {
	cfg := &Config{}
	provider := ProviderConfig{Profile: "deepseek"}
	model := ModelConfig{}
	if got := cfg.ProfileName(model, provider); got != "deepseek" {
		t.Fatalf("provider profile = %q", got)
	}
	model.Profile = "default"
	if got := cfg.ProfileName(model, provider); got != "default" {
		t.Fatalf("model profile = %q", got)
	}
}

func TestUpstreamProtocol(t *testing.T) {
	cfg := &Config{}
	if got := cfg.UpstreamProtocol(ModelConfig{UpstreamModel: "gpt-5.4"}, ProviderConfig{Type: "openai_compatible"}); got != "responses" {
		t.Fatalf("openai compatible gpt protocol = %q", got)
	}
	if got := cfg.UpstreamProtocol(ModelConfig{UpstreamModel: "gpt-5.4"}, ProviderConfig{Type: "openai_chat_compatible"}); got != "chat_completions" {
		t.Fatalf("legacy gpt protocol = %q", got)
	}
	if got := cfg.UpstreamProtocol(ModelConfig{UpstreamModel: "gpt-5.4"}, ProviderConfig{Protocol: "auto"}); got != "responses" {
		t.Fatalf("gpt auto protocol = %q", got)
	}
	if got := cfg.UpstreamProtocol(ModelConfig{UpstreamModel: "deepseek-v4-flash"}, ProviderConfig{Protocol: "auto"}); got != "chat_completions" {
		t.Fatalf("deepseek auto protocol = %q", got)
	}
	if got := cfg.UpstreamProtocol(ModelConfig{UpstreamModel: "deepseek-v4-flash"}, ProviderConfig{Protocol: "responses"}); got != "responses" {
		t.Fatalf("explicit responses protocol = %q", got)
	}
}

func TestBridgeBaseURL(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1:8787":    "http://127.0.0.1:8787/v1",
		":8787":             "http://127.0.0.1:8787/v1",
		"0.0.0.0:8787":      "http://127.0.0.1:8787/v1",
		"[::]:8787":         "http://127.0.0.1:8787/v1",
		"http://x.test/api": "http://x.test/api/v1",
		"http://x.test/v1":  "http://x.test/v1",
	}
	for input, want := range cases {
		if got := BridgeBaseURL(input); got != want {
			t.Fatalf("BridgeBaseURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestValidateRejectsUnknownProfile(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{Listen: "127.0.0.1:8787"},
		Codex:  CodexConfig{DefaultModel: "m", ModelCatalogPath: "/tmp/models.json", LocalToken: "token"},
		Providers: map[string]ProviderConfig{
			"p": {Type: "openai_chat_compatible", BaseURL: "https://example.test/v1", APIKey: "sk-test", Profile: "unknown"},
		},
		Models: map[string]ModelConfig{
			"m": {
				DisplayName: "M", Provider: "p", UpstreamModel: "m",
				ContextWindow: 1000000, SupportsParallelToolCalls: true, ApplyPatchToolType: "freeform",
			},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected unknown profile validation error")
	}
}

func TestValidateRejectsUnknownProviderProtocol(t *testing.T) {
	cfg := validTestConfig()
	provider := cfg.Providers["p"]
	provider.Protocol = "unknown"
	cfg.Providers["p"] = provider
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected unknown protocol validation error")
	}
}

func TestValidateCapabilityProviders(t *testing.T) {
	cfg := validTestConfig()
	cfg.Capabilities.Search.Enabled = true
	cfg.Capabilities.Search.Providers = []string{"jina"}
	cfg.SearchProviders = map[string]SearchProvider{"jina": {Type: "jina"}}
	cfg.Capabilities.Vision.Enabled = true
	cfg.Capabilities.Vision.Provider = "vision"
	cfg.VisionProviders = map[string]VisionProvider{"vision": {
		Type:    "openai_chat_compatible_vision",
		BaseURL: "https://vision.example/v1",
		APIKey:  "sk-test",
		Model:   "vision-model",
	}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate config: %v", err)
	}
}

func TestValidateRejectsMissingVisionProvider(t *testing.T) {
	cfg := validTestConfig()
	cfg.Capabilities.Vision.Enabled = true
	cfg.Capabilities.Vision.Provider = "missing"
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected missing vision provider error")
	}
}

func TestValidateExtensionProxyURL(t *testing.T) {
	cfg := validTestConfig()
	cfg.Extensions.Network.ProxyURL = "socks5://127.0.0.1:1080"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate socks5 proxy: %v", err)
	}
	cfg.Extensions.Network.ProxyURL = "ftp://127.0.0.1:21"
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected invalid proxy scheme error")
	}
}

func TestConfigFileModeAllowed(t *testing.T) {
	if !configFileModeAllowed(os.FileMode(0o600), "linux") {
		t.Fatalf("0600 should be allowed on linux")
	}
	if configFileModeAllowed(os.FileMode(0o644), "linux") {
		t.Fatalf("0644 should be rejected on linux")
	}
	if !configFileModeAllowed(os.FileMode(0o644), "windows") {
		t.Fatalf("windows should not reject config by unix permission bits")
	}
}

func validTestConfig() *Config {
	return &Config{
		Server: ServerConfig{Listen: "127.0.0.1:8787"},
		Codex:  CodexConfig{DefaultModel: "m", ModelCatalogPath: "/tmp/models.json", LocalToken: "token"},
		Providers: map[string]ProviderConfig{
			"p": {Type: "openai_chat_compatible", BaseURL: "https://example.test/v1", APIKey: "sk-test", Profile: "default"},
		},
		Models: map[string]ModelConfig{
			"m": {
				DisplayName: "M", Provider: "p", UpstreamModel: "m",
				ContextWindow: 1000000, SupportsParallelToolCalls: true, ApplyPatchToolType: "freeform",
			},
		},
	}
}
