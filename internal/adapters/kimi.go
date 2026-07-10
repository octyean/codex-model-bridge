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

func (kimiAdapter) PrepareResponseMessages(messages []providers.ChatMessage) []providers.ChatMessage {
	return repairToolPairing(messages)
}

func (kimiAdapter) PrepareResponseRequest(req map[string]any) map[string]any {
	return req
}

func (kimiAdapter) ResponseDisciplineNote() string {
	return `KIMI_CODEX_RESPONSE_DISCIPLINE
When an active instruction says the final response must contain only or exactly specific text, output exactly that text with no prefix, suffix, verification summary, or restatement of completed work.
Do not weaken an exact final-response constraint merely because the requested repository work succeeded.
When read_file output contains READ_FILE_RANGE_LIMIT_REACHED, the file is not fully read. Continue from the required start_line before claiming full-file review or deriving conclusions from the partial range.
After a failed command, check, test, or incomplete edit, do not end the turn with a content-only progress message. Call the next corrective tool in the same response unless user input is genuinely required.`
}
