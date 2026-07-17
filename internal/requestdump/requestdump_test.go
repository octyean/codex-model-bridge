package requestdump

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteStoresGzipJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvPath, dir)

	path, err := Write("req_test", "model", "profile", map[string]any{"message": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, ".json.gz") {
		t.Fatalf("dump should be gzip: %s", path)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	var body map[string]any
	if err := json.NewDecoder(reader).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["message"] != "hello" {
		t.Fatalf("unexpected body: %#v", body)
	}
}

func TestMaybePruneDumpDirIsRateLimited(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "expired.json.gz")
	if err := os.WriteFile(path, []byte("expired"), 0o600); err != nil {
		t.Fatal(err)
	}
	expiredAt := time.Now().Add(-maxDumpAge - time.Hour)
	if err := os.Chtimes(path, expiredAt, expiredAt); err != nil {
		t.Fatal(err)
	}

	pruneMu.Lock()
	lastPruneDir = dir
	lastPruneAt = time.Now()
	pruneMu.Unlock()
	t.Cleanup(func() {
		pruneMu.Lock()
		lastPruneDir = ""
		lastPruneAt = time.Time{}
		pruneMu.Unlock()
	})

	maybePruneDumpDir(dir)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("rate-limited prune removed file: %v", err)
	}

	pruneMu.Lock()
	lastPruneAt = time.Time{}
	pruneMu.Unlock()
	maybePruneDumpDir(dir)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expired dump should be removed, stat error = %v", err)
	}
}
