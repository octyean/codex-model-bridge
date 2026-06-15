package adapters

import (
	"sort"
	"strings"

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
	req = prepareChatPatchRequest(req)
	if req.Stream && req.StreamOptions == nil {
		req.StreamOptions = &providers.StreamOptions{IncludeUsage: true}
	}
	req.AssistantToolContentNull = true
	return req
}

func (deepSeekAdapter) CustomToolDescription(tool ToolDescriptor) string {
	if tool.Kind != "patch" {
		return defaultAdapter{}.CustomToolDescription(tool)
	}
	parts := []string{
		chatPatchToolDescription(tool),
		"If the user gives exact replacement or insertion text, copy that text verbatim. Do not paraphrase, invent examples, rename commands, or change quoted content.",
		"For style, markup, config, and template files, new lines inside an existing block must preserve the surrounding indentation style exactly.",
		"If the tool reports APPLY_PATCH_CONTEXT_MISMATCH, the next action must be reading the current target file lines before any new apply_patch call.",
		"Never retry the same patch after a context mismatch. Generate a smaller patch from freshly inspected file content.",
	}
	return strings.Join(parts, "\n")
}

func (deepSeekAdapter) NormalizeCustomInput(name string, input string) string {
	if name == "apply_patch" {
		return deepSeekAdapter{}.NormalizePatchInput(input)
	}
	return input
}

func (deepSeekAdapter) NormalizePatchInput(input string) string {
	return RepairDeepSeekPatchInput(NormalizePatchInput(input))
}

func (deepSeekAdapter) FormatToolOutput(tool ToolDescriptor, output string) string {
	if tool.Kind == "patch" {
		kind := ClassifyPatchFailure(output)
		if recovery := PatchRecoveryText(kind); recovery != "" {
			return output + "\n\n" + recovery
		}
		if PatchSucceeded(output) {
			return output + "\n\n" + "APPLY_PATCH_SUCCEEDED\nfile_edit_state: completed\nnext_action: stop_or_summarize\nforbidden_next_action: patch_same_file_again_without_user_request"
		}
	}
	return DefaultToolOutput(tool, output)
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
