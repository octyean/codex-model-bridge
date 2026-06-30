package setup

import (
	"path/filepath"
	"testing"

	"codex-bridge/internal/config"
	"codex-bridge/internal/upstreamprobe"
)

func TestRunCreatesResponsesConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	result, err := Run(Options{
		ConfigPath:   path,
		CodexHome:    filepath.Join(dir, ".codex"),
		BaseURL:      "https://example.test/v1",
		APIKey:       "sk-test",
		DefaultModel: "kimi-for-coding",
	}, upstreamprobe.Result{
		Models:              []string{"kimi-for-coding"},
		ResponsesStreamOK:   true,
		ChatStreamOK:        true,
		RecommendedProtocol: "responses",
	})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if !result.Created || result.Protocol != "responses" || result.DefaultModel != "gpt-5.3-codex" {
		t.Fatalf("result = %#v", result)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	provider := cfg.Providers["upstream"]
	if provider.BaseURL != "https://example.test/v1" || provider.APIKey != "sk-test" || provider.Protocol != "responses" {
		t.Fatalf("provider = %#v", provider)
	}
	model := cfg.Models["gpt-5.3-codex"]
	if model.Profile != "kimi" || model.ContextWindow != 256000 {
		t.Fatalf("model = %#v", model)
	}
}

func TestRunPreservesExistingConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	probe := upstreamprobe.Result{Models: []string{"mimo-v2.5"}, ResponsesStreamOK: true, RecommendedProtocol: "responses"}
	if _, err := Run(Options{ConfigPath: path, CodexHome: filepath.Join(dir, ".codex"), BaseURL: "https://old.test/v1", APIKey: "sk-old"}, probe); err != nil {
		t.Fatalf("initial setup: %v", err)
	}
	result, err := Run(Options{ConfigPath: path, CodexHome: filepath.Join(dir, ".codex"), BaseURL: "https://new.test/v1", APIKey: "sk-new"}, probe)
	if err != nil {
		t.Fatalf("second setup: %v", err)
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
