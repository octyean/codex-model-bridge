package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
)

func TestApplyPatchCustomToolBecomesChatFunction(t *testing.T) {
	chatTools, ctx := FromCodex([]codex.ResponseTool{{Type: "custom", Name: "apply_patch"}}, adapters.Get(adapters.DefaultName))
	if len(chatTools) != 1 {
		t.Fatalf("tools len = %d", len(chatTools))
	}
	if chatTools[0].Function.Name != "apply_patch" {
		t.Fatalf("tool name = %q", chatTools[0].Function.Name)
	}
	if !ctx.IsCustom("apply_patch") {
		t.Fatalf("apply_patch should be custom")
	}
}

func TestExtractCustomInput(t *testing.T) {
	got := ExtractCustomInput(`{"input":"*** Begin Patch\n*** End Patch\n"}`)
	want := "*** Begin Patch\n*** End Patch\n"
	if got != want {
		t.Fatalf("input = %q, want %q", got, want)
	}
}

func TestDeepSeekApplyPatchDescription(t *testing.T) {
	chatTools, _ := FromCodex([]codex.ResponseTool{{Type: "custom", Name: "apply_patch"}}, adapters.Get(adapters.DeepSeekName))
	description := chatTools[0].Function.Description
	if !strings.Contains(description, "*** Begin Patch") || !strings.Contains(description, "*** End Patch") {
		t.Fatalf("description should include patch boundaries: %q", description)
	}
}

func TestNormalizeCustomInputRemovesMarkdownFence(t *testing.T) {
	got := adapters.Get(adapters.DefaultName).NormalizeCustomInput("apply_patch", "```patch\n*** Begin Patch\n*** End Patch\n```")
	want := "*** Begin Patch\n*** End Patch"
	if got != want {
		t.Fatalf("normalized input = %q, want %q", got, want)
	}
}

func TestToolSearchAndLocalShellBecomeChatFunctions(t *testing.T) {
	chatTools, ctx := FromCodex([]codex.ResponseTool{
		{Type: "tool_search"},
		{Type: "local_shell"},
	}, adapters.Get(adapters.DefaultName))
	if len(chatTools) != 2 {
		t.Fatalf("tools len = %d", len(chatTools))
	}
	if ctx.Entry("tool_search").Kind != KindToolSearch {
		t.Fatalf("tool_search entry = %#v", ctx.Entry("tool_search"))
	}
	if ctx.Entry("shell").Kind != KindShell {
		t.Fatalf("shell entry = %#v", ctx.Entry("shell"))
	}
}

func TestNamespaceToolsKeepNamespaceInRegistry(t *testing.T) {
	var tool codex.ResponseTool
	raw := `{"type":"namespace","name":"browser","tools":[{"type":"function","name":"open","description":"open url","parameters":{"type":"object"}}]}`
	if err := json.Unmarshal([]byte(raw), &tool); err != nil {
		t.Fatalf("decode tool: %v", err)
	}
	chatTools, ctx := FromCodex([]codex.ResponseTool{tool}, adapters.Get(adapters.DefaultName))
	if len(chatTools) != 1 || chatTools[0].Function.Name != "open" {
		t.Fatalf("chat tools = %#v", chatTools)
	}
	if entry := ctx.Entry("open"); entry.Namespace != "browser" {
		t.Fatalf("entry = %#v", entry)
	}
}

func TestUnsupportedHostedToolsAreFiltered(t *testing.T) {
	chatTools, _ := FromCodex([]codex.ResponseTool{
		{Type: "web_search_preview"},
		{Type: "mcp", Name: "github"},
		{Type: "function", Name: "ok"},
	}, adapters.Get(adapters.DefaultName))
	if len(chatTools) != 1 || chatTools[0].Function.Name != "ok" {
		t.Fatalf("chat tools = %#v", chatTools)
	}
}

func TestToolChoiceConvertsForcedFunction(t *testing.T) {
	_, ctx := FromCodex([]codex.ResponseTool{{Type: "function", Name: "shell"}}, adapters.Get(adapters.DefaultName))
	got := ToolChoice(map[string]any{"type": "function", "name": "shell"}, ctx)
	obj, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("tool choice = %#v", got)
	}
	if obj["type"] != "function" {
		t.Fatalf("tool choice = %#v", obj)
	}
}

func TestToolChoiceFiltersAllowedTools(t *testing.T) {
	_, ctx := FromCodex([]codex.ResponseTool{{Type: "function", Name: "keep"}}, adapters.Get(adapters.DefaultName))
	got := ToolChoice(map[string]any{
		"type": "allowed_tools",
		"mode": "auto",
		"tools": []any{
			map[string]any{"type": "function", "name": "keep"},
			map[string]any{"type": "function", "name": "drop"},
		},
	}, ctx)
	obj, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("tool choice = %#v", got)
	}
	tools := obj["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("allowed tools = %#v", tools)
	}
}

func TestShellArgumentsAcceptArrayCommand(t *testing.T) {
	got := ShellArguments(`["bash","-lc","pwd"]`)
	commands := got["commands"].([]string)
	if len(commands) != 3 {
		t.Fatalf("commands = %#v", commands)
	}
}
