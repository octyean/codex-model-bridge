package server

import (
	"context"
	"sort"
	"strings"
	"time"

	"codex-bridge/internal/providers"
)

func (s *Server) modelList(ctx context.Context) []providers.ModelInfo {
	mode := strings.TrimSpace(s.cfg.ModelDiscovery.Mode)
	if mode == "" {
		mode = "config"
	}
	models := map[string]providers.ModelInfo{}
	if mode == "config" || mode == "merge" {
		for slug, model := range s.cfg.Models {
			models[slug] = providers.ModelInfo{
				ID:      slug,
				Object:  "model",
				Created: 0,
				OwnedBy: model.Provider,
			}
		}
	}
	if mode == "upstream" || mode == "merge" {
		for name, provider := range s.providers {
			resp, err := provider.ListModels(ctx)
			if err != nil {
				s.logger.Warn("model_discovery_failed", "provider", name, "error", err.Error())
				continue
			}
			for _, item := range resp.Data {
				if item.ID == "" {
					continue
				}
				if item.Object == "" {
					item.Object = "model"
				}
				if item.Created == 0 {
					item.Created = time.Now().Unix()
				}
				if item.OwnedBy == "" {
					item.OwnedBy = name
				}
				models[item.ID] = item
			}
		}
	}
	slugs := make([]string, 0, len(models))
	for slug := range models {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	out := make([]providers.ModelInfo, 0, len(slugs))
	for _, slug := range slugs {
		out = append(out, models[slug])
	}
	return out
}
