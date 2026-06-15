package adapters

import "codex-bridge/internal/providers"

type openAIAdapter struct{}

func (openAIAdapter) Name() string {
	return OpenAIName
}

func (openAIAdapter) Capabilities() Capabilities {
	return Capabilities{
		InputModalities:             []string{"text", "image"},
		SupportsImageDetailOriginal: true,
		SupportsSearchTool:          true,
		ExperimentalSupportedTools:  []string{"function", "custom", "apply_patch", "tool_search", "local_shell"},
	}
}

func (openAIAdapter) ToolPolicy() ToolPolicy {
	return defaultAdapter{}.ToolPolicy()
}

func (openAIAdapter) PrepareChatRequest(req providers.ChatCompletionRequest) providers.ChatCompletionRequest {
	return defaultAdapter{}.PrepareChatRequest(req)
}

func (openAIAdapter) CustomToolDescription(tool ToolDescriptor) string {
	return defaultAdapter{}.CustomToolDescription(tool)
}

func (openAIAdapter) NormalizeCustomInput(name string, input string) string {
	return defaultAdapter{}.NormalizeCustomInput(name, input)
}

func (openAIAdapter) NormalizePatchInput(input string) string {
	return defaultAdapter{}.NormalizePatchInput(input)
}

func (openAIAdapter) FormatToolOutput(tool ToolDescriptor, output string) string {
	return defaultAdapter{}.FormatToolOutput(tool, output)
}
