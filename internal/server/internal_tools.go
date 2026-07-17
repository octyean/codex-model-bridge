package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
	"codex-bridge/internal/providers"
	"codex-bridge/internal/toollog"
	"codex-bridge/internal/tools"
)

const maxInternalToolRounds = 4

func (s *Server) hasInternalTools(toolCtx tools.Context) bool {
	for _, entry := range toolCtx.Tools {
		if entry.Kind() == tools.KindWebSearch || entry.Kind() == tools.KindTextEditor || entry.Kind() == tools.KindMCPResource || entry.Kind() == tools.KindTaskEnd {
			return true
		}
	}
	return false
}

func (s *Server) resolveInternalTools(ctx context.Context, provider providers.ChatProvider, sessionID string, req providers.ChatCompletionRequest, response *providers.ChatCompletionResponse, toolCtx tools.Context, adapter adapters.Adapter, logCtx toollog.OutputContext) (*providers.ChatCompletionResponse, providers.ChatCompletionRequest, providers.NormalizedUsage, bool, error) {
	current := response.Choices[0].Message
	currentReq := req
	resp := response
	totalUsage := providers.NormalizeUsage(response.Usage)
	handled := false
	sequence := 0
	taskProtocolRetries := 0
	explicitTaskEnd := toolCtx.Has(tools.TaskEndToolName)
	for {
		if explicitTaskEnd {
			_, result, ended, protocolErr := chatTaskEndResult(current, toolCtx)
			if protocolErr != nil {
				if taskProtocolRetries >= maxTaskProtocolRetries {
					violation := taskProtocolViolation(protocolErr)
					s.writeTaskProtocolFailure(sessionID, logCtx.RequestID, logCtx.Model, currentReq.Model, logCtx.Profile, violation.Error(), messageText(current.Content), false)
					return nil, providers.ChatCompletionRequest{}, totalUsage, false, violation
				}
				taskProtocolRetries++
				handled = true
				sequence++
				followUp := chatTaskProtocolRetryRequest(currentReq, current, adapter.ToolPolicy().RequiredToolChoice, adapter.PrepareChatRequest)
				s.writeTaskProtocolRetry(sessionID, logCtx.RequestID, logCtx.Model, followUp.Model, logCtx.Profile, protocolErr.Error(), messageText(current.Content), false)
				s.writePromptRequest(sessionID, logCtx.RequestID, logCtx.Model, followUp.Model, logCtx.Profile, "task_protocol_retry", providers.PreparedChatRequest(followUp), map[string]any{"sequence": sequence})
				next, err := provider.Create(ctx, followUp)
				if err != nil {
					s.writePromptFailure(sessionID, logCtx.RequestID, logCtx.Model, followUp.Model, logCtx.Profile, "task_protocol_retry", err.Error(), map[string]any{"sequence": sequence})
					return nil, providers.ChatCompletionRequest{}, totalUsage, false, err
				}
				s.writePromptResponse(sessionID, logCtx.RequestID, logCtx.Model, followUp.Model, logCtx.Profile, "task_protocol_retry", next, map[string]any{"sequence": sequence})
				totalUsage = addUsage(totalUsage, providers.NormalizeUsage(next.Usage))
				if len(next.Choices) == 0 {
					return next, followUp, totalUsage, true, nil
				}
				resp = next
				currentReq = followUp
				current = next.Choices[0].Message
				continue
			}
			if ended {
				resp.Choices[0].Message = providers.ChatMessage{Role: "assistant", Content: sanitizeTaskProtocolText(result)}
				return resp, currentReq, totalUsage, true, nil
			}
			current = chatWithoutTaskEndCalls(current, toolCtx)
			resp.Choices[0].Message = current
		}
		followUp, ok := s.internalToolFollowUpRequest(ctx, currentReq, current, toolCtx, adapter, logCtx)
		if !ok {
			break
		}
		if sequence >= maxInternalToolRounds {
			return nil, providers.ChatCompletionRequest{}, totalUsage, false, fmt.Errorf("internal tool follow-up exceeded %d rounds", maxInternalToolRounds)
		}
		sequence++
		s.writePromptRequest(sessionID, logCtx.RequestID, logCtx.Model, followUp.Model, logCtx.Profile, "internal_tool_followup", providers.PreparedChatRequest(followUp), map[string]any{"sequence": sequence})
		next, err := provider.Create(ctx, followUp)
		if err != nil {
			s.logger.Error("internal_tool_followup_failed", "error", err.Error())
			s.writePromptFailure(sessionID, logCtx.RequestID, logCtx.Model, followUp.Model, logCtx.Profile, "internal_tool_followup", err.Error(), map[string]any{"sequence": sequence})
			return nil, providers.ChatCompletionRequest{}, totalUsage, false, err
		}
		s.writePromptResponse(sessionID, logCtx.RequestID, logCtx.Model, followUp.Model, logCtx.Profile, "internal_tool_followup", next, map[string]any{"sequence": sequence})
		totalUsage = addUsage(totalUsage, providers.NormalizeUsage(next.Usage))
		if len(next.Choices) == 0 {
			return next, followUp, totalUsage, true, nil
		}
		resp = next
		currentReq = followUp
		current = next.Choices[0].Message
		handled = true
	}
	if !handled {
		return nil, providers.ChatCompletionRequest{}, totalUsage, false, nil
	}
	return resp, currentReq, totalUsage, true, nil
}

func (s *Server) internalToolFollowUpRequest(ctx context.Context, req providers.ChatCompletionRequest, message providers.ChatMessage, toolCtx tools.Context, adapter adapters.Adapter, logCtx toollog.OutputContext) (providers.ChatCompletionRequest, bool) {
	if len(message.ToolCalls) == 0 {
		return providers.ChatCompletionRequest{}, false
	}
	type resolvedOutput struct {
		call           providers.ChatToolCall
		entry          tools.Entry
		modelArguments string
		arguments      string
		descriptor     adapters.ToolDescriptor
		output         string
	}
	resolved := make([]resolvedOutput, 0, len(message.ToolCalls))
	for _, call := range message.ToolCalls {
		entry := toolCtx.Entry(call.Function.Name)
		arguments := tools.CanonicalArguments(entry, call.Function.Arguments)
		output, descriptor, ok := s.internalToolOutput(ctx, req, entry, arguments, toolCtx, adapter)
		if !ok {
			return providers.ChatCompletionRequest{}, false
		}
		resolved = append(resolved, resolvedOutput{call: call, entry: entry, modelArguments: call.Function.Arguments, arguments: arguments, descriptor: descriptor, output: output})
	}
	if len(resolved) == 0 {
		return providers.ChatCompletionRequest{}, false
	}
	var outputs []providers.ChatMessage
	internalMessage := message
	internalMessage.ToolCalls = make([]providers.ChatToolCall, 0, len(resolved))
	for _, item := range resolved {
		formattedOutput := adapters.FormatToolOutputWithArguments(adapter, item.descriptor, item.arguments, item.output)
		toollog.ToolCall(logCtx.RequestID, logCtx.Model, logCtx.Profile, item.call.ID, item.entry, item.modelArguments, message.ReasoningContent)
		toollog.ToolCallFrame(logCtx.RequestID, logCtx.Model, logCtx.Profile, item.call.ID, item.entry, item.modelArguments, item.arguments, tools.RuntimeArguments(item.entry, item.arguments))
		toollog.ToolOutput(logCtx, item.call.ID, item.descriptor, item.arguments, item.output, formattedOutput)
		internalMessage.ToolCalls = append(internalMessage.ToolCalls, item.call)
		outputs = append(outputs, providers.ChatMessage{
			Role:       "tool",
			ToolCallID: item.call.ID,
			Content:    formattedOutput,
		})
	}
	followUp := req
	followUp.ToolChoice = "auto"
	if toolCtx.Has(tools.TaskEndToolName) && adapter.ToolPolicy().RequiredToolChoice {
		followUp.ToolChoice = "required"
	}
	followUp.Messages = append(append(followUp.Messages, internalMessage), outputs...)
	followUp = adapter.PrepareChatRequest(followUp)
	return followUp, true
}

func (s *Server) internalToolOutput(ctx context.Context, req providers.ChatCompletionRequest, entry tools.Entry, arguments string, toolCtx tools.Context, adapter adapters.Adapter) (string, adapters.ToolDescriptor, bool) {
	switch {
	case entry.Kind() == tools.KindWebSearch:
		return s.searchToolOutput(ctx, arguments), adapters.ToolDescriptor{Name: tools.WebSearchProxyToolName, Kind: tools.KindWebSearch, OriginalType: "web_search_preview"}, true
	case entry.Kind() == tools.KindTextEditor:
		input := tools.ExtractCustomToolInputWithWorkspace(entry, arguments, adapter, toolCtx.Workspace)
		if strings.TrimSpace(input) == "" {
			return tools.TextEditorInvalidArgumentsResult(""), internalToolDescriptor(entry), true
		}
		if strings.HasPrefix(strings.TrimSpace(input), "TEXT_EDITOR_") {
			return input, internalToolDescriptor(entry), true
		}
		return "", adapters.ToolDescriptor{}, false
	default:
		return "", adapters.ToolDescriptor{}, false
	}
}

func internalToolDescriptor(entry tools.Entry) adapters.ToolDescriptor {
	descriptor := entry.Descriptor
	descriptor.Name = entry.Name()
	if descriptor.SideEffect == "" || descriptor.SideEffect == tools.SideEffectNone {
		descriptor.SideEffect = tools.SideEffectRead
	}
	return descriptor
}

func (s *Server) localToolResultResolver(logCtx toollog.OutputContext, toolCtx tools.Context) toolCallLocalResolver {
	return func(ctx context.Context, callID string, entry tools.Entry, _ string, canonicalArguments string, _ string) (codex.ResponseItem, bool) {
		if entry.Kind() != tools.KindWebSearch {
			return nil, false
		}
		output := s.searchToolOutput(ctx, canonicalArguments)
		toollog.ToolOutput(logCtx, callID, entry.Descriptor, canonicalArguments, output, output)
		item := localToolResultMessageItem(callID, output)
		toollog.ToolCallRerouted(logCtx.RequestID, logCtx.Model, logCtx.Profile, callID, entry, canonicalArguments, "message", output, "server_executed_internal_tool")
		return item, true
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
