package capabilities

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type MultiSearchProvider struct {
	Providers []SearchProvider
}

func (p MultiSearchProvider) Search(ctx context.Context, query string, maxResults int) (SearchResult, error) {
	var failures []string
	for _, provider := range p.Providers {
		result, err := provider.Search(ctx, query, maxResults)
		if err == nil && (result.RawText != "" || len(result.Items) > 0) {
			return result, nil
		}
		failures = append(failures, searchFailure(err, "empty result"))
	}
	return SearchResult{}, fmt.Errorf("all search providers failed: %s", strings.Join(failures, "; "))
}

func (p MultiSearchProvider) Read(ctx context.Context, targetURL string) (string, error) {
	var failures []string
	for _, provider := range p.Providers {
		text, err := provider.Read(ctx, targetURL)
		if err == nil && strings.TrimSpace(text) != "" {
			return text, nil
		}
		failures = append(failures, searchFailure(err, "empty result"))
	}
	return "", fmt.Errorf("all reader providers failed: %s", strings.Join(failures, "; "))
}

func searchFailure(err error, fallback string) string {
	if err == nil {
		err = errors.New(fallback)
	}
	return err.Error()
}
