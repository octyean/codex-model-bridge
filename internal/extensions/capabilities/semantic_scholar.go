package capabilities

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	base "codex-bridge/internal/capabilities"
)

type SemanticScholarProvider struct {
	APIKey string
	client httpJSONClient
}

func NewSemanticScholarProvider(apiKey string, client *http.Client) *SemanticScholarProvider {
	return &SemanticScholarProvider{APIKey: apiKey, client: newHTTPJSONClient(client)}
}

func (p *SemanticScholarProvider) Search(ctx context.Context, query string, maxResults int) (base.SearchResult, error) {
	count := maxResultsOrDefault(maxResults)
	targetURL := "https://api.semanticscholar.org/graph/v1/paper/search?query=" + url.QueryEscape(query) + "&limit=" + fmt.Sprint(count) + "&fields=title,url,abstract"
	headers := map[string]string{}
	if p.APIKey != "" {
		headers["x-api-key"] = p.APIKey
	}
	var resp struct {
		Data []struct {
			Title    string `json:"title"`
			URL      string `json:"url"`
			Abstract string `json:"abstract"`
		} `json:"data"`
	}
	if err := p.client.get(ctx, targetURL, headers, &resp); err != nil {
		return base.SearchResult{}, err
	}
	items := make([]base.SearchItem, 0, count)
	for _, item := range resp.Data {
		items = append(items, base.SearchItem{Title: item.Title, URL: item.URL, Snippet: item.Abstract})
	}
	return base.SearchResult{Query: query, Items: items, RawText: searchItemsText(items)}, nil
}

func (p *SemanticScholarProvider) Read(_ context.Context, _ string) (string, error) {
	return "", fmt.Errorf("semantic_scholar does not provide reader")
}
