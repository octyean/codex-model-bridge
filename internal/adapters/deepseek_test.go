package adapters

import (
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
