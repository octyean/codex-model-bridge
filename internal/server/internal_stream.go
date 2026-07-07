package server

import (
	"log/slog"
	"net/http"
	"time"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
	"codex-bridge/internal/incidentlog"
	"codex-bridge/internal/optimization"
	"codex-bridge/internal/providers"
	"codex-bridge/internal/toollog"
	"codex-bridge/internal/tools"
)

func (s *Server) streamInternalToolResponse(w http.ResponseWriter, r *http.Request, requestID string, sessionID string, req codex.ResponsesRequest, chatReq providers.ChatCompletionRequest, provider providers.ChatProvider, toolCtx tools.Context, adapter adapters.Adapter, profile string, shape optimization.Shape, dumpPath string) {
	writer := codex.NewSSEWriter(w)
	respID := "resp_" + requestID
	createdAt := time.Now().Unix()
	_ = writer.Event(responseCreatedEvent(respID, createdAt, req.Model))
	_ = writer.Event(responseInProgressEvent(respID, createdAt, req.Model))

	trace := &internalToolTrace{}
	finalState, finalShape, err := s.streamInternalToolRounds(r, writer, respID, createdAt, chatReq, provider, toolCtx, adapter, requestID, sessionID, req.Model, profile, shape, trace, toollog.OutputContext{
		RequestID:      requestID,
		Model:          req.Model,
		UpstreamModel:  chatReq.Model,
		Profile:        profile,
		RequestSummary: incidentlog.RequestSummary(req.Raw),
	})
	if err != nil {
		if requestCanceled(r, err) {
			return
		}
		extra := map[string]any{"stream": true, "internal_tools": true}
		s.writeBridgeFailure(sessionID, requestID, req.Model, chatReq.Model, profile, http.StatusBadGateway, err.Error(), extra)
		incidentlog.Write("upstream_stream_event_error", s.incidentRecord(r, req, requestID, profile, dumpPath, map[string]any{"error": err.Error(), "stream": true, "internal_tools": true}))
		_ = writer.Event(map[string]any{"type": "response.failed", "response": map[string]any{"id": respID, "error": map[string]any{"message": err.Error(), "type": "server_error"}}})
		return
	}
	items := finalState.Done()
	completedItems := append(append([]codex.ResponseItem{}, trace.items...), items...)
	outputIndexOffset := len(trace.items)
	if emptyOutput(completedItems) {
		incidentlog.Write("empty_stream_response", s.incidentRecord(r, req, requestID, profile, dumpPath, map[string]any{"stream": true, "internal_tools": true, "output": outputSummary(completedItems, providers.NormalizedUsage{})}))
	}
	for i, item := range items {
		alreadyAdded := finalState.eventsEmitted && ((item["id"] == finalState.textItemID && finalState.textAdded) || (item["id"] == finalState.reasoningItemID && finalState.reasoningAdded))
		for _, event := range outputDoneEvents(item, i+outputIndexOffset, alreadyAdded) {
			_ = writer.Event(event)
		}
	}
	responseCompleted := map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id": respID, "object": "response", "created_at": createdAt, "model": req.Model, "status": "completed", "output": completedItems,
		},
	}
	_ = writer.Event(responseCompleted)
	s.writeBridgeResponse(sessionID, requestID, req.Model, chatReq.Model, profile, responseCompleted["response"], map[string]any{"stream": true, "internal_tools": true})
	s.logUsage(requestID, req.Model, profile, adapter, finalShape, providers.NormalizedUsage{})
	s.logger.Info("request_completed", slog.String("request_id", requestID), slog.String("status", "completed"), slog.Int("tool_call_count", finalState.ToolCallCount()))
}

func (s *Server) streamInternalToolRounds(r *http.Request, writer *codex.SSEWriter, respID string, createdAt int64, chatReq providers.ChatCompletionRequest, provider providers.ChatProvider, toolCtx tools.Context, adapter adapters.Adapter, requestID string, sessionID string, model string, profile string, shape optimization.Shape, trace *internalToolTrace, logCtx toollog.OutputContext) (*streamState, optimization.Shape, error) {
	currentReq := chatReq
	localResolver := s.localToolResultResolver(logCtx, toolCtx)
	finalState, err := s.streamVisibleMessage(r, writer, respID, createdAt, currentReq, provider, toolCtx, adapter, requestID, sessionID, model, profile, "initial", true, localResolver, false)
	if err != nil {
		return nil, shape, err
	}
	sequence := 0
	for {
		trace.emit(writer, chatMessageFromStreamState(finalState), toolCtx)
		followUpReq, ok := s.internalToolFollowUpRequest(r.Context(), currentReq, chatMessageFromStreamState(finalState), toolCtx, adapter, logCtx)
		if !ok {
			return finalState, shape, nil
		}
		sequence++
		s.writePromptRequest(sessionID, requestID, model, followUpReq.Model, profile, "internal_tool_followup", providers.PreparedChatRequest(followUpReq), map[string]any{"sequence": sequence, "stream": true})
		shape = optimization.CaptureShape(followUpReq)
		currentReq = followUpReq
		finalState, err = s.streamVisibleMessage(r, writer, respID, createdAt, currentReq, provider, toolCtx, adapter, requestID, sessionID, model, profile, "internal_tool_followup", true, localResolver, false)
		if err != nil {
			return nil, shape, err
		}
	}
}

type internalToolTrace struct {
	items []codex.ResponseItem
}

func (t *internalToolTrace) emit(writer *codex.SSEWriter, message providers.ChatMessage, toolCtx tools.Context) {
	for _, call := range message.ToolCalls {
		item, ok := internalToolCallItem(call, toolCtx)
		if !ok {
			continue
		}
		outputIndex := len(t.items)
		t.items = append(t.items, item)
		for _, event := range outputDoneEvents(item, outputIndex, false) {
			_ = writer.Event(event)
		}
	}
}

func internalToolCallItem(call providers.ChatToolCall, toolCtx tools.Context) (codex.ResponseItem, bool) {
	entry := toolCtx.Entry(call.Function.Name)
	switch entry.Kind() {
	case tools.KindWebSearch:
	default:
		return nil, false
	}
	arguments := tools.CanonicalArguments(entry, call.Function.Arguments)
	return codex.ResponseItem{
		"id":        "fco_" + call.ID,
		"type":      "function_call_output",
		"call_id":   call.ID,
		"name":      entry.Name(),
		"arguments": arguments,
		"output":    entry.Name() + ": " + arguments,
	}, true
}

func (s *Server) streamVisibleMessage(r *http.Request, writer *codex.SSEWriter, respID string, createdAt int64, chatReq providers.ChatCompletionRequest, provider providers.ChatProvider, toolCtx tools.Context, adapter adapters.Adapter, requestID string, sessionID string, model string, profile string, stage string, hideInternalTools bool, localResolver toolCallLocalResolver, emitEvents bool) (*streamState, error) {
	startedAt := time.Now()
	stream, err := provider.Stream(r.Context(), chatReq)
	if err != nil {
		s.writePromptFailure(sessionID, requestID, model, chatReq.Model, profile, stage, err.Error(), map[string]any{"stream": true})
		return nil, err
	}
	s.logger.Info("upstream_stream_opened",
		slog.String("request_id", requestID),
		slog.Int64("elapsed_ms", time.Since(startedAt).Milliseconds()),
	)
	state := newStreamState(r.Context(), toolCtx, adapter, requestID, model, profile, s.logger, localResolver)
	firstChunk := true
	streamSeq := 0
	heartbeat := time.NewTicker(3 * time.Second)
	defer heartbeat.Stop()
streamLoop:
	for {
		select {
		case event, ok := <-stream:
			if !ok {
				break streamLoop
			}
			if event.Err != nil {
				s.writePromptFailure(sessionID, requestID, model, chatReq.Model, profile, stage, event.Err.Error(), map[string]any{"stream": true})
				return nil, event.Err
			}
			if event.Done {
				break streamLoop
			}
			streamSeq++
			s.writePromptStreamEvent(sessionID, requestID, model, chatReq.Model, profile, stage, streamSeq, event.Chunk)
			if firstChunk {
				firstChunk = false
				s.logger.Info("upstream_stream_first_chunk",
					slog.String("request_id", requestID),
					slog.Int64("elapsed_ms", time.Since(startedAt).Milliseconds()),
				)
			}
			for _, out := range state.AddChunk(event.Chunk) {
				if !emitEvents {
					continue
				}
				if hideInternalTools && isInternalToolEvent(out) {
					continue
				}
				_ = writer.Event(out)
				state.eventsEmitted = true
			}
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-heartbeat.C:
			_ = writer.Event(responseInProgressEvent(respID, createdAt, model))
		}
	}
	s.writePromptResponse(sessionID, requestID, model, chatReq.Model, profile, stage, map[string]any{
		"stream":      true,
		"chunk_count": streamSeq,
		"message":     chatMessageFromStreamState(state),
	}, nil)
	return state, nil
}

func isInternalToolEvent(event map[string]any) bool {
	item, _ := event["item"].(map[string]any)
	name, _ := item["name"].(string)
	return name == tools.WebSearchProxyToolName || name == tools.ReadFileToolName || name == tools.ListFilesToolName || name == tools.FileSearchToolName
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
