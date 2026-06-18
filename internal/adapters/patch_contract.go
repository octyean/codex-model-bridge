package adapters

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

const chatPatchSystemInstruction = `CHAT_COMPLETIONS_APPLY_PATCH_CONTRACT
apply_patch is a Codex freeform patch transported through Chat Completions function arguments.
The function arguments must decode to a complete patch string in input.
Use apply_patch for source, document, and config file creation, edits, deletes, and moves. Do not use shell commands as a file editor.
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
After apply_patch succeeds for a file, do not repeat an already-completed edit. Use read-only commands to verify. If another requested change is still missing in the same file, make the smallest follow-up patch from exact current context; otherwise summarize.`

type PatchFailureKind string

const (
	PatchFailureNone                PatchFailureKind = ""
	PatchFailureContextMismatch     PatchFailureKind = "context_mismatch"
	PatchFailureMalformedPatch      PatchFailureKind = "malformed_patch"
	PatchFailureInvalidHunk         PatchFailureKind = "invalid_hunk"
	PatchFailureReadFileOperation   PatchFailureKind = "read_file_operation"
	PatchFailureAlreadyApplied      PatchFailureKind = "already_applied"
	PatchFailurePathError           PatchFailureKind = "path_error"
	PatchFailurePermissionOrSandbox PatchFailureKind = "permission_or_sandbox"
	PatchFailureUnknown             PatchFailureKind = "unknown"
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
	case strings.Contains(text, "*** read file:"):
		return PatchFailureReadFileOperation
	case strings.Contains(text, "text_editor_already_applied"):
		return PatchFailureAlreadyApplied
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

func PatchFilesOverlap(left []string, right []string) bool {
	seen := map[string]bool{}
	for _, file := range left {
		if normalized := normalizePatchFilePath(file); normalized != "" {
			seen[normalized] = true
		}
	}
	for _, file := range right {
		if seen[normalizePatchFilePath(file)] {
			return true
		}
	}
	return false
}

func PatchRecoveryText(kind PatchFailureKind) string {
	switch kind {
	case PatchFailureContextMismatch:
		return "APPLY_PATCH_CONTEXT_MISMATCH\nrequired_next_action: inspect_current_file\nforbidden_next_action: retry_same_patch\nrecovery: read the current target file lines, then generate a smaller patch using exact current context.\npatch_discipline: do not broaden the hunk, do not rewrite whole blocks, and do not use shell as a file editor."
	case PatchFailureMalformedPatch:
		return "APPLY_PATCH_MALFORMED\nrequired_next_action: regenerate_complete_freeform_patch\nforbidden_next_action: send_json_or_markdown_as_patch\nrecovery: send a complete patch starting with *** Begin Patch and ending with *** End Patch."
	case PatchFailureInvalidHunk:
		return "APPLY_PATCH_INVALID_HUNK\nrequired_next_action: fix_patch_syntax\nforbidden_next_action: change_target_code_to_fit_bad_patch\nrecovery: preserve exact hunk line prefixes: space for context, + for additions, - for removals.\npatch_discipline: keep the intended edit small; do not switch to shell file writes."
	case PatchFailureReadFileOperation:
		return "APPLY_PATCH_WRONG_TOOL_FOR_READ\nrequired_next_action: inspect_file_with_read_only_shell\nforbidden_next_action: use_apply_patch_to_read_files\nrecovery: apply_patch only supports Add File, Update File, Delete File, and Move. Use read-only shell commands such as sed, grep, rg, head, tail, or cat to inspect files, then call apply_patch only for the actual edit."
	case PatchFailureAlreadyApplied:
		return "APPLY_PATCH_ALREADY_APPLIED\nrequired_next_action: read_only_verify_current_file_or_summarize\nforbidden_next_action: repeat_same_patch\nrecovery: the requested content is already present. Do not send the same patch again; inspect current file content, then patch a different missing change or summarize."
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
		return "TEXT_EDITOR_CONTEXT_MISMATCH\nrequired_next_action: inspect_current_file\nforbidden_next_action: retry_same_edit\nrecovery: read the current target file lines. If the requested content is already present, stop editing and summarize; otherwise send a smaller text editor edit using exact current old_str or insert_after text.\nedit_discipline: do not broaden the edit, do not rewrite whole blocks, and do not use shell as a file editor."
	case PatchFailureMalformedPatch, PatchFailureInvalidHunk, PatchFailureReadFileOperation:
		return "TEXT_EDITOR_INVALID_EDIT\nrequired_next_action: regenerate_text_editor_arguments\nforbidden_next_action: send_diff_or_patch_syntax\nrecovery: use command=create, str_replace, insert_after, or delete_file with exact JSON arguments."
	case PatchFailureAlreadyApplied:
		return "TEXT_EDITOR_ALREADY_APPLIED\nfile_edit_state: already_applied\nrequired_next_action: read_only_verify_current_file_or_summarize\nforbidden_next_action: repeat_same_text_editor_edit\nrecovery: the requested content is already present. Do not send the same text editor edit again; inspect current file content, then edit a different missing change or summarize."
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
		"Use this tool for source, document, and config file creation, edits, deletes, and moves. Shell is for reading, searching, building, testing, formatting, and real generators, not manual file edits.",
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
