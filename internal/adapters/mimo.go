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

func (mimoAdapter) ResponseDisciplineNote() string {
	return `MIMO_CODEX_FILE_EDIT_DISCIPLINE
When the user requests Codex apply_patch and the visible tools include write_file, replace_text, insert_text_at_line, insert_text_after_match, move_file, or delete_file, perform the requested edit with the matching visible tool.
These tools are the active apply_patch capability for this profile. Never claim apply_patch is unavailable and never ask the user to relax that requirement.`
}
