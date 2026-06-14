package adapters

import (
	"sort"
	"strings"

	"codex-bridge/internal/codex"
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

func (deepSeekAdapter) PrepareChatRequest(req providers.ChatCompletionRequest) providers.ChatCompletionRequest {
	if name := ForcedToolName(req.ToolChoice); name != "" {
		req.Messages = append([]providers.ChatMessage{{
			Role:    "system",
			Content: "The upstream DeepSeek thinking mode does not accept forced tool_choice. You must call the " + name + " tool in this turn unless the tool is unavailable.",
		}}, req.Messages...)
		req.ToolChoice = "auto"
	}
	req.Messages = repairToolPairing(req.Messages)
	req.Tools = stableTools(req.Tools)
	if req.Stream && req.StreamOptions == nil {
		req.StreamOptions = &providers.StreamOptions{IncludeUsage: true}
	}
	req.AssistantToolContentNull = true
	return req
}

func (deepSeekAdapter) CustomToolDescription(name string, tool codex.ResponseTool) string {
	if name != "apply_patch" {
		return defaultAdapter{}.CustomToolDescription(name, tool)
	}
	parts := []string{
		"Submit a complete Codex apply_patch patch as the input string.",
		"The input must start with *** Begin Patch and end with *** End Patch.",
		"Do not wrap the patch in Markdown fences, JSON text, or explanatory prose.",
		"Example: *** Begin Patch\n*** Add File: hello.txt\n+hello\n*** End Patch\n",
	}
	if len(tool.Raw) > 0 {
		if meta := canonicalJSON(tool.Raw); meta != "" {
			parts = append(parts, "Original tool metadata: "+meta)
		}
	}
	return strings.Join(parts, "\n")
}

func (deepSeekAdapter) NormalizeCustomInput(name string, input string) string {
	if name == "apply_patch" {
		return normalizeApplyPatchInput(input)
	}
	return input
}

func stableTools(tools []providers.ChatTool) []providers.ChatTool {
	out := append([]providers.ChatTool(nil), tools...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Function.Name < out[j].Function.Name
	})
	return out
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
