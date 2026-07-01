package requestdump

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const EnvPath = "CODEX_BRIDGE_DUMP_UPSTREAM_REQUEST"

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
	data, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return "", err
	}
	name := safeName(time.Now().Format("20060102-150405.000") + "-" + requestID + "-" + model + "-" + profile + ".json")
	path := filepath.Join(dir, name)
	return path, os.WriteFile(path, data, 0o600)
}

func Hash(body any) string {
	data, err := json.Marshal(body)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

func safeName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	return b.String()
}
