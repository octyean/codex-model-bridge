package adapters

import (
	"codex-bridge/internal/optimization"
	"codex-bridge/internal/providers"
)

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

func (mimoAdapter) Optimization() optimization.Options {
	return optimization.Options{
		StabilizeTools:   true,
		CacheDiagnostics: true,
	}
}

func (mimoAdapter) PrepareChatRequest(req providers.ChatCompletionRequest) providers.ChatCompletionRequest {
	req.Messages = repairToolPairing(req.Messages)
	req = optimization.PrepareRequest(req, mimoAdapter{}.Optimization())
	req = prepareChatPatchRequest(req)
	if req.Stream && req.StreamOptions == nil {
		req.StreamOptions = &providers.StreamOptions{IncludeUsage: true}
	}
	req.AssistantToolContentNull = true
	return req
}

func (mimoAdapter) PrepareResponseRequest(req map[string]any) map[string]any {
	return req
}
