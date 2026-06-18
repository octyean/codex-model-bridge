package adapters

import (
	"strings"

	"codex-bridge/internal/optimization"
	"codex-bridge/internal/providers"
)

type deepSeekAdapter struct{}

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
	if hasOpenVikingReadTool(req.Tools) && !hasDeepSeekToolBoundaryNote(req.Messages) {
		req.Messages = append([]providers.ChatMessage{{
			Role:    "system",
			Content: deepSeekOpenVikingToolBoundaryNote,
		}}, req.Messages...)
	}
	if name := ForcedToolName(req.ToolChoice); name != "" {
		req.Messages = append([]providers.ChatMessage{{
			Role:    "system",
			Content: "The upstream DeepSeek thinking mode does not accept forced tool_choice. You must call the " + name + " tool in this turn unless the tool is unavailable.",
		}}, req.Messages...)
		req.ToolChoice = "auto"
	}
	req.Messages = repairToolPairing(req.Messages)
	req = optimization.PrepareRequest(req, deepSeekAdapter{}.Optimization())
	req = prepareChatPatchRequest(req)
	if req.Stream && req.StreamOptions == nil {
		req.StreamOptions = &providers.StreamOptions{IncludeUsage: true}
	}
	req.AssistantToolContentNull = true
	return req
}

const deepSeekOpenVikingToolBoundaryNote = "OPENVIKING_READ_TOOL_BOUNDARY: OpenViking memory read tools only read viking:// URIs. Do not pass file:// URLs or local filesystem paths to OpenViking read. Read local files, skills, and repository sources with the available local file or shell tools instead."

func (deepSeekAdapter) CustomToolDescription(tool ToolDescriptor) string {
	return defaultAdapter{}.CustomToolDescription(tool)
}

func (deepSeekAdapter) NormalizeCustomInput(name string, input string) string {
	if name == "apply_patch" {
		return deepSeekAdapter{}.NormalizePatchInput(input)
	}
	return input
}

func (deepSeekAdapter) NormalizePatchInput(input string) string {
	return defaultAdapter{}.NormalizePatchInput(input)
}

func (deepSeekAdapter) FormatToolOutput(tool ToolDescriptor, output string) string {
	return defaultAdapter{}.FormatToolOutput(tool, output)
}

func hasOpenVikingReadTool(tools []providers.ChatTool) bool {
	for _, tool := range tools {
		name := strings.ToLower(tool.Function.Name)
		description := strings.ToLower(tool.Function.Description)
		if strings.Contains(name, "openviking") && name == "read" {
			return true
		}
		if strings.Contains(name, "openviking") && strings.Contains(name, "read") {
			return true
		}
		if strings.Contains(description, "openviking") && strings.Contains(name, "read") {
			return true
		}
		if strings.Contains(description, "viking://") && strings.Contains(name, "read") {
			return true
		}
	}
	return false
}

func hasDeepSeekToolBoundaryNote(messages []providers.ChatMessage) bool {
	for _, message := range messages {
		if message.Role != "system" {
			continue
		}
		if text, ok := message.Content.(string); ok && strings.Contains(text, "OPENVIKING_READ_TOOL_BOUNDARY") {
			return true
		}
	}
	return false
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
