package server

import (
	"log/slog"
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

	finalState, finalShape, err := s.streamInternalToolRounds(r, writer, chatReq, provider, toolCtx, adapter, requestID, req.Model, profile, shape)
	if err != nil {
		_ = writer.Event(map[string]any{"type": "response.failed", "response": map[string]any{"id": respID, "error": map[string]any{"message": err.Error(), "type": "server_error"}}})
		return
	}
	items := finalState.Done()
	for i, item := range items {
		alreadyAdded := (item["id"] == "msg_0" && finalState.textAdded) || (item["id"] == "rs_0" && finalState.reasoningAdded)
		for _, event := range outputDoneEvents(item, i, alreadyAdded) {
			_ = writer.Event(event)
		}
	}
	_ = writer.Event(map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id": respID, "object": "response", "created_at": createdAt, "model": req.Model, "status": "completed", "output": items,
		},
	})
	s.logUsage(requestID, req.Model, profile, adapter, finalShape, providers.NormalizedUsage{})
	s.logger.Info("request_completed", slog.String("request_id", requestID), slog.String("status", "completed"), slog.Int("tool_call_count", finalState.ToolCallCount()))
}

func (s *Server) streamInternalToolRounds(r *http.Request, writer *codex.SSEWriter, chatReq providers.ChatCompletionRequest, provider providers.ChatProvider, toolCtx tools.Context, adapter adapters.Adapter, requestID string, model string, profile string, shape optimization.Shape) (*streamState, optimization.Shape, error) {
	currentReq := chatReq
	finalState, err := s.streamVisibleMessage(r, writer, currentReq, provider, toolCtx, adapter, requestID, model, profile, true)
	if err != nil {
		return nil, shape, err
	}
	for {
		followUpReq, ok := s.internalToolFollowUpRequest(r.Context(), currentReq, chatMessageFromStreamState(finalState))
		if !ok {
			return finalState, shape, nil
		}
		shape = optimization.CaptureShape(followUpReq)
		currentReq = followUpReq
		finalState, err = s.streamVisibleMessage(r, writer, currentReq, provider, toolCtx, adapter, requestID, model, profile, true)
		if err != nil {
			return nil, shape, err
		}
	}
	return finalState, shape, nil
}

func (s *Server) streamVisibleMessage(r *http.Request, writer *codex.SSEWriter, chatReq providers.ChatCompletionRequest, provider providers.ChatProvider, toolCtx tools.Context, adapter adapters.Adapter, requestID string, model string, profile string, hideInternalTools bool) (*streamState, error) {
	startedAt := time.Now()
	stream, err := provider.Stream(r.Context(), chatReq)
	if err != nil {
		return nil, err
	}
	s.logger.Info("upstream_stream_opened",
		slog.String("request_id", requestID),
		slog.Int64("elapsed_ms", time.Since(startedAt).Milliseconds()),
	)
	state := newStreamState(toolCtx, adapter, requestID, model, profile, s.logger)
	firstChunk := true
	for event := range stream {
		if event.Err != nil {
			return nil, event.Err
		}
		if event.Done {
			break
		}
		if firstChunk {
			firstChunk = false
			s.logger.Info("upstream_stream_first_chunk",
				slog.String("request_id", requestID),
				slog.Int64("elapsed_ms", time.Since(startedAt).Milliseconds()),
			)
		}
		for _, out := range state.AddChunk(event.Chunk) {
			if hideInternalTools && isInternalToolEvent(out) {
				continue
			}
			_ = writer.Event(out)
		}
	}
	return state, nil
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
