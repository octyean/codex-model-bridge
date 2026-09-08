package setup

import (
	"path/filepath"
	"strings"
	"testing"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/config"
	"codex-bridge/internal/upstreamprobe"
)

func TestRunUsesExplicitProfileForUnknownModelName(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	result, err := Run(Options{
		ConfigPath:   configPath,
		CodexHome:    filepath.Join(dir, "codex"),
		BaseURL:      "https://example.test/v1",
		APIKey:       "secret",
		DefaultModel: "k3",
		Profile:      "KIMI",
	}, upstreamprobe.Result{
		Models:              []string{"k3"},
		ProbeModel:          "k3",
		RecommendedProtocol: "responses",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Profile != adapters.KimiName {
		t.Fatalf("result profile = %q, want kimi", result.Profile)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	provider := cfg.Providers["upstream"]
	model := cfg.Models[cfg.Codex.DefaultModel]
	if provider.Profile != adapters.KimiName || model.Profile != adapters.KimiName {
		t.Fatalf("provider profile = %q, model profile = %q", provider.Profile, model.Profile)
	}
	if model.ExecutionMode != config.ExecutionModeProjectedResponses {
		t.Fatalf("execution mode = %q, want projected_responses", model.ExecutionMode)
	}
	if cfg.Server.ShutdownTimeoutSeconds != 30 {
		t.Fatalf("shutdown timeout = %d, want 30", cfg.Server.ShutdownTimeoutSeconds)
	}
}

func TestRunRejectsUnknownExplicitProfile(t *testing.T) {
	_, err := Run(Options{
		ConfigPath: filepath.Join(t.TempDir(), "config.toml"),
		Profile:    "unknown-profile",
	}, upstreamprobe.Result{})
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestExecutionModeUsesExplicitProfileBeforeModelName(t *testing.T) {
	if got := executionModeForModel("gpt-internal-kimi", "responses", "kimi"); got != config.ExecutionModeProjectedResponses {
		t.Fatalf("kimi execution mode = %q, want projected_responses", got)
	}
	if got := executionModeForModel("internal-openai", "responses", "openai"); got != config.ExecutionModeNativeResponses {
		t.Fatalf("openai execution mode = %q, want native_responses", got)
	}
}
