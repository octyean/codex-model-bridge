package server

import (
	"sort"

	"codex-bridge/internal/providers"
)

func (s *Server) modelList() []providers.ModelInfo {
	slugs := make([]string, 0, len(s.cfg.Models))
	for slug := range s.cfg.Models {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	out := make([]providers.ModelInfo, 0, len(slugs))
	for _, slug := range slugs {
		model := s.cfg.Models[slug]
		out = append(out, providers.ModelInfo{
			ID:      slug,
			Object:  "model",
			Created: 0,
			OwnedBy: model.Provider,
		})
	}
	return out
}
