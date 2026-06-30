package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex-bridge/internal/config"
	"codex-bridge/internal/providers"
	bridgesetup "codex-bridge/internal/setup"
	"codex-bridge/internal/upstreamprobe"
)

func TestEnsureDefaultConfigCreatesOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	created, err := ensureDefaultConfig(path)
	if err != nil {
		t.Fatalf("ensure default config: %v", err)
	}
	if !created {
		t.Fatalf("expected config to be created")
	}
	if _, err := config.Load(path); err != nil {
		t.Fatalf("load generated config: %v", err)
	}
	created, err = ensureDefaultConfig(path)
	if err != nil {
		t.Fatalf("ensure existing config: %v", err)
	}
	if created {
		t.Fatalf("existing config should not be recreated")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if info.Size() == 0 {
		t.Fatalf("config is empty")
	}
}

func TestAuthHelperForGoRunUsesStableSourcePath(t *testing.T) {
	dir := t.TempDir()
	command, args, timeout := authHelperFromPath(filepath.Join(dir, ".cache", "go-build", "abc", "codex-bridge"), dir, "config/config.toml")
	if command != "go" {
		t.Fatalf("command = %q", command)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, filepath.Join(dir, "cmd", "codex-bridge")) || !strings.Contains(joined, "auth token --config config/config.toml") {
		t.Fatalf("args = %#v", args)
	}
	if timeout != 30000 {
		t.Fatalf("timeout = %d", timeout)
	}
}

func TestAuthHelperForTmpGoRunUsesStableSourcePath(t *testing.T) {
	dir := t.TempDir()
	command, args, timeout := authHelperFromPath(filepath.Join(os.TempDir(), "go-build123", "b001", "exe", "codex-bridge"), dir, "/tmp/bridge.toml")
	if command != "go" {
		t.Fatalf("command = %q", command)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, filepath.Join(dir, "cmd", "codex-bridge")) || !strings.Contains(joined, "--config /tmp/bridge.toml") {
		t.Fatalf("args = %#v", args)
	}
	if timeout != 30000 {
		t.Fatalf("timeout = %d", timeout)
	}
}

type modelListProvider struct{}

func (modelListProvider) Create(context.Context, providers.ChatCompletionRequest) (*providers.ChatCompletionResponse, error) {
	return nil, nil
}

func (modelListProvider) Stream(context.Context, providers.ChatCompletionRequest) (<-chan providers.StreamEvent, error) {
	return nil, nil
}

func (modelListProvider) ListModels(context.Context) (*providers.ModelsResponse, error) {
	return &providers.ModelsResponse{Data: []providers.ModelInfo{{ID: "auto-model"}}}, nil
}

func TestDiscoverModelsAddsRoutableModel(t *testing.T) {
	cfg := &config.Config{
		ModelDiscovery: config.ModelDiscoveryConfig{Enabled: true, Mode: "upstream"},
		Providers: map[string]config.ProviderConfig{
			"fake": {Profile: "default"},
		},
	}
	discoverModels(context.Background(), cfg, map[string]providers.ChatProvider{"fake": modelListProvider{}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	model, ok := cfg.Models["gpt-5.3-codex"]
	if !ok {
		t.Fatalf("desktop slot missing")
	}
	if model.Provider != "fake" || model.UpstreamModel != "auto-model" {
		t.Fatalf("model = %#v", model)
	}
}

func TestConfigureCodexRequiresDefaultModel(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Path:  filepath.Join(dir, "bridge.toml"),
		Codex: config.CodexConfig{ModelCatalogPath: filepath.Join(dir, "models.json"), LocalToken: "token"},
	}
	if _, err := configureCodex(cfg, filepath.Join(dir, ".codex"), "", "Codex Bridge", "http://127.0.0.1:8787/v1"); err == nil {
		t.Fatalf("expected missing default model error")
	}
	cfg.Codex.DefaultModel = "auto-model"
	if _, err := configureCodex(cfg, filepath.Join(dir, ".codex"), "", "Codex Bridge", "http://127.0.0.1:8787/v1"); err != nil {
		t.Fatalf("configure codex: %v", err)
	}
}

func TestRunSetupPreservesExistingConfigWithoutUpstream(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if _, err := bridgesetup.Run(bridgesetup.Options{
		ConfigPath:   path,
		CodexHome:    filepath.Join(dir, ".codex"),
		BaseURL:      "https://old.test/v1",
		APIKey:       "sk-old",
		DefaultModel: "kimi-for-coding",
	}, upstreamprobe.Result{
		Models:              []string{"kimi-for-coding"},
		ResponsesStreamOK:   true,
		RecommendedProtocol: "responses",
	}); err != nil {
		t.Fatalf("initial setup: %v", err)
	}
	result, err := runSetup(path, filepath.Join(dir, ".codex"), "", "", "", false, true)
	if err != nil {
		t.Fatalf("preserve setup: %v", err)
	}
	if !result.ExistingPreserved {
		t.Fatalf("expected existing config to be preserved: %#v", result)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got := cfg.Providers["upstream"].BaseURL; got != "https://old.test/v1" {
		t.Fatalf("base url = %q", got)
	}
}
