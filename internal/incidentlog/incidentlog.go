package incidentlog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"codex-bridge/internal/diagnostics"
)

const EnvPath = "CODEX_BRIDGE_INCIDENT_LOG"

func ConfiguredPath() string {
	if path := strings.TrimSpace(os.Getenv(EnvPath)); path != "" {
		if path == "-" {
			return ""
		}
		return path
	}
	if path := strings.TrimSpace(os.Getenv("CODEX_BRIDGE_TOOL_LOG")); path != "" {
		return filepath.Join(filepath.Dir(path), "incidents.jsonl")
	}
	if dir := strings.TrimSpace(os.Getenv("CODEX_BRIDGE_DUMP_UPSTREAM_REQUEST")); dir != "" {
		return filepath.Join(filepath.Dir(dir), "incidents.jsonl")
	}
	return ""
}

func CheckConfiguredPath() (string, error) {
	return diagnostics.CheckJSONL(ConfiguredPath())
}

func Write(event string, record map[string]any) {
	path := ConfiguredPath()
	if path == "" {
		return
	}
	if record == nil {
		record = map[string]any{}
	}
	record["time"] = time.Now().Format(time.RFC3339Nano)
	record["event"] = event
	diagnostics.WriteJSONL(path, record)
	if sessionID := recordSessionID(record); sessionID != "" {
		diagnostics.WriteSessionRecord(path, sessionID, "incidents.jsonl", record)
	}
}

func Headers(headers http.Header) map[string]string {
	out := map[string]string{}
	for key, values := range headers {
		canonical := http.CanonicalHeaderKey(key)
		if !isTrackedHeader(canonical) || len(values) == 0 {
			continue
		}
		if canonical == "X-Codex-Turn-Metadata" {
			out[canonical] = codexTurnMetadata(strings.Join(values, ","))
			continue
		}
		out[canonical] = strings.Join(values, ",")
	}
	return out
}

func RequestSummary(raw map[string]any) map[string]any {
	summary := map[string]any{}
	if raw == nil {
		return summary
	}
	if value, ok := raw["previous_response_id"]; ok {
		summary["previous_response_id"] = value
	}
	if value, ok := raw["conversation"]; ok {
		summary["conversation"] = value
	}
	if value, ok := raw["thread_id"]; ok {
		summary["thread_id"] = value
	}
	if value, ok := raw["session_id"]; ok {
		summary["session_id"] = value
	}
	if metadata, ok := raw["metadata"].(map[string]any); ok {
		for _, key := range []string{"thread_id", "session_id", "conversation_id", "codex_thread_id"} {
			if value, ok := metadata[key]; ok {
				summary["metadata."+key] = value
			}
		}
	}
	if instructions, ok := raw["instructions"].(string); ok && strings.TrimSpace(instructions) != "" {
		summary["instructions"] = TextSummary(instructions)
	}
	if input, ok := raw["input"]; ok {
		summary["input"] = inputSummary(input)
	}
	return summary
}

func CodexSessionID(raw map[string]any, headers http.Header) string {
	for _, key := range []string{"X-Codex-Thread-Id", "X-Codex-Session-Id", "Openai-Conversation-Id", "Openai-Session-Id"} {
		if value := strings.TrimSpace(headers.Get(key)); value != "" {
			return value
		}
	}
	if value := sessionIDFromTurnMetadata(headers.Get("X-Codex-Turn-Metadata")); value != "" {
		return value
	}
	for _, key := range []string{"thread_id", "session_id", "conversation"} {
		if value := stringField(raw, key); value != "" {
			return value
		}
	}
	if metadata, ok := raw["metadata"].(map[string]any); ok {
		for _, key := range []string{"codex_thread_id", "thread_id", "session_id", "conversation_id"} {
			if value := stringField(metadata, key); value != "" {
				return value
			}
		}
	}
	return ""
}

func CodexWorkspace(raw map[string]any, headers http.Header) string {
	if value := workspaceFromRaw(raw); value != "" {
		return value
	}
	return workspaceFromTurnMetadata(headers.Get("X-Codex-Turn-Metadata"))
}

func workspaceFromRaw(raw map[string]any) string {
	if raw == nil {
		return ""
	}
	return workspaceFromValue(raw["input"])
}

func workspaceFromValue(value any) string {
	switch v := value.(type) {
	case string:
		return cwdFromText(v)
	case []any:
		for _, item := range v {
			if workspace := workspaceFromValue(item); workspace != "" {
				return workspace
			}
		}
	case map[string]any:
		for _, key := range []string{"content", "text"} {
			if workspace := workspaceFromValue(v[key]); workspace != "" {
				return workspace
			}
		}
	}
	return ""
}

func cwdFromText(text string) string {
	start := strings.Index(text, "<cwd>")
	if start < 0 {
		return ""
	}
	start += len("<cwd>")
	end := strings.Index(text[start:], "</cwd>")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(text[start : start+end])
}

func workspaceFromTurnMetadata(text string) string {
	var raw struct {
		Workspaces map[string]any `json:"workspaces"`
	}
	if err := json.Unmarshal([]byte(text), &raw); err != nil || len(raw.Workspaces) == 0 {
		return ""
	}
	paths := make([]string, 0, len(raw.Workspaces))
	for path := range raw.Workspaces {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths[0]
}

func TextSummary(text string) map[string]any {
	return map[string]any{
		"chars":   len([]rune(text)),
		"hash":    Hash(text),
		"preview": Preview(text, 500),
	}
}

func Preview(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}

func Hash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:8])
}

func isTrackedHeader(key string) bool {
	switch {
	case key == "Openai-Conversation-Id", key == "Openai-Session-Id", key == "X-Request-Id", key == "User-Agent", key == "X-Codex-Thread-Id", key == "X-Codex-Session-Id", key == "X-Codex-Turn-Id", key == "X-Codex-Window-Id", key == "X-Codex-Turn-Metadata":
		return true
	default:
		return false
	}
}

func codexTurnMetadata(text string) string {
	var raw map[string]any
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return Preview(text, 500)
	}
	keep := map[string]any{}
	for _, key := range []string{"session_id", "thread_id", "turn_id", "window_id", "request_kind", "thread_source", "sandbox", "workspace_kind", "turn_started_at_unix_ms"} {
		if value, ok := raw[key]; ok {
			keep[key] = value
		}
	}
	data, err := json.Marshal(keep)
	if err != nil {
		return Preview(text, 500)
	}
	return string(data)
}

func sessionIDFromTurnMetadata(text string) string {
	var raw map[string]any
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return ""
	}
	for _, key := range []string{"thread_id", "session_id"} {
		if value := stringField(raw, key); value != "" {
			return value
		}
	}
	return ""
}

func stringField(raw map[string]any, key string) string {
	value, ok := raw[key]
	if !ok {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return strings.TrimSpace(toString(v))
	}
}

func toString(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

func WriteSessionRecord(basePath string, sessionID string, fileName string, record map[string]any) {
	diagnostics.WriteSessionRecord(basePath, sessionID, fileName, record)
}

func WriteSessionIndex(basePath string, record map[string]any) {
	diagnostics.WriteSessionIndex(basePath, record)
}

func SessionLogPath(basePath string, sessionID string, fileName string) string {
	return diagnostics.SessionLogPath(basePath, sessionID, fileName)
}

func SafeSessionID(sessionID string) string {
	return diagnostics.SafeName(sessionID)
}

func recordSessionID(record map[string]any) string {
	for _, key := range []string{"codex_session_id", "session_id"} {
		if value := stringField(record, key); value != "" {
			return value
		}
	}
	return ""
}

func inputSummary(input any) map[string]any {
	out := map[string]any{}
	switch value := input.(type) {
	case string:
		out["type"] = "string"
		out["text"] = TextSummary(value)
	case []any:
		out["type"] = "array"
		out["item_count"] = len(value)
		if text := lastUserText(value); text != "" {
			out["last_user_text"] = TextSummary(text)
		}
	default:
		out["type"] = "object"
	}
	return out
}

func lastUserText(items []any) string {
	for i := len(items) - 1; i >= 0; i-- {
		item, ok := items[i].(map[string]any)
		if !ok || item["role"] != "user" {
			continue
		}
		return contentText(item["content"])
	}
	return ""
}

func contentText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		var parts []string
		for _, part := range value {
			partMap, ok := part.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := partMap["text"].(string); ok {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}
