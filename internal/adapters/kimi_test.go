package adapters

import (
	"strings"
	"testing"

	"codex-bridge/internal/providers"
)

func TestKimiPrepareRequestAddsToolDiscipline(t *testing.T) {
	adapter := Get(KimiName)
	prepared := adapter.PrepareChatRequest(providers.ChatCompletionRequest{
		Model: "kimi-for-coding",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "edit a file"},
		},
		Tools: []providers.ChatTool{{
			Type: "function",
			Function: providers.ChatFunction{
				Name: "codex_text_editor",
			},
		}},
		Stream: true,
	})
	if prepared.StreamOptions == nil || !prepared.StreamOptions.IncludeUsage {
		t.Fatalf("kimi stream request should include usage")
	}
	if !prepared.AssistantToolContentNull {
		t.Fatalf("kimi adapter should request null assistant tool content")
	}
	if len(prepared.Messages) == 0 || prepared.Messages[0].Role != "system" {
		t.Fatalf("missing system discipline note: %#v", prepared.Messages)
	}
	text, _ := prepared.Messages[0].Content.(string)
	for _, want := range []string{"KIMI_CODEX_TOOL_DISCIPLINE", "codex_text_editor", "Never call shell for file mutations"} {
		if !strings.Contains(text, want) {
			t.Fatalf("discipline note missing %q: %s", want, text)
		}
	}
}

func TestKimiToolPolicyBlocksManualShellWrites(t *testing.T) {
	policy := Get(KimiName).ToolPolicy()
	if got := policy.BlockedShellOutput("cat > README.md <<'EOF'\nhello\nEOF"); !strings.Contains(got, "SHELL_FILE_WRITE_BLOCKED") {
		t.Fatalf("command should be blocked: %q", got)
	}
	if got := policy.BlockedShellOutput("sed -n '1,80p' README.md"); got != "" {
		t.Fatalf("read command should be allowed: %q", got)
	}
}
