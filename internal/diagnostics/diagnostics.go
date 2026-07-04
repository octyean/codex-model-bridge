package diagnostics

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

func CheckJSONL(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return path, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return path, err
	}
	return path, file.Close()
}

func WriteJSONL(path string, record map[string]any) {
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	data, err := json.Marshal(record)
	if err != nil {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.Write(append(data, '\n'))
}

func WriteSessionRecord(basePath string, sessionID string, fileName string, record map[string]any) {
	path := SessionLogPath(basePath, sessionID, fileName)
	if path == "" {
		return
	}
	WriteJSONL(path, record)
}

func WriteSessionIndex(basePath string, record map[string]any) {
	if basePath == "" {
		return
	}
	WriteJSONL(filepath.Join(filepath.Dir(basePath), "sessions", "index.jsonl"), record)
}

func SessionLogPath(basePath string, sessionID string, fileName string) string {
	if basePath == "" || strings.TrimSpace(sessionID) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(basePath), "sessions", SafeName(sessionID), fileName)
}

func SafeName(name string) string {
	var out []rune
	for _, r := range strings.TrimSpace(name) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r), r == '-', r == '_', r == '.':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return Hash(name)
	}
	if len(out) > 120 {
		return string(out[:100]) + "-" + Hash(name)
	}
	return string(out)
}

func Hash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:8])
}
