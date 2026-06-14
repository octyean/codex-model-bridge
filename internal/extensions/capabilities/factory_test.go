package capabilities

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	base "codex-bridge/internal/capabilities"
	"codex-bridge/internal/config"
)

func TestNewSearchProviderSupportsConfiguredTypes(t *testing.T) {
	client, err := newHTTPClient("")
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	cases := []config.SearchProvider{
		{Type: "jina"},
		{Type: "mcp", ServerURL: "https://mcp.example"},
		{Type: "searxng", BaseURL: "https://searxng.example"},
		{Type: "brave", APIKey: "key"},
		{Type: "tavily", APIKey: "key"},
		{Type: "serper", APIKey: "key"},
		{Type: "duckduckgo_instant_answer"},
		{Type: "firecrawl", APIKey: "key"},
		{Type: "wikipedia"},
		{Type: "semantic_scholar"},
	}
	for _, tc := range cases {
		if NewSearchProvider(tc, client) == nil {
			t.Fatalf("provider %s was nil", tc.Type)
		}
	}
}

func TestSearXNGSearchMapsJSONResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" || r.URL.Query().Get("format") != "json" {
			t.Fatalf("unexpected request: %s", r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"One","url":"https://one.example","content":"first"},{"title":"Two","url":"https://two.example","content":"second"}]}`))
	}))
	defer server.Close()

	client, err := newHTTPClient("")
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	provider := NewSearXNGProvider(server.URL, client)
	result, err := provider.Search(t.Context(), "codex bridge", 1)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %#v", result.Items)
	}
	if result.Items[0] != (base.SearchItem{Title: "One", URL: "https://one.example", Snippet: "first"}) {
		t.Fatalf("item = %#v", result.Items[0])
	}
	if !strings.Contains(result.RawText, "https://one.example") {
		t.Fatalf("raw text = %q", result.RawText)
	}
}

func TestParseJinaItemsHandlesNumberedLines(t *testing.T) {
	items := parseJinaItems(`[1] Title: GitHub - openai/codex
[1] URL Source: https://github.com/openai/codex
[1] Description: Lightweight coding agent
`, 2)
	if len(items) != 1 {
		t.Fatalf("items = %#v", items)
	}
	if items[0].Title != "GitHub - openai/codex" || items[0].URL != "https://github.com/openai/codex" {
		t.Fatalf("item = %#v", items[0])
	}
}
