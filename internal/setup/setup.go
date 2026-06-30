package setup

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codex-bridge/internal/config"
	"codex-bridge/internal/upstreamprobe"

	"github.com/pelletier/go-toml/v2"
)

type Options struct {
	ConfigPath      string
	CodexHome       string
	BaseURL         string
	APIKey          string
	DefaultModel    string
	ReplaceUpstream bool
}

type Result struct {
	Created           bool
	Updated           bool
	ConfigPath        string
	ProviderName      string
	DefaultModel      string
	Protocol          string
	Models            []string
	ResponsesStream   bool
	ChatStream        bool
	ExistingPreserved bool
}

func Run(options Options, probe upstreamprobe.Result) (Result, error) {
	if strings.TrimSpace(options.ConfigPath) == "" {
		return Result{}, fmt.Errorf("config path is required")
	}
	if strings.TrimSpace(options.CodexHome) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Result{}, fmt.Errorf("resolve user home: %w", err)
		}
		options.CodexHome = filepath.Join(home, ".codex")
	}
	if err := os.MkdirAll(filepath.Dir(options.ConfigPath), 0o700); err != nil {
		return Result{}, fmt.Errorf("create config directory: %w", err)
	}
	if _, err := os.Stat(options.ConfigPath); err == nil && !options.ReplaceUpstream {
		cfg, loadErr := config.Load(options.ConfigPath)
		if loadErr != nil {
			return Result{}, loadErr
		}
		return Result{
			ConfigPath:        options.ConfigPath,
			DefaultModel:      cfg.Codex.DefaultModel,
			ExistingPreserved: true,
		}, nil
	} else if err != nil && !os.IsNotExist(err) {
		return Result{}, fmt.Errorf("stat config: %w", err)
	}
	cfg := buildConfig(options, probe)
	cfg.Path = options.ConfigPath
	data, err := toml.Marshal(cfg)
	if err != nil {
		return Result{}, fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(options.ConfigPath, data, 0o600); err != nil {
		return Result{}, fmt.Errorf("write config: %w", err)
	}
	models := append([]string(nil), probe.Models...)
	sort.Strings(models)
	return Result{
		Created:         true,
		Updated:         true,
		ConfigPath:      options.ConfigPath,
		ProviderName:    "upstream",
		DefaultModel:    cfg.Codex.DefaultModel,
		Protocol:        cfg.Providers["upstream"].Protocol,
		Models:          models,
		ResponsesStream: probe.ResponsesStreamOK,
		ChatStream:      probe.ChatStreamOK,
	}, nil
}

func buildConfig(options Options, probe upstreamprobe.Result) config.Config {
	modelIDs := probe.Models
	if len(modelIDs) == 0 {
		modelIDs = []string{firstNonEmpty(options.DefaultModel, "upstream-model")}
	}
	defaultModel := firstNonEmpty(options.DefaultModel, modelIDs[0])
	protocol := probe.RecommendedProtocol
	if protocol == "" {
		protocol = "chat_completions"
	}
	cfg := config.Config{
		Server: config.ServerConfig{Listen: "127.0.0.1:8787"},
		Codex: config.CodexConfig{
			ModelCatalogPath: filepath.ToSlash(filepath.Join(options.CodexHome, "models.codex-bridge.json")),
			DefaultModel:     defaultModel,
			LocalToken:       randomToken(),
		},
		ModelDiscovery: config.ModelDiscoveryConfig{Enabled: true, Mode: "merge"},
		Extensions:     config.ExtensionsConfig{},
		Capabilities: config.CapabilitiesConfig{
			Search: config.SearchCapabilityConfig{Enabled: false, Providers: []string{"jina"}, MaxResults: 5, ReadTopK: 3},
			Vision: config.VisionCapabilityConfig{Enabled: false, Provider: "jina_vlm", Mode: "describe"},
		},
		SearchProviders: map[string]config.SearchProvider{
			"jina": {Type: "jina", SearchBaseURL: "https://s.jina.ai", ReaderBaseURL: "https://r.jina.ai", APIKey: "jina_xxx"},
		},
		VisionProviders: map[string]config.VisionProvider{
			"jina_vlm": {Type: "openai_chat_compatible_vision", BaseURL: "https://api-beta-vlm.jina.ai/v1", APIKey: "jina_xxx", Model: "jina-vlm"},
		},
		Providers: map[string]config.ProviderConfig{
			"upstream": {
				Type:     "openai_compatible",
				BaseURL:  options.BaseURL,
				APIKey:   options.APIKey,
				Profile:  profileForModel(defaultModel),
				Protocol: protocol,
			},
		},
		Models: map[string]config.ModelConfig{},
	}
	for _, id := range modelIDs {
		slug := desktopSlug(id)
		if _, exists := cfg.Models[slug]; exists {
			continue
		}
		cfg.Models[slug] = config.ModelConfig{
			DisplayName:               id,
			Provider:                  "upstream",
			Profile:                   profileForModel(id),
			UpstreamModel:             id,
			ContextWindow:             contextWindowForModel(id),
			SupportsParallelToolCalls: true,
			ApplyPatchToolType:        "freeform",
			InputModalities:           inputModalitiesForModel(id),
		}
	}
	if _, ok := cfg.Models[defaultModel]; !ok {
		for slug, model := range cfg.Models {
			if model.UpstreamModel == defaultModel {
				cfg.Codex.DefaultModel = slug
				break
			}
		}
	}
	return cfg
}

func desktopSlug(model string) string {
	value := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.Contains(value, "kimi"):
		return "gpt-5.3-codex"
	case strings.Contains(value, "mimo-v2.5-pro"):
		return "gpt-5.2"
	case strings.Contains(value, "mimo-v2.5"):
		return "gpt-5.4-mini"
	default:
		return model
	}
}

func profileForModel(model string) string {
	value := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.Contains(value, "kimi"):
		return "kimi"
	case strings.Contains(value, "mimo"):
		return "mimo"
	case strings.Contains(value, "deepseek"):
		return "deepseek"
	default:
		return "default"
	}
}

func contextWindowForModel(model string) int64 {
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

func inputModalitiesForModel(model string) []string {
	value := strings.ToLower(strings.TrimSpace(model))
	if strings.Contains(value, "mimo") {
		return []string{"text", "image"}
	}
	return nil
}

func randomToken() string {
	var data [20]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "codex-bridge-local-token"
	}
	return hex.EncodeToString(data[:])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
