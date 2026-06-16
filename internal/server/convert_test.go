package server

import (
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
	}, toolCtx, adapter, "req_test", logger)
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
	}, toolCtx, adapter, "req_test", logger)
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

func TestResponseItemsAllowsDifferentFilePatchDuringCooldown(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	adapter := adapters.Get(adapters.DeepSeekName)
	_, toolCtx := tools.FromCodex([]codex.ResponseTool{{Type: "custom", Name: "apply_patch"}}, adapter)
	items := responseItemsFromMessageWithOptions(providers.ChatMessage{
		ToolCalls: []providers.ChatToolCall{{
			ID:   "call_1",
			Type: "function",
			Function: providers.ChatCallFunction{
				Name:      "codex_text_editor",
				Arguments: `{"command":"str_replace","path":"b.vue","old_str":"old","new_str":"new"}`,
			},
		}},
	}, toolCtx, adapter, "req_test", logger, responseConversionOptions{patchCooldownFiles: []string{"a.java"}})
	if len(items) != 1 || items[0]["type"] != "custom_tool_call" {
		t.Fatalf("items = %#v", items)
	}
	if !strings.Contains(items[0]["input"].(string), "b.vue") {
		t.Fatalf("input = %#v", items[0]["input"])
	}
}

func TestResponseItemsBlocksSameFilePatchDuringCooldown(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	adapter := adapters.Get(adapters.DeepSeekName)
	_, toolCtx := tools.FromCodex([]codex.ResponseTool{{Type: "custom", Name: "apply_patch"}}, adapter)
	items := responseItemsFromMessageWithOptions(providers.ChatMessage{
		ToolCalls: []providers.ChatToolCall{{
			ID:   "call_1",
			Type: "function",
			Function: providers.ChatCallFunction{
				Name:      "codex_text_editor",
				Arguments: `{"command":"str_replace","path":"./a.java","old_str":"old","new_str":"new"}`,
			},
		}},
	}, toolCtx, adapter, "req_test", logger, responseConversionOptions{patchCooldownFiles: []string{"a.java"}})
	if len(items) != 1 || items[0]["type"] != "message" {
		t.Fatalf("items = %#v", items)
	}
	content := items[0]["content"].([]map[string]string)[0]["text"]
	if !strings.Contains(content, "已跳过重复的同文件编辑") || !strings.Contains(content, "a.java") {
		t.Fatalf("content = %s", content)
	}
}

func TestResponseItemsCollapsesRepeatedSameFileCooldownMessages(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	adapter := adapters.Get(adapters.DeepSeekName)
	_, toolCtx := tools.FromCodex([]codex.ResponseTool{{Type: "custom", Name: "apply_patch"}}, adapter)
	items := responseItemsFromMessageWithOptions(providers.ChatMessage{
		ToolCalls: []providers.ChatToolCall{
			{
				ID:   "call_1",
				Type: "function",
				Function: providers.ChatCallFunction{
					Name:      "codex_text_editor",
					Arguments: `{"command":"str_replace","path":"a.java","old_str":"old","new_str":"new"}`,
				},
			},
			{
				ID:   "call_2",
				Type: "function",
				Function: providers.ChatCallFunction{
					Name:      "codex_text_editor",
					Arguments: `{"command":"str_replace","path":"./a.java","old_str":"old2","new_str":"new2"}`,
				},
			},
		},
	}, toolCtx, adapter, "req_test", logger, responseConversionOptions{patchCooldownFiles: []string{"a.java"}})
	if len(items) != 1 || items[0]["type"] != "message" {
		t.Fatalf("items = %#v", items)
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
	}, toolCtx, adapters.Get(adapters.DeepSeekName), "req_test", logger)
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

func TestResponseItemsFromToolSearchCall(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	_, toolCtx := tools.FromCodex([]codex.ResponseTool{{Type: "tool_search"}}, adapters.Get(adapters.DefaultName))
	items := responseItemsFromMessage(providers.ChatMessage{
		ToolCalls: []providers.ChatToolCall{{
			ID: "call_1", Type: "function",
			Function: providers.ChatCallFunction{Name: "tool_search", Arguments: `{"goal":"find shell"}`},
		}},
	}, toolCtx, adapters.Get(adapters.DefaultName), "req_test", logger)
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
	}, toolCtx, adapters.Get(adapters.DefaultName), "req_test", logger)
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
	}, toolCtx, adapter, "req_test", logger)
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
	}, toolCtx, adapter, "req_test", logger)
	if items[0]["type"] != "function_call" {
		t.Fatalf("item = %#v", items[0])
	}
	arguments, _ := items[0]["arguments"].(string)
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
	}, toolCtx, adapter, "req_test", logger)
	if items[0]["type"] != "function_call" {
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
	}, toolCtx, adapter, "req_test", logger)

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	logText := string(data)
	for _, want := range []string{"tool_call_rewritten", "rm README.md", "SHELL_FILE_WRITE_BLOCKED"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("log missing %q: %s", want, logText)
		}
	}
}
