package adapters

import (
	"codex-bridge/internal/optimization"
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

func (mimoAdapter) ToolPolicy() ToolPolicy {
	return defaultAdapter{}.ToolPolicy()
}

func (mimoAdapter) Optimization() optimization.Options {
	return defaultAdapter{}.Optimization()
}

func (mimoAdapter) PrepareChatRequest(req providers.ChatCompletionRequest) providers.ChatCompletionRequest {
	return defaultAdapter{}.PrepareChatRequest(req)
}

func (mimoAdapter) CustomToolDescription(tool ToolDescriptor) string {
	return defaultAdapter{}.CustomToolDescription(tool)
}

func (mimoAdapter) NormalizeCustomInput(name string, input string) string {
	return defaultAdapter{}.NormalizeCustomInput(name, input)
}

func (mimoAdapter) NormalizePatchInput(input string) string {
	return defaultAdapter{}.NormalizePatchInput(input)
}

func (mimoAdapter) FormatToolOutput(tool ToolDescriptor, output string) string {
	return defaultAdapter{}.FormatToolOutput(tool, output)
}
