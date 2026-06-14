package capabilities

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	base "codex-bridge/internal/capabilities"
)

type FirecrawlProvider struct {
	APIKey  string
	BaseURL string
	client  httpJSONClient
}

func NewFirecrawlProvider(apiKey string, baseURL string, client *http.Client) *FirecrawlProvider {
	return &FirecrawlProvider{
		APIKey:  apiKey,
		BaseURL: strings.TrimRight(defaultString(baseURL, "https://api.firecrawl.dev"), "/"),
		client:  newHTTPJSONClient(client),
	}
}

func (p *FirecrawlProvider) Search(ctx context.Context, query string, maxResults int) (base.SearchResult, error) {
	if p.APIKey == "" {
		return base.SearchResult{}, fmt.Errorf("firecrawl api_key is required")
	}
	count := maxResultsOrDefault(maxResults)
	body := map[string]any{"query": query, "limit": count}
	var resp struct {
		Data []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
		} `json:"data"`
	}
	if err := p.client.post(ctx, p.BaseURL+"/v1/search", map[string]string{"Authorization": "Bearer " + p.APIKey}, body, &resp); err != nil {
		return base.SearchResult{}, err
	}
	items := make([]base.SearchItem, 0, count)
	for _, item := range resp.Data {
		items = append(items, base.SearchItem{Title: item.Title, URL: item.URL, Snippet: item.Description})
	}
	return base.SearchResult{Query: query, Items: items, RawText: searchItemsText(items)}, nil
}

func (p *FirecrawlProvider) Read(ctx context.Context, targetURL string) (string, error) {
	if p.APIKey == "" {
		return "", fmt.Errorf("firecrawl api_key is required")
	}
	body := map[string]any{"url": targetURL, "formats": []string{"markdown"}}
	var resp struct {
		Data struct {
			Markdown string `json:"markdown"`
		} `json:"data"`
	}
	if err := p.client.post(ctx, p.BaseURL+"/v1/scrape", map[string]string{"Authorization": "Bearer " + p.APIKey}, body, &resp); err != nil {
		return "", err
	}
	return trimText(resp.Data.Markdown, 12000), nil
}
