package providers

import (
	"encoding/json"
	"testing"
)

func TestChatCompletionsURL(t *testing.T) {
	tests := []struct {
		name string
		base string
		want string
	}{
		{name: "base", base: "https://api.deepseek.com", want: "https://api.deepseek.com/chat/completions"},
		{name: "v1", base: "https://example.test/v1", want: "https://example.test/v1/chat/completions"},
		{name: "full path", base: "https://example.test/v1/chat/completions", want: "https://example.test/v1/chat/completions"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := chatCompletionsURL(tc.base); got != tc.want {
				t.Fatalf("url = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDeepSeekPrepareRequestStabilizesToolsAndRepairsToolResults(t *testing.T) {
	req := ChatCompletionRequest{
		Model:                    "deepseek-v4-flash",
		Stream:                   true,
		AssistantToolContentNull: true,
		Messages: []ChatMessage{
			{Role: "user", Content: "edit"},
			{Role: "assistant", ToolCalls: []ChatToolCall{{
				ID: "call_1", Type: "function",
				Function: ChatCallFunction{Name: "apply_patch", Arguments: `{"input":"*** Begin Patch\n*** End Patch\n"}`},
			}}},
			{Role: "user", Content: "continue"},
		},
		Tools: []ChatTool{
			{Type: "function", Function: ChatFunction{Name: "z_tool"}},
			{Type: "function", Function: ChatFunction{Name: "a_tool"}},
		},
	}
	wire := prepareRequest(req)
	if wire.Messages[1].Content != nil {
		t.Fatalf("assistant tool_calls content = %#v, want nil", wire.Messages[1].Content)
	}
}

func TestDefaultProfileKeepsAssistantToolContentString(t *testing.T) {
	wire := prepareRequest(ChatCompletionRequest{
		Model: "generic-model",
		Messages: []ChatMessage{
			{Role: "assistant", ToolCalls: []ChatToolCall{{
				ID: "call_1", Type: "function",
				Function: ChatCallFunction{Name: "tool", Arguments: `{}`},
			}}},
		},
	})
	if got, ok := wire.Messages[0].Content.(string); !ok || got != "" {
		t.Fatalf("default assistant tool content = %#v, want empty string", wire.Messages[0].Content)
	}
}

func TestNormalizeUsageDeepSeekAndNestedShapes(t *testing.T) {
	deepseek := NormalizeUsage(map[string]any{
		"prompt_tokens":            float64(1000),
		"completion_tokens":        float64(200),
		"total_tokens":             float64(1200),
		"prompt_cache_hit_tokens":  float64(900),
		"prompt_cache_miss_tokens": float64(100),
	})
	if deepseek.CachedInputTokens != 900 || deepseek.FreshInputTokens != 100 {
		t.Fatalf("deepseek cache = %d/%d", deepseek.CachedInputTokens, deepseek.FreshInputTokens)
	}

	nestedRaw := json.RawMessage(`{
		"prompt_tokens": 1000,
		"completion_tokens": 200,
		"total_tokens": 1200,
		"prompt_tokens_details": {"cached_tokens": 600},
		"completion_tokens_details": {"reasoning_tokens": 50}
	}`)
	nested := NormalizeUsage(nestedRaw)
	if nested.CachedInputTokens != 600 || nested.FreshInputTokens != 400 || nested.ReasoningTokens != 50 {
		t.Fatalf("nested usage = %#v", nested)
	}
}
