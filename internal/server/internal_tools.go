package server

import (
	"context"
	"encoding/json"
	"strings"

	"codex-bridge/internal/codex"
	"codex-bridge/internal/providers"
)

const bridgeWebSearchTool = "web_search"

var bridgeWebSearchParameters = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"url":{"type":"string"}},"required":["query"],"additionalProperties":false}`)

func (s *Server) addInternalTools(req codex.ResponsesRequest, chatReq providers.ChatCompletionRequest) providers.ChatCompletionRequest {
	if !s.hasInternalTools(req) {
		return chatReq
	}
	chatReq.Tools = append(chatReq.Tools, providers.ChatTool{
		Type: "function",
		Function: providers.ChatFunction{
			Name:        bridgeWebSearchTool,
			Description: "Search the web through the bridge search capability. Use this when the user asks for current web information.",
			Parameters:  bridgeWebSearchParameters,
		},
	})
	return chatReq
}

func (s *Server) hasInternalTools(req codex.ResponsesRequest) bool {
	return s.runtime.HasSearch() && requestHasWebSearch(req.Tools)
}

func requestHasWebSearch(tools []codex.ResponseTool) bool {
	for _, tool := range tools {
		toolType := tool.Type
		if tool.Raw != nil {
			if rawType, ok := tool.Raw["type"].(string); ok {
				toolType = rawType
			}
		}
		if strings.HasPrefix(toolType, "web_search") {
			return true
		}
	}
	return false
}

func (s *Server) resolveInternalTools(ctx context.Context, provider providers.ChatProvider, req providers.ChatCompletionRequest, message providers.ChatMessage) (*providers.ChatCompletionResponse, providers.ChatCompletionRequest, bool) {
	followUp, ok := s.internalToolFollowUpRequest(ctx, req, message)
	if !ok {
		return nil, providers.ChatCompletionRequest{}, false
	}
	resp, err := provider.Create(ctx, followUp)
	if err != nil {
		s.logger.Error("internal_tool_followup_failed", "error", err.Error())
		return nil, providers.ChatCompletionRequest{}, false
	}
	return resp, followUp, true
}

func (s *Server) internalToolFollowUpRequest(ctx context.Context, req providers.ChatCompletionRequest, message providers.ChatMessage) (providers.ChatCompletionRequest, bool) {
	if len(message.ToolCalls) == 0 {
		return providers.ChatCompletionRequest{}, false
	}
	var outputs []providers.ChatMessage
	for _, call := range message.ToolCalls {
		if call.Function.Name != bridgeWebSearchTool {
			return providers.ChatCompletionRequest{}, false
		}
		outputs = append(outputs, providers.ChatMessage{
			Role:       "tool",
			ToolCallID: call.ID,
			Content:    s.searchToolOutput(ctx, call.Function.Arguments),
		})
	}
	followUp := req
	followUp.ToolChoice = "none"
	followUp.Messages = append(append(followUp.Messages, message), outputs...)
	followUp.Tools = nil
	return followUp, true
}

func (s *Server) searchToolOutput(ctx context.Context, arguments string) string {
	var args struct {
		Query string `json:"query"`
		URL   string `json:"url"`
	}
	_ = json.Unmarshal([]byte(arguments), &args)
	if args.URL != "" {
		text, err := s.runtime.Search.Read(ctx, args.URL)
		if err != nil {
			return "Search read failed: " + err.Error()
		}
		return text
	}
	result, err := s.runtime.Search.Search(ctx, args.Query, s.cfg.Capabilities.Search.MaxResults)
	if err != nil {
		return "Search failed: " + err.Error()
	}
	if result.RawText != "" {
		return result.RawText
	}
	data, _ := json.Marshal(result.Items)
	return string(data)
}
