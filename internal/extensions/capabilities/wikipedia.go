package capabilities

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	base "codex-bridge/internal/capabilities"
)

type WikipediaProvider struct {
	BaseURL string
	client  httpJSONClient
}

func NewWikipediaProvider(baseURL string, client *http.Client) *WikipediaProvider {
	return &WikipediaProvider{
		BaseURL: strings.TrimRight(defaultString(baseURL, "https://en.wikipedia.org"), "/"),
		client:  newHTTPJSONClient(client),
	}
}

func (p *WikipediaProvider) Search(ctx context.Context, query string, maxResults int) (base.SearchResult, error) {
	count := maxResultsOrDefault(maxResults)
	targetURL := p.BaseURL + "/w/api.php?action=query&list=search&format=json&srlimit=" + fmt.Sprint(count) + "&srsearch=" + url.QueryEscape(query)
	var resp struct {
		Query struct {
			Search []struct {
				Title   string `json:"title"`
				Snippet string `json:"snippet"`
			} `json:"search"`
		} `json:"query"`
	}
	if err := p.client.get(ctx, targetURL, map[string]string{"User-Agent": "codex-bridge/0.2.5"}, &resp); err != nil {
		return base.SearchResult{}, err
	}
	items := make([]base.SearchItem, 0, count)
	for _, item := range resp.Query.Search {
		pageURL := p.BaseURL + "/wiki/" + url.PathEscape(strings.ReplaceAll(item.Title, " ", "_"))
		items = append(items, base.SearchItem{Title: item.Title, URL: pageURL, Snippet: item.Snippet})
	}
	return base.SearchResult{Query: query, Items: items, RawText: searchItemsText(items)}, nil
}

func (p *WikipediaProvider) Read(ctx context.Context, targetURL string) (string, error) {
	title := strings.TrimPrefix(targetURL, p.BaseURL+"/wiki/")
	title, _ = url.PathUnescape(title)
	if title == targetURL {
		return "", fmt.Errorf("wikipedia reader only accepts wikipedia page urls")
	}
	targetURL = p.BaseURL + "/api/rest_v1/page/summary/" + url.PathEscape(title)
	var resp struct {
		Title   string `json:"title"`
		Extract string `json:"extract"`
	}
	if err := p.client.get(ctx, targetURL, map[string]string{"User-Agent": "codex-bridge/0.2.5"}, &resp); err != nil {
		return "", err
	}
	return trimText(resp.Title+"\n"+resp.Extract, 12000), nil
}
