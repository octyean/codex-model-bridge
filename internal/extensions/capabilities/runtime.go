package capabilities

import (
	"net/http"

	base "codex-bridge/internal/capabilities"
	"codex-bridge/internal/config"
)

func NewRuntime(cfg *config.Config) base.Runtime {
	client, err := newHTTPClient(cfg.Extensions.Network.ProxyURL)
	if err != nil {
		client, _ = newHTTPClient("")
	}
	return base.Runtime{
		Search: newSearchProvider(cfg, client),
		Vision: newVisionProvider(cfg, client),
	}
}

func newSearchProvider(cfg *config.Config, client *http.Client) base.SearchProvider {
	search := cfg.Capabilities.Search
	if !search.Enabled {
		return nil
	}
	providers := make([]base.SearchProvider, 0, len(search.Providers))
	for _, name := range search.Providers {
		provider, ok := cfg.SearchProviders[name]
		if !ok {
			continue
		}
		searchProvider := NewSearchProvider(provider, client)
		if searchProvider != nil {
			providers = append(providers, searchProvider)
		}
	}
	switch len(providers) {
	case 0:
		return nil
	case 1:
		return providers[0]
	default:
		return base.MultiSearchProvider{Providers: providers}
	}
}

func newVisionProvider(cfg *config.Config, client *http.Client) base.VisionProvider {
	vision := cfg.Capabilities.Vision
	if !vision.Enabled {
		return nil
	}
	provider := cfg.VisionProviders[vision.Provider]
	if provider.Type != "openai_chat_compatible_vision" {
		return nil
	}
	return NewOpenAIVisionProvider(provider.BaseURL, provider.APIKey, provider.Model, client)
}
