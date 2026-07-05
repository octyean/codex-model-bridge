package adapters

import (
	"strings"

	"codex-bridge/internal/optimization"
	"codex-bridge/internal/providers"
)

const kimiToolDisciplineNote = `KIMI_CODEX_TOOL_DISCIPLINE
Read and edit files with str_replace_based_edit_tool.
Use command=view before editing unless the exact current text is already present in the conversation.
Use command=create for new files, command=str_replace for exact replacements, and command=insert for line-based inserts.
Never call shell for file mutations. Do not use shell commands, redirects, tee, sed -i, perl -pi, Python file writes, Node fs writes, rm, mv, or cp for source, document, or config file changes.
Use shell only for reading files, searching, building, testing, formatting, and real project generators.
Do not create temporary helper scripts or scratch files for read-only inspection. If the user says not to modify files, only use command=view.
If a file edit fails, inspect the current target lines with command=view or read-only shell commands, then send a smaller exact edit.`

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
	if hasTool(req.Tools, "str_replace_based_edit_tool") && !hasKimiToolDisciplineNote(req.Messages) {
		req.Messages = append([]providers.ChatMessage{{
			Role:    "system",
			Content: kimiToolDisciplineNote,
		}}, req.Messages...)
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

func (kimiAdapter) PrepareResponseRequest(req map[string]any) map[string]any {
	if responseHasTool(req, "str_replace_based_edit_tool") && !responseInstructionsContain(req, "KIMI_CODEX_TOOL_DISCIPLINE") {
		prependResponseInstructions(req, kimiToolDisciplineNote)
	}
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

func responseHasTool(req map[string]any, name string) bool {
	rawTools, ok := req["tools"].([]any)
	if !ok {
		return false
	}
	for _, rawTool := range rawTools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		if toolName, _ := tool["name"].(string); toolName == name {
			return true
		}
		if function, ok := tool["function"].(map[string]any); ok {
			if toolName, _ := function["name"].(string); toolName == name {
				return true
			}
		}
	}
	return false
}

func responseInstructionsContain(req map[string]any, marker string) bool {
	text, _ := req["instructions"].(string)
	return strings.Contains(text, marker)
}

func prependResponseInstructions(req map[string]any, note string) {
	if text, _ := req["instructions"].(string); strings.TrimSpace(text) != "" {
		req["instructions"] = note + "\n\n" + text
		return
	}
	req["instructions"] = note
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
