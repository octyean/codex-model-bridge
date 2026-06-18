package adapters

import (
	"encoding/json"
	"strings"
	"testing"

	"codex-bridge/internal/providers"
)

func TestDeepSeekPrepareRequestStabilizesToolsAndRepairsToolResults(t *testing.T) {
	adapter := Get(DeepSeekName)
	req := providers.ChatCompletionRequest{
		Model:  "deepseek-v4-flash",
		Stream: true,
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "edit"},
			{Role: "assistant", ToolCalls: []providers.ChatToolCall{{
				ID: "call_1", Type: "function",
				Function: providers.ChatCallFunction{Name: "apply_patch", Arguments: `{"input":"*** Begin Patch\n*** End Patch\n"}`},
			}}},
			{Role: "user", Content: "continue"},
		},
		Tools: []providers.ChatTool{
			{Type: "function", Function: providers.ChatFunction{Name: "z_tool"}},
			{Type: "function", Function: providers.ChatFunction{Name: "a_tool"}},
		},
	}
	prepared := adapter.PrepareChatRequest(req)
	if prepared.StreamOptions == nil || !prepared.StreamOptions.IncludeUsage {
		t.Fatalf("deepseek stream request should include usage")
	}
	if prepared.Tools[0].Function.Name != "a_tool" || prepared.Tools[1].Function.Name != "z_tool" {
		t.Fatalf("tools not sorted: %#v", prepared.Tools)
	}
	if len(prepared.Messages) != 4 {
		t.Fatalf("messages len = %d", len(prepared.Messages))
	}
	if prepared.Messages[2].Role != "tool" || prepared.Messages[2].ToolCallID != "call_1" {
		t.Fatalf("missing repaired tool result: %#v", prepared.Messages[2])
	}
	if !prepared.AssistantToolContentNull {
		t.Fatalf("deepseek adapter should request null assistant tool content")
	}
}

func TestDeepSeekPrepareRequestCanonicalizesToolParameters(t *testing.T) {
	adapter := Get(DeepSeekName)
	prepared := adapter.PrepareChatRequest(providers.ChatCompletionRequest{
		Model: "deepseek-v4-flash",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "call tool"},
		},
		Tools: []providers.ChatTool{{
			Type: "function",
			Function: providers.ChatFunction{
				Name:       "record_result",
				Parameters: json.RawMessage(`{"properties":{"b":{"type":"string"},"a":{"type":"string"}},"type":"object"}`),
			},
		}},
	})
	want := `{"properties":{"a":{"type":"string"},"b":{"type":"string"}},"type":"object"}`
	if string(prepared.Tools[0].Function.Parameters) != want {
		t.Fatalf("parameters = %s", prepared.Tools[0].Function.Parameters)
	}
}

func TestDeepSeekPrepareRequestDowngradesForcedToolChoice(t *testing.T) {
	adapter := Get(DeepSeekName)
	prepared := adapter.PrepareChatRequest(providers.ChatCompletionRequest{
		Model: "deepseek-v4-flash",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "call tool"},
		},
		Tools: []providers.ChatTool{{
			Type:     "function",
			Function: providers.ChatFunction{Name: "record_result"},
		}},
		ToolChoice: map[string]any{
			"type":     "function",
			"function": map[string]any{"name": "record_result"},
		},
	})
	if prepared.ToolChoice != "auto" {
		t.Fatalf("tool_choice = %#v", prepared.ToolChoice)
	}
	if prepared.Messages[0].Role != "system" {
		t.Fatalf("messages = %#v", prepared.Messages)
	}
}

func TestDeepSeekPrepareRequestAddsOpenVikingReadBoundaryNote(t *testing.T) {
	adapter := Get(DeepSeekName)
	prepared := adapter.PrepareChatRequest(providers.ChatCompletionRequest{
		Model: "deepseek-v4-flash",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "read local skill"},
		},
		Tools: []providers.ChatTool{{
			Type: "function",
			Function: providers.ChatFunction{
				Name:        "read",
				Description: "Read full content from one or more viking:// file URIs. OpenViking Memory.",
			},
		}},
	})
	if prepared.Messages[0].Role != "system" {
		t.Fatalf("messages = %#v", prepared.Messages)
	}
	text, _ := prepared.Messages[0].Content.(string)
	for _, want := range []string{"OPENVIKING_READ_TOOL_BOUNDARY", "viking://", "file://", "local file"} {
		if !strings.Contains(text, want) {
			t.Fatalf("boundary note missing %q: %s", want, text)
		}
	}
}

func TestDeepSeekPrepareRequestDoesNotAddTextEditorSuccessStopNote(t *testing.T) {
	adapter := Get(DeepSeekName)
	prepared := adapter.PrepareChatRequest(providers.ChatCompletionRequest{
		Model: "deepseek-v4-flash",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "edit"},
			{Role: "assistant", ToolCalls: []providers.ChatToolCall{{
				ID: "call_1", Type: "function",
				Function: providers.ChatCallFunction{Name: "codex_text_editor", Arguments: `{"command":"str_replace","path":"a.java","old_str":"old","new_str":"new"}`},
			}}},
			{Role: "tool", ToolCallID: "call_1", Content: "Success. Updated the following files:\nM a.java\n\nTEXT_EDITOR_EDIT_SUCCEEDED"},
		},
		Tools: []providers.ChatTool{{
			Type:     "function",
			Function: providers.ChatFunction{Name: "codex_text_editor"},
		}},
	})
	for _, message := range prepared.Messages {
		text, _ := message.Content.(string)
		if message.Role == "system" && strings.Contains(text, "TEXT_EDITOR_SUCCESS_STOP") {
			t.Fatalf("unexpected stop note: %#v", prepared.Messages)
		}
	}
	if len(prepared.Tools) != 1 || prepared.Tools[0].Function.Name != "codex_text_editor" {
		t.Fatalf("text editor should remain available: %#v", prepared.Tools)
	}
}

func TestDeepSeekPrepareRequestDoesNotAddTextEditorSuccessStopNoteAfterNewUserRequest(t *testing.T) {
	adapter := Get(DeepSeekName)
	prepared := adapter.PrepareChatRequest(providers.ChatCompletionRequest{
		Model: "deepseek-v4-flash",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "edit"},
			{Role: "tool", ToolCallID: "call_1", Content: "TEXT_EDITOR_EDIT_SUCCEEDED"},
			{Role: "user", Content: "new task"},
		},
		Tools: []providers.ChatTool{{
			Type:     "function",
			Function: providers.ChatFunction{Name: "codex_text_editor"},
		}},
	})
	for _, message := range prepared.Messages {
		if text, ok := message.Content.(string); ok && strings.Contains(text, "TEXT_EDITOR_SUCCESS_STOP") {
			t.Fatalf("unexpected stop note after new user request: %#v", prepared.Messages)
		}
	}
	if len(prepared.Tools) != 1 || prepared.Tools[0].Function.Name != "codex_text_editor" {
		t.Fatalf("text editor should remain available for new user request: %#v", prepared.Tools)
	}
}

func TestDeepSeekApplyPatchDescriptionIsTextEditorOnly(t *testing.T) {
	adapter := Get(DeepSeekName)
	description := adapter.CustomToolDescription(ToolDescriptor{
		Name: "apply_patch",
		Kind: "text_editor_patch",
	})
	for _, forbidden := range []string{"APPLY_PATCH_CONTEXT_MISMATCH", "APPLY_PATCH_SUCCEEDED", "*** Begin Patch", "*** End Patch"} {
		if strings.Contains(description, forbidden) {
			t.Fatalf("description leaked old apply_patch protocol %q: %s", forbidden, description)
		}
	}
	for _, want := range []string{"Codex's text editor bridge", "str_replace", "old_str", "insert_after", "delete_file"} {
		if !strings.Contains(description, want) {
			t.Fatalf("description missing %q: %s", want, description)
		}
	}
}

func TestDeepSeekTextEditorDescriptionHidesPatchProtocol(t *testing.T) {
	adapter := Get(DeepSeekName)
	description := adapter.CustomToolDescription(ToolDescriptor{
		Name: "apply_patch",
		Kind: "text_editor_patch",
	})
	for _, forbidden := range []string{"apply_patch", "*** Begin Patch", "*** End Patch"} {
		if strings.Contains(description, forbidden) {
			t.Fatalf("description leaked %q: %s", forbidden, description)
		}
	}
	for _, want := range []string{"str_replace", "old_str", "insert_after", "delete_file"} {
		if !strings.Contains(description, want) {
			t.Fatalf("description missing %q: %s", want, description)
		}
	}
}

func TestDeepSeekPatchSystemInstructionCoversGeneralEditDiscipline(t *testing.T) {
	instruction := chatPatchSystemInstruction
	for _, want := range []string{
		"source, document, and config file creation, edits, deletes, and moves",
		"inspect the current target lines",
		"Prefer small, surgical hunks",
		"do not retry the same patch",
		"After apply_patch succeeds for a file, do not repeat an already-completed edit",
		"make the smallest follow-up patch from exact current context",
	} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("instruction missing %q: %s", want, instruction)
		}
	}
	if strings.Contains(instruction, "do not call apply_patch on that same file again") {
		t.Fatalf("instruction still contains hard same-file stop rule: %s", instruction)
	}
}

func TestDeepSeekFormatsTextEditorContextMismatch(t *testing.T) {
	adapter := Get(DeepSeekName)
	output := adapter.FormatToolOutput(ToolDescriptor{
		Name: "codex_text_editor",
		Kind: "text_editor_patch",
	}, "apply_patch verification failed: Failed to find expected lines in /tmp/file:\n   <view")
	for _, want := range []string{
		"TEXT_EDITOR_CONTEXT_MISMATCH",
		"required_next_action: inspect_current_file",
		"forbidden_next_action: retry_same_edit",
		"If the requested content is already present, stop editing and summarize",
		"edit_discipline: do not broaden the edit",
		"do not use shell as a file editor",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q: %s", want, output)
		}
	}
}

func TestDeepSeekFormatsInvalidTextEditorEdit(t *testing.T) {
	adapter := Get(DeepSeekName)
	output := adapter.FormatToolOutput(ToolDescriptor{
		Name: "codex_text_editor",
		Kind: "text_editor_patch",
	}, "invalid patch: missing *** Begin Patch")
	for _, want := range []string{
		"TEXT_EDITOR_INVALID_EDIT",
		"required_next_action: regenerate_text_editor_arguments",
		"forbidden_next_action: send_diff_or_patch_syntax",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q: %s", want, output)
		}
	}
}

func TestDeepSeekFormatsSuccessfulPatchWithContinueEditingProtocol(t *testing.T) {
	adapter := Get(DeepSeekName)
	output := adapter.FormatToolOutput(ToolDescriptor{
		Name: "codex_text_editor",
		Kind: "text_editor_patch",
	}, "Exit code: 0\nOutput:\nSuccess. Updated the following files:\nM README.md")
	for _, want := range []string{
		"TEXT_EDITOR_EDIT_SUCCEEDED",
		"file_edit_state: completed",
		"changed_files: README.md",
		"next_action: read_only_verify_or_summarize_or_continue_editing_if_needed",
		"allowed_next_action: grep_sed_diff_tests_or_text_editor_if_needed",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q: %s", want, output)
		}
	}
}

func TestDeepSeekToolPolicyBlocksManualShellWrites(t *testing.T) {
	policy := Get(DeepSeekName).ToolPolicy()
	for _, command := range []string{
		"cat > README.md <<'EOF'\nhello\nEOF",
		"printf 'hello' > README.md",
		"echo hello >> README.md",
		"tee README.md",
		"python3 - <<'PY'\nopen('README.md', 'w').write('hello')\nPY",
		"python3 - <<'PY'\nfrom pathlib import Path\nPath('README.md').write_text('hello')\nPY",
		"node -e \"require('fs').writeFileSync('README.md','hello')\"",
		"sed -i 's/old/new/' README.md",
		"perl -pi -e 's/old/new/' README.md",
		"rm README.md",
		"mv old.md new.md",
		"cp template.md README.md",
	} {
		if got := policy.BlockedShellOutput(command); !strings.Contains(got, "SHELL_FILE_WRITE_BLOCKED") {
			t.Fatalf("command should be blocked: %s => %q", command, got)
		}
	}
}

func TestDeepSeekToolPolicyAllowsReadAndGeneratorCommands(t *testing.T) {
	policy := Get(DeepSeekName).ToolPolicy()
	for _, command := range []string{
		"cat README.md",
		"sed -n '1,120p' README.md",
		"head -c 100 StoreOrderServiceImpl.java 2>&1 | head -c 4000",
		"wc -l StoreOrderServiceImpl.java 2>/dev/null || echo \"file not found\"",
		"find /tmp/work -name 'StoreOrderServiceImpl.java' -type f 2>/dev/null",
		"grep -n 'Map<Long, BigDecimal> stockUseMap' StoreOrderServiceImpl.java",
		"echo \"=== Private method ===\" && grep -n 'private Map<Long, BigDecimal> buildTakeoutStockUseMap' StoreOrderServiceImpl.java",
		"python3 - <<'PY'\n# Map<Long, StoreProduct> appears in the source being inspected.\nwith open('StoreOrderServiceImpl.java', 'r') as f:\n    print(repr(f.readline()))\nPY",
		"rg apply_patch",
		"go test ./...",
		"gofmt -w internal/adapters/deepseek.go",
		"npm run build",
	} {
		if got := policy.BlockedShellOutput(command); got != "" {
			t.Fatalf("command should be allowed: %s => %q", command, got)
		}
	}
}

func TestDeepSeekToolPolicyBlocksExecCommandManualShellWrites(t *testing.T) {
	policy := Get(DeepSeekName).ToolPolicy()
	for _, arguments := range []string{
		`{"cmd":"cat > README.md << 'EOF'\nhello\nEOF","workdir":"/tmp/test"}`,
		`{"command":["sed -n '1,80p' README.md","echo hello >> README.md"]}`,
	} {
		if got := policy.BlockedToolOutput("exec_command", arguments); !strings.Contains(got, "SHELL_FILE_WRITE_BLOCKED") {
			t.Fatalf("arguments should be blocked: %s => %q", arguments, got)
		}
	}
}

func TestDeepSeekToolPolicyAllowsExecCommandReadAndToolWrites(t *testing.T) {
	policy := Get(DeepSeekName).ToolPolicy()
	for _, arguments := range []string{
		`{"cmd":"sed -n '1,80p' README.md","workdir":"/tmp/test"}`,
		`{"cmd":"go test ./...","workdir":"/tmp/test"}`,
		`{"cmd":"gofmt -w internal/adapters/deepseek.go","workdir":"/tmp/test"}`,
	} {
		if got := policy.BlockedToolOutput("exec_command", arguments); got != "" {
			t.Fatalf("arguments should be allowed: %s => %q", arguments, got)
		}
	}
}

func TestDeepSeekToolPolicyRewritesBlockedExecCommand(t *testing.T) {
	policy := Get(DeepSeekName).ToolPolicy()
	rewritten, ok := policy.RewriteBlockedToolCall("exec_command", `{"cmd":"cat > README.md << 'EOF'\nhello\nEOF","workdir":"/tmp/test"}`)
	if !ok {
		t.Fatalf("tool call should be rewritten")
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(rewritten), &obj); err != nil {
		t.Fatalf("invalid rewritten json: %v", err)
	}
	if obj["workdir"] != "/tmp/test" {
		t.Fatalf("workdir lost: %#v", obj)
	}
	cmd, _ := obj["cmd"].(string)
	if !strings.Contains(cmd, "SHELL_FILE_WRITE_BLOCKED") || strings.Contains(cmd, "cat > README.md") {
		t.Fatalf("cmd = %q", cmd)
	}
}
