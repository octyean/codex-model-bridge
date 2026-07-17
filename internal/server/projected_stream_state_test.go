package server

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
	"codex-bridge/internal/tools"
)

func TestProjectedStreamStateKeepsOneResponseAndGlobalOutputIndexes(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := codex.NewSSEWriter(recorder)
	toolCtx := tools.Context{}
	tools.AddWebSearchProxy(nil, &toolCtx)
	tools.AddTaskEndTool(nil, &toolCtx)
	state := newProjectedStreamState(
		writer,
		context.Background(),
		toolCtx,
		adapters.Get(adapters.KimiName),
		"req_test",
		"gpt-5.3-codex",
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		true,
	)

	round0 := projectedStreamRound{sequence: 0, indexes: map[int]int{}, hidden: map[int]bool{}}
	state.handleEvent(map[string]any{
		"type":     "response.created",
		"response": map[string]any{"id": "resp_first", "model": "kimi-for-coding"},
	}, &round0)
	state.handleEvent(map[string]any{
		"type":         "response.output_item.added",
		"output_index": 0,
		"item":         map[string]any{"id": "reasoning_1", "type": "reasoning"},
	}, &round0)
	state.handleEvent(map[string]any{
		"type":         "response.output_item.done",
		"output_index": 0,
		"item": map[string]any{
			"id":   "reasoning_1",
			"type": "reasoning",
			"summary": []any{map[string]any{
				"type": "summary_text",
				"text": "call " + tools.TaskEndToolName + " when done",
			}},
		},
	}, &round0)
	state.handleEvent(map[string]any{
		"type":         "response.output_item.added",
		"output_index": 1,
		"item":         map[string]any{"id": "web_1", "type": "function_call", "name": tools.WebSearchProxyToolName},
	}, &round0)
	state.handleEvent(map[string]any{
		"type":         "response.output_item.done",
		"output_index": 1,
		"item":         map[string]any{"id": "web_1", "type": "function_call", "name": tools.WebSearchProxyToolName, "call_id": "call_web", "arguments": `{}`},
	}, &round0)
	state.handleEvent(map[string]any{
		"type":         "response.output_item.done",
		"output_index": 2,
		"item":         map[string]any{"id": "end_1", "type": "function_call", "name": tools.TaskEndToolName, "call_id": "call_end", "arguments": `{"status":"completed","result":"done"}`},
	}, &round0)

	round1 := projectedStreamRound{sequence: 1, indexes: map[int]int{}, hidden: map[int]bool{}}
	state.handleEvent(map[string]any{
		"type":     "response.created",
		"response": map[string]any{"id": "resp_second", "model": "kimi-for-coding"},
	}, &round1)
	message := map[string]any{
		"id":   "message_1",
		"type": "message",
		"role": "assistant",
		"content": []any{map[string]any{
			"type": "output_text",
			"text": "done",
		}},
	}
	state.handleEvent(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": message}, &round1)
	state.handleEvent(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": message}, &round1)

	completed := state.completedResponse(map[string]any{"id": "resp_second", "model": "kimi-for-coding"})
	output := completed["output"].([]any)
	if len(output) != 2 {
		t.Fatalf("completed output = %#v", output)
	}
	if completed["id"] != "resp_first" {
		t.Fatalf("response id = %#v", completed["id"])
	}
	body := recorder.Body.String()
	if strings.Contains(body, "resp_second") {
		t.Fatalf("second upstream response id leaked: %s", body)
	}
	if strings.Contains(body, tools.WebSearchProxyToolName) {
		t.Fatalf("internal web tool leaked: %s", body)
	}
	if strings.Contains(body, tools.TaskEndToolName) {
		t.Fatalf("task end tool leaked: %s", body)
	}
	if !strings.Contains(body, `"output_index":1`) {
		t.Fatalf("second round did not use global output index: %s", body)
	}
}
