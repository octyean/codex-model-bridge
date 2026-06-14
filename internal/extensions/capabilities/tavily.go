package capabilities

import (
	"context"
	"fmt"
	"net/http"

	base "codex-bridge/internal/capabilities"
)

type TavilyProvider struct {
	APIKey string
	client httpJSONClient
}

func NewTavilyProvider(apiKey string, client *http.Client) *TavilyProvider {
	return &TavilyProvider{APIKey: apiKey, client: newHTTPJSONClient(client)}
}

func (p *TavilyProvider) Search(ctx context.Context, query string, maxResults int) (base.SearchResult, error) {
	if p.APIKey == "" {
		return base.SearchResult{}, fmt.Errorf("tavily api_key is required")
	}
	count := maxResultsOrDefault(maxResults)
	body := map[string]any{
		"query":       query,
		"max_results": count,
	}
	var resp struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := p.client.post(ctx, "https://api.tavily.com/search", map[string]string{"Authorization": "Bearer " + p.APIKey}, body, &resp); err != nil {
		return base.SearchResult{}, err
	}
	items := make([]base.SearchItem, 0, count)
	for _, item := range resp.Results {
		items = append(items, base.SearchItem{Title: item.Title, URL: item.URL, Snippet: item.Content})
		if len(items) >= count {
			break
		}
	}
	return base.SearchResult{Query: query, Items: items, RawText: searchItemsText(items)}, nil
}

func (p *TavilyProvider) Read(_ context.Context, _ string) (string, error) {
	return "", fmt.Errorf("tavily does not provide reader")
}
