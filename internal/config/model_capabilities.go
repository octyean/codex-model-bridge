package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/upstreamprobe"
)

const (
	modelCapabilityCacheVersion = 3
	defaultCapabilityMaxAge     = 30 * 24 * time.Hour
)

type VerifiedModelCapability struct {
	Provider                    string            `json:"provider"`
	BaseURL                     string            `json:"base_url"`
	Model                       string            `json:"model"`
	CredentialFingerprint       string            `json:"credential_fingerprint"`
	ProfileFingerprint          string            `json:"profile_fingerprint"`
	ProbeVersion                int               `json:"probe_version"`
	VerifiedAt                  time.Time         `json:"verified_at"`
	ExpiresAt                   time.Time         `json:"expires_at"`
	RecommendedProtocol         string            `json:"recommended_protocol"`
	ResponsesStreamOK           bool              `json:"responses_stream_ok"`
	ResponsesToolsOK            bool              `json:"responses_tools_ok"`
	ResponsesToolStreamOK       bool              `json:"responses_tool_stream_ok"`
	ResponsesToolContinuationOK bool              `json:"responses_tool_continuation_ok"`
	ResponsesOptionsOK          bool              `json:"responses_options_ok"`
	ResponsesStructuredOutputOK bool              `json:"responses_structured_output_ok"`
	ChatStreamOK                bool              `json:"chat_stream_ok"`
	ChatToolsOK                 bool              `json:"chat_tools_ok"`
	ChatToolStreamOK            bool              `json:"chat_tool_stream_ok"`
	Failures                    map[string]string `json:"failures,omitempty"`
}

type modelCapabilityCache struct {
	Version int                                           `json:"version"`
	Models  map[string]map[string]VerifiedModelCapability `json:"models"`
}

func (cfg *Config) CapabilityCachePath() string {
	if path := strings.TrimSpace(cfg.Verification.CachePath); path != "" {
		return resolveStatePath(cfg.Path, path)
	}
	if strings.TrimSpace(cfg.Path) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(cfg.Path), "model-capabilities.json")
}

func (cfg *Config) CapabilityMaxAge() time.Duration {
	if cfg.Verification.MaxAgeHours > 0 {
		return time.Duration(cfg.Verification.MaxAgeHours) * time.Hour
	}
	return defaultCapabilityMaxAge
}

func (cfg *Config) VerifiedCapability(model ModelConfig, provider ProviderConfig) (VerifiedModelCapability, bool) {
	byModel := cfg.verifiedCapabilities.Models[model.Provider]
	capability, ok := byModel[model.UpstreamModel]
	if !ok {
		return VerifiedModelCapability{}, false
	}
	if strings.TrimRight(capability.BaseURL, "/") != strings.TrimRight(provider.BaseURL, "/") {
		return VerifiedModelCapability{}, false
	}
	if capability.CredentialFingerprint != providerCredentialFingerprint(provider) {
		return VerifiedModelCapability{}, false
	}
	if capability.ProfileFingerprint != cfg.providerProfileFingerprint(model.Provider, model.UpstreamModel, provider) {
		return VerifiedModelCapability{}, false
	}
	if capability.ProbeVersion != upstreamprobe.ProbeVersion {
		return VerifiedModelCapability{}, false
	}
	if capability.VerifiedAt.IsZero() || time.Since(capability.VerifiedAt) > cfg.CapabilityMaxAge() {
		return VerifiedModelCapability{}, false
	}
	return capability, true
}

func (cfg *Config) UpdateVerifiedCapability(providerName string, provider ProviderConfig, result upstreamprobe.Result) bool {
	if !result.Cacheable() {
		return false
	}
	if cfg.verifiedCapabilities.Models == nil {
		cfg.verifiedCapabilities = modelCapabilityCache{
			Version: modelCapabilityCacheVersion,
			Models:  map[string]map[string]VerifiedModelCapability{},
		}
	}
	if cfg.verifiedCapabilities.Models[providerName] == nil {
		cfg.verifiedCapabilities.Models[providerName] = map[string]VerifiedModelCapability{}
	}
	verifiedAt := time.Now().UTC()
	cfg.verifiedCapabilities.Models[providerName][result.ProbeModel] = VerifiedModelCapability{
		Provider:                    providerName,
		BaseURL:                     strings.TrimRight(provider.BaseURL, "/"),
		Model:                       result.ProbeModel,
		CredentialFingerprint:       providerCredentialFingerprint(provider),
		ProfileFingerprint:          cfg.providerProfileFingerprint(providerName, result.ProbeModel, provider),
		ProbeVersion:                upstreamprobe.ProbeVersion,
		VerifiedAt:                  verifiedAt,
		ExpiresAt:                   verifiedAt.Add(cfg.CapabilityMaxAge()),
		RecommendedProtocol:         result.RecommendedProtocol,
		ResponsesStreamOK:           result.ResponsesStreamOK,
		ResponsesToolsOK:            result.ResponsesToolsOK,
		ResponsesToolStreamOK:       result.ResponsesToolStreamOK,
		ResponsesToolContinuationOK: result.ResponsesToolContinuationOK,
		ResponsesOptionsOK:          result.ResponsesOptionsOK,
		ResponsesStructuredOutputOK: result.ResponsesStructuredOutputOK,
		ChatStreamOK:                result.ChatStreamOK,
		ChatToolsOK:                 result.ChatToolsOK,
		ChatToolStreamOK:            result.ChatToolStreamOK,
		Failures:                    result.Failures,
	}
	return true
}

func (cfg *Config) WriteCapabilityCache() error {
	path := cfg.CapabilityCachePath()
	if path == "" {
		return fmt.Errorf("capability cache path is unavailable")
	}
	return withStateFileLock(path, func() error {
		latest := modelCapabilityCache{}
		if err := readStateJSON(path, &latest); err != nil {
			return err
		}
		normalizeCapabilityCache(&latest)
		normalizeCapabilityCache(&cfg.verifiedCapabilities)
		for provider, models := range cfg.verifiedCapabilities.Models {
			if latest.Models[provider] == nil {
				latest.Models[provider] = map[string]VerifiedModelCapability{}
			}
			for model, capability := range models {
				latest.Models[provider][model] = capability
			}
		}
		if err := writeStateJSONUnlocked(path, latest); err != nil {
			return err
		}
		cfg.verifiedCapabilities = latest
		return nil
	})
}

func (cfg *Config) loadCapabilityCache() error {
	path := cfg.CapabilityCachePath()
	if path == "" {
		return nil
	}
	var cache modelCapabilityCache
	if err := readStateJSON(path, &cache); err != nil {
		return fmt.Errorf("load model capability cache: %w", err)
	}
	if cache.Version > modelCapabilityCacheVersion {
		return fmt.Errorf("load model capability cache: unsupported version %d", cache.Version)
	}
	normalizeCapabilityCache(&cache)
	cfg.verifiedCapabilities = cache
	return nil
}

func (cfg *Config) CapabilityWarnings(now time.Time) []string {
	var warnings []string
	seen := map[string]bool{}
	for _, model := range cfg.Models {
		provider, ok := cfg.Providers[model.Provider]
		if !ok {
			continue
		}
		profile := cfg.ProfileName(model, provider)
		if profile == adapters.DefaultName || profile == adapters.OpenAIName {
			continue
		}
		key := model.Provider + "\x00" + model.UpstreamModel
		if seen[key] {
			continue
		}
		seen[key] = true
		capability, ok := cfg.verifiedCapabilities.Models[model.Provider][model.UpstreamModel]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("%s/%s has no capability verification; run verify", model.Provider, model.UpstreamModel))
			continue
		}
		switch {
		case capability.ProbeVersion != upstreamprobe.ProbeVersion:
			warnings = append(warnings, fmt.Sprintf("%s/%s uses probe version %d; run verify again", model.Provider, model.UpstreamModel, capability.ProbeVersion))
		case capability.CredentialFingerprint != providerCredentialFingerprint(provider) ||
			capability.ProfileFingerprint != cfg.providerProfileFingerprint(model.Provider, model.UpstreamModel, provider):
			warnings = append(warnings, fmt.Sprintf("%s/%s provider credentials or profile changed; run verify again", model.Provider, model.UpstreamModel))
		case capability.VerifiedAt.IsZero() || now.Sub(capability.VerifiedAt) > cfg.CapabilityMaxAge():
			warnings = append(warnings, fmt.Sprintf("%s/%s capability verification expired; run verify again", model.Provider, model.UpstreamModel))
		}
	}
	sort.Strings(warnings)
	return warnings
}

func normalizeCapabilityCache(cache *modelCapabilityCache) {
	cache.Version = modelCapabilityCacheVersion
	if cache.Models == nil {
		cache.Models = map[string]map[string]VerifiedModelCapability{}
	}
}

func providerCredentialFingerprint(provider ProviderConfig) string {
	return cacheFingerprint("credential", strings.TrimSpace(provider.APIKey))
}

func (cfg *Config) providerProfileFingerprint(providerName string, upstreamModel string, provider ProviderConfig) string {
	profiles := map[string]struct{}{}
	for _, model := range cfg.Models {
		if model.Provider != providerName || model.UpstreamModel != upstreamModel {
			continue
		}
		profiles[cfg.ProfileName(model, provider)] = struct{}{}
	}
	if len(profiles) == 0 {
		profiles[adapters.Normalize(provider.Profile)] = struct{}{}
	}
	values := make([]string, 0, len(profiles)+3)
	values = append(values,
		"provider_type="+strings.TrimSpace(provider.Type),
		"protocol="+strings.TrimSpace(provider.Protocol),
	)
	for profile := range profiles {
		values = append(values, "profile="+profile)
	}
	sort.Strings(values)
	return cacheFingerprint(values...)
}

func cacheFingerprint(values ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return hex.EncodeToString(sum[:])
}

func resolveStatePath(configPath string, statePath string) string {
	if filepath.IsAbs(statePath) || strings.TrimSpace(configPath) == "" {
		return statePath
	}
	return filepath.Join(filepath.Dir(configPath), statePath)
}
