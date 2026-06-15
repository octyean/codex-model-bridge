package toollog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
	"codex-bridge/internal/tools"
)

const envToolLogPath = "CODEX_BRIDGE_TOOL_LOG"

func PatchToolCall(requestID string, callID string, entry tools.Entry, rawArguments string, item codex.ResponseItem) {
	if entry.Kind() != tools.KindPatch {
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

func BlockedToolRewrite(callID string, entry tools.Entry, rawArguments string, rewrittenArguments string) {
	appendRecord(map[string]any{
		"time":                time.Now().Format(time.RFC3339Nano),
		"event":               "tool_call_rewritten",
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
	if descriptor.Kind != tools.KindPatch {
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

func appendRecord(record map[string]any) {
	path := os.Getenv(envToolLogPath)
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
