package adapters

import (
	"encoding/json"

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

func (defaultAdapter) PrepareChatRequest(req providers.ChatCompletionRequest) providers.ChatCompletionRequest {
	return req
}

func (defaultAdapter) CustomToolDescription(tool ToolDescriptor) string {
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
		return normalizeApplyPatchInput(input)
	}
	return input
}

func (defaultAdapter) FormatToolOutput(tool ToolDescriptor, output string) string {
	return DefaultToolOutput(tool, output)
}

func objectParameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":true}`)
}
