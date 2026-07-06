package adapters

import (
	"codex-bridge/internal/optimization"
	"codex-bridge/internal/providers"
)

type kimiAdapter struct{ defaultAdapter }

func (kimiAdapter) Name() string {
	return KimiName
}

func (kimiAdapter) Capabilities() Capabilities {
	return Capabilities{
		InputModalities:            []string{"text"},
		SupportsSearchTool:         true,
		ExperimentalSupportedTools: []string{"function", "custom", "apply_patch", "tool_search", "local_shell"},
	}
}

func (kimiAdapter) ToolPolicy() ToolPolicy {
	return ToolPolicy{}
}

func (kimiAdapter) Optimization() optimization.Options {
	return optimization.Options{
		StabilizeTools:   true,
		CacheDiagnostics: true,
	}
}

func (kimiAdapter) PrepareChatRequest(req providers.ChatCompletionRequest) providers.ChatCompletionRequest {
	req.Messages = repairToolPairing(req.Messages)
	req = optimization.PrepareRequest(req, kimiAdapter{}.Optimization())
	req = prepareChatPatchRequest(req)
	if req.Stream && req.StreamOptions == nil {
		req.StreamOptions = &providers.StreamOptions{IncludeUsage: true}
	}
	req.AssistantToolContentNull = true
	return req
}

func (kimiAdapter) PrepareResponseRequest(req map[string]any) map[string]any {
	return req
}
