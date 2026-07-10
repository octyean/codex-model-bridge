package diagnostics

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneSessionsRemovesOnlyExpiredSessionDirectories(t *testing.T) {
	root := t.TempDir()
	basePath := filepath.Join(root, "tool-calls.jsonl")
	oldDir := filepath.Join(root, "sessions", "old-session")
	newDir := filepath.Join(root, "sessions", "new-session")
	if err := os.MkdirAll(oldDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newDir, 0o700); err != nil {
		t.Fatal(err)
	}
	oldFile := filepath.Join(oldDir, "requests.jsonl")
	newFile := filepath.Join(newDir, "requests.jsonl")
	if err := os.WriteFile(oldFile, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newFile, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(oldFile, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(oldDir, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	result, err := PruneSessions(basePath, 14, 1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	if result.Deleted != 1 {
		t.Fatalf("deleted = %d, want 1", result.Deleted)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("old session still exists: %v", err)
	}
	if _, err := os.Stat(newDir); err != nil {
		t.Fatalf("new session was removed: %v", err)
	}
}
