package toollog

import (
	"os"
	"strings"
	"testing"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
	"codex-bridge/internal/tools"
)

func TestToolLogDisabledWithoutEnv(t *testing.T) {
	logPath := t.TempDir() + "/tool-calls.jsonl"
	t.Setenv(EnvToolLogPath, "")

	PatchToolCall("req_test", "call_1", tools.Entry{
		Descriptor: adapters.ToolDescriptor{Name: "apply_patch", Kind: tools.KindPatch},
	}, `{"input":"*** Begin Patch\n*** End Patch"}`, codex.ResponseItem{"type": "custom_tool_call"})

	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("tool log should not be created when %s is unset", EnvToolLogPath)
	}
}

func TestToolLogCheckConfiguredPathCreatesFile(t *testing.T) {
	logPath := t.TempDir() + "/nested/tool-calls.jsonl"
	t.Setenv(EnvToolLogPath, logPath)

	got, err := CheckConfiguredPath()
	if err != nil {
		t.Fatalf("check configured path: %v", err)
	}
	if got != logPath {
		t.Fatalf("path = %q", got)
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("stat log path: %v", err)
	}
}

func TestToolLogWritesWhenEnvIsSet(t *testing.T) {
	logPath := t.TempDir() + "/tool-calls.jsonl"
	t.Setenv(EnvToolLogPath, logPath)
	entry := tools.Entry{
		Descriptor: adapters.ToolDescriptor{Name: "apply_patch", Kind: tools.KindPatch, OriginalType: "custom"},
	}

	PatchToolCall("req_test", "call_1", entry, `{"input":"*** Begin Patch\n*** End Patch"}`, codex.ResponseItem{"type": "custom_tool_call"})
	BlockedToolRewrite("req_test", "gpt-5.3-codex", "kimi", "call_2", entry, `{"cmd":"rm README.md"}`, `{"cmd":"printf blocked"}`)
	PatchToolOutput("call_3", adapters.ToolDescriptor{Name: "apply_patch", Kind: tools.KindPatch}, "Failed to find context", "Failed to find context\n\nAPPLY_PATCH_CONTEXT_MISMATCH")

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	text := string(data)
	for _, want := range []string{"req_test", "gpt-5.3-codex", "kimi", "tool_call_rewritten", "tool_output", "context_mismatch"} {
		if !strings.Contains(text, want) {
			t.Fatalf("log missing %q: %s", want, text)
		}
	}
}

func TestToolLogWritesTextEditorPatchCalls(t *testing.T) {
	logPath := t.TempDir() + "/tool-calls.jsonl"
	t.Setenv(EnvToolLogPath, logPath)
	entry := tools.Entry{
		Descriptor:   adapters.ToolDescriptor{Name: "apply_patch", Kind: tools.KindTextEditor, OriginalType: "custom"},
		UpstreamName: "codex_text_editor",
	}

	PatchToolCall("req_test", "call_1", entry, `{"command":"str_replace"}`, codex.ResponseItem{"type": "custom_tool_call", "name": "apply_patch"})
	PatchToolOutput("call_1", adapters.ToolDescriptor{Name: "codex_text_editor", Kind: tools.KindTextEditor}, "Success. Updated the following files:\nM a.java", "TEXT_EDITOR_EDIT_SUCCEEDED")

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	text := string(data)
	for _, want := range []string{"codex_text_editor", "text_editor_patch", "tool_output"} {
		if !strings.Contains(text, want) {
			t.Fatalf("log missing %q: %s", want, text)
		}
	}
}

func TestPatchToolOutputDeduplicatesReplayedOutputs(t *testing.T) {
	logPath := t.TempDir() + "/tool-calls.jsonl"
	t.Setenv(EnvToolLogPath, logPath)
	descriptor := adapters.ToolDescriptor{Name: "apply_patch", Kind: tools.KindPatch}

	PatchToolOutput("call_1", descriptor, "Failed to find context", "Failed to find context\n\nAPPLY_PATCH_CONTEXT_MISMATCH")
	PatchToolOutput("call_1", descriptor, "Failed to find context", "Failed to find context\n\nAPPLY_PATCH_CONTEXT_MISMATCH")

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if got := strings.Count(string(data), `"event":"tool_output"`); got != 1 {
		t.Fatalf("tool output log count = %d, log = %s", got, data)
	}
}
