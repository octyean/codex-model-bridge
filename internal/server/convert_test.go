package server

import (
	"testing"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/providers"
	"codex-bridge/internal/tools"
)

func TestStripChatResponseStateRemovesFinalMarker(t *testing.T) {
	text, state := stripChatResponseState("CHAT_RESPONSE_STATE: final\n任务完成。")
	if state != chatResponseStateFinal {
		t.Fatalf("state = %q, want final", state)
	}
	if text != "任务完成。" {
		t.Fatalf("text = %q", text)
	}
}

func TestContentOnlyNeedsRetryWhenToolsAvailableAndStateMissing(t *testing.T) {
	if !contentOnlyNeedsRetry(providers.ChatMessage{Content: "现在跑测试。"}, testToolContext()) {
		t.Fatalf("content-only progress without state should trigger retry")
	}
}

func TestContentOnlyDoesNotRetryForMarkedFinal(t *testing.T) {
	msg := providers.ChatMessage{Content: "CHAT_RESPONSE_STATE: final\n任务完成。"}
	if contentOnlyNeedsRetry(msg, testToolContext()) {
		t.Fatalf("marked final content-only response should not trigger retry")
	}
}

func TestContentOnlyDoesNotRetryWhenToolCallPresent(t *testing.T) {
	msg := providers.ChatMessage{
		Content: "现在跑测试。",
		ToolCalls: []providers.ChatToolCall{{
			ID:   "call_1",
			Type: "function",
			Function: providers.ChatCallFunction{
				Name:      "exec_command",
				Arguments: "{}",
			},
		}},
	}
	if contentOnlyNeedsRetry(msg, testToolContext()) {
		t.Fatalf("progress with tool calls should not trigger retry")
	}
}

func testToolContext() tools.Context {
	return tools.Context{Tools: map[string]tools.Entry{
		"exec_command": {Descriptor: adapters.ToolDescriptor{Name: "exec_command"}},
	}}
}
