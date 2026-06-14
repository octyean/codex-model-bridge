package capabilities

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	base "codex-bridge/internal/capabilities"
)

type BraveProvider struct {
	APIKey string
	client httpJSONClient
}

func NewBraveProvider(apiKey string, client *http.Client) *BraveProvider {
	return &BraveProvider{APIKey: apiKey, client: newHTTPJSONClient(client)}
}

func (p *BraveProvider) Search(ctx context.Context, query string, maxResults int) (base.SearchResult, error) {
	if p.APIKey == "" {
		return base.SearchResult{}, fmt.Errorf("brave api_key is required")
	}
	count := maxResultsOrDefault(maxResults)
	targetURL := "https://api.search.brave.com/res/v1/web/search?q=" + url.QueryEscape(query) + "&count=" + fmt.Sprint(count)
	var resp struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := p.client.get(ctx, targetURL, map[string]string{"X-Subscription-Token": p.APIKey}, &resp); err != nil {
		return base.SearchResult{}, err
	}
	items := make([]base.SearchItem, 0, count)
	for _, item := range resp.Web.Results {
		items = append(items, base.SearchItem{Title: item.Title, URL: item.URL, Snippet: item.Description})
		if len(items) >= count {
			break
		}
	}
	return base.SearchResult{Query: query, Items: items, RawText: searchItemsText(items)}, nil
}

func (p *BraveProvider) Read(_ context.Context, _ string) (string, error) {
	return "", fmt.Errorf("brave does not provide reader")
}
