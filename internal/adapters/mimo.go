package adapters

import (
	"strings"

	"codex-bridge/internal/providers"
)

const mimoToolDisciplineNote = `MIMO_CODEX_TOOL_DISCIPLINE
Edit files with str_replace_based_edit_tool.
Read files with codex_context_resource or read-only shell commands before editing unless the exact current text is already present in the conversation.
Use command=create for new files, command=str_replace for exact replacements, and command=insert for line-based inserts.
Never call shell for file mutations. Do not use shell commands, redirects, tee, sed -i, perl -pi, Python file writes, Node fs writes, rm, mv, or cp for source, document, or config file changes.
Use shell only for reading files, searching, building, testing, formatting, and real project generators.
Do not create temporary helper scripts or scratch files for read-only inspection. If the user says not to modify files, do not use str_replace_based_edit_tool.
If a file edit fails, inspect the current target lines with codex_context_resource or read-only shell commands, then send a smaller exact edit.`

const mimoStructuredOutputNote = `MIMO_CODEX_STRUCTURED_OUTPUT
The final assistant content must be one valid JSON object matching the requested response_format schema.
Use exactly the JSON property names defined in the schema. Do not invent or rename keys.
Do not include markdown, code fences, explanations, metadata, or any text outside the JSON object.`

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
	if req.ResponseFormat != nil && !hasMimoStructuredOutputNote(req.Messages) {
		req.Messages = append([]providers.ChatMessage{{
			Role:    "system",
			Content: mimoStructuredOutputInstruction(req.ResponseFormat),
		}}, req.Messages...)
	}
	if hasTool(req.Tools, "str_replace_based_edit_tool") && !hasMimoToolDisciplineNote(req.Messages) {
		req.Messages = append([]providers.ChatMessage{{
			Role:    "system",
			Content: mimoToolDisciplineNote,
		}}, req.Messages...)
	}
	return defaultAdapter{}.PrepareChatRequest(req)
}

func mimoStructuredOutputInstruction(responseFormat any) string {
	schema := responseFormatSchema(responseFormat)
	if schema == "" {
		return mimoStructuredOutputNote
	}
	return mimoStructuredOutputNote + "\nJSON schema:\n" + schema
}

func responseFormatSchema(responseFormat any) string {
	format, ok := responseFormat.(map[string]any)
	if !ok {
		return ""
	}
	jsonSchema, ok := format["json_schema"].(map[string]any)
	if !ok {
		return ""
	}
	return canonicalJSON(jsonSchema["schema"])
}

func (mimoAdapter) PrepareResponseRequest(req map[string]any) map[string]any {
	if responseHasTool(req, "str_replace_based_edit_tool") && !responseInstructionsContain(req, "MIMO_CODEX_TOOL_DISCIPLINE") {
		prependResponseInstructions(req, mimoToolDisciplineNote)
	}
	return req
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

func hasMimoStructuredOutputNote(messages []providers.ChatMessage) bool {
	for _, message := range messages {
		if message.Role != "system" {
			continue
		}
		if text, ok := message.Content.(string); ok && strings.Contains(text, "MIMO_CODEX_STRUCTURED_OUTPUT") {
			return true
		}
	}
	return false
}
