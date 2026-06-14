package server

import (
	"io"
	"log/slog"
	"testing"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/providers"
	"codex-bridge/internal/tools"
)

func TestResponseItemsFromApplyPatchToolCall(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	items := responseItemsFromMessage(providers.ChatMessage{
		ToolCalls: []providers.ChatToolCall{{
			ID: "call_1", Type: "function",
			Function: providers.ChatCallFunction{Name: "apply_patch", Arguments: `{"input":"*** Begin Patch\n*** End Patch\n"}`},
		}},
	}, tools.Context{Tools: map[string]tools.Entry{"apply_patch": {Name: "apply_patch", Kind: tools.KindApplyPatch, OriginalName: "apply_patch"}}}, adapters.Get(adapters.DeepSeekName), "req_test", logger)
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
	items := responseItemsFromMessage(providers.ChatMessage{
		ToolCalls: []providers.ChatToolCall{{
			ID: "call_1", Type: "function",
			Function: providers.ChatCallFunction{Name: "tool_search", Arguments: `{"goal":"find shell"}`},
		}},
	}, tools.Context{Tools: map[string]tools.Entry{"tool_search": {Name: "tool_search", Kind: tools.KindToolSearch, OriginalName: "tool_search"}}}, adapters.Get(adapters.DefaultName), "req_test", logger)
	if items[0]["type"] != "tool_search_call" {
		t.Fatalf("item = %#v", items[0])
	}
}

func TestResponseItemsFromShellCall(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	items := responseItemsFromMessage(providers.ChatMessage{
		ToolCalls: []providers.ChatToolCall{{
			ID: "call_1", Type: "function",
			Function: providers.ChatCallFunction{Name: "shell", Arguments: `{"command":"ls"}`},
		}},
	}, tools.Context{Tools: map[string]tools.Entry{"shell": {Name: "shell", Kind: tools.KindShell, OriginalName: "shell"}}}, adapters.Get(adapters.DefaultName), "req_test", logger)
	if items[0]["type"] != "shell_call" {
		t.Fatalf("item = %#v", items[0])
	}
	action := items[0]["action"].(map[string]any)
	if len(action["commands"].([]any)) != 1 {
		t.Fatalf("action = %#v", action)
	}
}
