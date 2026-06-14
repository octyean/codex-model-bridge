package capabilities

import (
	"context"
	"net/http"
	"net/url"

	base "codex-bridge/internal/capabilities"
)

type DuckDuckGoInstantAnswerProvider struct {
	client httpJSONClient
}

func NewDuckDuckGoInstantAnswerProvider(client *http.Client) *DuckDuckGoInstantAnswerProvider {
	return &DuckDuckGoInstantAnswerProvider{client: newHTTPJSONClient(client)}
}

func (p *DuckDuckGoInstantAnswerProvider) Search(ctx context.Context, query string, maxResults int) (base.SearchResult, error) {
	targetURL := "https://api.duckduckgo.com/?q=" + url.QueryEscape(query) + "&format=json&no_html=1&skip_disambig=1"
	var resp struct {
		AbstractText  string `json:"AbstractText"`
		AbstractURL   string `json:"AbstractURL"`
		Heading       string `json:"Heading"`
		RelatedTopics []struct {
			Text     string `json:"Text"`
			FirstURL string `json:"FirstURL"`
		} `json:"RelatedTopics"`
	}
	if err := p.client.get(ctx, targetURL, nil, &resp); err != nil {
		return base.SearchResult{}, err
	}
	count := maxResultsOrDefault(maxResults)
	items := make([]base.SearchItem, 0, count)
	if resp.AbstractText != "" || resp.AbstractURL != "" {
		items = append(items, base.SearchItem{Title: resp.Heading, URL: resp.AbstractURL, Snippet: resp.AbstractText})
	}
	for _, item := range resp.RelatedTopics {
		if item.FirstURL == "" {
			continue
		}
		items = append(items, base.SearchItem{Title: item.Text, URL: item.FirstURL, Snippet: item.Text})
		if len(items) >= count {
			break
		}
	}
	return base.SearchResult{Query: query, Items: items, RawText: searchItemsText(items)}, nil
}

func (p *DuckDuckGoInstantAnswerProvider) Read(_ context.Context, _ string) (string, error) {
	return "", nil
}
