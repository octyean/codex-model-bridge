package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
	"codex-bridge/internal/config"
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
	finalState, finalShape, usage, err := s.streamInternalToolRounds(r, writer, respID, createdAt, chatReq, provider, toolCtx, adapter, requestID, sessionID, req.Model, profile, shape, trace, toollog.OutputContext{
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
		_ = writer.Event(map[string]any{"type": "response.failed", "response": map[string]any{"id": respID, "error": map[string]any{"message": err.Error(), "type": responseFailureType(err)}}})
		return
	}
	items := enforceStructuredOutput(finalState.Done(), chatReq.ResponseFormat)
	completedItems := append(append([]codex.ResponseItem{}, trace.items...), items...)
	outputIndexOffset := len(trace.items)
	if emptyOutput(completedItems) {
		incidentlog.Write("empty_stream_response", s.incidentRecord(r, req, requestID, profile, dumpPath, map[string]any{"stream": true, "internal_tools": true, "output": outputSummary(completedItems, usage)}))
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
			"usage": codexUsage(usage),
		},
	}
	_ = writer.Event(responseCompleted)
	s.writeBridgeResponse(sessionID, requestID, req.Model, chatReq.Model, profile, responseCompleted["response"], map[string]any{"stream": true, "internal_tools": true})
	s.logUsage(requestID, req.Model, chatReq.Model, profile, config.ExecutionModeChatCompletions, "", -1, adapter, finalShape, usage)
	s.logger.Info("request_completed", slog.String("request_id", requestID), slog.String("status", "completed"), slog.Int("tool_call_count", finalState.ToolCallCount()))
}

func (s *Server) streamInternalToolRounds(r *http.Request, writer *codex.SSEWriter, respID string, createdAt int64, chatReq providers.ChatCompletionRequest, provider providers.ChatProvider, toolCtx tools.Context, adapter adapters.Adapter, requestID string, sessionID string, model string, profile string, shape optimization.Shape, trace *internalToolTrace, logCtx toollog.OutputContext) (*streamState, optimization.Shape, providers.NormalizedUsage, error) {
	currentReq := chatReq
	localResolver := s.localToolResultResolver(logCtx, toolCtx)
	var totalUsage providers.NormalizedUsage
	finalState, usage, err := s.streamVisibleMessage(r, writer, respID, createdAt, currentReq, provider, toolCtx, adapter, requestID, sessionID, model, profile, "initial", true, localResolver, true, 0)
	if err != nil {
		return nil, shape, totalUsage, err
	}
	totalUsage = addUsage(totalUsage, usage)
	sequence := 0
	taskProtocolRetries := 0
	explicitTaskEnd := toolCtx.Has(tools.TaskEndToolName)
	for {
		message := chatMessageFromStreamState(finalState)
		if explicitTaskEnd {
			_, result, ended, protocolErr := chatTaskEndResult(message, toolCtx)
			if protocolErr != nil {
				if taskProtocolRetries == 0 && finalState.eventsEmitted {
					trace.captureNarrative(writer, finalState)
				}
				if taskProtocolRetries >= maxTaskProtocolRetries {
					violation := taskProtocolViolation(protocolErr)
					s.writeTaskProtocolFailure(sessionID, requestID, model, currentReq.Model, profile, violation.Error(), messageText(message.Content), true)
					return nil, shape, totalUsage, violation
				}
				taskProtocolRetries++
				sequence++
				followUpReq := chatTaskProtocolRetryRequest(currentReq, message, adapter.ToolPolicy().RequiredToolChoice, adapter.PrepareChatRequest)
				s.writeTaskProtocolRetry(sessionID, requestID, model, followUpReq.Model, profile, protocolErr.Error(), messageText(message.Content), true)
				s.writePromptRequest(sessionID, requestID, model, followUpReq.Model, profile, "task_protocol_retry", providers.PreparedChatRequest(followUpReq), map[string]any{"sequence": sequence, "stream": true})
				shape = optimization.CaptureShape(followUpReq)
				currentReq = followUpReq
				finalState, usage, err = s.streamVisibleMessage(r, writer, respID, createdAt, currentReq, provider, toolCtx, adapter, requestID, sessionID, model, profile, "task_protocol_retry", true, localResolver, false, len(trace.items))
				if err != nil {
					return nil, shape, totalUsage, err
				}
				totalUsage = addUsage(totalUsage, usage)
				continue
			}
			if ended {
				if finalState.textAdded || finalState.reasoningAdded {
					trace.captureNarrative(writer, finalState)
				}
				visibleTexts := make([]string, 0, len(trace.items))
				for _, item := range trace.items {
					visibleTexts = append(visibleTexts, messageOutputText(item))
				}
				result = taskEndResultToEmit(result, visibleTexts...)
				if result == "" {
					return emptyStreamState(r, toolCtx, adapter, requestID, model, profile, s.logger, localResolver), shape, totalUsage, nil
				}
				completed := newStreamState(r.Context(), toolCtx, adapter, requestID, model, profile, s.logger, localResolver)
				completed.text = result
				return completed, shape, totalUsage, nil
			}
		}
		followUpReq, ok := s.internalToolFollowUpRequest(r.Context(), currentReq, message, toolCtx, adapter, logCtx)
		if !ok {
			return finalState, shape, totalUsage, nil
		}
		if finalState.eventsEmitted {
			trace.captureNarrative(writer, finalState)
		}
		trace.emit(writer, message, toolCtx)
		if sequence >= maxInternalToolRounds {
			return nil, shape, totalUsage, fmt.Errorf("internal tool follow-up exceeded %d rounds", maxInternalToolRounds)
		}
		sequence++
		s.writePromptRequest(sessionID, requestID, model, followUpReq.Model, profile, "internal_tool_followup", providers.PreparedChatRequest(followUpReq), map[string]any{"sequence": sequence, "stream": true})
		shape = optimization.CaptureShape(followUpReq)
		currentReq = followUpReq
		finalState, usage, err = s.streamVisibleMessage(r, writer, respID, createdAt, currentReq, provider, toolCtx, adapter, requestID, sessionID, model, profile, "internal_tool_followup", true, localResolver, true, len(trace.items))
		if err != nil {
			return nil, shape, totalUsage, err
		}
		totalUsage = addUsage(totalUsage, usage)
	}
}

type internalToolTrace struct {
	items []codex.ResponseItem
}

func (t *internalToolTrace) captureNarrative(writer *codex.SSEWriter, state *streamState) {
	var narrative []indexedResponseItem
	if item := reasoningItem(state.reasoning); item != nil {
		if state.reasoningAdded {
			item["id"] = state.reasoningItemID
		}
		narrative = append(narrative, indexedResponseItem{index: state.itemIndex(state.reasoningIndex), item: item})
	}
	if state.textAdded || state.text != "" {
		narrative = append(narrative, indexedResponseItem{index: state.itemIndex(state.textIndex), item: codex.ResponseItem{
			"id":      state.textItemID,
			"type":    "message",
			"role":    "assistant",
			"content": []map[string]string{{"type": "output_text", "text": state.text}},
		}})
	}
	for _, item := range sortedResponseItems(narrative) {
		outputIndex := len(t.items)
		t.items = append(t.items, item)
		for _, event := range outputDoneEvents(item, outputIndex, state.eventsEmitted) {
			_ = writer.Event(event)
		}
	}
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

func (s *Server) streamVisibleMessage(r *http.Request, writer *codex.SSEWriter, respID string, createdAt int64, chatReq providers.ChatCompletionRequest, provider providers.ChatProvider, toolCtx tools.Context, adapter adapters.Adapter, requestID string, sessionID string, model string, profile string, stage string, hideInternalTools bool, localResolver toolCallLocalResolver, emitEvents bool, outputIndexOffset int) (*streamState, providers.NormalizedUsage, error) {
	startedAt := time.Now()
	stream, err := provider.Stream(r.Context(), chatReq)
	if err != nil {
		s.writePromptFailure(sessionID, requestID, model, chatReq.Model, profile, stage, err.Error(), map[string]any{"stream": true})
		return nil, providers.NormalizedUsage{}, err
	}
	s.logger.Info("upstream_stream_opened",
		slog.String("request_id", requestID),
		slog.Int64("elapsed_ms", time.Since(startedAt).Milliseconds()),
	)
	state := newStreamState(r.Context(), toolCtx, adapter, requestID, model, profile, s.logger, localResolver)
	state.nextOutputIndex = outputIndexOffset
	if outputIndexOffset > 0 {
		state.textItemID = fmt.Sprintf("msg_%s_%d", requestID, outputIndexOffset)
		state.reasoningItemID = fmt.Sprintf("rs_%s_%d", requestID, outputIndexOffset)
	}
	var usage providers.NormalizedUsage
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
				return nil, usage, event.Err
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
			if event.Chunk.Usage != nil {
				usage = providers.NormalizeUsage(event.Chunk.Usage)
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
			return nil, usage, r.Context().Err()
		case <-heartbeat.C:
			_ = writer.Event(responseInProgressEvent(respID, createdAt, model))
		}
	}
	s.writePromptResponse(sessionID, requestID, model, chatReq.Model, profile, stage, map[string]any{
		"stream":      true,
		"chunk_count": streamSeq,
		"message":     chatMessageFromStreamState(state),
		"usage":       usage,
	}, nil)
	return state, usage, nil
}

func emptyStreamState(r *http.Request, toolCtx tools.Context, adapter adapters.Adapter, requestID string, model string, profile string, logger *slog.Logger, localResolver toolCallLocalResolver) *streamState {
	return newStreamState(r.Context(), toolCtx, adapter, requestID, model, profile, logger, localResolver)
}

func addUsage(total providers.NormalizedUsage, usage providers.NormalizedUsage) providers.NormalizedUsage {
	total.InputTokens += usage.InputTokens
	total.CachedInputTokens += usage.CachedInputTokens
	total.FreshInputTokens += usage.FreshInputTokens
	total.OutputTokens += usage.OutputTokens
	total.ReasoningTokens += usage.ReasoningTokens
	total.TotalTokens += usage.TotalTokens
	return total
}

func isInternalToolEvent(event map[string]any) bool {
	item, _ := event["item"].(map[string]any)
	name, _ := item["name"].(string)
	return name == tools.WebSearchProxyToolName || name == tools.TaskEndToolName || name == tools.ReadFileToolName || name == tools.ListFilesToolName || name == tools.FileSearchToolName
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
