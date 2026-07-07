package adapters

import "codex-bridge/internal/providers"

type mimoAdapter struct{ defaultAdapter }

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

func (mimoAdapter) PrepareResponseRequest(req map[string]any) map[string]any {
	return req
}
