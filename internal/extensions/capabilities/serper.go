package capabilities

import (
	"context"
	"fmt"
	"net/http"

	base "codex-bridge/internal/capabilities"
)

type SerperProvider struct {
	APIKey string
	client httpJSONClient
}

func NewSerperProvider(apiKey string, client *http.Client) *SerperProvider {
	return &SerperProvider{APIKey: apiKey, client: newHTTPJSONClient(client)}
}

func (p *SerperProvider) Search(ctx context.Context, query string, maxResults int) (base.SearchResult, error) {
	if p.APIKey == "" {
		return base.SearchResult{}, fmt.Errorf("serper api_key is required")
	}
	count := maxResultsOrDefault(maxResults)
	body := map[string]any{"q": query, "num": count}
	var resp struct {
		Organic []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
		} `json:"organic"`
	}
	if err := p.client.post(ctx, "https://google.serper.dev/search", map[string]string{"X-API-KEY": p.APIKey}, body, &resp); err != nil {
		return base.SearchResult{}, err
	}
	items := make([]base.SearchItem, 0, count)
	for _, item := range resp.Organic {
		items = append(items, base.SearchItem{Title: item.Title, URL: item.Link, Snippet: item.Snippet})
		if len(items) >= count {
			break
		}
	}
	return base.SearchResult{Query: query, Items: items, RawText: searchItemsText(items)}, nil
}

func (p *SerperProvider) Read(_ context.Context, _ string) (string, error) {
	return "", fmt.Errorf("serper does not provide reader")
}
