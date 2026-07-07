package adapters

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

const chatPatchSystemInstruction = `CHAT_COMPLETIONS_APPLY_PATCH_CONTRACT
apply_patch is a Codex freeform patch transported through Chat Completions function arguments.
The function arguments must decode to a complete patch string in input.
Use apply_patch when you choose to submit source, document, or config file changes as a patch.
Before editing an existing file, inspect the current target lines unless this turn already contains the exact current text.
apply_patch cannot read files. There is no *** Read File operation; use read-only shell commands for file inspection.
Prefer small, surgical hunks. For large files or multi-area edits, make separate minimal hunks instead of rewriting broad surrounding blocks.
For replacements, write the removed line with - immediately followed by the added line with +.
Only mark lines with - or + when their content must actually change. Do not remove and re-add unchanged surrounding lines.
Never write the old line as unchanged context and then also remove the same old line.
Never use an insertion-only hunk when the requested task is to replace existing text.
For Add File operations, do not use @@ hunks; every content line must start with +.
For appending to an existing file, use Update File, not Add File.
Unchanged context lines are byte-significant: copy indentation, tabs, spaces, and text exactly from the current file.
For whitespace-sensitive files, use the smallest valid hunk and avoid nearby context unless needed for uniqueness.
If apply_patch reports a context mismatch, do not retry the same patch. Read the current file and generate a smaller patch from exact current lines.
After apply_patch succeeds for a file, verify current state when needed. If another requested change is still missing in the same file, make the smallest follow-up patch from exact current context; otherwise summarize.`

type PatchFailureKind string

type ToolFailureKind string

const (
	PatchFailureNone                PatchFailureKind = ""
	PatchFailureContextMismatch     PatchFailureKind = "context_mismatch"
	PatchFailureMalformedPatch      PatchFailureKind = "malformed_patch"
	PatchFailureInvalidHunk         PatchFailureKind = "invalid_hunk"
	PatchFailureReadFileOperation   PatchFailureKind = "read_file_operation"
	PatchFailureAlreadyApplied      PatchFailureKind = "already_applied"
	PatchFailureNoProgress          PatchFailureKind = "no_progress"
	PatchFailurePathError           PatchFailureKind = "path_error"
	PatchFailurePermissionOrSandbox PatchFailureKind = "permission_or_sandbox"
	PatchFailureUnknown             PatchFailureKind = "unknown"
)

const (
	ToolFailureNone                     ToolFailureKind = ""
	ToolFailureMCPResourceLocalID       ToolFailureKind = "mcp_resource_local_identifier"
	ToolFailureMCPResourceServerUnknown ToolFailureKind = "mcp_resource_server_unknown"
	ToolFailureMCPResourceUnlistedID    ToolFailureKind = "mcp_resource_unlisted_identifier"
	ToolFailureMCPResourceReadFailed    ToolFailureKind = "mcp_resource_read_failed"
	ToolFailureMCPResourcesEmpty        ToolFailureKind = "mcp_resources_empty"
	ToolFailureToolSearchEmpty          ToolFailureKind = "tool_search_empty"
	ToolFailureRuntimeNoProgress        ToolFailureKind = "runtime_no_progress"
	ToolFailureSchemaValidation         ToolFailureKind = "schema_validation_error"
	ToolFailureExecutionError           ToolFailureKind = "tool_execution_error"
	ToolFailureStructuredFailure        ToolFailureKind = "structured_failure"
)

func NormalizePatchInput(input string) string {
	text := strings.ReplaceAll(input, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimSpace(text)
	if extracted, ok := extractPatchFromJSONEnvelope(text); ok {
		text = strings.ReplaceAll(extracted, "\r\n", "\n")
		text = strings.ReplaceAll(text, "\r", "\n")
		text = strings.TrimSpace(text)
	}
	text = stripMarkdownFence(text)
	return completePatchEnvelope(text)
}

func ClassifyPatchFailure(output string) PatchFailureKind {
	text := strings.ToLower(output)
	switch {
	case strings.Contains(text, "apply_patch_succeeded"),
		strings.Contains(text, "text_editor_edit_succeeded"),
		strings.Contains(text, "file_edit_state: completed"):
		return PatchFailureNone
	case strings.Contains(text, "*** read file:"):
		return PatchFailureReadFileOperation
	case strings.Contains(text, "text_editor_already_applied"):
		return PatchFailureAlreadyApplied
	case strings.Contains(text, "file_edit_state: not_modified"),
		strings.Contains(text, "text_editor_create_target_already_exists"),
		strings.Contains(text, "text_editor_move_target_same_as_source"),
		strings.Contains(text, "text_editor_move_target_is_directory"):
		return PatchFailureNoProgress
	case strings.Contains(text, "invalid hunk"),
		strings.Contains(text, "expected hunk"),
		strings.Contains(text, "expected line prefix"),
		strings.Contains(text, "expected context"),
		strings.Contains(text, "unexpected line found in update hunk"):
		return PatchFailureInvalidHunk
	case strings.Contains(text, "failed to find context"),
		strings.Contains(text, "failed to find expected lines"),
		strings.Contains(text, "verification failed"):
		return PatchFailureContextMismatch
	case strings.Contains(text, "permission denied"),
		strings.Contains(text, "sandbox denied"),
		strings.Contains(text, "outside workspace"):
		return PatchFailurePermissionOrSandbox
	case strings.Contains(text, "no such file"),
		strings.Contains(text, "file not found"),
		strings.Contains(text, "cannot open"):
		return PatchFailurePathError
	case strings.Contains(text, "invalid patch"),
		strings.Contains(text, "malformed"),
		strings.Contains(text, "parse patch"),
		strings.Contains(text, "begin patch"),
		strings.Contains(text, "end patch"):
		return PatchFailureMalformedPatch
	case strings.Contains(text, "apply_patch"):
		return PatchFailureUnknown
	default:
		return PatchFailureNone
	}
}

func ClassifyToolFailure(tool ToolDescriptor, output string) ToolFailureKind {
	return ClassifyToolFailureWithArguments(tool, "", output)
}

func ClassifyToolFailureWithArguments(tool ToolDescriptor, arguments string, output string) ToolFailureKind {
	trimmed := strings.TrimSpace(output)
	lower := strings.ToLower(trimmed)
	switch {
	case diagnosticMarkerLine(trimmed, "TOOL_RUNTIME_NO_PROGRESS"):
		return ToolFailureRuntimeNoProgress
	case toolSchemaValidationFailure(trimmed):
		return ToolFailureSchemaValidation
	case toolExecutionFailure(trimmed):
		return ToolFailureExecutionError
	case structuredToolFailure(trimmed):
		return ToolFailureStructuredFailure
	}
	switch tool.Name {
	case "read_mcp_resource":
		switch {
		case mcpResourceUsesLocalIdentifier(arguments):
			return ToolFailureMCPResourceLocalID
		case strings.Contains(lower, "unknown mcp server"):
			return ToolFailureMCPResourceServerUnknown
		case mcpResourceIdentifierNotListed(lower):
			return ToolFailureMCPResourceUnlistedID
		case strings.Contains(lower, "resources/read failed"):
			return ToolFailureMCPResourceReadFailed
		}
	case "list_mcp_resources", "list_mcp_resource_templates":
		if mcpResourceListEmpty(trimmed, tool.Name) {
			return ToolFailureMCPResourcesEmpty
		}
	case "tool_search":
		if trimmed == "[]" {
			return ToolFailureToolSearchEmpty
		}
	}
	return ToolFailureNone
}

func diagnosticMarkerLine(output string, marker string) bool {
	for _, line := range strings.Split(output, "\n") {
		text := strings.TrimSpace(line)
		if text == marker || strings.HasPrefix(text, marker+"_") {
			return true
		}
	}
	return false
}

func toolSchemaValidationFailure(output string) bool {
	text := strings.ToLower(toolFailureText(output))
	return strings.Contains(text, "validation error for ") ||
		strings.Contains(text, "validation errors for ")
}

func toolExecutionFailure(output string) bool {
	text := strings.ToLower(toolFailureText(output))
	return strings.Contains(text, "error executing tool") ||
		strings.Contains(text, "tool execution failed")
}

func structuredToolFailure(output string) bool {
	for _, candidate := range structuredToolFailureCandidates(output) {
		var value any
		if err := json.Unmarshal([]byte(candidate), &value); err == nil && structuredFailureValue(value) {
			return true
		}
	}
	return false
}

func structuredToolFailureCandidates(output string) []string {
	trimmed := strings.TrimSpace(output)
	candidates := []string{trimmed}
	if _, body, ok := strings.Cut(trimmed, "Output:"); ok {
		candidates = append(candidates, strings.TrimSpace(body))
	}
	return candidates
}

func structuredFailureValue(value any) bool {
	obj, ok := value.(map[string]any)
	if !ok {
		return false
	}
	if value, ok := boolField(obj, "success"); ok && !value && hasFailureDetail(obj) {
		return true
	}
	if value, ok := boolField(obj, "ok"); ok && !value && hasFailureDetail(obj) {
		return true
	}
	if value, ok := boolField(obj, "isError"); ok && value {
		return true
	}
	if value, ok := boolField(obj, "is_error"); ok && value {
		return true
	}
	for _, key := range []string{"result", "error"} {
		nested, ok := obj[key].(string)
		if !ok {
			continue
		}
		var nestedValue any
		if err := json.Unmarshal([]byte(strings.TrimSpace(nested)), &nestedValue); err == nil && structuredFailureValue(nestedValue) {
			return true
		}
	}
	return false
}

func boolField(obj map[string]any, key string) (bool, bool) {
	value, ok := obj[key].(bool)
	return value, ok
}

func hasFailureDetail(obj map[string]any) bool {
	for _, key := range []string{"error", "message", "reason", "stderr", "code"} {
		if value, ok := obj[key]; ok && value != nil {
			return true
		}
	}
	return false
}

func toolFailureText(output string) string {
	parts := []string{output}
	if _, body, ok := strings.Cut(output, "Output:"); ok {
		parts = append(parts, body)
	}
	return strings.Join(parts, "\n")
}

func FormatToolOutputWithArguments(adapter Adapter, tool ToolDescriptor, arguments string, output string) string {
	if recovery := ToolRecoveryText(ClassifyToolFailureWithArguments(tool, arguments, output)); recovery != "" {
		return output + "\n\n" + recovery
	}
	return adapter.FormatToolOutput(tool, output)
}

func ToolRecoveryText(kind ToolFailureKind) string {
	switch kind {
	case ToolFailureMCPResourceLocalID:
		return "MCP_RESOURCE_LOCAL_IDENTIFIER\nrequired_next_action: treat_skill_paths_and_local_file_paths_as_local_files_not_mcp_resources\nforbidden_next_action: use_skill_names_local_paths_or_file_uris_as_mcp_resource_server_or_uri\nrecovery: read local skill files and repository files with available filesystem/read-only shell tools when allowed. Use read_mcp_resource only with exact server and URI values returned by list_mcp_resources or list_mcp_resource_templates."
	case ToolFailureMCPResourceServerUnknown:
		return "MCP_RESOURCE_SERVER_UNKNOWN\nrequired_next_action: use_only_server_and_uri_values_returned_by_list_mcp_resources_or_list_mcp_resource_templates\nforbidden_next_action: retry_same_server_uri_or_invent_mcp_resource_identifiers\nrecovery: if no MCP resource list entry is available for the needed content, stop MCP resource reading and use another available tool or explain that no readable MCP resource is available."
	case ToolFailureMCPResourceUnlistedID:
		return "MCP_RESOURCE_UNLISTED_IDENTIFIER\nrequired_next_action: list_mcp_resources_or_list_mcp_resource_templates_before_any_resource_read_retry\nforbidden_next_action: retry_or_invent_resource_uri_from_prompt_text_skill_names_tool_names_or_url_like_strings\nrecovery: read_mcp_resource only accepts exact server and URI values returned by MCP resource listing. If the needed content came from prompt text, a local skill, a repository file, or a callable MCP tool, use the matching non-resource path instead."
	case ToolFailureMCPResourceReadFailed:
		return "MCP_RESOURCE_READ_FAILED\nrequired_next_action: inspect_listed_mcp_resources_before_any_retry\nforbidden_next_action: retry_same_server_uri_without_new_list_result\nrecovery: retry only when list_mcp_resources or list_mcp_resource_templates returned the exact server and URI in this turn."
	case ToolFailureMCPResourcesEmpty:
		return "MCP_RESOURCES_EMPTY\nrequired_next_action: stop_mcp_resource_reading_or_use_non_resource_tools\nforbidden_next_action: guess_server_names_or_resource_uris\nrecovery: there are no listed MCP resources in this result. MCP tools may still exist; discover callable MCP tools with tool_search, not read_mcp_resource."
	case ToolFailureToolSearchEmpty:
		return "TOOL_SEARCH_EMPTY\nrequired_next_action: revise_tool_search_query_or_use_existing_visible_tools\nforbidden_next_action: use_mcp_resources_as_tool_discovery_or_invent_tool_names\nrecovery: tool_search found no matching callable tools for this query. Do not switch to list_mcp_resources/read_mcp_resource to discover tools; use already visible tools, narrow/broaden the tool_search query, or proceed with shell/read-only inspection when appropriate."
	case ToolFailureRuntimeNoProgress:
		return "TOOL_RUNTIME_NO_PROGRESS_CONFIRMED\nrequired_next_action: stop_retrying_the_same_tool_call_and_choose_a_materially_different_action\nforbidden_next_action: execute_or_echo_the_runtime_diagnostic_text\nrecovery: the bridge has already determined that this tool action made no progress. Do not call a shell command to print this message; use another available tool or continue from existing context."
	case ToolFailureSchemaValidation:
		return "TOOL_SCHEMA_VALIDATION_ERROR\nrequired_next_action: regenerate_arguments_from_the_visible_tool_schema_or_use_a_different_tool\nforbidden_next_action: retry_same_arguments_or_wrap_unwrap_fields_blindly\nrecovery: the tool rejected the argument shape. Re-read the visible tool schema, then call the same tool only with a materially corrected argument object."
	case ToolFailureExecutionError:
		return "TOOL_EXECUTION_ERROR\nrequired_next_action: inspect_the_error_and_choose_a_materially_different_action\nforbidden_next_action: repeat_same_tool_arguments_without_new_information\nrecovery: the tool runtime failed after invocation. Retry only when the next call changes the failed precondition, target, or argument shape."
	case ToolFailureStructuredFailure:
		return "TOOL_STRUCTURED_FAILURE\nrequired_next_action: treat_success_false_ok_false_or_isError_true_as_a_failed_tool_result\nforbidden_next_action: assume_the_tool_succeeded_from_json_presence_alone\nrecovery: the returned structured payload says the operation failed. Continue only after changing tool, target, or arguments."
	default:
		return ""
	}
}

func mcpResourceUsesLocalIdentifier(arguments string) bool {
	var obj map[string]any
	if err := json.Unmarshal([]byte(arguments), &obj); err != nil {
		return false
	}
	uri, _ := obj["uri"].(string)
	uri = strings.TrimSpace(uri)
	return strings.HasPrefix(uri, "file://") || filepath.IsAbs(uri)
}

func mcpResourceIdentifierNotListed(output string) bool {
	return strings.Contains(output, "unknown resource") ||
		strings.Contains(output, "resource not found") ||
		strings.Contains(output, "no such resource")
}

func mcpResourceListEmpty(output string, toolName string) bool {
	if strings.TrimSpace(output) == "[]" {
		return true
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(output), &obj); err != nil {
		return false
	}
	key := "resources"
	if toolName == "list_mcp_resource_templates" {
		key = "resourceTemplates"
		if _, ok := obj[key]; !ok {
			key = "resource_templates"
		}
	}
	values, ok := obj[key].([]any)
	return ok && len(values) == 0
}

func PatchSucceeded(output string) bool {
	text := strings.ToLower(output)
	return strings.Contains(text, "success. updated the following files") ||
		strings.Contains(text, "successfully applied patch")
}

func PatchSucceededFiles(output string) []string {
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

	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	collecting := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if collecting {
				break
			}
			continue
		}
		if strings.HasPrefix(trimmed, "changed_files:") {
			for _, file := range strings.Split(strings.TrimPrefix(trimmed, "changed_files:"), ",") {
				add(file)
			}
			continue
		}
		if strings.Contains(strings.ToLower(trimmed), "success. updated the following files") {
			collecting = true
			continue
		}
		if !collecting {
			continue
		}
		parts := strings.Fields(trimmed)
		if len(parts) < 2 || !isPatchFileStatus(parts[0]) {
			break
		}
		add(strings.TrimPrefix(trimmed, parts[0]))
	}
	return files
}

func PatchTouchedFiles(input string) []string {
	lines := strings.Split(NormalizePatchInput(input), "\n")
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
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			add(strings.TrimPrefix(line, "*** Add File: "))
		case strings.HasPrefix(line, "*** Update File: "):
			add(strings.TrimPrefix(line, "*** Update File: "))
		case strings.HasPrefix(line, "*** Delete File: "):
			add(strings.TrimPrefix(line, "*** Delete File: "))
		case strings.HasPrefix(line, "*** Move to: "):
			add(strings.TrimPrefix(line, "*** Move to: "))
		}
	}
	return files
}

func PatchIsNoopUpdate(input string) bool {
	lines := strings.Split(NormalizePatchInput(input), "\n")
	hasUpdate := false
	hasHunk := false
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "*** Add File: "),
			strings.HasPrefix(line, "*** Delete File: "),
			strings.HasPrefix(line, "*** Move to: "):
			return false
		case strings.HasPrefix(line, "*** Update File: "):
			hasUpdate = true
		case line == "@@":
			hasHunk = true
		case strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-"):
			return false
		}
	}
	return hasUpdate && hasHunk
}

func PatchIsAlreadyApplied(input string) bool {
	return strings.Contains(strings.ToLower(NormalizePatchInput(input)), "text_editor_already_applied") ||
		PatchIsNoopUpdate(input)
}

func PatchRecoveryText(kind PatchFailureKind) string {
	switch kind {
	case PatchFailureContextMismatch:
		return "APPLY_PATCH_CONTEXT_MISMATCH\nrequired_next_action: inspect_current_file\nforbidden_next_action: retry_same_patch\nrecovery: read the current target file lines, then generate a smaller patch using exact current context."
	case PatchFailureMalformedPatch:
		return "APPLY_PATCH_MALFORMED\nrequired_next_action: regenerate_complete_freeform_patch\nforbidden_next_action: send_json_or_markdown_as_patch\nrecovery: send a complete patch starting with *** Begin Patch and ending with *** End Patch."
	case PatchFailureInvalidHunk:
		return "APPLY_PATCH_INVALID_HUNK\nrequired_next_action: fix_patch_syntax\nforbidden_next_action: change_target_code_to_fit_bad_patch\nrecovery: preserve exact hunk line prefixes: space for context, + for additions, - for removals."
	case PatchFailureReadFileOperation:
		return "APPLY_PATCH_WRONG_TOOL_FOR_READ\nrequired_next_action: inspect_file_with_read_only_shell\nforbidden_next_action: use_apply_patch_to_read_files\nrecovery: apply_patch only supports Add File, Update File, Delete File, and Move. Use read-only shell commands such as sed, grep, rg, head, tail, or cat to inspect files, then call apply_patch only for the actual edit."
	case PatchFailureAlreadyApplied:
		return "APPLY_PATCH_ALREADY_APPLIED\nrequired_next_action: read_only_verify_current_file_or_summarize\nforbidden_next_action: repeat_same_patch\nrecovery: the requested content is already present. Do not send the same patch again; inspect current file content, then patch a different missing change or summarize."
	case PatchFailureNoProgress:
		return "APPLY_PATCH_NO_PROGRESS\nrequired_next_action: inspect_current_file_or_choose_different_target\nforbidden_next_action: repeat_same_noop_file_edit\nrecovery: the file operation did not modify anything. Read the current state, then make a materially different edit or summarize the blocker."
	case PatchFailurePathError:
		return "APPLY_PATCH_PATH_ERROR\nrequired_next_action: verify_target_path\nforbidden_next_action: retry_same_path_blindly\nrecovery: inspect the directory or target file path, then generate a patch for the correct path."
	case PatchFailurePermissionOrSandbox:
		return "APPLY_PATCH_BLOCKED_BY_ENVIRONMENT\nrequired_next_action: report_blocker\nforbidden_next_action: retry_patch\nrecovery: explain the permission or sandbox blocker instead of retrying the patch."
	case PatchFailureUnknown:
		return "APPLY_PATCH_FAILED\nrequired_next_action: inspect_error_and_current_state\nforbidden_next_action: retry_same_patch_blindly\nrecovery: keep the original error, inspect current file state if needed, then choose the smallest safe next action."
	default:
		return ""
	}
}

func TextEditorRecoveryText(kind PatchFailureKind) string {
	switch kind {
	case PatchFailureContextMismatch:
		return "TEXT_EDITOR_CONTEXT_MISMATCH\nrequired_next_action: inspect_current_file\nforbidden_next_action: retry_same_edit\nrecovery: read the current target file lines. If the requested content is already present, stop editing and summarize; otherwise send a smaller file editor call using exact current old_str or insert_line."
	case PatchFailureMalformedPatch, PatchFailureInvalidHunk, PatchFailureReadFileOperation:
		return "TEXT_EDITOR_INVALID_EDIT\nrequired_next_action: regenerate_text_editor_arguments\nforbidden_next_action: send_diff_or_patch_syntax\nrecovery: call the matching file editor tool with exact JSON arguments."
	case PatchFailureAlreadyApplied:
		return "TEXT_EDITOR_ALREADY_APPLIED\nfile_edit_state: already_applied\nrequired_next_action: read_only_verify_current_file_or_summarize\nforbidden_next_action: repeat_same_text_editor_edit\nrecovery: the requested content is already present. Do not send the same text editor edit again; inspect current file content, then edit a different missing change or summarize."
	case PatchFailureNoProgress:
		return "TEXT_EDITOR_NO_PROGRESS\nfile_edit_state: not_modified\nrequired_next_action: inspect_current_file_or_choose_different_target\nforbidden_next_action: repeat_same_noop_text_editor_edit\nrecovery: the text editor operation did not modify anything. Read the current state, then make a materially different file editor call or summarize the blocker."
	case PatchFailurePathError:
		return "TEXT_EDITOR_PATH_ERROR\nrequired_next_action: verify_target_path\nforbidden_next_action: retry_same_path_blindly\nrecovery: inspect the directory or target file path, then send a text editor edit for the correct path."
	case PatchFailurePermissionOrSandbox:
		return "TEXT_EDITOR_BLOCKED_BY_ENVIRONMENT\nrequired_next_action: report_blocker\nforbidden_next_action: retry_edit\nrecovery: explain the permission or sandbox blocker instead of retrying the edit."
	case PatchFailureUnknown:
		return "TEXT_EDITOR_EDIT_FAILED\nrequired_next_action: inspect_error_and_current_state\nforbidden_next_action: retry_same_edit_blindly\nrecovery: keep the original error, inspect current file state if needed, then choose the smallest safe next action."
	default:
		return ""
	}
}

func isPatchFileStatus(status string) bool {
	switch status {
	case "A", "D", "M", "R", "C":
		return true
	default:
		return false
	}
}

func normalizePatchFilePath(file string) string {
	file = strings.TrimSpace(file)
	if file == "" {
		return ""
	}
	file = filepath.ToSlash(filepath.Clean(file))
	file = strings.TrimPrefix(file, "./")
	if file == "." {
		return ""
	}
	return file
}

func extractPatchFromJSONEnvelope(text string) (string, bool) {
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return "", false
	}
	return patchStringFromValue(value)
}

func patchStringFromValue(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		if looksLikePatchEnvelope(v) || strings.HasPrefix(strings.TrimSpace(v), "```") {
			return v, true
		}
	case map[string]any:
		for _, key := range []string{"input", "patch", "content"} {
			if text, ok := patchStringFromValue(v[key]); ok {
				return text, true
			}
		}
		if nested, ok := v["arguments"]; ok {
			if text, ok := patchStringFromValue(nested); ok {
				return text, true
			}
		}
	}
	return "", false
}

func stripMarkdownFence(text string) string {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "```") {
		return text
	}
	lines := strings.Split(text, "\n")
	if len(lines) < 2 || !strings.HasPrefix(strings.TrimSpace(lines[0]), "```") {
		return text
	}
	if strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
		lines = lines[1 : len(lines)-1]
	} else {
		lines = lines[1:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func looksLikePatchEnvelope(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "*** Begin Patch") || strings.Contains(trimmed, "*** Begin Patch\n")
}

func completePatchEnvelope(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return trimmed
	}
	hasBegin := strings.HasPrefix(trimmed, "*** Begin Patch")
	hasEnd := strings.HasSuffix(trimmed, "*** End Patch")
	if hasBegin && hasEnd {
		return trimmed
	}
	if hasBegin {
		return trimmed + "\n*** End Patch"
	}
	if startsWithPatchOperation(trimmed) {
		if hasEnd {
			return "*** Begin Patch\n" + trimmed
		}
		return "*** Begin Patch\n" + trimmed + "\n*** End Patch"
	}
	return trimmed
}

func startsWithPatchOperation(text string) bool {
	return strings.HasPrefix(text, "*** Add File: ") ||
		strings.HasPrefix(text, "*** Update File: ") ||
		strings.HasPrefix(text, "*** Delete File: ")
}

func chatPatchToolDescription(tool ToolDescriptor) string {
	parts := []string{
		"This is Codex's file-editing patch tool encoded through Chat Completions. Treat it as a freeform patch transported inside JSON function arguments.",
		"The decoded input string must start with *** Begin Patch and end with *** End Patch.",
		"Use this tool when you choose to submit source, document, or config file changes as a patch. Shell remains a separate execution tool.",
		"apply_patch cannot read files. Do not invent *** Read File; inspect files with read-only shell commands such as sed, grep, rg, head, tail, or cat.",
		"Before editing an existing file, inspect the current target lines unless the current turn already includes the exact current text.",
		"Prefer small, surgical hunks. For large files or multi-area edits, make separate minimal hunks instead of rewriting broad surrounding blocks.",
		"For single-line replacements, use a minimal hunk with one - old line immediately followed by one + new line.",
		"Only mark lines with - or + when their content must actually change. Do not remove and re-add unchanged surrounding lines.",
		"Do not duplicate the old line as both unchanged context and a removed line.",
		"Do not use an insertion-only hunk when replacing existing text.",
		"For Add File operations, do not include @@ hunk headers; every content line must start with +.",
		"For appending to an existing file, use Update File, not Add File.",
		"Every unchanged context line is byte-significant and must be copied exactly from the current file.",
		"Whitespace inside hunks is significant: preserve tabs, spaces, blank lines, and line prefixes exactly.",
		"If a patch fails because context does not match, read the current file and generate a smaller patch from exact current lines; never retry the same patch.",
		"After a patch succeeds for a file, do not repeat an already-completed edit. Use read-only commands to verify. If another requested change is still missing in the same file, make the smallest follow-up patch from exact current context; otherwise summarize.",
		"Do not wrap the patch in Markdown fences, JSON text, or explanatory prose.",
		"Example: *** Begin Patch\n*** Update File: hello.txt\n@@\n-old\n+new\n*** End Patch\n",
	}
	if len(tool.Raw) > 0 {
		if meta := canonicalJSON(tool.Raw); meta != "" {
			parts = append(parts, "Original tool metadata: "+meta)
		}
	}
	return strings.Join(parts, "\n")
}
