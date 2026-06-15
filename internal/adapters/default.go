package adapters

import (
	"encoding/json"
	"strings"

	"codex-bridge/internal/providers"
)

type defaultAdapter struct{}

func (defaultAdapter) Name() string {
	return DefaultName
}

func (defaultAdapter) Capabilities() Capabilities {
	return Capabilities{
		InputModalities:            []string{"text"},
		SupportsSearchTool:         true,
		ExperimentalSupportedTools: []string{"function", "custom", "apply_patch", "tool_search", "local_shell"},
	}
}

func (defaultAdapter) ToolPolicy() ToolPolicy {
	return ToolPolicy{}
}

func (defaultAdapter) PrepareChatRequest(req providers.ChatCompletionRequest) providers.ChatCompletionRequest {
	return prepareChatPatchRequest(req)
}

func (defaultAdapter) CustomToolDescription(tool ToolDescriptor) string {
	if tool.Kind == "patch" {
		return chatPatchToolDescription(tool)
	}
	if len(tool.Raw) > 0 {
		if meta := canonicalJSON(tool.Raw); meta != "" {
			return "Submit complete freeform input for this Codex custom tool.\nOriginal tool metadata: " + meta
		}
	}
	if tool.Description != "" {
		return tool.Description
	}
	return "Submit complete freeform input for this Codex custom tool."
}

func (defaultAdapter) NormalizeCustomInput(name string, input string) string {
	if name == "apply_patch" {
		return defaultAdapter{}.NormalizePatchInput(input)
	}
	return input
}

func (defaultAdapter) NormalizePatchInput(input string) string {
	return NormalizePatchInput(input)
}

func (defaultAdapter) FormatToolOutput(tool ToolDescriptor, output string) string {
	return DefaultToolOutput(tool, output)
}

func objectParameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":true}`)
}

func prepareChatPatchRequest(req providers.ChatCompletionRequest) providers.ChatCompletionRequest {
	if !hasApplyPatchTool(req.Tools) || hasPatchSystemInstruction(req.Messages) {
		return req
	}
	req.Messages = append([]providers.ChatMessage{{
		Role:    "system",
		Content: chatPatchSystemInstruction,
	}}, req.Messages...)
	return req
}

func hasApplyPatchTool(items []providers.ChatTool) bool {
	for _, item := range items {
		if item.Function.Name == "apply_patch" {
			return true
		}
	}
	return false
}

func hasPatchSystemInstruction(messages []providers.ChatMessage) bool {
	for _, message := range messages {
		if message.Role != "system" {
			continue
		}
		if text, ok := message.Content.(string); ok && strings.Contains(text, "CHAT_COMPLETIONS_APPLY_PATCH_CONTRACT") {
			return true
		}
	}
	return false
}
