package server

import (
	"context"
	"encoding/json"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
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
	var outputs []providers.ChatMessage
	for _, call := range message.ToolCalls {
		if call.Function.Name != tools.WebSearchProxyToolName {
			return providers.ChatCompletionRequest{}, false
		}
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

func (s *Server) searchToolOutput(ctx context.Context, arguments string) string {
	query, url := tools.WebSearchArguments(arguments)
	if url != "" {
		text, err := s.runtime.Search.Read(ctx, url)
		if err != nil {
			return "Search read failed: " + err.Error()
		}
		return text
	}
	result, err := s.runtime.Search.Search(ctx, query, s.cfg.Capabilities.Search.MaxResults)
	if err != nil {
		return "Search failed: " + err.Error()
	}
	if result.RawText != "" {
		return result.RawText
	}
	data, _ := json.Marshal(result.Items)
	return string(data)
}
