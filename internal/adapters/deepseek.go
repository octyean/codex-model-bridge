package adapters

import (
	"codex-bridge/internal/optimization"
	"codex-bridge/internal/providers"
)

type deepSeekAdapter struct{ defaultAdapter }

func (deepSeekAdapter) Name() string {
	return DeepSeekName
}

func (deepSeekAdapter) Capabilities() Capabilities {
	return Capabilities{
		InputModalities:            []string{"text"},
		SupportsSearchTool:         true,
		ExperimentalSupportedTools: []string{"function", "custom", "apply_patch", "tool_search", "local_shell"},
	}
}

func (deepSeekAdapter) ToolPolicy() ToolPolicy {
	return ToolPolicy{BlockShellFileWrites: true}
}

func (deepSeekAdapter) Optimization() optimization.Options {
	return optimization.Options{
		StabilizeTools:   true,
		CacheDiagnostics: true,
	}
}

func (deepSeekAdapter) PrepareChatRequest(req providers.ChatCompletionRequest) providers.ChatCompletionRequest {
	req.Messages = repairToolPairing(req.Messages)
	req = optimization.PrepareRequest(req, deepSeekAdapter{}.Optimization())
	req = prepareChatPatchRequest(req)
	if req.Stream && req.StreamOptions == nil {
		req.StreamOptions = &providers.StreamOptions{IncludeUsage: true}
	}
	req.AssistantToolContentNull = true
	return req
}

func repairToolPairing(messages []providers.ChatMessage) []providers.ChatMessage {
	out := make([]providers.ChatMessage, 0, len(messages))
	for i := 0; i < len(messages); {
		message := messages[i]
		if message.Role == "assistant" && len(message.ToolCalls) > 0 {
			j := i + 1
			for j < len(messages) && messages[j].Role == "tool" {
				j++
			}
			out = append(out, message)
			out = append(out, pairedToolMessages(message.ToolCalls, messages[i+1:j])...)
			i = j
			continue
		}
		if message.Role == "tool" {
			i++
			continue
		}
		out = append(out, message)
		i++
	}
	return out
}

func pairedToolMessages(calls []providers.ChatToolCall, candidates []providers.ChatMessage) []providers.ChatMessage {
	byID := map[string]providers.ChatMessage{}
	for _, candidate := range candidates {
		byID[candidate.ToolCallID] = candidate
	}
	out := make([]providers.ChatMessage, 0, len(calls))
	for _, call := range calls {
		if message, ok := byID[call.ID]; ok {
			out = append(out, message)
			continue
		}
		out = append(out, providers.ChatMessage{
			Role:       "tool",
			ToolCallID: call.ID,
			Content:    "[no result: the previous turn was interrupted before this tool call completed]",
		})
	}
	return out
}
