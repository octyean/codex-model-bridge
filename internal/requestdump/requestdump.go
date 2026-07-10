package requestdump

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"codex-bridge/internal/diagnostics"
)

const EnvPath = "CODEX_BRIDGE_DUMP_UPSTREAM_REQUEST"

const (
	maxDumpAge   = 7 * 24 * time.Hour
	maxDumpBytes = 512 * 1024 * 1024
)

type dumpFile struct {
	path    string
	size    int64
	modTime time.Time
}

func ConfiguredPath() string {
	return strings.TrimSpace(os.Getenv(EnvPath))
}

func CheckConfiguredPath() (string, error) {
	path := ConfiguredPath()
	if path == "" {
		return "", nil
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return path, err
	}
	return path, nil
}

func Write(requestID string, model string, profile string, body any) (string, error) {
	dir := ConfiguredPath()
	if dir == "" {
		return "", nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := diagnostics.SafeName(time.Now().Format("20060102-150405.000") + "-" + requestID + "-" + model + "-" + profile + ".json.gz")
	path := filepath.Join(dir, name)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return path, err
	}
	gzipWriter := gzip.NewWriter(file)
	encoder := json.NewEncoder(gzipWriter)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(body); err != nil {
		_ = gzipWriter.Close()
		_ = file.Close()
		return path, err
	}
	if err := gzipWriter.Close(); err != nil {
		_ = file.Close()
		return path, err
	}
	if err := file.Close(); err != nil {
		return path, err
	}
	pruneDumpDir(dir)
	return path, nil
}

func Hash(body any) string {
	data, err := json.Marshal(body)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

func pruneDumpDir(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxDumpAge)
	files := make([]dumpFile, 0, len(entries))
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || !isDumpFile(entry.Name()) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(path)
			continue
		}
		size := info.Size()
		total += size
		files = append(files, dumpFile{path: path, size: size, modTime: info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.Before(files[j].modTime)
	})
	for total > maxDumpBytes && len(files) > 1 {
		oldest := files[0]
		files = files[1:]
		if err := os.Remove(oldest.path); err == nil {
			total -= oldest.size
		}
	}
}

func isDumpFile(name string) bool {
	return strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".json.gz")
}
