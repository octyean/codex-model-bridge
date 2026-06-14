package adapters

import (
	"codex-bridge/internal/codex"
	"codex-bridge/internal/providers"
)

type mimoAdapter struct{}

func (mimoAdapter) Name() string {
	return MimoName
}

func (mimoAdapter) Capabilities() Capabilities {
	return Capabilities{
		InputModalities:             []string{"text", "image"},
		SupportsImageDetailOriginal: true,
		SupportsSearchTool:          true,
		ExperimentalSupportedTools:  []string{"function", "custom", "apply_patch", "tool_search", "local_shell"},
	}
}

func (mimoAdapter) PrepareChatRequest(req providers.ChatCompletionRequest) providers.ChatCompletionRequest {
	return defaultAdapter{}.PrepareChatRequest(req)
}

func (mimoAdapter) CustomToolDescription(name string, tool codex.ResponseTool) string {
	return defaultAdapter{}.CustomToolDescription(name, tool)
}

func (mimoAdapter) NormalizeCustomInput(name string, input string) string {
	return defaultAdapter{}.NormalizeCustomInput(name, input)
}
