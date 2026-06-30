package toollog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
	"codex-bridge/internal/tools"
)

const EnvToolLogPath = "CODEX_BRIDGE_TOOL_LOG"

var seenPatchToolOutputs sync.Map

func PatchToolCall(requestID string, callID string, entry tools.Entry, rawArguments string, item codex.ResponseItem) {
	if !isPatchWriteKind(entry.Kind()) {
		return
	}
	appendRecord(map[string]any{
		"time":          time.Now().Format(time.RFC3339Nano),
		"request_id":    requestID,
		"call_id":       callID,
		"tool":          entry.Name(),
		"kind":          entry.Kind(),
		"original_type": entry.OriginalType(),
		"raw_arguments": rawArguments,
		"item":          item,
	})
}

func ConfiguredPath() string {
	return strings.TrimSpace(os.Getenv(EnvToolLogPath))
}

func CheckConfiguredPath() (string, error) {
	path := ConfiguredPath()
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

func BlockedToolRewrite(requestID string, model string, profile string, callID string, entry tools.Entry, rawArguments string, rewrittenArguments string) {
	appendRecord(map[string]any{
		"time":                time.Now().Format(time.RFC3339Nano),
		"event":               "tool_call_rewritten",
		"request_id":          requestID,
		"model":               model,
		"profile":             profile,
		"call_id":             callID,
		"tool":                entry.Name(),
		"kind":                entry.Kind(),
		"original_type":       entry.OriginalType(),
		"raw_arguments":       rawArguments,
		"rewritten_arguments": rewrittenArguments,
		"reason":              "shell_file_mutation_blocked",
	})
}

func PatchToolOutput(callID string, descriptor adapters.ToolDescriptor, rawOutput string, formattedOutput string) {
	if !isPatchWriteKind(descriptor.Kind) {
		return
	}
	if seenToolOutput(callID, rawOutput) {
		return
	}
	appendRecord(map[string]any{
		"time":             time.Now().Format(time.RFC3339Nano),
		"event":            "tool_output",
		"call_id":          callID,
		"tool":             descriptor.Name,
		"kind":             descriptor.Kind,
		"original_type":    descriptor.OriginalType,
		"failure_kind":     adapters.ClassifyPatchFailure(rawOutput),
		"raw_output":       rawOutput,
		"formatted_output": formattedOutput,
	})
}

func isPatchWriteKind(kind string) bool {
	return kind == tools.KindPatch || kind == tools.KindTextEditor
}

func seenToolOutput(callID string, rawOutput string) bool {
	key := callID + ":" + outputHash(rawOutput)
	if _, loaded := seenPatchToolOutputs.LoadOrStore(key, true); loaded {
		return true
	}
	return false
}

func outputHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func appendRecord(record map[string]any) {
	path := ConfiguredPath()
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
