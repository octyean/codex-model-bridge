package config

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const modelDiscoveryStateVersion = 1

type DiscoveryReport struct {
	Provider    string            `json:"provider"`
	Discovered  int               `json:"discovered"`
	Added       int               `json:"added"`
	Assignments map[string]string `json:"assignments"`
	Skipped     []string          `json:"skipped,omitempty"`
}

type modelDiscoveryAssignments struct {
	Version   int                          `json:"version"`
	Providers map[string]map[string]string `json:"providers"`
}

func (cfg *Config) ModelDiscoveryStatePath() string {
	if path := strings.TrimSpace(cfg.ModelDiscovery.StatePath); path != "" {
		return resolveStatePath(cfg.Path, path)
	}
	if strings.TrimSpace(cfg.Path) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(cfg.Path), "model-slots.json")
}

func (cfg *Config) PrepareModelDiscovery() {
	if cfg.ModelDiscoveryMode() == "upstream" {
		cfg.Models = map[string]ModelConfig{}
	}
}

func (cfg *Config) AddDiscoveredModels(providerName string, ids []string) int {
	return cfg.AddDiscoveredModelsReport(providerName, ids).Added
}

func (cfg *Config) AddDiscoveredModelsReport(providerName string, ids []string) DiscoveryReport {
	report := DiscoveryReport{
		Provider:    providerName,
		Assignments: map[string]string{},
	}
	mode := cfg.ModelDiscoveryMode()
	if !cfg.ModelDiscovery.Enabled || mode == "config" || len(ids) == 0 {
		return report
	}
	if cfg.Models == nil {
		cfg.Models = map[string]ModelConfig{}
	}

	modelIDs := normalizedModelIDs(ids)
	report.Discovered = len(modelIDs)
	assignedModels := map[string]bool{}
	persisted := cfg.discoveryAssignments.Providers[providerName]
	for _, id := range modelIDs {
		slug := persisted[id]
		if !desktopVisibleModel(slug) {
			continue
		}
		if existing, ok := cfg.Models[slug]; ok {
			if existing.UpstreamModel != id || existing.Provider != providerName {
				continue
			}
			if mode == "merge" {
				report.Assignments[id] = slug
				assignedModels[id] = true
				continue
			}
		}
		cfg.Models[slug] = discoveredModel(providerName, cfg.Providers[providerName], id)
		report.Assignments[id] = slug
		assignedModels[id] = true
		report.Added++
	}

	slotIndex := 0
	for _, id := range modelIDs {
		if assignedModels[id] {
			continue
		}
		slug := DesktopModelSlug(id)
		if slug == "" || cfg.desktopSlotOccupiedByOtherModel(slug, id) {
			slug = ""
			for slotIndex < len(desktopModelSlots) {
				candidate := desktopModelSlots[slotIndex]
				slotIndex++
				if _, exists := cfg.Models[candidate]; !exists {
					slug = candidate
					break
				}
			}
		}
		if slug == "" || !desktopVisibleModel(slug) {
			report.Skipped = append(report.Skipped, id)
			continue
		}
		if mode == "merge" {
			if existing, exists := cfg.Models[slug]; exists {
				if existing.UpstreamModel == id && existing.Provider == providerName {
					report.Assignments[id] = slug
					assignedModels[id] = true
					continue
				}
				report.Skipped = append(report.Skipped, id)
				continue
			}
		}
		cfg.Models[slug] = discoveredModel(providerName, cfg.Providers[providerName], id)
		report.Assignments[id] = slug
		report.Added++
	}
	sort.Strings(report.Skipped)
	cfg.setDiscoveryAssignments(providerName, report.Assignments)
	cfg.ensureDefaultModel()
	return report
}

func (cfg *Config) WriteModelDiscoveryState() error {
	path := cfg.ModelDiscoveryStatePath()
	if path == "" {
		return fmt.Errorf("model discovery state path is unavailable")
	}
	return withStateFileLock(path, func() error {
		latest := modelDiscoveryAssignments{}
		if err := readStateJSON(path, &latest); err != nil {
			return err
		}
		normalizeDiscoveryState(&latest)
		normalizeDiscoveryState(&cfg.discoveryAssignments)
		for provider, assignments := range cfg.discoveryAssignments.Providers {
			copied := make(map[string]string, len(assignments))
			for model, slug := range assignments {
				copied[model] = slug
			}
			latest.Providers[provider] = copied
		}
		if err := writeStateJSONUnlocked(path, latest); err != nil {
			return err
		}
		cfg.discoveryAssignments = latest
		return nil
	})
}

func (cfg *Config) loadModelDiscoveryState() error {
	path := cfg.ModelDiscoveryStatePath()
	if path == "" {
		return nil
	}
	var state modelDiscoveryAssignments
	if err := readStateJSON(path, &state); err != nil {
		return fmt.Errorf("load model discovery state: %w", err)
	}
	if state.Version > modelDiscoveryStateVersion {
		return fmt.Errorf("load model discovery state: unsupported version %d", state.Version)
	}
	normalizeDiscoveryState(&state)
	cfg.discoveryAssignments = state
	return nil
}

func normalizeDiscoveryState(state *modelDiscoveryAssignments) {
	state.Version = modelDiscoveryStateVersion
	if state.Providers == nil {
		state.Providers = map[string]map[string]string{}
	}
}

func (cfg *Config) setDiscoveryAssignments(providerName string, assignments map[string]string) {
	if cfg.discoveryAssignments.Providers == nil {
		cfg.discoveryAssignments = modelDiscoveryAssignments{
			Version:   modelDiscoveryStateVersion,
			Providers: map[string]map[string]string{},
		}
	}
	copied := make(map[string]string, len(assignments))
	for model, slug := range assignments {
		copied[model] = slug
	}
	cfg.discoveryAssignments.Providers[providerName] = copied
}

func (cfg *Config) loadRuntimeState() error {
	if err := cfg.loadCapabilityCache(); err != nil {
		return err
	}
	return cfg.loadModelDiscoveryState()
}

func normalizedModelIDs(ids []string) []string {
	seen := map[string]bool{}
	modelIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		modelIDs = append(modelIDs, id)
	}
	sort.Slice(modelIDs, func(i, j int) bool {
		leftPriority := desktopModelPriority(modelIDs[i])
		rightPriority := desktopModelPriority(modelIDs[j])
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		return modelIDs[i] < modelIDs[j]
	})
	return modelIDs
}

func discoveredModel(providerName string, provider ProviderConfig, id string) ModelConfig {
	return ModelConfig{
		DisplayName:               id,
		Provider:                  providerName,
		Profile:                   provider.Profile,
		UpstreamModel:             id,
		ContextWindow:             DefaultContextWindowForModel(id),
		SupportsParallelToolCalls: true,
		ApplyPatchToolType:        "freeform",
	}
}
