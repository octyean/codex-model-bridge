package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
	"codex-bridge/internal/config"
	"codex-bridge/internal/incidentlog"
	"codex-bridge/internal/providers"
	"codex-bridge/internal/requestdump"
	"codex-bridge/internal/toollog"
	"codex-bridge/internal/tools"
	"codex-bridge/internal/transcript"
)

func (s *Server) forwardProjectedResponses(w http.ResponseWriter, r *http.Request, requestID string, sessionID string, req codex.ResponsesRequest, modelCfg config.ModelConfig, provider providers.ResponsesProvider, adapter adapters.Adapter, executionPlan config.ModelExecutionPlan, workspace string, dumpPath string) {
	transcriptResult, err := transcript.ToChatMessagesWithRuntime(r.Context(), req, adapter, s.runtime, transcript.LogContext{
		RequestID:       requestID,
		SessionID:       sessionID,
		Model:           req.Model,
		UpstreamModel:   modelCfg.UpstreamModel,
		Profile:         adapter.Name(),
		InputModalities: effectiveInputModalities(modelCfg, adapter),
	})
	if err != nil {
		s.writeBridgeFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), http.StatusBadRequest, err.Error(), nil)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	chatTools, toolCtx := transcriptResult.Tools, transcriptResult.ToolContext
	toolCtx.Workspace = workspace
	chatTools = filterUnavailableRuntimeTools(chatTools, &toolCtx, transcriptResult.Messages)
	if s.runtime.HasSearch() && tools.HasWebSearch(req.Tools) {
		chatTools = tools.AddWebSearchProxy(chatTools, &toolCtx)
	}
	if s.writeForcedLocalToolChoice(w, requestID, req, toolCtx, adapter) {
		return
	}
	responseFormat := responseFormatFromText(req.Raw)
	structuredOutputFallback := responseFormat != nil && !executionPlan.SupportsResponsesStructuredOutput
	if structuredOutputFallback {
		transcriptResult.Messages = structuredOutputMessages(transcriptResult.Messages, responseFormat)
	}
	explicitTaskEnd := responseFormat == nil && adapters.UseExplicitTaskEnd(adapter, modelCfg.UpstreamModel)
	if explicitTaskEnd {
		chatTools = tools.AddTaskEndTool(chatTools, &toolCtx)
		transcriptResult.Messages = append(transcriptResult.Messages, providers.ChatMessage{Role: "system", Content: taskProtocolInstruction})
	}
	toolChoice := tools.ToolChoice(req.ToolChoice, toolCtx)
	if note := tools.SoftRequiredToolChoiceNote(toolChoice, adapter.ToolPolicy().RequiredToolChoice); note != "" {
		transcriptResult.Messages = append(transcriptResult.Messages, providers.ChatMessage{Role: "system", Content: note})
	}
	chatTools, toolChoice = tools.ApplyToolChoice(chatTools, toolChoice, adapter.ToolPolicy().RequiredToolChoice)
	transcriptResult.Messages = adapter.PrepareResponseMessages(transcriptResult.Messages)

	upstreamReq, removedFields := prepareProjectedResponseRequest(req.Raw, executionPlan.SupportsResponsesOptions, executionPlan.SupportsResponsesStructuredOutput)
	upstreamReq["model"] = modelCfg.UpstreamModel
	upstreamReq["input"] = projectedResponseInput(transcriptResult.Messages)
	delete(upstreamReq, "instructions")
	if projectedTools := projectedResponseTools(chatTools); len(projectedTools) > 0 {
		upstreamReq["tools"] = projectedTools
		if projectedChoice := projectedResponseToolChoice(toolChoice); projectedChoice != nil {
			upstreamReq["tool_choice"] = projectedChoice
		} else {
			delete(upstreamReq, "tool_choice")
		}
	} else {
		delete(upstreamReq, "tools")
		delete(upstreamReq, "tool_choice")
	}
	if toolCtx.Has(tools.WebSearchProxyToolName) || explicitTaskEnd {
		upstreamReq["parallel_tool_calls"] = false
	}
	upstreamReq = adapter.PrepareResponseRequest(upstreamReq)

	requestBodyHash := requestdump.Hash(upstreamReq)
	promptExtra := map[string]any{
		"stream":            req.Stream,
		"request_body_hash": requestBodyHash,
		"tool_count":        len(chatTools),
	}
	if len(removedFields) > 0 {
		promptExtra["removed_fields"] = removedFields
	}
	if path, err := requestdump.Write(requestID, req.Model, adapter.Name(), upstreamReq); err != nil {
		s.logger.Warn("upstream_request_dump_failed",
			slog.String("request_id", requestID),
			slog.String("error", err.Error()),
			slog.String("env", requestdump.EnvPath),
		)
	} else if path != "" {
		dumpPath = path
		promptExtra["upstream_request_dump"] = path
		s.logger.Info("upstream_request_dumped",
			slog.String("request_id", requestID),
			slog.String("path", path),
			slog.String("body_hash", requestBodyHash),
		)
	}
	s.writePromptRequest(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), config.ExecutionModeProjectedResponses, upstreamReq, promptExtra)
	s.writeToolCatalog(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), chatTools, toolCtx, toolChoice)

	if req.Stream {
		s.streamProjectedResponses(w, r, requestID, sessionID, req, modelCfg, provider, adapter, toolCtx, upstreamReq, responseFormat, structuredOutputFallback, dumpPath)
		return
	}

	resp, err := provider.CreateResponse(r.Context(), upstreamReq)
	if err != nil {
		s.projectedResponseFailure(w, r, requestID, sessionID, req, modelCfg, adapter, dumpPath, false, err)
		return
	}
	s.writePromptResponse(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), config.ExecutionModeProjectedResponses, resp, nil)
	logCtx := toollog.OutputContext{
		RequestID:      requestID,
		Model:          req.Model,
		UpstreamModel:  modelCfg.UpstreamModel,
		Profile:        adapter.Name(),
		RequestSummary: incidentlog.RequestSummary(req.Raw),
	}
	resp, finalReq, totalUsage, internalTools, err := s.resolveProjectedInternalTools(r.Context(), provider, sessionID, upstreamReq, resp, toolCtx, adapter, logCtx)
	if err != nil {
		s.projectedResponseFailure(w, r, requestID, sessionID, req, modelCfg, adapter, dumpPath, false, err)
		return
	}
	taskProtocolRetry := false
	taskProtocolRetries := 0
	taskEndStatus := ""
	if explicitTaskEnd {
		for {
			status, result, ended, protocolErr := projectedTaskEndResult(resp, toolCtx)
			if protocolErr == nil {
				if ended {
					taskEndStatus = status
					resp = projectedTaskEndResponse(resp, result)
				} else {
					resp = projectedWithoutTaskEndCalls(resp, toolCtx)
				}
				break
			}
			if taskProtocolRetries >= maxTaskProtocolRetries {
				violation := taskProtocolViolation(protocolErr)
				s.writeTaskProtocolFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), violation.Error(), projectedResponseAssistantText(resp), false)
				s.projectedResponseFailure(w, r, requestID, sessionID, req, modelCfg, adapter, dumpPath, false, violation)
				return
			}
			taskProtocolRetry = true
			taskProtocolRetries++
			s.writeTaskProtocolRetry(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), protocolErr.Error(), projectedResponseAssistantText(resp), false)
			retryReq := projectedTaskProtocolRetryRequest(finalReq, resp, adapter.ToolPolicy().RequiredToolChoice)
			s.writePromptRequest(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), "projected_task_protocol_retry", retryReq, map[string]any{"sequence": taskProtocolRetries})
			retried, retryErr := provider.CreateResponse(r.Context(), retryReq)
			if retryErr != nil {
				s.writePromptFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), "projected_task_protocol_retry", retryErr.Error(), map[string]any{"sequence": taskProtocolRetries})
				s.projectedResponseFailure(w, r, requestID, sessionID, req, modelCfg, adapter, dumpPath, false, retryErr)
				return
			}
			s.writePromptResponse(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), "projected_task_protocol_retry", retried, map[string]any{"sequence": taskProtocolRetries})
			var retryUsage providers.NormalizedUsage
			var retryInternalTools bool
			retried, finalReq, retryUsage, retryInternalTools, retryErr = s.resolveProjectedInternalTools(r.Context(), provider, sessionID, retryReq, retried, toolCtx, adapter, logCtx)
			if retryErr != nil {
				s.projectedResponseFailure(w, r, requestID, sessionID, req, modelCfg, adapter, dumpPath, false, retryErr)
				return
			}
			resp = retried
			totalUsage = addUsage(totalUsage, retryUsage)
			internalTools = internalTools || retryInternalTools
		}
	}
	projected := projectResponseObject(r.Context(), resp, req.Model, toolCtx, adapter, requestID, s.logger, nil, nil)
	if structuredOutputFallback {
		if err := enforceProjectedResponseStructuredOutput(projected, responseFormat); err != nil {
			repairReq := projectedStructuredOutputRepairRequest(finalReq, resp, responseFormat, err)
			s.writePromptRequest(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), "projected_structured_output_repair", repairReq, map[string]any{"sequence": 1})
			repaired, repairErr := provider.CreateResponse(r.Context(), repairReq)
			if repairErr != nil {
				s.writePromptFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), "projected_structured_output_repair", repairErr.Error(), map[string]any{"sequence": 1})
				s.projectedResponseFailure(w, r, requestID, sessionID, req, modelCfg, adapter, dumpPath, false, repairErr)
				return
			}
			s.writePromptResponse(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), "projected_structured_output_repair", repaired, map[string]any{"sequence": 1})
			totalUsage = addUsage(totalUsage, providers.NormalizeUsage(repaired["usage"]))
			resp = repaired
			projected = projectResponseObject(r.Context(), repaired, req.Model, toolCtx, adapter, requestID, s.logger, nil, nil)
			if repairErr := enforceProjectedResponseStructuredOutput(projected, responseFormat); repairErr != nil {
				failure := fmt.Errorf("structured output repair failed: %w", repairErr)
				s.writePromptFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), "projected_structured_output_repair", failure.Error(), map[string]any{"sequence": 1})
				s.projectedResponseFailure(w, r, requestID, sessionID, req, modelCfg, adapter, dumpPath, false, failure)
				return
			}
		}
	}
	if internalTools || taskProtocolRetry || structuredOutputFallback {
		projected["usage"] = codexUsage(totalUsage)
	}
	if nativeResponseEmpty(projected) {
		incidentlog.Write("empty_projected_response", s.incidentRecord(r, req, requestID, adapter.Name(), dumpPath, map[string]any{"stream": false}))
	}
	s.writeBridgeResponse(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), projected, map[string]any{"stream": false, "projected_responses": true, "internal_tools": internalTools, "task_protocol_retry": taskProtocolRetry, "task_end_status": taskEndStatus})
	writeJSON(w, http.StatusOK, projected)
}

func prepareProjectedResponseRequest(raw map[string]any, supportsResponsesOptions bool, supportsStructuredOutput bool) (map[string]any, []string) {
	request := cloneResponseRequest(raw)
	removed := make([]string, 0, 5)
	if _, ok := request["client_metadata"]; ok {
		delete(request, "client_metadata")
		removed = append(removed, "client_metadata")
	}
	if !supportsResponsesOptions {
		if _, ok := request["reasoning"]; ok {
			delete(request, "reasoning")
			removed = append(removed, "reasoning")
		}
	}
	if text, ok := request["text"].(map[string]any); ok {
		projectedText := cloneResponseRequest(text)
		if !supportsResponsesOptions {
			if _, ok := projectedText["verbosity"]; ok {
				delete(projectedText, "verbosity")
				removed = append(removed, "text.verbosity")
			}
		}
		if !supportsStructuredOutput {
			if _, ok := projectedText["format"]; ok {
				delete(projectedText, "format")
				removed = append(removed, "text.format")
			}
		}
		if len(projectedText) == 0 {
			delete(request, "text")
		} else {
			request["text"] = projectedText
		}
	}
	if !supportsResponsesOptions {
		if include, ok := request["include"].([]any); ok {
			filtered := make([]any, 0, len(include))
			removedReasoning := false
			for _, item := range include {
				if item == "reasoning.encrypted_content" {
					removedReasoning = true
					continue
				}
				filtered = append(filtered, item)
			}
			if removedReasoning {
				removed = append(removed, "include.reasoning.encrypted_content")
			}
			if len(filtered) == 0 {
				delete(request, "include")
			} else {
				request["include"] = filtered
			}
		}
	}
	return request, removed
}

func enforceProjectedResponseStructuredOutput(response map[string]any, responseFormat any) error {
	output, _ := response["output"].([]any)
	items := make([]codex.ResponseItem, 0, len(output))
	for _, rawItem := range output {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		items = append(items, codex.ResponseItem(item))
	}
	return normalizeStructuredOutputItems(items, responseFormat)
}

func projectedStructuredOutputRepairRequest(req map[string]any, response map[string]any, responseFormat any, validationErr error) map[string]any {
	input, _ := req["input"].([]any)
	nextInput := append([]any(nil), input...)
	output, _ := response["output"].([]any)
	for _, item := range output {
		if object, ok := item.(map[string]any); ok {
			nextInput = append(nextInput, cloneResponseRequest(object))
		}
	}
	nextInput = append(nextInput, map[string]any{
		"type": "message",
		"role": "user",
		"content": structuredOutputInstruction(responseFormat) +
			"\nThe previous assistant output was invalid: " + validationErr.Error() +
			"\nReturn a corrected JSON value now.",
	})
	followUp := cloneResponseRequest(req)
	followUp["input"] = nextInput
	delete(followUp, "tools")
	delete(followUp, "tool_choice")
	delete(followUp, "parallel_tool_calls")
	return followUp
}

func (s *Server) resolveProjectedInternalTools(ctx context.Context, provider providers.ResponsesProvider, sessionID string, req map[string]any, response map[string]any, toolCtx tools.Context, adapter adapters.Adapter, logCtx toollog.OutputContext) (map[string]any, map[string]any, providers.NormalizedUsage, bool, error) {
	currentReq := req
	currentResponse := response
	totalUsage := providers.NormalizeUsage(response["usage"])
	handled := false
	for sequence := 1; ; sequence++ {
		followUp, ok := s.projectedInternalToolFollowUpRequest(ctx, currentReq, currentResponse, toolCtx, adapter, logCtx)
		if !ok {
			return currentResponse, currentReq, totalUsage, handled, nil
		}
		if sequence > maxInternalToolRounds {
			return nil, currentReq, totalUsage, handled, fmt.Errorf("projected internal tool follow-up exceeded %d rounds", maxInternalToolRounds)
		}
		handled = true
		s.writePromptRequest(sessionID, logCtx.RequestID, logCtx.Model, logCtx.UpstreamModel, logCtx.Profile, "projected_internal_tool_followup", followUp, map[string]any{"sequence": sequence})
		next, err := provider.CreateResponse(ctx, followUp)
		if err != nil {
			s.writePromptFailure(sessionID, logCtx.RequestID, logCtx.Model, logCtx.UpstreamModel, logCtx.Profile, "projected_internal_tool_followup", err.Error(), map[string]any{"sequence": sequence})
			return nil, currentReq, totalUsage, handled, err
		}
		s.writePromptResponse(sessionID, logCtx.RequestID, logCtx.Model, logCtx.UpstreamModel, logCtx.Profile, "projected_internal_tool_followup", next, map[string]any{"sequence": sequence})
		totalUsage = addUsage(totalUsage, providers.NormalizeUsage(next["usage"]))
		currentReq = followUp
		currentResponse = next
	}
}

func (s *Server) projectedInternalToolFollowUpRequest(ctx context.Context, req map[string]any, response map[string]any, toolCtx tools.Context, adapter adapters.Adapter, logCtx toollog.OutputContext) (map[string]any, bool) {
	output, _ := response["output"].([]any)
	calls := make([]map[string]any, 0)
	for _, rawItem := range output {
		item, _ := rawItem.(map[string]any)
		if item["type"] != "function_call" {
			continue
		}
		name, _ := item["name"].(string)
		kind := toolCtx.Entry(name).Kind()
		if kind == tools.KindTaskEnd {
			continue
		}
		if kind != tools.KindWebSearch {
			return nil, false
		}
		calls = append(calls, item)
	}
	if len(calls) == 0 {
		return nil, false
	}

	input, _ := req["input"].([]any)
	nextInput := append([]any(nil), input...)
	for _, rawItem := range output {
		item, _ := rawItem.(map[string]any)
		if item["type"] != "function_call" {
			nextInput = append(nextInput, cloneResponseRequest(item))
		}
	}
	for _, call := range calls {
		callID := responseFunctionCallID(call)
		name, _ := call["name"].(string)
		arguments := responseFunctionCallArguments(call)
		entry := toolCtx.Entry(name)
		canonicalArguments := tools.CanonicalArguments(entry, arguments)
		output := s.searchToolOutput(ctx, canonicalArguments)
		descriptor := adapters.ToolDescriptor{Name: entry.Name(), Kind: tools.KindWebSearch, OriginalType: entry.OriginalType()}

		toollog.ToolCall(logCtx.RequestID, logCtx.Model, logCtx.Profile, callID, entry, arguments, "")
		toollog.ToolCallFrame(logCtx.RequestID, logCtx.Model, logCtx.Profile, callID, entry, arguments, canonicalArguments, tools.RuntimeArguments(entry, canonicalArguments))
		toollog.ToolOutput(logCtx, callID, descriptor, canonicalArguments, output, output)
		toollog.ToolCallRerouted(logCtx.RequestID, logCtx.Model, logCtx.Profile, callID, entry, canonicalArguments, "function_call_output", output, "server_executed_internal_tool")

		nextInput = append(nextInput,
			map[string]any{
				"type":      "function_call",
				"call_id":   callID,
				"name":      name,
				"arguments": arguments,
				"status":    "completed",
			},
			map[string]any{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  output,
			},
		)
	}

	followUp := cloneResponseRequest(req)
	followUp["input"] = nextInput
	followUp["tool_choice"] = "auto"
	if toolCtx.Has(tools.TaskEndToolName) && adapter.ToolPolicy().RequiredToolChoice {
		followUp["tool_choice"] = "required"
	}
	followUp["parallel_tool_calls"] = false
	return followUp, true
}

func (s *Server) streamProjectedResponses(w http.ResponseWriter, r *http.Request, requestID string, sessionID string, req codex.ResponsesRequest, modelCfg config.ModelConfig, provider providers.ResponsesProvider, adapter adapters.Adapter, toolCtx tools.Context, upstreamReq map[string]any, responseFormat any, structuredOutputFallback bool, dumpPath string) {
	writer := codex.NewSSEWriter(w)
	currentReq := upstreamReq
	explicitTaskEnd := toolCtx.Has(tools.TaskEndToolName)
	streamState := newProjectedStreamState(writer, r.Context(), toolCtx, adapter, requestID, req.Model, s.logger, !structuredOutputFallback)
	streamState.emitWorkTools = explicitTaskEnd
	var totalUsage providers.NormalizedUsage
	internalTools := false
	taskProtocolRetry := false
	taskProtocolRetries := 0
	taskEndStatus := ""
	stage := config.ExecutionModeProjectedResponses
	logCtx := toollog.OutputContext{
		RequestID:      requestID,
		Model:          req.Model,
		UpstreamModel:  modelCfg.UpstreamModel,
		Profile:        adapter.Name(),
		RequestSummary: incidentlog.RequestSummary(req.Raw),
	}
	for sequence := 0; ; sequence++ {
		if sequence > 0 {
			s.writePromptRequest(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), stage, currentReq, map[string]any{"sequence": sequence, "stream": true})
		}
		response, eventCount, responseID, err := s.streamProjectedResponseRound(r, streamState, requestID, sessionID, req.Model, modelCfg.UpstreamModel, adapter, provider, currentReq, stage, sequence)
		if err != nil {
			if requestCanceled(r, err) {
				return
			}
			extra := map[string]any{"stream": true, "stage": stage, "sequence": sequence}
			s.writePromptFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), stage, err.Error(), extra)
			s.writeBridgeFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), http.StatusBadGateway, err.Error(), extra)
			incidentlog.Write("upstream_projected_response_stream_event_error", s.incidentRecord(r, req, requestID, adapter.Name(), dumpPath, map[string]any{"error": err.Error(), "stream": true, "stage": stage}))
			_ = writer.Event(map[string]any{
				"type": "response.failed",
				"response": map[string]any{
					"id":    streamState.responseID,
					"error": map[string]any{"message": err.Error(), "type": "server_error"},
				},
			})
			return
		}
		s.writePromptResponse(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), stage, map[string]any{
			"stream":      true,
			"event_count": eventCount,
			"response_id": responseID,
		}, map[string]any{"sequence": sequence})
		totalUsage = addUsage(totalUsage, providers.NormalizeUsage(response["usage"]))

		followUp, ok := s.projectedInternalToolFollowUpRequest(r.Context(), currentReq, response, toolCtx, adapter, logCtx)
		if ok {
			if sequence >= maxInternalToolRounds {
				err := fmt.Errorf("projected internal tool follow-up exceeded %d rounds", maxInternalToolRounds)
				s.writeBridgeFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), http.StatusBadGateway, err.Error(), map[string]any{"stream": true, "internal_tools": true})
				incidentlog.Write("projected_internal_tool_followup_limit", s.incidentRecord(r, req, requestID, adapter.Name(), dumpPath, map[string]any{"error": err.Error(), "stream": true}))
				_ = writer.Event(map[string]any{"type": "response.failed", "response": map[string]any{"id": streamState.responseID, "error": map[string]any{"message": err.Error(), "type": "server_error"}}})
				return
			}
			internalTools = true
			currentReq = followUp
			stage = "projected_internal_tool_followup"
			continue
		}
		taskEnded := false
		taskEndResult := ""
		if explicitTaskEnd {
			status, result, ended, protocolErr := projectedTaskEndResult(response, toolCtx)
			if protocolErr != nil {
				if taskProtocolRetries >= maxTaskProtocolRetries {
					violation := taskProtocolViolation(protocolErr)
					s.writeTaskProtocolFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), violation.Error(), projectedResponseAssistantText(response), true)
					extra := map[string]any{"stream": true, "stage": stage, "task_protocol_retry": true}
					s.writePromptFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), stage, violation.Error(), extra)
					s.writeBridgeFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), http.StatusBadGateway, violation.Error(), extra)
					incidentlog.Write("projected_task_protocol_error", s.incidentRecord(r, req, requestID, adapter.Name(), dumpPath, map[string]any{"error": violation.Error(), "stream": true}))
					_ = writer.Event(map[string]any{"type": "response.failed", "response": map[string]any{"id": streamState.responseID, "error": map[string]any{"message": violation.Error(), "type": responseFailureType(violation)}}})
					return
				} else {
					taskProtocolRetry = true
					taskProtocolRetries++
					s.writeTaskProtocolRetry(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), protocolErr.Error(), projectedResponseAssistantText(response), true)
					currentReq = projectedTaskProtocolRetryRequest(currentReq, response, adapter.ToolPolicy().RequiredToolChoice)
					stage = "projected_task_protocol_retry"
					continue
				}
			}
			if ended {
				taskEnded = true
				taskEndStatus = status
				visibleText := projectedResponseAssistantText(streamState.completedResponse(response))
				if visibleText == "" {
					taskEndResult = result
				}
			}
		}

		projected := streamState.completedResponse(response)
		var taskEndProjected map[string]any
		if taskEndResult != "" {
			taskEndResponse := projectedTaskEndResponse(response, taskEndResult)
			taskEndProjected = projectResponseObject(r.Context(), taskEndResponse, req.Model, toolCtx, adapter, requestID, s.logger, nil, nil)
			if streamState.responseID != "" {
				taskEndProjected["id"] = streamState.responseID
			}
			projected = taskEndProjected
		}
		if structuredOutputFallback {
			projected = projectResponseObject(r.Context(), response, req.Model, toolCtx, adapter, requestID, s.logger, nil, nil)
			if validationErr := enforceProjectedResponseStructuredOutput(projected, responseFormat); validationErr != nil {
				repairReq := projectedStructuredOutputRepairRequest(currentReq, response, responseFormat, validationErr)
				repairSequence := sequence + 1
				stage = "projected_structured_output_repair"
				s.writePromptRequest(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), stage, repairReq, map[string]any{"sequence": 1, "stream": true})
				repairState := newProjectedStreamState(writer, r.Context(), toolCtx, adapter, requestID, req.Model, s.logger, false)
				repairState.responseID = streamState.responseID
				repaired, repairEvents, repairResponseID, repairErr := s.streamProjectedResponseRound(r, repairState, requestID, sessionID, req.Model, modelCfg.UpstreamModel, adapter, provider, repairReq, stage, repairSequence)
				if repairErr != nil {
					s.writePromptFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), stage, repairErr.Error(), map[string]any{"sequence": 1, "stream": true})
					s.writeBridgeFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), http.StatusBadGateway, repairErr.Error(), map[string]any{"stream": true, "stage": stage})
					_ = writer.Event(map[string]any{"type": "response.failed", "response": map[string]any{"id": streamState.responseID, "error": map[string]any{"message": repairErr.Error(), "type": "server_error"}}})
					return
				}
				s.writePromptResponse(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), stage, map[string]any{
					"stream":      true,
					"event_count": repairEvents,
					"response_id": repairResponseID,
				}, map[string]any{"sequence": 1})
				totalUsage = addUsage(totalUsage, providers.NormalizeUsage(repaired["usage"]))
				projected = projectResponseObject(r.Context(), repaired, req.Model, toolCtx, adapter, requestID, s.logger, nil, nil)
				if repairValidationErr := enforceProjectedResponseStructuredOutput(projected, responseFormat); repairValidationErr != nil {
					failure := fmt.Errorf("structured output repair failed: %w", repairValidationErr)
					s.writePromptFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), stage, failure.Error(), map[string]any{"sequence": 1, "stream": true})
					s.writeBridgeFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), http.StatusBadGateway, failure.Error(), map[string]any{"stream": true, "stage": stage})
					_ = writer.Event(map[string]any{"type": "response.failed", "response": map[string]any{"id": streamState.responseID, "error": map[string]any{"message": failure.Error(), "type": "server_error"}}})
					return
				}
			}
			emitProjectedResponseItems(writer, projected)
		} else if explicitTaskEnd && taskEnded && taskEndProjected != nil {
			emitProjectedResponseItems(writer, taskEndProjected)
		}
		if streamState.responseID != "" {
			projected["id"] = streamState.responseID
		}
		if internalTools || taskProtocolRetry || structuredOutputFallback {
			projected["usage"] = codexUsage(totalUsage)
		}
		completed := map[string]any{"type": "response.completed", "response": projected}
		_ = writer.Event(completed)
		s.writeBridgeResponse(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), projected, map[string]any{"stream": true, "projected_responses": true, "internal_tools": internalTools, "task_protocol_retry": taskProtocolRetry, "task_end_status": taskEndStatus})
		return
	}
}

type projectedStreamState struct {
	writer        *codex.SSEWriter
	ctx           context.Context
	toolCtx       tools.Context
	adapter       adapters.Adapter
	requestID     string
	model         string
	logger        *slog.Logger
	emitOutput    bool
	emitWorkTools bool
	responseID    string
	items         []any
	itemIndexes   map[string]int
	nextItemSlot  int
}

type projectedStreamRound struct {
	sequence      int
	hideNarrative bool
	indexes       map[int]int
	hidden        map[int]bool
}

func newProjectedStreamState(writer *codex.SSEWriter, ctx context.Context, toolCtx tools.Context, adapter adapters.Adapter, requestID string, model string, logger *slog.Logger, emitOutput bool) *projectedStreamState {
	return &projectedStreamState{
		writer:      writer,
		ctx:         ctx,
		toolCtx:     toolCtx,
		adapter:     adapter,
		requestID:   requestID,
		model:       model,
		logger:      logger,
		emitOutput:  emitOutput,
		itemIndexes: map[string]int{},
	}
}

func (s *Server) streamProjectedResponseRound(r *http.Request, state *projectedStreamState, requestID string, sessionID string, model string, upstreamModel string, adapter adapters.Adapter, provider providers.ResponsesProvider, upstreamReq map[string]any, stage string, sequence int) (map[string]any, int, string, error) {
	stream, err := provider.StreamResponse(r.Context(), upstreamReq)
	if err != nil {
		return nil, 0, "", err
	}
	round := projectedStreamRound{
		sequence:      sequence,
		hideNarrative: stage == "projected_task_protocol_retry",
		indexes:       map[int]int{},
		hidden:        map[int]bool{},
	}
	eventCount := 0
	responseID := ""
	for event := range stream {
		if event.Err != nil {
			return nil, eventCount, responseID, event.Err
		}
		if event.Done {
			return nil, eventCount, responseID, fmt.Errorf("projected responses stream ended without response.completed")
		}
		eventCount++
		s.writePromptStreamEvent(sessionID, requestID, model, upstreamModel, adapter.Name(), stage, eventCount, event.Data)
		eventType, _ := event.Data["type"].(string)
		if eventType == "response.created" {
			if response, ok := event.Data["response"].(map[string]any); ok {
				responseID, _ = response["id"].(string)
			}
		}
		if eventType == "response.completed" {
			response, _ := event.Data["response"].(map[string]any)
			if responseID == "" {
				responseID, _ = response["id"].(string)
			}
			if state.responseID == "" {
				state.responseID = responseID
				if state.responseID == "" {
					state.responseID = "resp_" + requestID
				}
			}
			state.absorbCompletedResponse(response, &round)
			return response, eventCount, responseID, nil
		}
		state.handleEvent(event.Data, &round)
	}
	return nil, eventCount, responseID, fmt.Errorf("projected responses stream closed without response.completed")
}

func (state *projectedStreamState) handleEvent(event map[string]any, round *projectedStreamRound) {
	eventType, _ := event["type"].(string)
	switch eventType {
	case "response.created":
		response, _ := event["response"].(map[string]any)
		upstreamID, _ := response["id"].(string)
		first := state.responseID == ""
		if first {
			state.responseID = upstreamID
			if state.responseID == "" {
				state.responseID = "resp_" + state.requestID
			}
			state.emitEvent(event)
		}
		return
	case "response.in_progress", "response.queued":
		if round.sequence == 0 {
			state.emitEvent(event)
		}
		return
	case "response.function_call_arguments.delta", "response.function_call_arguments.done":
		return
	case "response.output_item.added":
		item, _ := event["item"].(map[string]any)
		upstreamIndex := intValue(event["output_index"])
		if state.internalBridgeCall(item) {
			round.hidden[upstreamIndex] = true
			return
		}
		if item["type"] == "function_call" {
			return
		}
		if round.hideNarrative {
			round.hidden[upstreamIndex] = true
			return
		}
		index := state.reserveItem(item, round, upstreamIndex)
		state.emitOutputEvent(event, index)
		return
	case "response.output_item.done":
		item, _ := event["item"].(map[string]any)
		upstreamIndex := intValue(event["output_index"])
		if round.hidden[upstreamIndex] || state.internalBridgeCall(item) {
			round.hidden[upstreamIndex] = true
			return
		}
		if item["type"] == "function_call" {
			projected := projectFunctionCallItem(state.ctx, item, state.toolCtx, state.adapter, state.requestID, state.model, state.adapter.Name(), state.logger, nil)
			index := state.storeProjectedItem(projected, round, upstreamIndex)
			if state.emitOutput || state.emitWorkTools {
				for _, projectedEvent := range outputDoneEvents(projected, index, false) {
					_ = state.writer.Event(projectedEvent)
				}
			}
			return
		}
		index := state.storeItem(item, round, upstreamIndex)
		state.emitOutputEvent(event, index)
		return
	}

	if upstreamIndex, ok := eventOutputIndex(event); ok {
		if round.hideNarrative {
			round.hidden[upstreamIndex] = true
			return
		}
		if round.hidden[upstreamIndex] {
			return
		}
		index, exists := round.indexes[upstreamIndex]
		if !exists {
			index = state.reserveItem(map[string]any{}, round, upstreamIndex)
		}
		state.emitOutputEvent(event, index)
		return
	}
	state.emitEvent(event)
}

func (state *projectedStreamState) absorbCompletedResponse(response map[string]any, round *projectedStreamRound) {
	output, _ := response["output"].([]any)
	for upstreamIndex, rawItem := range output {
		item, ok := rawItem.(map[string]any)
		if !ok || round.hidden[upstreamIndex] || state.internalBridgeCall(item) {
			continue
		}
		if round.hideNarrative && item["type"] != "function_call" {
			continue
		}
		if item["type"] == "function_call" {
			if index, exists := state.itemIndex(item, round, upstreamIndex); exists {
				round.indexes[upstreamIndex] = index
				continue
			}
			projected := projectFunctionCallItem(state.ctx, item, state.toolCtx, state.adapter, state.requestID, state.model, state.adapter.Name(), state.logger, nil)
			index := state.storeProjectedItem(projected, round, upstreamIndex)
			if state.emitOutput || state.emitWorkTools {
				for _, projectedEvent := range outputDoneEvents(projected, index, false) {
					_ = state.writer.Event(projectedEvent)
				}
			}
			continue
		}
		index, exists := state.itemIndex(item, round, upstreamIndex)
		if exists {
			state.items[index] = state.outputItem(item)
			continue
		}
		index = state.storeItem(item, round, upstreamIndex)
		if state.emitOutput {
			stored, _ := state.items[index].(map[string]any)
			for _, projectedEvent := range outputDoneEvents(codex.ResponseItem(stored), index, false) {
				_ = state.writer.Event(projectedEvent)
			}
		}
	}
}

func (state *projectedStreamState) completedResponse(response map[string]any) map[string]any {
	projected := cloneResponseRequest(response)
	projected["id"] = state.responseID
	projected["model"] = state.model
	projected["output"] = append([]any(nil), state.items...)
	return projected
}

func (state *projectedStreamState) reserveItem(item map[string]any, round *projectedStreamRound, upstreamIndex int) int {
	if index, ok := round.indexes[upstreamIndex]; ok {
		return index
	}
	index := state.nextItemSlot
	state.nextItemSlot++
	round.indexes[upstreamIndex] = index
	stored := state.outputItem(item)
	state.items = append(state.items, stored)
	if key := projectedItemKey(stored); key != "" {
		state.itemIndexes[key] = index
	}
	return index
}

func (state *projectedStreamState) storeItem(item map[string]any, round *projectedStreamRound, upstreamIndex int) int {
	index := state.reserveItem(item, round, upstreamIndex)
	stored := state.outputItem(item)
	state.items[index] = stored
	if key := projectedItemKey(stored); key != "" {
		state.itemIndexes[key] = index
	}
	return index
}

func (state *projectedStreamState) outputItem(item map[string]any) map[string]any {
	if state.toolCtx.Has(tools.TaskEndToolName) {
		return sanitizeTaskProtocolItem(item)
	}
	return cloneResponseRequest(item)
}

func (state *projectedStreamState) storeProjectedItem(item codex.ResponseItem, round *projectedStreamRound, upstreamIndex int) int {
	index := state.reserveItem(map[string]any(item), round, upstreamIndex)
	state.items[index] = item
	if key := projectedItemKey(item); key != "" {
		state.itemIndexes[key] = index
	}
	return index
}

func (state *projectedStreamState) itemIndex(item map[string]any, round *projectedStreamRound, upstreamIndex int) (int, bool) {
	if key := projectedItemKey(item); key != "" {
		if index, ok := state.itemIndexes[key]; ok {
			round.indexes[upstreamIndex] = index
			return index, true
		}
	}
	index, ok := round.indexes[upstreamIndex]
	return index, ok
}

func (state *projectedStreamState) internalBridgeCall(item map[string]any) bool {
	if item["type"] != "function_call" {
		return false
	}
	name, _ := item["name"].(string)
	kind := state.toolCtx.Entry(name).Kind()
	return kind == tools.KindWebSearch || kind == tools.KindTaskEnd
}

func (state *projectedStreamState) emitOutputEvent(event map[string]any, outputIndex int) {
	if !state.emitOutput {
		return
	}
	projected := cloneResponseRequest(event)
	projected["output_index"] = outputIndex
	state.emitEvent(projected)
}

func (state *projectedStreamState) emitEvent(event map[string]any) {
	projected := cloneResponseRequest(event)
	if state.toolCtx.Has(tools.TaskEndToolName) {
		projected = sanitizeTaskProtocolEvent(projected)
	}
	replaceResponseModel(projected, state.model)
	if response, ok := projected["response"].(map[string]any); ok {
		response = cloneResponseRequest(response)
		response["id"] = state.responseID
		if _, exists := response["model"]; exists {
			response["model"] = state.model
		}
		projected["response"] = response
	}
	_ = state.writer.Event(projected)
}

func projectedItemKey(item map[string]any) string {
	if id, _ := item["id"].(string); id != "" {
		return "id:" + id
	}
	if callID, _ := item["call_id"].(string); callID != "" {
		return "call:" + callID
	}
	return ""
}

func eventOutputIndex(event map[string]any) (int, bool) {
	if _, ok := event["output_index"]; !ok {
		return 0, false
	}
	return intValue(event["output_index"]), true
}

func emitProjectedResponseItems(writer *codex.SSEWriter, response map[string]any) {
	output, _ := response["output"].([]any)
	for index, rawItem := range output {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		for _, event := range outputDoneEvents(codex.ResponseItem(item), index, false) {
			_ = writer.Event(event)
		}
	}
}

func (s *Server) projectedResponseFailure(w http.ResponseWriter, r *http.Request, requestID string, sessionID string, req codex.ResponsesRequest, modelCfg config.ModelConfig, adapter adapters.Adapter, dumpPath string, stream bool, err error) {
	if requestCanceled(r, err) {
		return
	}
	extra := map[string]any{"stream": stream}
	s.writePromptFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), config.ExecutionModeProjectedResponses, err.Error(), extra)
	s.writeBridgeFailure(sessionID, requestID, req.Model, modelCfg.UpstreamModel, adapter.Name(), http.StatusBadGateway, err.Error(), extra)
	incidentlog.Write("upstream_projected_response_error", s.incidentRecord(r, req, requestID, adapter.Name(), dumpPath, map[string]any{"error": err.Error(), "stream": stream}))
	if errors.Is(err, errTaskProtocolViolation) {
		writeErrorType(w, http.StatusBadGateway, err.Error(), responseFailureType(err))
		return
	}
	writeError(w, http.StatusBadGateway, err.Error())
}

func projectedResponseInput(messages []providers.ChatMessage) []any {
	input := make([]any, 0, len(messages))
	for _, message := range messages {
		if message.Role == "tool" {
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": message.ToolCallID,
				"output":  messageText(message.Content),
			})
			continue
		}
		if text := strings.TrimSpace(messageText(message.Content)); text != "" || len(message.ToolCalls) == 0 {
			input = append(input, map[string]any{
				"type":    "message",
				"role":    message.Role,
				"content": projectedMessageContent(message.Role, message.Content),
			})
		}
		for _, call := range message.ToolCalls {
			input = append(input, map[string]any{
				"type":      "function_call",
				"call_id":   call.ID,
				"name":      call.Function.Name,
				"arguments": call.Function.Arguments,
				"status":    "completed",
			})
		}
	}
	return input
}

func projectedMessageContent(role string, content any) any {
	if _, ok := content.(string); ok {
		return content
	}
	rawParts, ok := content.([]map[string]any)
	if !ok {
		return messageText(content)
	}
	parts := make([]map[string]any, 0, len(rawParts))
	for _, part := range rawParts {
		switch part["type"] {
		case "text":
			partType := "input_text"
			if role == "assistant" {
				partType = "output_text"
			}
			parts = append(parts, map[string]any{"type": partType, "text": part["text"]})
		case "image_url":
			image, _ := part["image_url"].(map[string]any)
			projected := map[string]any{"type": "input_image", "image_url": image["url"]}
			if detail, ok := image["detail"]; ok {
				projected["detail"] = detail
			}
			parts = append(parts, projected)
		}
	}
	if len(parts) == 0 {
		return messageText(content)
	}
	return parts
}

func projectedResponseTools(chatTools []providers.ChatTool) []map[string]any {
	out := make([]map[string]any, 0, len(chatTools))
	for _, tool := range chatTools {
		item := map[string]any{
			"type":        "function",
			"name":        tool.Function.Name,
			"description": tool.Function.Description,
		}
		if len(tool.Function.Parameters) > 0 {
			var parameters any
			if json.Unmarshal(tool.Function.Parameters, &parameters) == nil {
				item["parameters"] = parameters
			}
		}
		out = append(out, item)
	}
	return out
}

func projectedResponseToolChoice(choice any) any {
	obj, ok := choice.(map[string]any)
	if !ok {
		return choice
	}
	switch obj["type"] {
	case "function":
		function, _ := obj["function"].(map[string]any)
		return map[string]any{"type": "function", "name": function["name"]}
	case "allowed_tools":
		rawTools, _ := obj["tools"].([]any)
		projectedTools := make([]any, 0, len(rawTools))
		for _, rawTool := range rawTools {
			tool, _ := rawTool.(map[string]any)
			function, _ := tool["function"].(map[string]any)
			if name, _ := function["name"].(string); name != "" {
				projectedTools = append(projectedTools, map[string]any{"type": "function", "name": name})
			}
		}
		return map[string]any{"type": "allowed_tools", "mode": obj["mode"], "tools": projectedTools}
	default:
		return choice
	}
}

func projectResponseObject(ctx context.Context, response map[string]any, model string, toolCtx tools.Context, adapter adapters.Adapter, requestID string, logger *slog.Logger, localResolver toolCallLocalResolver, projectedCalls map[string]codex.ResponseItem) map[string]any {
	projected := cloneResponseRequest(response)
	output, ok := response["output"].([]any)
	if ok {
		items := make([]any, 0, len(output))
		for _, rawItem := range output {
			item, ok := rawItem.(map[string]any)
			if !ok {
				items = append(items, rawItem)
				continue
			}
			if item["type"] == "function_call" {
				callID := responseFunctionCallID(item)
				if projected, ok := projectedCalls[callID]; ok {
					items = append(items, projected)
					continue
				}
				items = append(items, projectFunctionCallItem(ctx, item, toolCtx, adapter, requestID, model, adapter.Name(), logger, localResolver))
				continue
			}
			items = append(items, item)
		}
		projected["output"] = items
	}
	replaceResponseModel(projected, model)
	return projected
}

func projectFunctionCallItem(ctx context.Context, item map[string]any, toolCtx tools.Context, adapter adapters.Adapter, requestID string, model string, profile string, logger *slog.Logger, localResolver toolCallLocalResolver) codex.ResponseItem {
	callID := responseFunctionCallID(item)
	name, _ := item["name"].(string)
	arguments := responseFunctionCallArguments(item)
	entry := toolCtx.Entry(name)
	toollog.ToolCall(requestID, model, profile, callID, entry, arguments, "")
	projected := responseItemFromToolCall(ctx, callID, entry, arguments, toolCtx, adapter, requestID, model, profile, logger, localResolver)
	logToolTranslation(logger, requestID, entry, projected["type"].(string))
	logPatchWriteToolCall(requestID, callID, entry, arguments, projected)
	return projected
}

func emitMissingProjectedCalls(writer *codex.SSEWriter, ctx context.Context, response map[string]any, projectedCalls map[string]codex.ResponseItem, toolCtx tools.Context, adapter adapters.Adapter, requestID string, model string, logger *slog.Logger) {
	output, _ := response["output"].([]any)
	for index, rawItem := range output {
		item, _ := rawItem.(map[string]any)
		if item["type"] != "function_call" {
			continue
		}
		callID := responseFunctionCallID(item)
		if _, ok := projectedCalls[callID]; ok {
			continue
		}
		projected := projectFunctionCallItem(ctx, item, toolCtx, adapter, requestID, model, adapter.Name(), logger, nil)
		for _, event := range outputDoneEvents(projected, index, false) {
			_ = writer.Event(event)
		}
		projectedCalls[callID] = projected
	}
}

func responseFunctionCallID(item map[string]any) string {
	callID, _ := item["call_id"].(string)
	if callID == "" {
		callID, _ = item["id"].(string)
	}
	return callID
}

func responseFunctionCallArguments(item map[string]any) string {
	arguments, _ := item["arguments"].(string)
	if arguments == "" && item["arguments"] != nil {
		data, _ := json.Marshal(item["arguments"])
		arguments = string(data)
	}
	return arguments
}

func intValue(value any) int {
	switch number := value.(type) {
	case float64:
		return int(number)
	case int:
		return number
	default:
		return 0
	}
}
