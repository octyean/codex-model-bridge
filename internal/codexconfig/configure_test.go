package codexconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigureCreatesCodexConfig(t *testing.T) {
	dir := t.TempDir()
	result, err := Configure(Settings{
		CodexHome:           dir,
		ProviderName:        "codex_bridge",
		ProviderDisplayName: "Codex Bridge",
		BaseURL:             "http://127.0.0.1:8787/v1",
		ModelCatalogPath:    filepath.Join(dir, "models.codex-bridge.json"),
		DefaultModel:        "deepseek-v4-flash",
		AuthCommand:         "/usr/local/bin/codex-bridge",
		AuthConfigPath:      filepath.Join(dir, "bridge.toml"),
	})
	if err != nil {
		t.Fatalf("configure codex: %v", err)
	}
	data, err := os.ReadFile(result.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		`model_provider = "codex_bridge"`,
		`model = "deepseek-v4-flash"`,
		`model_catalog_json = "` + filepath.Join(dir, "models.codex-bridge.json") + `"`,
		`[model_providers.codex_bridge]`,
		`base_url = "http://127.0.0.1:8787/v1"`,
		`wire_api = "responses"`,
		`[model_providers.codex_bridge.auth]`,
		`command = "/usr/local/bin/codex-bridge"`,
		`args = ["auth", "token", "--config", "` + filepath.Join(dir, "bridge.toml") + `"]`,
		`refresh_interval_ms = 0`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("config missing %q:\n%s", want, text)
		}
	}
}

func TestConfigureUpdatesExistingCodexConfigAndBacksUp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	before := `model_provider = "openai"
model = "gpt-5"

[features]
unified_exec = true

[model_providers.codex_bridge]
name = "Old"
base_url = "http://127.0.0.1:1/v1"
wire_api = "chat"
experimental_bearer_token = "old-token"
`
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	result, err := Configure(Settings{
		CodexHome:           dir,
		ProviderName:        "codex_bridge",
		ProviderDisplayName: "Codex Bridge",
		BaseURL:             "http://127.0.0.1:8787/v1",
		ModelCatalogPath:    filepath.Join(dir, "models.codex-bridge.json"),
		DefaultModel:        "deepseek-v4-flash",
		AuthCommand:         "/usr/local/bin/codex-bridge",
		AuthConfigPath:      filepath.Join(dir, "bridge.toml"),
	})
	if err != nil {
		t.Fatalf("configure codex: %v", err)
	}
	if result.BackupPath == "" {
		t.Fatalf("expected backup path")
	}
	backup, err := os.ReadFile(result.BackupPath)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(backup) != before {
		t.Fatalf("backup = %q", string(backup))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		`model_provider = "openai"`,
		`model = "gpt-5"`,
		`model_catalog_json = "` + filepath.Join(dir, "models.codex-bridge.json") + `"`,
		`[features]`,
		`unified_exec = true`,
		`name = "Old"`,
		`base_url = "http://127.0.0.1:8787/v1"`,
		`wire_api = "responses"`,
		`[model_providers.codex_bridge.auth]`,
		`command = "/usr/local/bin/codex-bridge"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("config missing %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{
		`model_provider = "codex_bridge"`,
		`model = "deepseek-v4-flash"`,
	} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("config should preserve existing default, found %q:\n%s", unwanted, text)
		}
	}
}

func TestConfigureReusesExistingModelProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	before := `model_provider = "mcodex"
model = "gpt-5.5"

[model_providers.mcodex]
name = "mcodex"
base_url = "http://upstream.example/v1"
wire_api = "responses"
requires_openai_auth = true
`
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err := Configure(Settings{
		CodexHome:           dir,
		ProviderDisplayName: "mcodex",
		BaseURL:             "http://127.0.0.1:8787/v1",
		ModelCatalogPath:    filepath.Join(dir, "models.codex-bridge.json"),
		DefaultModel:        "deepseek-v4-flash",
		AuthCommand:         "/usr/local/bin/codex-bridge",
		AuthConfigPath:      filepath.Join(dir, "bridge.toml"),
	})
	if err != nil {
		t.Fatalf("configure codex: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		`model_provider = "mcodex"`,
		`model = "gpt-5.5"`,
		`[model_providers.mcodex]`,
		`name = "mcodex"`,
		`base_url = "http://127.0.0.1:8787/v1"`,
		`[model_providers.mcodex.auth]`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("config missing %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{
		`experimental_bearer_token`,
		`[model_providers.codex_bridge]`,
		`name = "Codex Bridge"`,
	} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("config should not contain %q:\n%s", unwanted, text)
		}
	}
}
