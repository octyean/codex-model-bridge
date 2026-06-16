package adapters

import (
	"encoding/json"
	"strings"

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

func (defaultAdapter) ToolPolicy() ToolPolicy {
	return ToolPolicy{BlockShellFileWrites: true}
}

func (defaultAdapter) PrepareChatRequest(req providers.ChatCompletionRequest) providers.ChatCompletionRequest {
	req = prepareTextEditorRequest(req)
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

func prepareTextEditorRequest(req providers.ChatCompletionRequest) providers.ChatCompletionRequest {
	if hasTextEditorTool(req.Tools) && shouldAddTextEditorSuccessStopNote(req.Messages) {
		req.Messages = append([]providers.ChatMessage{{
			Role:    "system",
			Content: textEditorSuccessStopNote(req.Messages),
		}}, req.Messages...)
	}
	return req
}

func hasTextEditorTool(items []providers.ChatTool) bool {
	for _, item := range items {
		if item.Function.Name == "codex_text_editor" {
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
		"Use command=delete_file to delete path.",
		"Before str_replace or insert_after, inspect the target lines with read-only shell commands unless the current turn already contains the exact text.",
		"If old_str or insert_after is not exact and unique, the edit will fail. Do not retry blindly; read the current file and send a smaller exact edit.",
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
		extra += "\nnext_action: read_only_verify_or_summarize_or_edit_different_file\nallowed_next_action: grep_sed_diff_tests_or_text_editor_different_file\nforbidden_next_action: text_editor_same_file_again_without_verified_missing_edit"
		return output + "\n\n" + extra
	}
	return output
}

func sanitizeTextEditorOutput(output string) string {
	output = strings.ReplaceAll(output, "apply_patch verification failed", "text editor verification failed")
	output = strings.ReplaceAll(output, "apply_patch failed", "text editor edit failed")
	return output
}

func shouldAddTextEditorSuccessStopNote(messages []providers.ChatMessage) bool {
	if hasTextEditorSuccessStopNote(messages) {
		return false
	}
	return len(textEditorImmediateSuccessOutputs(messages)) > 0
}

func TextEditorCooldownFiles(messages []providers.ChatMessage) []string {
	files := make([]string, 0)
	seen := map[string]bool{}
	add := func(file string) {
		file = normalizePatchFilePath(file)
		if file == "" || seen[file] {
			return
		}
		seen[file] = true
		files = append(files, file)
	}
	for _, output := range textEditorImmediateSuccessOutputs(messages) {
		for _, file := range PatchSucceededFiles(output) {
			add(file)
		}
	}
	return files
}

func textEditorSuccessStopNote(messages []providers.ChatMessage) string {
	files := TextEditorCooldownFiles(messages)
	if len(files) == 0 {
		return "TEXT_EDITOR_SUCCESS_STOP: The previous text editor call already succeeded. In this immediate follow-up turn, do not call the text editor again for the same completed edit. If no different file still needs editing, stop editing now and write the final answer. Use read-only verification commands such as grep, sed, git diff, or tests if you need evidence."
	}
	return "TEXT_EDITOR_SUCCESS_STOP: The previous text editor call already succeeded for these files: " + strings.Join(files, ", ") + ". Do not call the text editor on those files again in this immediate follow-up turn, and never use delete_file to recover from a duplicate edit. If all requested edits are already on the listed files, stop editing now and write the final answer. The text editor remains available only for different files explicitly still needing edits. Use read-only verification commands such as grep, sed, git diff, or tests for the listed files if you need evidence."
}

func textEditorImmediateSuccessOutputs(messages []providers.ChatMessage) []string {
	start := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			start = i + 1
			break
		}
	}
	var outputs []string
	verifiedAfterSuccess := false
	pendingTextEditorOutput := false
	for _, message := range messages[start:] {
		if message.Role == "assistant" && len(message.ToolCalls) > 0 {
			pendingTextEditorOutput = onlyTextEditorToolCalls(message.ToolCalls)
			if !pendingTextEditorOutput && len(outputs) > 0 {
				verifiedAfterSuccess = true
			}
			continue
		}
		text, _ := message.Content.(string)
		if message.Role == "system" && strings.Contains(text, "TEXT_EDITOR_HISTORY_OUTPUT_HIDDEN") && textEditorOutputSucceeded(text) {
			outputs = append(outputs, text)
			verifiedAfterSuccess = false
			continue
		}
		if message.Role == "tool" && pendingTextEditorOutput && textEditorOutputSucceeded(text) {
			outputs = append(outputs, text)
			verifiedAfterSuccess = false
		}
	}
	if verifiedAfterSuccess {
		return nil
	}
	return outputs
}

func textEditorOutputSucceeded(text string) bool {
	return strings.Contains(text, "TEXT_EDITOR_EDIT_SUCCEEDED") || strings.Contains(text, "APPLY_PATCH_SUCCEEDED") || PatchSucceeded(text)
}

func onlyTextEditorToolCalls(calls []providers.ChatToolCall) bool {
	for _, call := range calls {
		if call.Function.Name != "codex_text_editor" && call.Function.Name != "apply_patch" {
			return false
		}
	}
	return len(calls) > 0
}

func hasTextEditorSuccessStopNote(messages []providers.ChatMessage) bool {
	for _, message := range messages {
		if message.Role != "system" {
			continue
		}
		if text, ok := message.Content.(string); ok &&
			(strings.Contains(text, "TEXT_EDITOR_SUCCESS_STOP") ||
				strings.Contains(text, "DEEPSEEK_TEXT_EDITOR_SUCCESS_STOP") ||
				strings.Contains(text, "DEEPSEEK_APPLY_PATCH_SUCCESS_STOP")) {
			return true
		}
	}
	return false
}
