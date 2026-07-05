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
	return ToolPolicy{BlockShellFileWrites: true, RequiredToolChoice: true}
}

func (defaultAdapter) PrepareChatRequest(req providers.ChatCompletionRequest) providers.ChatCompletionRequest {
	return prepareChatPatchRequest(req)
}

func (defaultAdapter) PrepareResponseRequest(req map[string]any) map[string]any {
	return req
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
	if recovery := ToolRecoveryText(ClassifyToolFailure(tool, output)); recovery != "" {
		return output + "\n\n" + recovery
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
		"Claude-compatible text editor for reading and editing files.",
		"Use command=view with path and optional view_range to inspect files before editing.",
		"Use command=create with path and file_text to create a new file.",
		"Use command=str_replace with path, old_str, and new_str to replace exact existing text. old_str must be copied exactly from the current file.",
		"Use command=insert with path, insert_line, and insert_text to insert text after a line number; insert_line=0 inserts at the beginning.",
		"Do not invent command names. Use only view, create, str_replace, or insert.",
		"Do not use this tool for temporary helper scripts or scratch files. If the user says not to modify files, only use view.",
		"If old_str is not exact and unique, the edit will fail. Do not retry blindly; view the current file and send a smaller exact edit.",
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
