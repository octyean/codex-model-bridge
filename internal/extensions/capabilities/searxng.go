package capabilities

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	base "codex-bridge/internal/capabilities"
)

type SearXNGProvider struct {
	BaseURL string
	client  httpJSONClient
}

func NewSearXNGProvider(baseURL string, client *http.Client) *SearXNGProvider {
	return &SearXNGProvider{
		BaseURL: strings.TrimRight(baseURL, "/"),
		client:  newHTTPJSONClient(client),
	}
}

func (p *SearXNGProvider) Search(ctx context.Context, query string, maxResults int) (base.SearchResult, error) {
	if p.BaseURL == "" {
		return base.SearchResult{}, fmt.Errorf("searxng base_url is required")
	}
	targetURL := p.BaseURL + "/search?q=" + url.QueryEscape(query) + "&format=json"
	var resp struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := p.client.get(ctx, targetURL, nil, &resp); err != nil {
		return base.SearchResult{}, err
	}
	items := make([]base.SearchItem, 0, maxResultsOrDefault(maxResults))
	for _, item := range resp.Results {
		if item.URL == "" {
			continue
		}
		items = append(items, base.SearchItem{Title: item.Title, URL: item.URL, Snippet: item.Content})
		if len(items) >= maxResultsOrDefault(maxResults) {
			break
		}
	}
	return base.SearchResult{Query: query, Items: items, RawText: searchItemsText(items)}, nil
}

func (p *SearXNGProvider) Read(_ context.Context, _ string) (string, error) {
	return "", fmt.Errorf("searxng does not provide reader")
}
