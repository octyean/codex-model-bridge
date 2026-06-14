package config

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"codex-bridge/internal/adapters"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Path            string
	Server          ServerConfig              `toml:"server"`
	Codex           CodexConfig               `toml:"codex"`
	ModelDiscovery  ModelDiscoveryConfig      `toml:"model_discovery"`
	Extensions      ExtensionsConfig          `toml:"extensions"`
	Capabilities    CapabilitiesConfig        `toml:"capabilities"`
	SearchProviders map[string]SearchProvider `toml:"search_providers"`
	VisionProviders map[string]VisionProvider `toml:"vision_providers"`
	Providers       map[string]ProviderConfig `toml:"providers"`
	Models          map[string]ModelConfig    `toml:"models"`
}

type ServerConfig struct {
	Listen string `toml:"listen"`
}

type CodexConfig struct {
	ModelCatalogPath string `toml:"model_catalog_path"`
	DefaultModel     string `toml:"default_model"`
	LocalToken       string `toml:"local_token"`
}

type ProviderConfig struct {
	Type     string `toml:"type"`
	BaseURL  string `toml:"base_url"`
	APIKey   string `toml:"api_key"`
	Profile  string `toml:"profile"`
	Protocol string `toml:"protocol"`
}

type ModelDiscoveryConfig struct {
	Enabled  bool   `toml:"enabled"`
	Mode     string `toml:"mode"`
	CacheTTL string `toml:"cache_ttl"`
}

type ExtensionsConfig struct {
	Network NetworkConfig `toml:"network"`
}

type NetworkConfig struct {
	ProxyURL string `toml:"proxy_url"`
}

type CapabilitiesConfig struct {
	Search SearchCapabilityConfig `toml:"search"`
	Vision VisionCapabilityConfig `toml:"vision"`
}

type SearchCapabilityConfig struct {
	Enabled    bool     `toml:"enabled"`
	Providers  []string `toml:"providers"`
	MaxResults int      `toml:"max_results"`
	ReadTopK   int      `toml:"read_top_k"`
}

type VisionCapabilityConfig struct {
	Enabled  bool   `toml:"enabled"`
	Provider string `toml:"provider"`
	Mode     string `toml:"mode"`
}

type SearchProvider struct {
	Type          string `toml:"type"`
	APIKey        string `toml:"api_key"`
	BaseURL       string `toml:"base_url"`
	SearchBaseURL string `toml:"search_base_url"`
	ReaderBaseURL string `toml:"reader_base_url"`
	ServerURL     string `toml:"server_url"`
	Authorization string `toml:"authorization"`
	SearchTool    string `toml:"search_tool"`
	ReadTool      string `toml:"read_tool"`
}

type VisionProvider struct {
	Type    string `toml:"type"`
	BaseURL string `toml:"base_url"`
	APIKey  string `toml:"api_key"`
	Model   string `toml:"model"`
}

type ModelConfig struct {
	DisplayName               string   `toml:"display_name"`
	Provider                  string   `toml:"provider"`
	Profile                   string   `toml:"profile"`
	UpstreamModel             string   `toml:"upstream_model"`
	ContextWindow             int64    `toml:"context_window"`
	SupportsParallelToolCalls bool     `toml:"supports_parallel_tool_calls"`
	ApplyPatchToolType        string   `toml:"apply_patch_tool_type"`
	InputModalities           []string `toml:"input_modalities"`
}

type ModelsResponse struct {
	Models []ModelInfo `json:"models"`
}

type ModelInfo struct {
	Slug                          string                  `json:"slug"`
	DisplayName                   string                  `json:"display_name"`
	Description                   string                  `json:"description"`
	DefaultReasoningLevel         string                  `json:"default_reasoning_level"`
	SupportedReasoningLevels      []ReasoningEffortPreset `json:"supported_reasoning_levels"`
	ShellType                     string                  `json:"shell_type"`
	Visibility                    string                  `json:"visibility"`
	SupportedInAPI                bool                    `json:"supported_in_api"`
	Priority                      int                     `json:"priority"`
	AdditionalSpeedTiers          []string                `json:"additional_speed_tiers"`
	ServiceTiers                  []map[string]any        `json:"service_tiers"`
	AvailabilityNux               any                     `json:"availability_nux"`
	Upgrade                       any                     `json:"upgrade"`
	BaseInstructions              string                  `json:"base_instructions"`
	ModelMessages                 any                     `json:"model_messages"`
	SupportsReasoningSummaries    bool                    `json:"supports_reasoning_summaries"`
	SupportVerbosity              bool                    `json:"support_verbosity"`
	DefaultVerbosity              any                     `json:"default_verbosity"`
	ApplyPatchToolType            string                  `json:"apply_patch_tool_type"`
	WebSearchToolType             string                  `json:"web_search_tool_type"`
	TruncationPolicy              TruncationPolicy        `json:"truncation_policy"`
	SupportsParallelToolCalls     bool                    `json:"supports_parallel_tool_calls"`
	SupportsImageDetailOriginal   bool                    `json:"supports_image_detail_original"`
	ContextWindow                 int64                   `json:"context_window"`
	MaxContextWindow              int64                   `json:"max_context_window"`
	AutoCompactTokenLimit         int64                   `json:"auto_compact_token_limit"`
	EffectiveContextWindowPercent int64                   `json:"effective_context_window_percent"`
	ExperimentalSupportedTools    []string                `json:"experimental_supported_tools"`
	InputModalities               []string                `json:"input_modalities"`
	SupportsSearchTool            bool                    `json:"supports_search_tool"`
	UseResponsesLite              bool                    `json:"use_responses_lite"`
}

type ReasoningEffortPreset struct {
	Effort      string `json:"effort"`
	Description string `json:"description"`
}

type TruncationPolicy struct {
	Mode  string `json:"mode"`
	Limit int64  `json:"limit"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := Config{Path: path}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.checkPermissions(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (cfg *Config) Validate() error {
	if cfg.Server.Listen == "" {
		return fmt.Errorf("server.listen is required")
	}
	if cfg.Codex.ModelCatalogPath == "" {
		return fmt.Errorf("codex.model_catalog_path is required")
	}
	if cfg.Codex.DefaultModel == "" {
		return fmt.Errorf("codex.default_model is required")
	}
	if cfg.Codex.LocalToken == "" {
		return fmt.Errorf("codex.local_token is required")
	}
	if len(cfg.Providers) == 0 {
		return fmt.Errorf("at least one provider is required")
	}
	if len(cfg.Models) == 0 {
		return fmt.Errorf("at least one model is required")
	}
	if _, ok := cfg.Models[cfg.Codex.DefaultModel]; !ok {
		return fmt.Errorf("codex.default_model %q is not configured", cfg.Codex.DefaultModel)
	}
	for name, provider := range cfg.Providers {
		if provider.Type != "openai_chat_compatible" && provider.Type != "openai_compatible" {
			return fmt.Errorf("providers.%s.type must be openai_compatible or openai_chat_compatible", name)
		}
		if provider.Protocol != "" {
			switch provider.Protocol {
			case "auto", "chat_completions", "responses":
			default:
				return fmt.Errorf("providers.%s.protocol must be auto, chat_completions, or responses", name)
			}
		}
		if provider.BaseURL == "" {
			return fmt.Errorf("providers.%s.base_url is required", name)
		}
		if provider.APIKey == "" {
			return fmt.Errorf("providers.%s.api_key is required", name)
		}
		if !adapters.Known(provider.Profile) {
			return fmt.Errorf("providers.%s.profile %q is not supported", name, provider.Profile)
		}
	}
	for slug, model := range cfg.Models {
		if model.DisplayName == "" {
			return fmt.Errorf("models.%s.display_name is required", slug)
		}
		if _, ok := cfg.Providers[model.Provider]; !ok {
			return fmt.Errorf("models.%s.provider %q is not configured", slug, model.Provider)
		}
		if model.UpstreamModel == "" {
			return fmt.Errorf("models.%s.upstream_model is required", slug)
		}
		if model.ContextWindow <= 0 {
			return fmt.Errorf("models.%s.context_window must be greater than 0", slug)
		}
		if model.ApplyPatchToolType != "freeform" {
			return fmt.Errorf("models.%s.apply_patch_tool_type must be freeform", slug)
		}
		if !adapters.Known(model.Profile) {
			return fmt.Errorf("models.%s.profile %q is not supported", slug, model.Profile)
		}
	}
	if err := cfg.validateCapabilities(); err != nil {
		return err
	}
	if err := cfg.validateExtensions(); err != nil {
		return err
	}
	return nil
}

func (cfg *Config) validateExtensions() error {
	proxyURL := strings.TrimSpace(cfg.Extensions.Network.ProxyURL)
	if proxyURL == "" {
		return nil
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return fmt.Errorf("extensions.network.proxy_url is invalid: %w", err)
	}
	switch parsed.Scheme {
	case "http", "https", "socks5", "socks5h":
		return nil
	default:
		return fmt.Errorf("extensions.network.proxy_url scheme must be http, https, socks5, or socks5h")
	}
}

func (cfg *Config) validateCapabilities() error {
	if cfg.ModelDiscovery.Mode == "" {
		cfg.ModelDiscovery.Mode = "config"
	}
	switch cfg.ModelDiscovery.Mode {
	case "config", "upstream", "merge":
	default:
		return fmt.Errorf("model_discovery.mode must be config, upstream, or merge")
	}
	if cfg.Capabilities.Search.Enabled {
		if len(cfg.Capabilities.Search.Providers) == 0 {
			return fmt.Errorf("capabilities.search.providers is required when search is enabled")
		}
		for _, providerName := range cfg.Capabilities.Search.Providers {
			provider, ok := cfg.SearchProviders[providerName]
			if !ok {
				return fmt.Errorf("search provider %q is not configured", providerName)
			}
			if provider.Type == "" {
				return fmt.Errorf("search_providers.%s.type is required", providerName)
			}
			switch provider.Type {
			case "jina", "searxng", "brave", "tavily", "serper", "duckduckgo_instant_answer", "firecrawl", "wikipedia", "semantic_scholar":
			case "mcp":
				if provider.ServerURL == "" {
					return fmt.Errorf("search_providers.%s.server_url is required for mcp", providerName)
				}
			default:
				return fmt.Errorf("search_providers.%s.type is not supported", providerName)
			}
		}
	}
	if cfg.Capabilities.Vision.Enabled {
		if cfg.Capabilities.Vision.Provider == "" {
			return fmt.Errorf("capabilities.vision.provider is required when vision is enabled")
		}
		provider, ok := cfg.VisionProviders[cfg.Capabilities.Vision.Provider]
		if !ok {
			return fmt.Errorf("vision provider %q is not configured", cfg.Capabilities.Vision.Provider)
		}
		if provider.Type != "openai_chat_compatible_vision" {
			return fmt.Errorf("vision_providers.%s.type must be openai_chat_compatible_vision", cfg.Capabilities.Vision.Provider)
		}
		if provider.BaseURL == "" {
			return fmt.Errorf("vision_providers.%s.base_url is required", cfg.Capabilities.Vision.Provider)
		}
		if provider.APIKey == "" {
			return fmt.Errorf("vision_providers.%s.api_key is required", cfg.Capabilities.Vision.Provider)
		}
		if provider.Model == "" {
			return fmt.Errorf("vision_providers.%s.model is required", cfg.Capabilities.Vision.Provider)
		}
	}
	return nil
}

func (cfg *Config) checkPermissions() error {
	info, err := os.Stat(cfg.Path)
	if err != nil {
		return fmt.Errorf("stat config: %w", err)
	}
	if configFileModeAllowed(info.Mode().Perm(), runtime.GOOS) {
		return nil
	}
	return fmt.Errorf("config file %s must have 0600 permissions", cfg.Path)
}

func configFileModeAllowed(mode os.FileMode, goos string) bool {
	return goos == "windows" || mode == 0o600
}

func (cfg *Config) Model(slug string) (ModelConfig, bool) {
	model, ok := cfg.Models[slug]
	return model, ok
}

func (cfg *Config) Provider(name string) (ProviderConfig, bool) {
	provider, ok := cfg.Providers[name]
	return provider, ok
}

func (cfg *Config) ProfileName(model ModelConfig, provider ProviderConfig) string {
	if strings.TrimSpace(model.Profile) != "" {
		return adapters.Normalize(model.Profile)
	}
	if strings.TrimSpace(provider.Profile) != "" {
		return adapters.Normalize(provider.Profile)
	}
	return adapters.DefaultName
}

func (cfg *Config) UpstreamProtocol(model ModelConfig, provider ProviderConfig) string {
	switch provider.Protocol {
	case "responses", "chat_completions":
		return provider.Protocol
	case "auto":
		if isOpenAINativeModel(model.UpstreamModel) {
			return "responses"
		}
	}
	if provider.Protocol == "" && provider.Type == "openai_compatible" && isOpenAINativeModel(model.UpstreamModel) {
		return "responses"
	}
	return "chat_completions"
}

func isOpenAINativeModel(model string) bool {
	value := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(value, "gpt-") || strings.HasPrefix(value, "o3") || strings.HasPrefix(value, "o4")
}

func (cfg *Config) BridgeBaseURL() string {
	return BridgeBaseURL(cfg.Server.Listen)
}

func BridgeBaseURL(listen string) string {
	listen = strings.TrimSpace(listen)
	if strings.HasPrefix(listen, "http://") || strings.HasPrefix(listen, "https://") {
		baseURL := strings.TrimRight(listen, "/")
		if strings.HasSuffix(baseURL, "/v1") {
			return baseURL
		}
		return baseURL + "/v1"
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		if strings.HasPrefix(listen, ":") {
			host = "127.0.0.1"
			port = strings.TrimPrefix(listen, ":")
		} else {
			host = "127.0.0.1"
			port = listen
		}
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/v1"
}

func (cfg *Config) Catalog() ModelsResponse {
	slugs := make([]string, 0, len(cfg.Models))
	for slug := range cfg.Models {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	models := make([]ModelInfo, 0, len(slugs))
	for _, slug := range slugs {
		model := cfg.Models[slug]
		provider := cfg.Providers[model.Provider]
		adapter := adapters.Get(cfg.ProfileName(model, provider))
		caps := adapter.Capabilities()
		inputModalities := model.InputModalities
		if len(inputModalities) == 0 {
			inputModalities = caps.InputModalities
		}
		inputModalities = adapters.NormalizeInputModalities(inputModalities)
		contextWindow := model.ContextWindow
		models = append(models, ModelInfo{
			Slug:                       slug,
			DisplayName:                model.DisplayName,
			Description:                model.DisplayName + " through Codex Bridge",
			DefaultReasoningLevel:      "medium",
			SupportedReasoningLevels:   reasoningLevelsForModel(model),
			ShellType:                  "shell_command",
			Visibility:                 "list",
			SupportedInAPI:             true,
			Priority:                   20,
			AdditionalSpeedTiers:       []string{},
			ServiceTiers:               []map[string]any{},
			AvailabilityNux:            nil,
			Upgrade:                    nil,
			BaseInstructions:           "",
			ModelMessages:              nil,
			SupportsReasoningSummaries: false,
			SupportVerbosity:           false,
			DefaultVerbosity:           nil,
			ApplyPatchToolType:         model.ApplyPatchToolType,
			WebSearchToolType:          "text",
			TruncationPolicy: TruncationPolicy{
				Mode:  "tokens",
				Limit: contextWindow * 95 / 100,
			},
			SupportsParallelToolCalls:     model.SupportsParallelToolCalls,
			SupportsImageDetailOriginal:   caps.SupportsImageDetailOriginal,
			ContextWindow:                 contextWindow,
			MaxContextWindow:              contextWindow,
			AutoCompactTokenLimit:         contextWindow * 90 / 100,
			EffectiveContextWindowPercent: 95,
			ExperimentalSupportedTools:    caps.ExperimentalSupportedTools,
			InputModalities:               inputModalities,
			SupportsSearchTool:            caps.SupportsSearchTool,
			UseResponsesLite:              false,
		})
	}
	return ModelsResponse{Models: models}
}

func reasoningLevelsForModel(model ModelConfig) []ReasoningEffortPreset {
	levels := []ReasoningEffortPreset{
		{Effort: "low", Description: "Fast responses with lighter reasoning"},
		{Effort: "medium", Description: "Balanced reasoning for coding tasks"},
		{Effort: "high", Description: "Deeper reasoning for complex changes"},
	}
	if isOpenAINativeModel(model.UpstreamModel) {
		levels = append(levels, ReasoningEffortPreset{Effort: "xhigh", Description: "Maximum reasoning for the hardest coding tasks"})
	}
	return levels
}

func (cfg *Config) WriteCatalog() error {
	catalog := cfg.Catalog()
	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal catalog: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Codex.ModelCatalogPath), 0o700); err != nil {
		return fmt.Errorf("create catalog dir: %w", err)
	}
	if err := os.WriteFile(cfg.Codex.ModelCatalogPath, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write catalog: %w", err)
	}
	return nil
}

func Redact(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return "***"
}
