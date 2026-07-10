package diagnostics

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

const (
	SessionInlineMaxBytes = 16 * 1024
	DefaultPreviewRunes   = 1200
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
	return HashBytes([]byte(text))
}

func HashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

func CompactLargeFields(record map[string]any, maxBytes int, fields ...string) map[string]any {
	if record == nil {
		return nil
	}
	out := make(map[string]any, len(record)+len(fields))
	for key, value := range record {
		out[key] = value
	}
	for _, field := range fields {
		value, ok := out[field]
		if !ok || value == nil {
			continue
		}
		data, err := json.Marshal(value)
		if err != nil || len(data) <= maxBytes {
			continue
		}
		out[field+"_summary"] = summaryFromJSON(value, data, DefaultPreviewRunes)
		delete(out, field)
	}
	return out
}

func ValueSummary(value any, previewRunes int) map[string]any {
	data, err := json.Marshal(value)
	if err != nil {
		return map[string]any{
			"type": "unserializable",
		}
	}
	return summaryFromJSON(value, data, previewRunes)
}

func TextSummary(text string, previewRunes int) map[string]any {
	runes := []rune(text)
	out := map[string]any{
		"type":  "string",
		"chars": len(runes),
		"bytes": len([]byte(text)),
		"hash":  Hash(text),
	}
	if previewRunes > 0 {
		out["preview"] = previewRunesText(runes, previewRunes)
	}
	return out
}

func summaryFromJSON(value any, data []byte, previewRunes int) map[string]any {
	out := map[string]any{
		"type":  valueType(value),
		"bytes": len(data),
		"hash":  HashBytes(data),
	}
	switch v := value.(type) {
	case []any:
		out["items"] = len(v)
	case map[string]any:
		out["keys"] = sortedKeys(v, 20)
	}
	if previewRunes > 0 {
		out["preview"] = previewRunesText([]rune(string(data)), previewRunes)
	}
	return out
}

func valueType(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "bool"
	case float64, float32, int, int64, int32, uint, uint64, uint32:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "object"
	}
}

func sortedKeys(values map[string]any, limit int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if limit > 0 && len(keys) > limit {
		return keys[:limit]
	}
	return keys
}

func previewRunesText(runes []rune, limit int) string {
	if limit <= 0 || len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit])
}
