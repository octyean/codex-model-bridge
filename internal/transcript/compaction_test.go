package transcript

import (
	"fmt"
	"strings"
	"testing"

	"codex-bridge/internal/providers"
)

func TestCompactChatTranscriptPreservesCompletedToolHistory(t *testing.T) {
	messages := []providers.ChatMessage{{Role: "user", Content: "implement the task"}}
	for index := 0; index < 9; index++ {
		arguments := fmt.Sprintf(`{"path":"file-%d"}`, index)
		callID := fmt.Sprintf("call-%d", index)
		messages = append(messages,
			providers.ChatMessage{Role: "assistant", ToolCalls: []providers.ChatToolCall{{
				ID:   callID,
				Type: "function",
				Function: providers.ChatCallFunction{
					Name:      "read_file",
					Arguments: arguments,
				},
			}}},
			providers.ChatMessage{Role: "tool", ToolCallID: callID, Content: "output-" + callID},
		)
	}

	compacted := compactChatTranscript(messages)
	if len(compacted) != len(messages) {
		t.Fatalf("tool history length = %d, want %d", len(compacted), len(messages))
	}
	for index := range messages {
		if fmt.Sprint(compacted[index].Content) != fmt.Sprint(messages[index].Content) {
			t.Fatalf("message %d content changed", index)
		}
		if len(compacted[index].ToolCalls) != len(messages[index].ToolCalls) {
			t.Fatalf("message %d tool calls changed", index)
		}
	}
}

func TestCompactChatTranscriptKeepsOnlyLatestEnvironmentContext(t *testing.T) {
	messages := []providers.ChatMessage{
		{Role: "user", Content: "first\n<environment_context><cwd>/old</cwd></environment_context>"},
		{Role: "assistant", Content: "working"},
		{Role: "user", Content: "second\n<environment_context><cwd>/new</cwd></environment_context>"},
	}

	compacted := compactChatTranscript(messages)
	text := ""
	for _, message := range compacted {
		text += fmt.Sprint(message.Content) + "\n"
	}
	if strings.Contains(text, "/old") {
		t.Fatal("superseded environment context was retained")
	}
	if !strings.Contains(text, "/new") {
		t.Fatal("latest environment context was removed")
	}
	if !strings.Contains(text, "first") {
		t.Fatal("message text surrounding old environment context was removed")
	}
}
