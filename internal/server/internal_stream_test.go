package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
	"codex-bridge/internal/optimization"
	"codex-bridge/internal/providers"
	"codex-bridge/internal/toollog"
	"codex-bridge/internal/tools"
)

func TestStreamInternalToolRoundsTreatsContentOnlyAsFinal(t *testing.T) {
	provider := &streamSequenceProvider{streams: [][]providers.StreamEvent{
		{streamTextChunk(t, "阶段完成。")},
	}}
	server := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/v1/responses", nil)
	writer := codex.NewSSEWriter(recorder)
	toolCtx := tools.Context{Tools: map[string]tools.Entry{
		"apply_patch": {Descriptor: adapters.ToolDescriptor{Name: "apply_patch", Kind: tools.KindTextEditor, SideEffect: tools.SideEffectWriteFiles}},
	}}

	state, _, _, err := server.streamInternalToolRounds(
		request,
		writer,
		"resp_test",
		123,
		providers.ChatCompletionRequest{Model: "kimi-for-coding"},
		provider,
		toolCtx,
		adapters.Get(adapters.KimiName),
		"req_test",
		"",
		"gpt-5.3-codex",
		"kimi",
		optimization.Shape{},
		&internalToolTrace{},
		toollog.OutputContext{RequestID: "req_test", Model: "gpt-5.3-codex", UpstreamModel: "kimi-for-coding", Profile: "kimi"},
	)
	if err != nil {
		t.Fatalf("streamInternalToolRounds returned error: %v", err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("stream calls = %d, want 1", len(provider.requests))
	}

	items := state.Done()
	if len(items) != 1 {
		t.Fatalf("final items = %d, want 1", len(items))
	}
	text := responseItemText(t, items[0])
	if text != "阶段完成。" {
		t.Fatalf("final text = %q", text)
	}
	if !strings.Contains(recorder.Body.String(), `"delta":"阶段完成。"`) {
		t.Fatalf("text was not streamed before completion: %s", recorder.Body.String())
	}
}

func TestStreamInternalToolRoundsRetriesPlainTextThenAcceptsTaskEnd(t *testing.T) {
	progress := "我会在完成后调用 " + tools.TaskEndToolName
	sanitizedProgress := "我会在完成后调用 " + taskProtocolPublicToolName
	provider := &streamSequenceProvider{streams: [][]providers.StreamEvent{
		{streamTextChunk(t, progress)},
		{streamToolCallChunk(t, tools.TaskEndToolName, `{"status":"completed","result":"bridge-task-ok"}`)},
	}}
	server := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/v1/responses", nil)
	writer := codex.NewSSEWriter(recorder)
	toolCtx := tools.Context{}
	chatTools := tools.AddTaskEndTool(nil, &toolCtx)
	trace := &internalToolTrace{}

	state, _, _, err := server.streamInternalToolRounds(
		request,
		writer,
		"resp_test",
		123,
		providers.ChatCompletionRequest{Model: "kimi-for-coding", Tools: chatTools},
		provider,
		toolCtx,
		adapters.Get(adapters.KimiName),
		"req_test",
		"",
		"gpt-5.3-codex",
		"kimi",
		optimization.Shape{},
		trace,
		toollog.OutputContext{RequestID: "req_test", Model: "gpt-5.3-codex", UpstreamModel: "kimi-for-coding", Profile: "kimi"},
	)
	if err != nil {
		t.Fatalf("streamInternalToolRounds returned error: %v", err)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("stream calls = %d, want 2", len(provider.requests))
	}
	retryMessages := provider.requests[1].Messages
	if len(retryMessages) == 0 || !strings.Contains(messageText(retryMessages[len(retryMessages)-1].Content), "TASK_PROTOCOL_RETRY") {
		t.Fatalf("retry messages = %#v", retryMessages)
	}
	if len(trace.items) != 1 || responseItemText(t, trace.items[0]) != sanitizedProgress {
		t.Fatalf("streamed progress items = %#v", trace.items)
	}
	items := state.Done()
	if len(items) != 1 || responseItemText(t, items[0]) != "bridge-task-ok" {
		t.Fatalf("task end result = %#v", items)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"delta":"`+sanitizedProgress+`"`) {
		t.Fatalf("progress text was not streamed: %s", body)
	}
	if strings.Contains(body, tools.TaskEndToolName) {
		t.Fatalf("task end tool leaked to client: %s", body)
	}
}

func TestStreamInternalToolRoundsFailsAfterOneTaskProtocolRetry(t *testing.T) {
	provider := &streamSequenceProvider{streams: [][]providers.StreamEvent{
		{streamTextChunk(t, "later")},
		{streamTextChunk(t, "still later")},
	}}
	server := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := httptest.NewRequest("POST", "/v1/responses", nil)
	toolCtx := tools.Context{}
	chatTools := tools.AddTaskEndTool(nil, &toolCtx)
	trace := &internalToolTrace{}

	_, _, _, err := server.streamInternalToolRounds(
		request,
		codex.NewSSEWriter(httptest.NewRecorder()),
		"resp_test",
		123,
		providers.ChatCompletionRequest{Model: "kimi-for-coding", Tools: chatTools},
		provider,
		toolCtx,
		adapters.Get(adapters.KimiName),
		"req_test",
		"",
		"gpt-5.3-codex",
		"kimi",
		optimization.Shape{},
		trace,
		toollog.OutputContext{RequestID: "req_test", Model: "gpt-5.3-codex", UpstreamModel: "kimi-for-coding", Profile: "kimi"},
	)
	if !errors.Is(err, errTaskProtocolViolation) {
		t.Fatalf("error = %v, want task protocol violation", err)
	}
	if len(provider.requests) != maxTaskProtocolRetries+1 {
		t.Fatalf("stream calls = %d, want %d", len(provider.requests), maxTaskProtocolRetries+1)
	}
	if len(trace.items) != 1 || responseItemText(t, trace.items[0]) != "later" {
		t.Fatalf("visible progress items = %#v", trace.items)
	}
}

type streamSequenceProvider struct {
	streams  [][]providers.StreamEvent
	requests []providers.ChatCompletionRequest
}

func (p *streamSequenceProvider) Create(context.Context, providers.ChatCompletionRequest) (*providers.ChatCompletionResponse, error) {
	return nil, fmt.Errorf("Create should not be called")
}

func (p *streamSequenceProvider) Stream(_ context.Context, req providers.ChatCompletionRequest) (<-chan providers.StreamEvent, error) {
	p.requests = append(p.requests, req)
	var events []providers.StreamEvent
	if len(p.streams) > 0 {
		events = p.streams[0]
		p.streams = p.streams[1:]
	}
	ch := make(chan providers.StreamEvent, len(events)+1)
	for _, event := range events {
		ch <- event
	}
	ch <- providers.StreamEvent{Done: true}
	close(ch)
	return ch, nil
}

func (p *streamSequenceProvider) ListModels(context.Context) (*providers.ModelsResponse, error) {
	return &providers.ModelsResponse{}, nil
}

func streamTextChunk(t *testing.T, text string) providers.StreamEvent {
	t.Helper()
	value, err := json.Marshal(text)
	if err != nil {
		t.Fatalf("marshal stream text: %v", err)
	}
	var chunk providers.ChatCompletionChunk
	if err := json.Unmarshal([]byte(`{"choices":[{"delta":{"content":`+string(value)+`}}]}`), &chunk); err != nil {
		t.Fatalf("unmarshal stream chunk: %v", err)
	}
	return providers.StreamEvent{Chunk: chunk}
}

func streamToolCallChunk(t *testing.T, name string, arguments string) providers.StreamEvent {
	t.Helper()
	value, err := json.Marshal(map[string]any{
		"choices": []any{map[string]any{
			"delta": map[string]any{
				"tool_calls": []any{map[string]any{
					"index": 0,
					"id":    "call_task_end",
					"type":  "function",
					"function": map[string]any{
						"name":      name,
						"arguments": arguments,
					},
				}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("marshal stream tool call: %v", err)
	}
	var chunk providers.ChatCompletionChunk
	if err := json.Unmarshal(value, &chunk); err != nil {
		t.Fatalf("unmarshal stream tool call: %v", err)
	}
	return providers.StreamEvent{Chunk: chunk}
}

func responseItemText(t *testing.T, item codex.ResponseItem) string {
	t.Helper()
	content, ok := item["content"].([]map[string]string)
	if !ok || len(content) == 0 {
		t.Fatalf("message content has unexpected shape: %#v", item["content"])
	}
	return content[0]["text"]
}
