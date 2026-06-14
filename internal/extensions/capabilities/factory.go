package capabilities

import (
	"net/http"

	base "codex-bridge/internal/capabilities"
	"codex-bridge/internal/config"
)

func NewSearchProvider(provider config.SearchProvider, client *http.Client) base.SearchProvider {
	switch provider.Type {
	case "jina":
		return NewJinaSearchProvider(provider.SearchBaseURL, provider.ReaderBaseURL, provider.APIKey, client)
	case "mcp":
		return NewMCPProvider(provider.ServerURL, provider.Authorization, provider.SearchTool, provider.ReadTool, client)
	case "searxng":
		return NewSearXNGProvider(provider.BaseURL, client)
	case "brave":
		return NewBraveProvider(provider.APIKey, client)
	case "tavily":
		return NewTavilyProvider(provider.APIKey, client)
	case "serper":
		return NewSerperProvider(provider.APIKey, client)
	case "duckduckgo_instant_answer":
		return NewDuckDuckGoInstantAnswerProvider(client)
	case "firecrawl":
		return NewFirecrawlProvider(provider.APIKey, provider.BaseURL, client)
	case "wikipedia":
		return NewWikipediaProvider(provider.BaseURL, client)
	case "semantic_scholar":
		return NewSemanticScholarProvider(provider.APIKey, client)
	default:
		return nil
	}
}
