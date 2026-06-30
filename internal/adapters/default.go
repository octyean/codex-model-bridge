package adapters

import (
	"encoding/json"
	"strings"

	"codex-bridge/internal/optimization"
	"codex-bridge/internal/providers"
)

type defaultAdapter struct{}

func (defaultAdapter) Name() string {
	return DefaultName
}

func (defaultAdapter) Capabilities() Capabilities {
	return Capabilities{
		InputModalities:            []string{"text"},
		SupportsSearchTool:         true,
		ExperimentalSupportedTools: []string{"function", "custom", "apply_patch", "tool_search", "local_shell"},
	}
}

func (defaultAdapter) Optimization() optimization.Options {
	return optimization.Options{}
}

func (defaultAdapter) ToolPolicy() ToolPolicy {
	return ToolPolicy{BlockShellFileWrites: true}
}

func (defaultAdapter) PrepareChatRequest(req providers.ChatCompletionRequest) providers.ChatCompletionRequest {
	return prepareChatPatchRequest(req)
}

func (defaultAdapter) CustomToolDescription(tool ToolDescriptor) string {
	if tool.Kind == "text_editor_patch" {
		return textEditorToolDescription()
	}
	if tool.Kind == "patch" {
		return chatPatchToolDescription(tool)
	}
	if len(tool.Raw) > 0 {
		if meta := canonicalJSON(tool.Raw); meta != "" {
			return "Submit complete freeform input for this Codex custom tool.\nOriginal tool metadata: " + meta
		}
	}
	if tool.Description != "" {
		return tool.Description
	}
	return "Submit complete freeform input for this Codex custom tool."
}

func (defaultAdapter) NormalizeCustomInput(name string, input string) string {
	if name == "apply_patch" {
		return defaultAdapter{}.NormalizePatchInput(input)
	}
	return input
}

func (defaultAdapter) NormalizePatchInput(input string) string {
	return NormalizePatchInput(input)
}

func (defaultAdapter) FormatToolOutput(tool ToolDescriptor, output string) string {
	if tool.Kind == "text_editor_patch" {
		return formatTextEditorToolOutput(output)
	}
	return DefaultToolOutput(tool, output)
}

func objectParameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":true}`)
}

func prepareChatPatchRequest(req providers.ChatCompletionRequest) providers.ChatCompletionRequest {
	if !hasApplyPatchTool(req.Tools) || hasPatchSystemInstruction(req.Messages) {
		return req
	}
	req.Messages = append([]providers.ChatMessage{{
		Role:    "system",
		Content: chatPatchSystemInstruction,
	}}, req.Messages...)
	return req
}

func hasApplyPatchTool(items []providers.ChatTool) bool {
	for _, item := range items {
		if item.Function.Name == "apply_patch" {
			return true
		}
	}
	return false
}

func hasPatchSystemInstruction(messages []providers.ChatMessage) bool {
	for _, message := range messages {
		if message.Role != "system" {
			continue
		}
		if text, ok := message.Content.(string); ok && strings.Contains(text, "CHAT_COMPLETIONS_APPLY_PATCH_CONTRACT") {
			return true
		}
	}
	return false
}

func textEditorToolDescription() string {
	return strings.Join([]string{
		"Edit files through Codex's text editor bridge. The real file write is executed by Codex's native file-edit handler after this tool call is converted by the bridge.",
		"Use command=create to create a new file with path and file_text/text.",
		"Use command=str_replace to replace exact old_str with new_str in path. old_str must be copied exactly from the current file.",
		"Use command=insert_after to insert text/new_str immediately after an exact insert_after anchor.",
		"Use command=move_file to rename or move path to destination_path/new_path. When the moved file content also needs a small edit, include exact old_str and replacement new_str in the same call.",
		"Use command=delete_file to delete path.",
		"Before str_replace or insert_after, inspect the target lines with read-only shell commands unless the current turn already contains the exact text.",
		"If old_str or insert_after is not exact and unique, the edit will fail. Do not retry blindly; read the current file and send a smaller exact edit.",
		"If the result says TEXT_EDITOR_ALREADY_APPLIED, do not repeat that same edit; verify current file content, then edit a different missing change or summarize.",
		"Use this editor tool for file writes.",
	}, "\n")
}

func formatTextEditorToolOutput(output string) string {
	output = sanitizeTextEditorOutput(output)
	kind := ClassifyPatchFailure(output)
	if recovery := TextEditorRecoveryText(kind); recovery != "" {
		return output + "\n\n" + recovery
	}
	if PatchSucceeded(output) {
		files := PatchSucceededFiles(output)
		extra := "TEXT_EDITOR_EDIT_SUCCEEDED\nfile_edit_state: completed"
		if len(files) > 0 {
			extra += "\nchanged_files: " + strings.Join(files, ", ")
		}
		extra += "\nnext_action: read_only_verify_or_summarize_or_continue_editing_if_needed\nallowed_next_action: grep_sed_diff_tests_or_text_editor_if_needed"
		return output + "\n\n" + extra
	}
	return output
}

func sanitizeTextEditorOutput(output string) string {
	output = strings.ReplaceAll(output, "apply_patch verification failed", "text editor verification failed")
	output = strings.ReplaceAll(output, "apply_patch failed", "text editor edit failed")
	return output
}
