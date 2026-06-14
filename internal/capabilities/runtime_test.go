package capabilities

import (
	"context"
	"fmt"
	"testing"
)

type failingSearchProvider struct{}

func (failingSearchProvider) Search(context.Context, string, int) (SearchResult, error) {
	return SearchResult{}, fmt.Errorf("failed")
}

func (failingSearchProvider) Read(context.Context, string) (string, error) {
	return "", fmt.Errorf("failed")
}

func TestMultiSearchProviderFallsBackOnError(t *testing.T) {
	provider := MultiSearchProvider{Providers: []SearchProvider{
		failingSearchProvider{},
		StaticSearchProvider{Result: SearchResult{RawText: "ok"}},
	}}
	result, err := provider.Search(t.Context(), "query", 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if result.RawText != "ok" {
		t.Fatalf("result = %#v", result)
	}
}
