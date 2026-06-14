package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfigTextLoads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(DefaultConfigText(dir)), 0o600); err != nil {
		t.Fatalf("write default config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load default config: %v", err)
	}
	if cfg.Codex.DefaultModel != "deepseek-v4-flash" {
		t.Fatalf("default model = %q", cfg.Codex.DefaultModel)
	}
	if _, ok := cfg.Models["deepseek-v4-flash"]; !ok {
		t.Fatalf("deepseek-v4-flash model missing")
	}
}
