package capabilities

import (
	"fmt"
	"strings"

	base "codex-bridge/internal/capabilities"
)

func maxResultsOrDefault(maxResults int) int {
	if maxResults <= 0 {
		return 5
	}
	return maxResults
}

func searchItemsText(items []base.SearchItem) string {
	var out []string
	for i, item := range items {
		out = append(out, fmt.Sprintf("%d. %s\nURL: %s\nSnippet: %s", i+1, item.Title, item.URL, item.Snippet))
	}
	return trimText(strings.Join(out, "\n\n"), 12000)
}
