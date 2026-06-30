package adapters

import (
	"strings"

	"codex-bridge/internal/optimization"
	"codex-bridge/internal/providers"
)

const kimiToolDisciplineNote = `KIMI_CODEX_TOOL_DISCIPLINE
Create, edit, move, and delete files only with codex_text_editor.
For renames and moves, use codex_text_editor command=move_file. If the moved file also needs a small content edit, include exact old_str and new_str in that same move_file call.
Never call shell for file mutations. Do not use shell commands, redirects, tee, sed -i, perl -pi, Python file writes, Node fs writes, rm, mv, or cp for source, document, or config file changes.
Use shell only for reading files, searching, building, testing, formatting, and real project generators.
If a file edit fails, inspect the current target lines with read-only shell commands, then send a smaller exact codex_text_editor edit.`

type kimiAdapter struct{}

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
	return ToolPolicy{BlockShellFileWrites: true}
}

func (kimiAdapter) Optimization() optimization.Options {
	return optimization.Options{
		StabilizeTools:   true,
		CacheDiagnostics: true,
	}
}

func (kimiAdapter) PrepareChatRequest(req providers.ChatCompletionRequest) providers.ChatCompletionRequest {
	if hasOpenVikingReadTool(req.Tools) && !hasDeepSeekToolBoundaryNote(req.Messages) {
		req.Messages = append([]providers.ChatMessage{{
			Role:    "system",
			Content: deepSeekOpenVikingToolBoundaryNote,
		}}, req.Messages...)
	}
	if hasTool(req.Tools, "codex_text_editor") && !hasKimiToolDisciplineNote(req.Messages) {
		req.Messages = append([]providers.ChatMessage{{
			Role:    "system",
			Content: kimiToolDisciplineNote,
		}}, req.Messages...)
	}
	if name := ForcedToolName(req.ToolChoice); name != "" {
		req.Messages = append([]providers.ChatMessage{{
			Role:    "system",
			Content: "You must call the " + name + " tool in this turn unless the tool is unavailable.",
		}}, req.Messages...)
		req.ToolChoice = "auto"
	}
	req.Messages = repairToolPairing(req.Messages)
	req = optimization.PrepareRequest(req, kimiAdapter{}.Optimization())
	req = prepareChatPatchRequest(req)
	if req.Stream && req.StreamOptions == nil {
		req.StreamOptions = &providers.StreamOptions{IncludeUsage: true}
	}
	req.AssistantToolContentNull = true
	return req
}

func (kimiAdapter) CustomToolDescription(tool ToolDescriptor) string {
	return defaultAdapter{}.CustomToolDescription(tool)
}

func (kimiAdapter) NormalizeCustomInput(name string, input string) string {
	if name == "apply_patch" {
		return kimiAdapter{}.NormalizePatchInput(input)
	}
	return input
}

func (kimiAdapter) NormalizePatchInput(input string) string {
	return defaultAdapter{}.NormalizePatchInput(input)
}

func (kimiAdapter) FormatToolOutput(tool ToolDescriptor, output string) string {
	return defaultAdapter{}.FormatToolOutput(tool, output)
}

func hasTool(tools []providers.ChatTool, name string) bool {
	for _, tool := range tools {
		if tool.Function.Name == name {
			return true
		}
	}
	return false
}

func hasKimiToolDisciplineNote(messages []providers.ChatMessage) bool {
	for _, message := range messages {
		if message.Role != "system" {
			continue
		}
		if text, ok := message.Content.(string); ok && strings.Contains(text, "KIMI_CODEX_TOOL_DISCIPLINE") {
			return true
		}
	}
	return false
}
