package server

import (
	"context"
	"encoding/json"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
	"codex-bridge/internal/incidentlog"
	"codex-bridge/internal/providers"
	"codex-bridge/internal/toollog"
	"codex-bridge/internal/tools"
)

func (s *Server) hasInternalTools(req codex.ResponsesRequest) bool {
	return s.runtime.HasSearch() && tools.HasWebSearch(req.Tools)
}

func (s *Server) resolveInternalTools(ctx context.Context, provider providers.ChatProvider, req providers.ChatCompletionRequest, message providers.ChatMessage, logCtx toollog.OutputContext) (*providers.ChatCompletionResponse, providers.ChatCompletionRequest, bool) {
	current := message
	currentReq := req
	var resp *providers.ChatCompletionResponse
	handled := false
	for {
		followUp, ok := s.internalToolFollowUpRequest(ctx, currentReq, current, logCtx)
		if !ok {
			break
		}
		next, err := provider.Create(ctx, followUp)
		if err != nil {
			s.logger.Error("internal_tool_followup_failed", "error", err.Error())
			return nil, providers.ChatCompletionRequest{}, false
		}
		if len(next.Choices) == 0 {
			return next, followUp, true
		}
		resp = next
		currentReq = followUp
		current = next.Choices[0].Message
		handled = true
	}
	if !handled {
		return nil, providers.ChatCompletionRequest{}, false
	}
	return resp, currentReq, true
}

func (s *Server) internalToolFollowUpRequest(ctx context.Context, req providers.ChatCompletionRequest, message providers.ChatMessage, logCtx toollog.OutputContext) (providers.ChatCompletionRequest, bool) {
	if len(message.ToolCalls) == 0 {
		return providers.ChatCompletionRequest{}, false
	}
	for _, call := range message.ToolCalls {
		if call.Function.Name != tools.WebSearchProxyToolName {
			return providers.ChatCompletionRequest{}, false
		}
	}
	var outputs []providers.ChatMessage
	for _, call := range message.ToolCalls {
		output := s.searchToolOutput(ctx, call.Function.Arguments)
		toollog.ToolOutput(logCtx, call.ID, adapters.ToolDescriptor{
			Name:         tools.WebSearchProxyToolName,
			Kind:         tools.KindWebSearch,
			OriginalType: "web_search_preview",
		}, call.Function.Arguments, output, output)
		outputs = append(outputs, providers.ChatMessage{
			Role:       "tool",
			ToolCallID: call.ID,
			Content:    output,
		})
	}
	followUp := req
	followUp.ToolChoice = "auto"
	followUp.Messages = append(append(followUp.Messages, message), outputs...)
	return followUp, true
}

func (s *Server) localToolResultResolver(logCtx toollog.OutputContext, toolCtx tools.Context) toolCallLocalResolver {
	return func(ctx context.Context, callID string, entry tools.Entry, _ string, canonicalArguments string, _ string) (codex.ResponseItem, bool) {
		if entry.Kind() != tools.KindWebSearch {
			return nil, false
		}
		output := s.searchToolOutput(ctx, canonicalArguments)
		toollog.ToolOutput(logCtx, callID, entry.Descriptor, canonicalArguments, output, output)
		if toolCtx.Has("exec_command") {
			item := toolRuntimeLocalResultExecCommandCall(callID, entry, canonicalArguments, output)
			toollog.ToolCallRerouted(logCtx.RequestID, logCtx.Model, logCtx.Profile, callID, entry, canonicalArguments, "exec_command", item["arguments"].(string), "server_executed_internal_tool")
			return item, true
		}
		record := map[string]any{
			"request_id":     logCtx.RequestID,
			"model":          logCtx.Model,
			"upstream_model": logCtx.UpstreamModel,
			"profile":        logCtx.Profile,
			"call_id":        callID,
			"tool":           entry.Name(),
			"kind":           entry.Kind(),
			"arguments":      canonicalArguments,
			"reason":         "exec_command_unavailable_for_internal_tool_result",
		}
		if len(logCtx.RequestSummary) > 0 {
			record["request_summary"] = logCtx.RequestSummary
		}
		incidentlog.Write("internal_tool_transport_missing", record)
		return codex.ResponseItem{
			"id":   toolItemID("function_call", callID),
			"type": "message",
			"role": "assistant",
			"content": []map[string]string{{
				"type": "output_text",
				"text": "CODEX_BRIDGE_INTERNAL_TOOL_ERROR\n" +
					"tool: " + entry.Name() + "\n" +
					"reason: exec_command_unavailable_for_internal_tool_result",
			}},
		}, true
	}
}

func (s *Server) searchToolOutput(ctx context.Context, arguments string) string {
	query, url := tools.WebSearchArguments(arguments)
	if url != "" {
		text, err := s.runtime.Search.Read(ctx, url)
		if err != nil {
			return searchFailureOutput("search_read_failed", err)
		}
		return text
	}
	result, err := s.runtime.Search.Search(ctx, query, s.cfg.Capabilities.Search.MaxResults)
	if err != nil {
		return searchFailureOutput("search_failed", err)
	}
	if result.RawText != "" {
		return result.RawText
	}
	data, _ := json.Marshal(result.Items)
	return string(data)
}

func searchFailureOutput(kind string, err error) string {
	data, _ := json.Marshal(map[string]any{
		"ok":      false,
		"error":   kind,
		"message": err.Error(),
	})
	return string(data)
}
