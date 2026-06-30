package adapters

import (
	"strings"
	"testing"

	"codex-bridge/internal/providers"
)

func TestMimoAdapterSupportsImageInput(t *testing.T) {
	adapter := Get(MimoName)
	caps := adapter.Capabilities()
	if adapter.Name() != MimoName {
		t.Fatalf("adapter name = %q", adapter.Name())
	}
	if !HasImageInput(caps) {
		t.Fatalf("mimo adapter should support image input")
	}
	if !caps.SupportsImageDetailOriginal {
		t.Fatalf("mimo adapter should keep original image detail")
	}
}

func TestMimoPrepareRequestAddsToolDiscipline(t *testing.T) {
	adapter := Get(MimoName)
	prepared := adapter.PrepareChatRequest(providers.ChatCompletionRequest{
		Model:    "mimo-v2.5",
		Messages: []providers.ChatMessage{{Role: "user", Content: "edit a file"}},
		Tools: []providers.ChatTool{{
			Type:     "function",
			Function: providers.ChatFunction{Name: "codex_text_editor"},
		}},
	})
	if len(prepared.Messages) == 0 || prepared.Messages[0].Role != "system" {
		t.Fatalf("missing system discipline note: %#v", prepared.Messages)
	}
	text, _ := prepared.Messages[0].Content.(string)
	for _, want := range []string{"MIMO_CODEX_TOOL_DISCIPLINE", "codex_text_editor", "Never call shell for file mutations"} {
		if !strings.Contains(text, want) {
			t.Fatalf("discipline note missing %q: %s", want, text)
		}
	}
}
