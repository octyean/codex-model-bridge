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

func TestDeepSeekApplyPatchDescriptionCarriesRecoveryProtocol(t *testing.T) {
	adapter := Get(DeepSeekName)
	description := adapter.CustomToolDescription(ToolDescriptor{
		Name: "apply_patch",
		Kind: "patch",
	})
	for _, want := range []string{
		"APPLY_PATCH_CONTEXT_MISMATCH",
		"next action must be reading the current target file lines",
		"one - old line immediately followed by one + new line",
		"Do not use an insertion-only hunk",
		"copy that text verbatim",
		"preserve the surrounding indentation style exactly",
		"Never retry the same patch",
		"Whitespace inside hunks is significant",
	} {
		if !strings.Contains(description, want) {
			t.Fatalf("description missing %q: %s", want, description)
		}
	}
}

func TestDeepSeekFormatsExpectedLinesPatchFailure(t *testing.T) {
	adapter := Get(DeepSeekName)
	output := adapter.FormatToolOutput(ToolDescriptor{
		Name: "apply_patch",
		Kind: "patch",
	}, "apply_patch verification failed: Failed to find expected lines in /tmp/file:\n   <view")
	for _, want := range []string{
		"APPLY_PATCH_CONTEXT_MISMATCH",
		"required_next_action: inspect_current_file",
		"forbidden_next_action: retry_same_patch",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q: %s", want, output)
		}
	}
}

func TestDeepSeekFormatsMalformedPatchFailure(t *testing.T) {
	adapter := Get(DeepSeekName)
	output := adapter.FormatToolOutput(ToolDescriptor{
		Name: "apply_patch",
		Kind: "patch",
	}, "invalid patch: missing *** Begin Patch")
	for _, want := range []string{
		"APPLY_PATCH_MALFORMED",
		"required_next_action: regenerate_complete_freeform_patch",
		"forbidden_next_action: send_json_or_markdown_as_patch",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q: %s", want, output)
		}
	}
}

func TestDeepSeekFormatsSuccessfulPatchWithStopProtocol(t *testing.T) {
	adapter := Get(DeepSeekName)
	output := adapter.FormatToolOutput(ToolDescriptor{
		Name: "apply_patch",
		Kind: "patch",
	}, "Exit code: 0\nOutput:\nSuccess. Updated the following files:\nM README.md")
	for _, want := range []string{
		"APPLY_PATCH_SUCCEEDED",
		"file_edit_state: completed",
		"forbidden_next_action: patch_same_file_again_without_user_request",
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
		"node -e \"require('fs').writeFileSync('README.md','hello')\"",
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
