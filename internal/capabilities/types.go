package capabilities

import (
	"context"
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
