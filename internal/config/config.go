package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
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
	Verification    VerificationConfig        `toml:"verification"`
	Diagnostics     DiagnosticsConfig         `toml:"diagnostics"`
	Extensions      ExtensionsConfig          `toml:"extensions"`
	Capabilities    CapabilitiesConfig        `toml:"capabilities"`
	SearchProviders map[string]SearchProvider `toml:"search_providers"`
	VisionProviders map[string]VisionProvider `toml:"vision_providers"`
	Providers       map[string]ProviderConfig `toml:"providers"`
	Models          map[string]ModelConfig    `toml:"models"`

	verifiedCapabilities modelCapabilityCache
	discoveryAssignments modelDiscoveryAssignments
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
	Enabled   bool   `toml:"enabled"`
	Mode      string `toml:"mode"`
	StatePath string `toml:"state_path,omitempty"`
}

type VerificationConfig struct {
	CachePath   string `toml:"cache_path,omitempty"`
	MaxAgeHours int    `toml:"max_age_hours,omitempty"`
}

type DiagnosticsConfig struct {
	RetentionDays int `toml:"retention_days,omitempty"`
	MaxTotalMB    int `toml:"max_total_mb,omitempty"`
}

func (cfg *Config) DiagnosticsRetentionDays() int {
	if cfg.Diagnostics.RetentionDays > 0 {
		return cfg.Diagnostics.RetentionDays
	}
	return 14
}

func (cfg *Config) DiagnosticsMaxTotalBytes() int64 {
	if cfg.Diagnostics.MaxTotalMB > 0 {
		return int64(cfg.Diagnostics.MaxTotalMB) * 1024 * 1024
	}
	return 1024 * 1024 * 1024
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
	DisplayName                       string   `toml:"display_name"`
	Provider                          string   `toml:"provider"`
	Profile                           string   `toml:"profile"`
	UpstreamModel                     string   `toml:"upstream_model"`
	ExecutionMode                     string   `toml:"execution_mode,omitempty"`
	SupportsResponsesOptions          *bool    `toml:"supports_responses_options,omitempty"`
	SupportsResponsesStructuredOutput *bool    `toml:"supports_responses_structured_output,omitempty"`
	DefaultReasoningLevel             string   `toml:"default_reasoning_level,omitempty"`
	SupportedReasoningLevels          []string `toml:"supported_reasoning_levels,omitempty"`
	ContextWindow                     int64    `toml:"context_window"`
	SupportsParallelToolCalls         bool     `toml:"supports_parallel_tool_calls"`
	ApplyPatchToolType                string   `toml:"apply_patch_tool_type"`
	InputModalities                   []string `toml:"input_modalities"`
}

type ModelsResponse struct {
	Models []ModelInfo `json:"models"`
}

type ModelInfo struct {
	Slug                          string                  `json:"slug"`
	DisplayName                   string                  `json:"display_name"`
	Description                   string                  `json:"description"`
	DefaultReasoningLevel         string                  `json:"default_reasoning_level,omitempty"`
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
	if err := cfg.loadRuntimeState(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func CheckUnknownFields(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&cfg); err != nil {
		var missing *toml.StrictMissingError
		if errors.As(err, &missing) {
			fields := make([]string, 0, len(missing.Errors))
			for i := range missing.Errors {
				fields = append(fields, strings.Join(missing.Errors[i].Key(), "."))
			}
			sort.Strings(fields)
			return fmt.Errorf("unknown config fields: %s", strings.Join(fields, ", "))
		}
		return fmt.Errorf("strict config check: %w", err)
	}
	return nil
}

func (cfg *Config) Validate() error {
	if cfg.Server.Listen == "" {
		return fmt.Errorf("server.listen is required")
	}
	if cfg.Codex.ModelCatalogPath == "" {
		return fmt.Errorf("codex.model_catalog_path is required")
	}
	if cfg.Codex.LocalToken == "" {
		return fmt.Errorf("codex.local_token is required")
	}
	if len(cfg.Providers) == 0 {
		return fmt.Errorf("at least one provider is required")
	}
	if err := cfg.validateCapabilities(); err != nil {
		return err
	}
	discoveryProvidesModels := cfg.ModelDiscovery.Enabled && cfg.ModelDiscoveryMode() != "config"
	if cfg.Codex.DefaultModel == "" && !discoveryProvidesModels {
		return fmt.Errorf("codex.default_model is required")
	}
	if len(cfg.Models) == 0 && !discoveryProvidesModels {
		return fmt.Errorf("at least one model is required")
	}
	if _, ok := cfg.Models[cfg.Codex.DefaultModel]; !ok && !discoveryProvidesModels {
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
		switch model.ExecutionMode {
		case "", ExecutionModeNativeResponses, ExecutionModeProjectedResponses, ExecutionModeChatCompletions:
		default:
			return fmt.Errorf("models.%s.execution_mode must be native_responses, projected_responses, or chat_completions", slug)
		}
		reasoningLevels := make(map[string]bool, len(model.SupportedReasoningLevels))
		for _, rawLevel := range model.SupportedReasoningLevels {
			level := normalizeReasoningLevel(rawLevel)
			if level == "" {
				return fmt.Errorf("models.%s.supported_reasoning_levels must not contain empty values", slug)
			}
			if reasoningLevels[level] {
				return fmt.Errorf("models.%s.supported_reasoning_levels contains duplicate %q", slug, level)
			}
			reasoningLevels[level] = true
		}
		defaultLevel := normalizeReasoningLevel(model.DefaultReasoningLevel)
		if model.DefaultReasoningLevel != "" && defaultLevel == "" {
			return fmt.Errorf("models.%s.default_reasoning_level must not be empty", slug)
		}
		if defaultLevel != "" && len(reasoningLevels) > 0 && !reasoningLevels[defaultLevel] {
			return fmt.Errorf("models.%s.default_reasoning_level %q is not in supported_reasoning_levels", slug, defaultLevel)
		}
	}
	if err := cfg.validateExtensions(); err != nil {
		return err
	}
	if cfg.Verification.MaxAgeHours < 0 {
		return fmt.Errorf("verification.max_age_hours must be zero or greater")
	}
	if cfg.Diagnostics.RetentionDays < 0 {
		return fmt.Errorf("diagnostics.retention_days must be zero or greater")
	}
	if cfg.Diagnostics.MaxTotalMB < 0 {
		return fmt.Errorf("diagnostics.max_total_mb must be zero or greater")
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
	cfg.ModelDiscovery.Mode = cfg.ModelDiscoveryMode()
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
			case "jina", "searxng", "brave", "tavily", "serper", "duckduckgo_instant_answer", "duckduckgo_html", "firecrawl", "wikipedia", "semantic_scholar":
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

func (cfg *Config) ModelDiscoveryMode() string {
	mode := strings.TrimSpace(cfg.ModelDiscovery.Mode)
	if mode == "" {
		return "config"
	}
	return mode
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

func DefaultContextWindowForModel(model string) int64 {
	value := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.Contains(value, "kimi"):
		return 256000
	case strings.Contains(value, "mimo"):
		return 1000000
	case strings.Contains(value, "deepseek"):
		return 64000
	default:
		return 64000
	}
}

var desktopModelSlots = []string{"gpt-5.3-codex", "gpt-5.2", "gpt-5.4-mini", "gpt-5.5", "gpt-5.4"}

func DesktopModelSlug(model string) string {
	value := strings.ToLower(strings.TrimSpace(model))
	if desktopVisibleModel(value) {
		return value
	}
	switch {
	case strings.Contains(value, "kimi"):
		return "gpt-5.3-codex"
	case strings.Contains(value, "mimo-v2.5-pro"):
		return "gpt-5.2"
	case strings.Contains(value, "mimo-v2.5"):
		return "gpt-5.4-mini"
	default:
		return ""
	}
}

func desktopModelPriority(model string) int {
	value := strings.ToLower(strings.TrimSpace(model))
	switch {
	case desktopVisibleModel(value):
		return 0
	case strings.Contains(value, "kimi"):
		return 1
	case strings.Contains(value, "mimo-v2.5-pro"):
		return 2
	case strings.Contains(value, "mimo-v2.5"):
		return 3
	default:
		return 4
	}
}

func desktopVisibleModel(slug string) bool {
	switch strings.TrimSpace(slug) {
	case "gpt-5.5", "gpt-5.4", "gpt-5.4-mini", "gpt-5.3-codex", "gpt-5.2":
		return true
	default:
		return false
	}
}

func (cfg *Config) desktopSlotOccupiedByOtherModel(slug string, upstreamModel string) bool {
	model, ok := cfg.Models[slug]
	return ok && model.UpstreamModel != upstreamModel
}

func (cfg *Config) ensureDefaultModel() {
	if cfg.Codex.DefaultModel != "" {
		if _, ok := cfg.Models[cfg.Codex.DefaultModel]; ok {
			return
		}
	}
	slugs := make([]string, 0, len(cfg.Models))
	for slug := range cfg.Models {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	if len(slugs) > 0 {
		cfg.Codex.DefaultModel = slugs[0]
	}
}

func (cfg *Config) ProfileName(model ModelConfig, provider ProviderConfig) string {
	modelProfile := adapters.Normalize(model.Profile)
	providerProfile := adapters.Normalize(provider.Profile)
	if isOpenAINativeModel(model.UpstreamModel) && modelProfile == adapters.DefaultName && providerProfile == adapters.DefaultName {
		return adapters.OpenAIName
	}
	if strings.TrimSpace(model.Profile) != "" {
		return modelProfile
	}
	if isOpenAINativeModel(model.UpstreamModel) && providerProfile == adapters.DefaultName {
		return adapters.OpenAIName
	}
	if strings.TrimSpace(provider.Profile) != "" {
		return providerProfile
	}
	return adapters.DefaultName
}

func (cfg *Config) UpstreamProtocol(model ModelConfig, provider ProviderConfig) string {
	switch model.ExecutionMode {
	case ExecutionModeNativeResponses, ExecutionModeProjectedResponses:
		return "responses"
	case ExecutionModeChatCompletions:
		return "chat_completions"
	}
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
		plan := cfg.ExecutionPlan(model, provider)
		adapter := adapters.Get(plan.Profile)
		caps := adapter.Capabilities()
		inputModalities := model.InputModalities
		if len(inputModalities) == 0 {
			inputModalities = caps.InputModalities
		}
		inputModalities = adapters.NormalizeInputModalities(inputModalities)
		contextWindow := model.ContextWindow
		supportsResponsesOptions := plan.SupportsResponsesOptions
		supportsSearchTool := cfg.Capabilities.Search.Enabled && caps.SupportsSearchTool
		models = append(models, ModelInfo{
			Slug:                       slug,
			DisplayName:                model.DisplayName,
			Description:                model.DisplayName + " through Codex Bridge",
			DefaultReasoningLevel:      defaultReasoningLevel(model, supportsResponsesOptions),
			SupportedReasoningLevels:   reasoningLevelsForModel(model, supportsResponsesOptions),
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
			SupportsReasoningSummaries: supportsResponsesOptions,
			SupportVerbosity:           supportsResponsesOptions,
			DefaultVerbosity:           defaultVerbosityForModel(supportsResponsesOptions),
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
			SupportsSearchTool:            supportsSearchTool,
			UseResponsesLite:              false,
		})
	}
	return ModelsResponse{Models: models}
}

func defaultReasoningLevel(model ModelConfig, enabled bool) string {
	if !enabled {
		return ""
	}
	if configured := normalizeReasoningLevel(model.DefaultReasoningLevel); configured != "" {
		return configured
	}
	for _, level := range model.SupportedReasoningLevels {
		if normalizeReasoningLevel(level) == "high" {
			return "high"
		}
	}
	for _, level := range model.SupportedReasoningLevels {
		if normalized := normalizeReasoningLevel(level); normalized != "" {
			return normalized
		}
	}
	return "high"
}

func reasoningLevelsForModel(model ModelConfig, enabled bool) []ReasoningEffortPreset {
	if !enabled {
		return []ReasoningEffortPreset{}
	}
	efforts := model.SupportedReasoningLevels
	if len(efforts) == 0 {
		efforts = []string{"low", "medium", "high"}
	}

	levels := make([]ReasoningEffortPreset, 0, len(efforts))
	seen := make(map[string]bool, len(efforts))
	for _, rawEffort := range efforts {
		effort := normalizeReasoningLevel(rawEffort)
		if effort == "" || seen[effort] {
			continue
		}
		seen[effort] = true
		levels = append(levels, ReasoningEffortPreset{
			Effort:      effort,
			Description: reasoningLevelDescription(effort),
		})
	}
	return levels
}

func normalizeReasoningLevel(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func reasoningLevelDescription(effort string) string {
	switch effort {
	case "low":
		return "Fast responses with lighter reasoning"
	case "medium":
		return "Balanced reasoning for coding tasks"
	case "high":
		return "Deeper reasoning for complex changes"
	case "xhigh":
		return "Maximum reasoning for the hardest coding tasks"
	case "max":
		return "Maximum provider-supported reasoning"
	default:
		return "Provider-defined reasoning level"
	}
}

func defaultVerbosityForModel(enabled bool) any {
	if enabled {
		return "medium"
	}
	return nil
}

func (cfg *Config) WriteCatalog() error {
	catalog := cfg.Catalog()
	if err := withStateFileLock(cfg.Codex.ModelCatalogPath, func() error {
		return writeStateJSONUnlocked(cfg.Codex.ModelCatalogPath, catalog)
	}); err != nil {
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
