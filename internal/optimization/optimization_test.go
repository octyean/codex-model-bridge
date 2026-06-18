package optimization

import (
	"encoding/json"
	"testing"

	"codex-bridge/internal/providers"
)

func TestStabilizeToolsSortsAndCanonicalizesParameters(t *testing.T) {
	tools := []providers.ChatTool{
		{
			Type: "function",
			Function: providers.ChatFunction{
				Name:       "z_tool",
				Parameters: json.RawMessage(`{"properties":{"b":{"type":"string"},"a":{"type":"string"}},"type":"object"}`),
			},
		},
		{
			Type: "function",
			Function: providers.ChatFunction{
				Name:       "a_tool",
				Parameters: json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}}}`),
			},
		},
	}

	stable := StabilizeTools(tools)

	if stable[0].Function.Name != "a_tool" || stable[1].Function.Name != "z_tool" {
		t.Fatalf("tools not sorted: %#v", stable)
	}
	want := `{"properties":{"a":{"type":"string"},"b":{"type":"string"}},"type":"object"}`
	if string(stable[1].Function.Parameters) != want {
		t.Fatalf("parameters = %s", stable[1].Function.Parameters)
	}
	if string(tools[0].Function.Parameters) == want {
		t.Fatalf("StabilizeTools mutated caller input")
	}
}

func TestCaptureShapeIgnoresToolOrderAndParameterKeyOrder(t *testing.T) {
	first := providers.ChatCompletionRequest{
		Messages: []providers.ChatMessage{{Role: "system", Content: "system"}},
		Tools: []providers.ChatTool{
			{Type: "function", Function: providers.ChatFunction{Name: "b", Parameters: json.RawMessage(`{"z":1,"a":2}`)}},
			{Type: "function", Function: providers.ChatFunction{Name: "a", Parameters: json.RawMessage(`{"type":"object"}`)}},
		},
	}
	second := providers.ChatCompletionRequest{
		Messages: []providers.ChatMessage{{Role: "system", Content: "system"}},
		Tools: []providers.ChatTool{
			{Type: "function", Function: providers.ChatFunction{Name: "a", Parameters: json.RawMessage(`{"type":"object"}`)}},
			{Type: "function", Function: providers.ChatFunction{Name: "b", Parameters: json.RawMessage(`{"a":2,"z":1}`)}},
		},
	}

	firstShape := CaptureShape(first)
	secondShape := CaptureShape(second)

	if firstShape.ToolsHash != secondShape.ToolsHash || firstShape.PrefixHash != secondShape.PrefixHash {
		t.Fatalf("shape should be stable: first=%#v second=%#v", firstShape, secondShape)
	}
}

func TestTrackerExplainsPrefixChangesAndCacheRate(t *testing.T) {
	tracker := NewTracker()
	shape := Shape{SystemHash: "sys1", ToolsHash: "tools1", PrefixHash: "prefix1"}

	first := tracker.Observe("deepseek", shape, providers.NormalizedUsage{
		CachedInputTokens: 0,
		FreshInputTokens:  100,
	})
	if first.PrefixChanged {
		t.Fatalf("first observation should not be marked changed: %#v", first)
	}

	second := tracker.Observe("deepseek", Shape{SystemHash: "sys2", ToolsHash: "tools1", PrefixHash: "prefix2"}, providers.NormalizedUsage{
		CachedInputTokens: 80,
		FreshInputTokens:  20,
	})
	if !second.PrefixChanged || len(second.PrefixChangeReasons) != 1 || second.PrefixChangeReasons[0] != "system" {
		t.Fatalf("diagnostics = %#v", second)
	}
	if second.CacheHitRatePermille != 800 {
		t.Fatalf("cache hit rate = %d", second.CacheHitRatePermille)
	}
}
