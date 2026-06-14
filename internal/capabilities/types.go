package capabilities

import (
	"context"

	"codex-bridge/internal/providers"
)

type SearchProvider interface {
	Search(ctx context.Context, query string, maxResults int) (SearchResult, error)
	Read(ctx context.Context, url string) (string, error)
}

type SearchResult struct {
	Query   string
	Items   []SearchItem
	RawText string
}

type SearchItem struct {
	Title   string
	URL     string
	Snippet string
}

type VisionProvider interface {
	Analyze(ctx context.Context, input ImageInput, mode string) (VisionResult, error)
}

type ImageInput struct {
	URL    string
	Detail string
}

type VisionResult struct {
	Text string
}

type Runtime struct {
	Search SearchProvider
	Vision VisionProvider
}

func (r Runtime) HasSearch() bool {
	return r.Search != nil
}

func (r Runtime) HasVision() bool {
	return r.Vision != nil
}

type StaticSearchProvider struct {
	Result SearchResult
}

func (p StaticSearchProvider) Search(_ context.Context, query string, _ int) (SearchResult, error) {
	result := p.Result
	result.Query = query
	return result, nil
}

func (p StaticSearchProvider) Read(_ context.Context, _ string) (string, error) {
	return p.Result.RawText, nil
}

type StaticVisionProvider struct {
	Result VisionResult
}

func (p StaticVisionProvider) Analyze(_ context.Context, _ ImageInput, _ string) (VisionResult, error) {
	return p.Result, nil
}

func VisionMessages(result VisionResult) []providers.ChatMessage {
	if result.Text == "" {
		return nil
	}
	return []providers.ChatMessage{{Role: "system", Content: "[image analysis]\n" + result.Text}}
}
