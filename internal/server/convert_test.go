package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
	"codex-bridge/internal/providers"
	"codex-bridge/internal/tools"
)

func TestResponseItemsFromApplyPatchToolCall(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	adapter := adapters.Get(adapters.OpenAIName)
	_, toolCtx := tools.FromCodex([]codex.ResponseTool{{Type: "custom", Name: "apply_patch"}}, adapter)
	items := responseItemsFromMessage(providers.ChatMessage{
		ToolCalls: []providers.ChatToolCall{{
			ID: "call_1", Type: "function",
			Function: providers.ChatCallFunction{Name: "apply_patch", Arguments: `{"input":"*** Begin Patch\n*** End Patch\n"}`},
		}},
	}, toolCtx, adapter, "req_test", "", "", logger)
	if len(items) != 1 {
		t.Fatalf("items len = %d", len(items))
	}
	if items[0]["type"] != "custom_tool_call" {
		t.Fatalf("item type = %v", items[0]["type"])
	}
	if items[0]["input"] != "*** Begin Patch\n*** End Patch" {
		t.Fatalf("input = %q", items[0]["input"])
	}
}

func TestResponseItemsConvertsDeepSeekTextEditorToApplyPatchCall(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	adapter := adapters.Get(adapters.DeepSeekName)
	chatTools, toolCtx := tools.FromCodex([]codex.ResponseTool{{Type: "custom", Name: "apply_patch"}}, adapter)
	if len(chatTools) != 1 || chatTools[0].Function.Name != "codex_text_editor" {
		t.Fatalf("chat tools = %#v", chatTools)
	}
	items := responseItemsFromMessage(providers.ChatMessage{
		ToolCalls: []providers.ChatToolCall{{
			ID:   "call_1",
			Type: "function",
			Function: providers.ChatCallFunction{
				Name:      "codex_text_editor",
				Arguments: `{"command":"str_replace","path":"a.java","old_str":"old","new_str":"new"}`,
			},
		}},
	}, toolCtx, adapter, "req_test", "", "", logger)
	if len(items) != 1 {
		t.Fatalf("items len = %d", len(items))
	}
	if items[0]["type"] != "custom_tool_call" || items[0]["name"] != "apply_patch" {
		t.Fatalf("item = %#v", items[0])
	}
	input, _ := items[0]["input"].(string)
	for _, want := range []string{"*** Begin Patch", "*** Update File: a.java", "-old", "+new", "*** End Patch"} {
		if !strings.Contains(input, want) {
			t.Fatalf("input missing %q: %s", want, input)
		}
	}
}

func TestResponseItemsTurnsAlreadyAppliedTextEditorIntoExecCommand(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/a.java"
	if err := os.WriteFile(path, []byte("old done\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	arguments, _ := json.Marshal(map[string]string{
		"command": "str_replace",
		"path":    path,
		"old_str": "old",
		"new_str": "old done",
	})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	adapter := adapters.Get(adapters.DeepSeekName)
	_, toolCtx := tools.FromCodex([]codex.ResponseTool{{Type: "custom", Name: "apply_patch"}}, adapter)
	items := responseItemsFromMessage(providers.ChatMessage{
		ToolCalls: []providers.ChatToolCall{{
			ID:   "call_1",
			Type: "function",
			Function: providers.ChatCallFunction{
				Name:      "codex_text_editor",
				Arguments: string(arguments),
			},
		}},
	}, toolCtx, adapter, "req_test", "", "", logger)
	if len(items) != 1 {
		t.Fatalf("items len = %d", len(items))
	}
	if items[0]["type"] != "shell_call" {
		t.Fatalf("item = %#v", items[0])
	}
	action := items[0]["action"].(map[string]any)
	commands := action["commands"].([]any)
	if !strings.Contains(commands[0].(string), "TEXT_EDITOR_ALREADY_APPLIED") || !strings.Contains(commands[0].(string), "sed -n") {
		t.Fatalf("action = %#v", action)
	}
	if strings.Contains(commands[0].(string), "exit 1") || !strings.Contains(commands[0].(string), "exit 0") {
		t.Fatalf("local result should be a non-failing status command: %#v", action)
	}
}

func TestResponseItemsAllowsDifferentFilePatchAfterSuccess(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	adapter := adapters.Get(adapters.DeepSeekName)
	_, toolCtx := tools.FromCodex([]codex.ResponseTool{{Type: "custom", Name: "apply_patch"}}, adapter)
	items := responseItemsFromMessage(providers.ChatMessage{
		ToolCalls: []providers.ChatToolCall{{
			ID:   "call_1",
			Type: "function",
			Function: providers.ChatCallFunction{
				Name:      "codex_text_editor",
				Arguments: `{"command":"str_replace","path":"b.vue","old_str":"old","new_str":"new"}`,
			},
		}},
	}, toolCtx, adapter, "req_test", "", "", logger)
	if len(items) != 1 || items[0]["type"] != "custom_tool_call" {
		t.Fatalf("items = %#v", items)
	}
	if !strings.Contains(items[0]["input"].(string), "b.vue") {
		t.Fatalf("input = %#v", items[0]["input"])
	}
}

func TestResponseItemsAllowsSameFilePatchAfterSuccess(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	adapter := adapters.Get(adapters.DeepSeekName)
	_, toolCtx := tools.FromCodex([]codex.ResponseTool{{Type: "custom", Name: "apply_patch"}}, adapter)
	items := responseItemsFromMessage(providers.ChatMessage{
		ToolCalls: []providers.ChatToolCall{{
			ID:   "call_1",
			Type: "function",
			Function: providers.ChatCallFunction{
				Name:      "codex_text_editor",
				Arguments: `{"command":"str_replace","path":"./a.java","old_str":"old","new_str":"new"}`,
			},
		}},
	}, toolCtx, adapter, "req_test", "", "", logger)
	if len(items) != 1 || items[0]["type"] != "custom_tool_call" {
		t.Fatalf("items = %#v", items)
	}
	if !strings.Contains(items[0]["input"].(string), "a.java") {
		t.Fatalf("input = %#v", items[0]["input"])
	}
}

func TestResponseItemsKeepsDeepSeekReasoningBeforeToolCall(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	_, toolCtx := tools.FromCodex([]codex.ResponseTool{{Type: "custom", Name: "apply_patch"}}, adapters.Get(adapters.DeepSeekName))
	items := responseItemsFromMessage(providers.ChatMessage{
		ReasoningContent: "think before patch",
		ToolCalls: []providers.ChatToolCall{{
			ID: "call_1", Type: "function",
			Function: providers.ChatCallFunction{Name: "codex_text_editor", Arguments: `{"command":"str_replace","path":"a.java","old_str":"old","new_str":"new"}`},
		}},
	}, toolCtx, adapters.Get(adapters.DeepSeekName), "req_test", "", "", logger)
	if len(items) != 2 {
		t.Fatalf("items len = %d", len(items))
	}
	if items[0]["type"] != "reasoning" || items[0]["reasoning_content"] != "think before patch" {
		t.Fatalf("reasoning item = %#v", items[0])
	}
	if items[1]["type"] != "custom_tool_call" {
		t.Fatalf("tool item = %#v", items[1])
	}
}

func TestTextEditorStreamProjectorStreamsStablePatchPrefix(t *testing.T) {
	adapter := adapters.Get(adapters.DeepSeekName)
	_, toolCtx := tools.FromCodex([]codex.ResponseTool{{Type: "custom", Name: "apply_patch"}}, adapter)
	entry := toolCtx.Entry("codex_text_editor")
	projector := newTextEditorStreamProjector("call_1", entry)

	if events := projector.update(`{"command":"create",`, adapter); len(events) != 0 {
		t.Fatalf("events before parseable JSON = %#v", events)
	}
	events := projector.update(`{"command":"create","path":"hello.txt","file_text":"hel`, adapter)
	if len(events) != 2 {
		t.Fatalf("events = %#v", events)
	}
	added := events[0]
	if added["type"] != "response.output_item.added" {
		t.Fatalf("added event = %#v", added)
	}
	item := added["item"].(map[string]any)
	if item["type"] != "custom_tool_call" || item["name"] != "apply_patch" || item["status"] != "in_progress" {
		t.Fatalf("added item = %#v", item)
	}
	delta := events[1]
	if delta["type"] != "response.custom_tool_call_input.delta" ||
		!strings.Contains(delta["delta"].(string), "*** Add File: hello.txt") ||
		!strings.Contains(delta["delta"].(string), "+hel") ||
		strings.Contains(delta["delta"].(string), "*** End Patch") {
		t.Fatalf("delta event = %#v", delta)
	}
	events = projector.update(`{"command":"create","path":"hello.txt","file_text":"hello"}`, adapter)
	if len(events) != 1 || !strings.Contains(events[0]["delta"].(string), "lo\n*** End Patch") {
		t.Fatalf("completion delta = %#v", events)
	}
}

func TestTextEditorStreamProjectorAcceptsCreateFileAlias(t *testing.T) {
	adapter := adapters.Get(adapters.MimoName)
	_, toolCtx := tools.FromCodex([]codex.ResponseTool{{Type: "custom", Name: "apply_patch"}}, adapter)
	entry := toolCtx.Entry("codex_text_editor")
	projector := newTextEditorStreamProjector("call_1", entry)

	events := projector.update(`{"command":"create_file","path":"hello.txt","file_text":"hello"}`, adapter)
	if len(events) != 2 {
		t.Fatalf("events = %#v", events)
	}
	delta := events[1]["delta"].(string)
	for _, want := range []string{"*** Add File: hello.txt", "+hello", "*** End Patch"} {
		if !strings.Contains(delta, want) {
			t.Fatalf("delta missing %q: %s", want, delta)
		}
	}
}

func TestTextEditorStreamProjectorStreamsMoveFile(t *testing.T) {
	adapter := adapters.Get(adapters.KimiName)
	_, toolCtx := tools.FromCodex([]codex.ResponseTool{{Type: "custom", Name: "apply_patch"}}, adapter)
	entry := toolCtx.Entry("codex_text_editor")
	projector := newTextEditorStreamProjector("call_1", entry)

	events := projector.update(`{"command":"move_file","path":"old.md","destination_path":"new.md"}`, adapter)
	if len(events) != 2 {
		t.Fatalf("events = %#v", events)
	}
	if events[0]["type"] != "response.output_item.added" {
		t.Fatalf("added event = %#v", events[0])
	}
	delta := events[1]["delta"].(string)
	for _, want := range []string{"*** Update File: old.md", "*** Move to: new.md", "*** End Patch"} {
		if !strings.Contains(delta, want) {
			t.Fatalf("delta missing %q: %s", want, delta)
		}
	}
}

func TestTextEditorStreamProjectorStreamsMoveFileWithReplace(t *testing.T) {
	adapter := adapters.Get(adapters.KimiName)
	_, toolCtx := tools.FromCodex([]codex.ResponseTool{{Type: "custom", Name: "apply_patch"}}, adapter)
	entry := toolCtx.Entry("codex_text_editor")
	projector := newTextEditorStreamProjector("call_1", entry)

	events := projector.update(`{"command":"move_file","path":"old.md","destination_path":"new.md","old_str":"# Old","new_str":"# New"}`, adapter)
	if len(events) != 2 {
		t.Fatalf("events = %#v", events)
	}
	delta := events[1]["delta"].(string)
	for _, want := range []string{"*** Move to: new.md", "-# Old", "+# New", "*** End Patch"} {
		if !strings.Contains(delta, want) {
			t.Fatalf("delta missing %q: %s", want, delta)
		}
	}
}

func TestTextEditorStreamProjectorDoesNotPretendLocalResultIsApplyPatch(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/a.java"
	if err := os.WriteFile(path, []byte("old done\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	adapter := adapters.Get(adapters.DeepSeekName)
	_, toolCtx := tools.FromCodex([]codex.ResponseTool{{Type: "custom", Name: "apply_patch"}}, adapter)
	entry := toolCtx.Entry("codex_text_editor")
	projector := newTextEditorStreamProjector("call_1", entry)
	arguments, _ := json.Marshal(map[string]string{
		"command": "str_replace",
		"path":    path,
		"old_str": "old",
		"new_str": "old done",
	})

	events := projector.update(string(arguments), adapter)
	if len(events) != 1 {
		t.Fatalf("events = %#v", events)
	}
	item := events[0]["item"].(codex.ResponseItem)
	if item["type"] != "shell_call" {
		t.Fatalf("item = %#v", item)
	}
}

func TestResponseItemsFromToolSearchCall(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	_, toolCtx := tools.FromCodex([]codex.ResponseTool{{Type: "tool_search"}}, adapters.Get(adapters.DefaultName))
	items := responseItemsFromMessage(providers.ChatMessage{
		ToolCalls: []providers.ChatToolCall{{
			ID: "call_1", Type: "function",
			Function: providers.ChatCallFunction{Name: "tool_search", Arguments: `{"goal":"find shell"}`},
		}},
	}, toolCtx, adapters.Get(adapters.DefaultName), "req_test", "", "", logger)
	if items[0]["type"] != "tool_search_call" {
		t.Fatalf("item = %#v", items[0])
	}
}

func TestResponseItemsFromShellCall(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	_, toolCtx := tools.FromCodex([]codex.ResponseTool{{Type: "shell"}}, adapters.Get(adapters.DefaultName))
	items := responseItemsFromMessage(providers.ChatMessage{
		ToolCalls: []providers.ChatToolCall{{
			ID: "call_1", Type: "function",
			Function: providers.ChatCallFunction{Name: "shell", Arguments: `{"command":"ls"}`},
		}},
	}, toolCtx, adapters.Get(adapters.DefaultName), "req_test", "", "", logger)
	if items[0]["type"] != "shell_call" {
		t.Fatalf("item = %#v", items[0])
	}
	action := items[0]["action"].(map[string]any)
	if len(action["commands"].([]any)) != 1 {
		t.Fatalf("action = %#v", action)
	}
}

func TestResponseItemsBlocksDeepSeekManualShellWrites(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	adapter := adapters.Get(adapters.DeepSeekName)
	_, toolCtx := tools.FromCodex([]codex.ResponseTool{{Type: "shell"}}, adapter)
	items := responseItemsFromMessage(providers.ChatMessage{
		ToolCalls: []providers.ChatToolCall{{
			ID: "call_1", Type: "function",
			Function: providers.ChatCallFunction{
				Name:      "shell",
				Arguments: `{"command":"cat > README.md <<'EOF'\nhello\nEOF"}`,
			},
		}},
	}, toolCtx, adapter, "req_test", "", "", logger)
	if items[0]["type"] != "shell_call" {
		t.Fatalf("item = %#v", items[0])
	}
	action := items[0]["action"].(map[string]any)
	commands := action["commands"].([]any)
	if !strings.Contains(commands[0].(string), "SHELL_FILE_WRITE_BLOCKED") {
		t.Fatalf("item = %#v", items[0])
	}
}

func TestResponseItemsBlocksDeepSeekExecCommandFileWrites(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	adapter := adapters.Get(adapters.DeepSeekName)
	_, toolCtx := tools.FromCodex([]codex.ResponseTool{{Type: "function", Name: "exec_command"}}, adapter)
	items := responseItemsFromMessage(providers.ChatMessage{
		ToolCalls: []providers.ChatToolCall{{
			ID: "call_1", Type: "function",
			Function: providers.ChatCallFunction{
				Name:      "exec_command",
				Arguments: `{"cmd":"cat > README.md << 'EOF'\nhello\nEOF","workdir":"/tmp/test"}`,
			},
		}},
	}, toolCtx, adapter, "req_test", "", "", logger)
	if items[0]["type"] != "function_call" || items[0]["name"] != "exec_command" {
		t.Fatalf("item = %#v", items[0])
	}
	arguments := items[0]["arguments"].(string)
	if !strings.Contains(arguments, "SHELL_FILE_WRITE_BLOCKED") || strings.Contains(arguments, "cat > README.md") {
		t.Fatalf("item = %#v", items[0])
	}
}

func TestResponseItemsAllowsDeepSeekExecCommandReadCommands(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	adapter := adapters.Get(adapters.DeepSeekName)
	_, toolCtx := tools.FromCodex([]codex.ResponseTool{{Type: "function", Name: "exec_command"}}, adapter)
	items := responseItemsFromMessage(providers.ChatMessage{
		ToolCalls: []providers.ChatToolCall{{
			ID: "call_1", Type: "function",
			Function: providers.ChatCallFunction{
				Name:      "exec_command",
				Arguments: `{"cmd":"sed -n '1,80p' README.md","workdir":"/tmp/test"}`,
			},
		}},
	}, toolCtx, adapter, "req_test", "", "", logger)
	if items[0]["type"] != "function_call" || items[0]["name"] != "exec_command" {
		t.Fatalf("item = %#v", items[0])
	}
}

func TestResponseItemsLogsBlockedToolRewrite(t *testing.T) {
	logPath := t.TempDir() + "/tool-calls.jsonl"
	t.Setenv("CODEX_BRIDGE_TOOL_LOG", logPath)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	adapter := adapters.Get(adapters.DeepSeekName)
	_, toolCtx := tools.FromCodex([]codex.ResponseTool{{Type: "function", Name: "exec_command"}}, adapter)

	_ = responseItemsFromMessage(providers.ChatMessage{
		ToolCalls: []providers.ChatToolCall{{
			ID: "call_1", Type: "function",
			Function: providers.ChatCallFunction{
				Name:      "exec_command",
				Arguments: `{"cmd":"rm README.md","workdir":"/tmp/test"}`,
			},
		}},
	}, toolCtx, adapter, "req_test", "gpt-5.3-codex", "kimi", logger)

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	logText := string(data)
	for _, want := range []string{"tool_call_rewritten", "gpt-5.3-codex", "kimi", "rm README.md", "SHELL_FILE_WRITE_BLOCKED"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("log missing %q: %s", want, logText)
		}
	}
}
