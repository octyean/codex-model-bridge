package server

import (
	"context"
	"encoding/json"
	"testing"

	"codex-bridge/internal/providers"
	"codex-bridge/internal/tools"
)

func TestStreamStateUsesRequestScopedTextItemID(t *testing.T) {
	chunk := chatChunk(t, `{"choices":[{"delta":{"content":"hello"}}]}`)

	first := newStreamState(context.Background(), tools.Context{}, nil, "req_one", "model", "default", nil, nil)
	firstEvents := first.AddChunk(chunk)
	firstItems := first.Done()

	second := newStreamState(context.Background(), tools.Context{}, nil, "req_two", "model", "default", nil, nil)
	secondEvents := second.AddChunk(chunk)

	firstID := firstEvents[0]["item"].(map[string]any)["id"]
	secondID := secondEvents[0]["item"].(map[string]any)["id"]
	if firstID == secondID {
		t.Fatalf("text item id reused across requests: %v", firstID)
	}
	if firstEvents[1]["item_id"] != firstID || firstEvents[2]["item_id"] != firstID || firstItems[0]["id"] != firstID {
		t.Fatalf("text item id is not stable within one stream: events=%v items=%v", firstEvents, firstItems)
	}
}

func chatChunk(t *testing.T, raw string) providers.ChatCompletionChunk {
	t.Helper()
	var chunk providers.ChatCompletionChunk
	if err := json.Unmarshal([]byte(raw), &chunk); err != nil {
		t.Fatal(err)
	}
	return chunk
}
