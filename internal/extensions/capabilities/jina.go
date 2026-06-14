package capabilities

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	base "codex-bridge/internal/capabilities"
)

type JinaSearchProvider struct {
	SearchBaseURL string
	ReaderBaseURL string
	APIKey        string
	client        *http.Client
}

func NewJinaSearchProvider(searchBaseURL string, readerBaseURL string, apiKey string, client *http.Client) *JinaSearchProvider {
	return &JinaSearchProvider{
		SearchBaseURL: strings.TrimRight(defaultString(searchBaseURL, "https://s.jina.ai"), "/"),
		ReaderBaseURL: strings.TrimRight(defaultString(readerBaseURL, "https://r.jina.ai"), "/"),
		APIKey:        apiKey,
		client:        client,
	}
}

func (p *JinaSearchProvider) Search(ctx context.Context, query string, maxResults int) (base.SearchResult, error) {
	body, err := p.getText(ctx, p.SearchBaseURL+"/"+url.PathEscape(query))
	if err != nil {
		return base.SearchResult{}, err
	}
	return base.SearchResult{Query: query, RawText: trimText(body, 12000), Items: parseJinaItems(body, maxResults)}, nil
}

func (p *JinaSearchProvider) Read(ctx context.Context, targetURL string) (string, error) {
	body, err := p.getText(ctx, p.ReaderBaseURL+"/"+targetURL)
	if err != nil {
		return "", err
	}
	return trimText(body, 12000), nil
}

func (p *JinaSearchProvider) getText(ctx context.Context, targetURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/plain")
	if strings.TrimSpace(p.APIKey) != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("jina status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return string(data), nil
}

func parseJinaItems(body string, maxResults int) []base.SearchItem {
	if maxResults <= 0 {
		maxResults = 5
	}
	lines := strings.Split(body, "\n")
	items := make([]base.SearchItem, 0, maxResults)
	var current *base.SearchItem
	for _, line := range lines {
		text := strings.TrimSpace(line)
		if text == "" {
			continue
		}
		if title, ok := parseJinaLine(text, "Title:"); ok {
			if current != nil && current.URL != "" {
				items = append(items, *current)
				if len(items) >= maxResults {
					return items
				}
			}
			current = &base.SearchItem{Title: title}
			continue
		}
		if source, ok := parseJinaLine(text, "URL Source:"); ok {
			if current == nil {
				current = &base.SearchItem{}
			}
			current.URL = source
			continue
		}
		if current != nil && current.Snippet == "" && !strings.HasPrefix(text, "Markdown Content:") {
			current.Snippet = text
		}
	}
	if current != nil && current.URL != "" && len(items) < maxResults {
		items = append(items, *current)
	}
	return items
}

func parseJinaLine(text string, prefix string) (string, bool) {
	if strings.HasPrefix(text, prefix) {
		return strings.TrimSpace(strings.TrimPrefix(text, prefix)), true
	}
	marker := "] " + prefix
	if idx := strings.Index(text, marker); idx >= 0 {
		return strings.TrimSpace(strings.TrimPrefix(text[idx+2:], prefix)), true
	}
	return "", false
}
