package server

import (
	"io"
	"log/slog"
	"testing"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
	"codex-bridge/internal/providers"
	"codex-bridge/internal/tools"
)

func TestResponseItemsFromApplyPatchToolCall(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	_, toolCtx := tools.FromCodex([]codex.ResponseTool{{Type: "custom", Name: "apply_patch"}}, adapters.Get(adapters.DeepSeekName))
	items := responseItemsFromMessage(providers.ChatMessage{
		ToolCalls: []providers.ChatToolCall{{
			ID: "call_1", Type: "function",
			Function: providers.ChatCallFunction{Name: "apply_patch", Arguments: `{"input":"*** Begin Patch\n*** End Patch\n"}`},
		}},
	}, toolCtx, adapters.Get(adapters.DeepSeekName), "req_test", logger)
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
