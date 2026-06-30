package adapters

import (
	"strings"

	"codex-bridge/internal/optimization"
	"codex-bridge/internal/providers"
)

const mimoToolDisciplineNote = `MIMO_CODEX_TOOL_DISCIPLINE
Create, edit, move, and delete files only with codex_text_editor.
For renames and moves, use codex_text_editor command=move_file. If the moved file also needs a small content edit, include exact old_str and new_str in that same move_file call.
Never call shell for file mutations. Do not use shell commands, redirects, tee, sed -i, perl -pi, Python file writes, Node fs writes, rm, mv, or cp for source, document, or config file changes.
Use shell only for reading files, searching, building, testing, formatting, and real project generators.
If a file edit fails, inspect the current target lines with read-only shell commands, then send a smaller exact codex_text_editor edit.`

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
	if hasTool(req.Tools, "codex_text_editor") && !hasMimoToolDisciplineNote(req.Messages) {
		req.Messages = append([]providers.ChatMessage{{
			Role:    "system",
			Content: mimoToolDisciplineNote,
		}}, req.Messages...)
	}
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

func hasMimoToolDisciplineNote(messages []providers.ChatMessage) bool {
	for _, message := range messages {
		if message.Role != "system" {
			continue
		}
		if text, ok := message.Content.(string); ok && strings.Contains(text, "MIMO_CODEX_TOOL_DISCIPLINE") {
			return true
		}
	}
	return false
}
