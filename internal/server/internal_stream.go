package server

import (
	"net/http"
	"time"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
	"codex-bridge/internal/optimization"
	"codex-bridge/internal/providers"
	"codex-bridge/internal/tools"
)

func (s *Server) streamInternalToolResponse(w http.ResponseWriter, r *http.Request, requestID string, req codex.ResponsesRequest, chatReq providers.ChatCompletionRequest, provider providers.ChatProvider, toolCtx tools.Context, adapter adapters.Adapter, profile string, shape optimization.Shape) {
	writer := codex.NewSSEWriter(w)
	respID := "resp_" + requestID
	createdAt := time.Now().Unix()
	_ = writer.Event(map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id": respID, "object": "response", "created_at": createdAt, "model": req.Model, "status": "in_progress", "output": []any{},
		},
	})
	_ = writer.Event(map[string]any{
		"type": "response.in_progress",
		"response": map[string]any{
			"id": respID, "object": "response", "created_at": createdAt, "model": req.Model, "status": "in_progress", "output": []any{},
		},
	})

	first, err := s.streamVisibleMessage(r, writer, chatReq, provider, toolCtx, adapter, requestID, req.Model, profile, true)
	if err != nil {
		_ = writer.Event(map[string]any{"type": "response.failed", "response": map[string]any{"id": respID, "error": map[string]any{"message": err.Error(), "type": "server_error"}}})
		return
	}
	final := first
	if followUpReq, ok := s.internalToolFollowUpRequest(r.Context(), chatReq, first); ok {
		shape = optimization.CaptureShape(followUpReq)
		final, err = s.streamVisibleMessage(r, writer, followUpReq, provider, toolCtx, adapter, requestID, req.Model, profile, false)
		if err != nil {
			_ = writer.Event(map[string]any{"type": "response.failed", "response": map[string]any{"id": respID, "error": map[string]any{"message": err.Error(), "type": "server_error"}}})
			return
		}
	}
	items := responseItemsFromMessage(final, toolCtx, adapter, requestID, req.Model, profile, s.logger)
	_ = writer.Event(map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id": respID, "object": "response", "created_at": createdAt, "model": req.Model, "status": "completed", "output": items,
		},
	})
	s.logUsage(requestID, req.Model, profile, adapter, shape, providers.NormalizedUsage{})
}

func (s *Server) streamVisibleMessage(r *http.Request, writer *codex.SSEWriter, chatReq providers.ChatCompletionRequest, provider providers.ChatProvider, toolCtx tools.Context, adapter adapters.Adapter, requestID string, model string, profile string, hideInternalTools bool) (providers.ChatMessage, error) {
	stream, err := provider.Stream(r.Context(), chatReq)
	if err != nil {
		return providers.ChatMessage{}, err
	}
	state := newStreamState(toolCtx, adapter, requestID, model, profile, s.logger)
	for event := range stream {
		if event.Err != nil {
			return providers.ChatMessage{}, event.Err
		}
		if event.Done {
			break
		}
		for _, out := range state.AddChunk(event.Chunk) {
			if hideInternalTools && isInternalToolEvent(out) {
				continue
			}
			_ = writer.Event(out)
		}
	}
	return chatMessageFromStreamState(state), nil
}

func isInternalToolEvent(event map[string]any) bool {
	item, _ := event["item"].(map[string]any)
	name, _ := item["name"].(string)
	return name == bridgeWebSearchTool
}

func chatMessageFromStreamState(state *streamState) providers.ChatMessage {
	if len(state.toolCalls) == 0 {
		return providers.ChatMessage{Role: "assistant", Content: state.text, ReasoningContent: state.reasoning}
	}
	calls := make([]providers.ChatToolCall, 0, len(state.toolCalls))
	for i := 0; i < len(state.toolCalls); i++ {
		call, ok := state.toolCalls[i]
		if !ok {
			continue
		}
		calls = append(calls, providers.ChatToolCall{
			ID:   call.id,
			Type: "function",
			Function: providers.ChatCallFunction{
				Name:      call.name,
				Arguments: call.arguments,
			},
		})
	}
	return providers.ChatMessage{Role: "assistant", ReasoningContent: state.reasoning, ToolCalls: calls}
}
