package requestdump

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"strings"
	"testing"
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
