package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
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
	ch := make(chan providers.StreamEvent, len(events))
	for _, event := range events {
		ch <- event
	}
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

func responseItemText(t *testing.T, item codex.ResponseItem) string {
	t.Helper()
	content, ok := item["content"].([]map[string]string)
	if !ok || len(content) == 0 {
		t.Fatalf("message content has unexpected shape: %#v", item["content"])
	}
	return content[0]["text"]
}
