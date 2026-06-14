package main

import (
	"os"
	"path/filepath"
	"testing"

	"codex-bridge/internal/config"
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
