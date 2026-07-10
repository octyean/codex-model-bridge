package server

import (
	"testing"

	"codex-bridge/internal/codex"
)

func TestNormalizeStructuredOutputItemsValidatesArbitrarySchema(t *testing.T) {
	items := []codex.ResponseItem{{
		"type": "message",
		"content": []any{map[string]any{
			"type": "output_text",
			"text": "```json\n{\"tags\":[\"bridge\"],\"count\":2,\"name\":\"result\"}\n```",
		}},
	}}
	format := map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":  map[string]any{"type": "string"},
					"count": map[string]any{"type": "integer", "minimum": 1},
					"tags":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
				"required":             []any{"name", "count", "tags"},
				"additionalProperties": false,
			},
		},
	}

	if err := normalizeStructuredOutputItems(items, format); err != nil {
		t.Fatal(err)
	}
	if got := messageOutputText(items[0]); got != `{"count":2,"name":"result","tags":["bridge"]}` {
		t.Fatalf("normalized output = %q", got)
	}
}

func TestNormalizeStructuredOutputItemsRejectsSchemaMismatch(t *testing.T) {
	items := []codex.ResponseItem{{
		"type": "message",
		"content": []any{map[string]any{
			"type": "output_text",
			"text": `{"count":0}`,
		}},
	}}
	format := map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"schema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"count": map[string]any{"type": "integer", "minimum": 1}},
				"required":   []any{"count"},
			},
		},
	}

	if err := normalizeStructuredOutputItems(items, format); err == nil {
		t.Fatal("schema mismatch should fail")
	}
}
