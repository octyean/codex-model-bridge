package capabilities

import (
	"context"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	base "codex-bridge/internal/capabilities"
)

type DuckDuckGoHTMLProvider struct {
	client *http.Client
}

func NewDuckDuckGoHTMLProvider(client *http.Client) *DuckDuckGoHTMLProvider {
	return &DuckDuckGoHTMLProvider{client: client}
}

func (p *DuckDuckGoHTMLProvider) Search(ctx context.Context, query string, maxResults int) (base.SearchResult, error) {
	targetURL := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return base.SearchResult{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := p.client.Do(req)
	if err != nil {
		return base.SearchResult{}, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return base.SearchResult{}, httpStatusError("duckduckgo_html", resp.StatusCode, data)
	}
	items := parseDuckDuckGoHTML(string(data), maxResultsOrDefault(maxResults))
	return base.SearchResult{Query: query, Items: items, RawText: searchItemsText(items)}, nil
}

func (p *DuckDuckGoHTMLProvider) Read(_ context.Context, _ string) (string, error) {
	return "", nil
}

var duckResultPattern = regexp.MustCompile(`(?s)<a[^>]*class="result__a"[^>]*href="([^"]+)"[^>]*>(.*?)</a>.*?<a[^>]*class="result__snippet"[^>]*>(.*?)</a>`)

func parseDuckDuckGoHTML(body string, maxResults int) []base.SearchItem {
	matches := duckResultPattern.FindAllStringSubmatch(body, -1)
	items := make([]base.SearchItem, 0, maxResults)
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		item := base.SearchItem{
			Title:   cleanHTMLText(match[2]),
			URL:     duckResultURL(match[1]),
			Snippet: cleanHTMLText(match[3]),
		}
		if item.Title == "" || item.URL == "" {
			continue
		}
		items = append(items, item)
		if len(items) >= maxResults {
			break
		}
	}
	return items
}

var htmlTagPattern = regexp.MustCompile(`<[^>]+>`)

func cleanHTMLText(text string) string {
	text = htmlTagPattern.ReplaceAllString(text, "")
	return strings.Join(strings.Fields(html.UnescapeString(text)), " ")
}

func duckResultURL(raw string) string {
	raw = html.UnescapeString(raw)
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	parsed, err := url.Parse(raw)
	if err == nil {
		if target := parsed.Query().Get("uddg"); target != "" {
			return target
		}
	}
	return raw
}
