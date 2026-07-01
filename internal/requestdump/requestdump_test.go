package requestdump

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteDisabledWithoutEnv(t *testing.T) {
	t.Setenv(EnvPath, "")

	path, err := Write("req", "model", "profile", map[string]any{"stream": true})
	if err != nil {
		t.Fatalf("write disabled: %v", err)
	}
	if path != "" {
		t.Fatalf("path = %q, want empty", path)
	}
}

func TestWriteCreatesPrivateJSONFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "requests")
	t.Setenv(EnvPath, dir)

	path, err := Write("req/1", "gpt:5", "kimi", map[string]any{"stream": true})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if !strings.HasPrefix(path, dir) {
		t.Fatalf("path = %q", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body["stream"] != true {
		t.Fatalf("body = %#v", body)
	}
}
