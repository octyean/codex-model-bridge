package adapters

import (
	"encoding/json"
	"strings"
)

const chatPatchSystemInstruction = `CHAT_COMPLETIONS_APPLY_PATCH_CONTRACT
apply_patch is a Codex freeform patch transported through Chat Completions function arguments.
The function arguments must decode to a complete patch string in input.
For replacements, write the removed line with - immediately followed by the added line with +.
Never write the old line as unchanged context and then also remove the same old line.
Never use an insertion-only hunk when the requested task is to replace existing text.
For Add File operations, do not use @@ hunks; every content line must start with +.
For appending to an existing file, use Update File, not Add File.
Unchanged context lines are byte-significant: copy indentation, tabs, spaces, and text exactly from the current file.
For whitespace-sensitive files, use the smallest valid hunk and avoid nearby context unless needed for uniqueness.`

type PatchFailureKind string

const (
	PatchFailureNone                PatchFailureKind = ""
	PatchFailureContextMismatch     PatchFailureKind = "context_mismatch"
	PatchFailureMalformedPatch      PatchFailureKind = "malformed_patch"
	PatchFailureInvalidHunk         PatchFailureKind = "invalid_hunk"
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

func PatchRecoveryText(kind PatchFailureKind) string {
	switch kind {
	case PatchFailureContextMismatch:
		return "APPLY_PATCH_CONTEXT_MISMATCH\nrequired_next_action: inspect_current_file\nforbidden_next_action: retry_same_patch\nrecovery: read the current target file lines, then generate a smaller patch using exact current context."
	case PatchFailureMalformedPatch:
		return "APPLY_PATCH_MALFORMED\nrequired_next_action: regenerate_complete_freeform_patch\nforbidden_next_action: send_json_or_markdown_as_patch\nrecovery: send a complete patch starting with *** Begin Patch and ending with *** End Patch."
	case PatchFailureInvalidHunk:
		return "APPLY_PATCH_INVALID_HUNK\nrequired_next_action: fix_patch_syntax\nforbidden_next_action: change_target_code_to_fit_bad_patch\nrecovery: preserve exact hunk line prefixes: space for context, + for additions, - for removals."
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
		"For single-line replacements, use a minimal hunk with one - old line immediately followed by one + new line.",
		"Do not duplicate the old line as both unchanged context and a removed line.",
		"Do not use an insertion-only hunk when replacing existing text.",
		"For Add File operations, do not include @@ hunk headers; every content line must start with +.",
		"For appending to an existing file, use Update File, not Add File.",
		"Every unchanged context line is byte-significant and must be copied exactly from the current file.",
		"Whitespace inside hunks is significant: preserve tabs, spaces, blank lines, and line prefixes exactly.",
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

func RepairDeepSeekPatchInput(text string) string {
	text = normalizePatchBoundaryLines(text)
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	inAddFile := false
	for i := 0; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "*** Add File: ") {
			inAddFile = true
			out = append(out, lines[i])
			continue
		}
		if strings.HasPrefix(lines[i], "*** Update File: ") || strings.HasPrefix(lines[i], "*** Delete File: ") || strings.HasPrefix(lines[i], "*** End Patch") {
			inAddFile = false
		}
		if inAddFile && strings.TrimSpace(lines[i]) == "@@" {
			continue
		}
		if i+2 < len(lines) && isPatchContextLine(lines[i]) && strings.HasPrefix(lines[i+1], "+") && strings.HasPrefix(lines[i+2], "-") {
			context := strings.TrimPrefix(lines[i], " ")
			removed := strings.TrimPrefix(lines[i+2], "-")
			if context == removed || strings.TrimSpace(context) == strings.TrimSpace(removed) {
				out = append(out, lines[i+2], lines[i+1])
				i += 2
				continue
			}
		}
		if i+1 < len(lines) && isPatchContextLine(lines[i]) && strings.HasPrefix(lines[i+1], "+") {
			context := strings.TrimPrefix(lines[i], " ")
			added := strings.TrimPrefix(lines[i+1], "+")
			if looksLikeReplacementLine(context, added) && len(lineIndent(added)) >= len(lineIndent(context)) {
				out = append(out, "-"+context, lines[i+1])
				i++
				continue
			}
		}
		out = append(out, lines[i])
	}
	return minimizeSingleReplacementHunks(repairDeepSeekMarkdownListContext(repairDeepSeekUpdateHunkPrefixes(mergeRepeatedPatchEnvelopes(strings.Join(out, "\n")))))
}

func normalizePatchBoundaryLines(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if normalized, ok := normalizedPatchBoundaryLine(line); ok {
			lines[i] = normalized
		}
	}
	return strings.Join(lines, "\n")
}

func normalizedPatchBoundaryLine(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	for {
		next := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(trimmed), "+-"))
		if next == trimmed {
			break
		}
		trimmed = next
	}
	switch trimmed {
	case "*** Begin Patch", "*** End Patch":
		return trimmed, true
	default:
		return "", false
	}
}

func mergeRepeatedPatchEnvelopes(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	seenBegin := false
	for _, line := range lines {
		switch line {
		case "*** Begin Patch":
			if seenBegin {
				continue
			}
			seenBegin = true
			out = append(out, line)
		case "*** End Patch":
			continue
		default:
			out = append(out, line)
		}
	}
	if seenBegin {
		out = append(out, "*** End Patch")
	}
	return strings.Join(out, "\n")
}

func repairDeepSeekMarkdownListContext(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	inUpdateFile := false
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "*** Update File: "):
			inUpdateFile = true
			out = append(out, line)
		case strings.HasPrefix(line, "*** "):
			inUpdateFile = false
			out = append(out, line)
		case inUpdateFile && line == "@@":
			hunkEnd := nextDeepSeekHunkBoundary(lines, i+1)
			out = append(out, line)
			out = append(out, repairMarkdownListContextHunk(lines[i+1:hunkEnd])...)
			i = hunkEnd - 1
		default:
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func repairMarkdownListContextHunk(lines []string) []string {
	if !hunkHasMutation(lines) {
		return lines
	}
	out := append([]string(nil), lines...)
	for i, line := range out {
		if looksLikeMarkdownListDeletion(line) {
			out[i] = " " + line
		}
	}
	return out
}

func looksLikeMarkdownListDeletion(line string) bool {
	if strings.HasPrefix(line, "-- ") || strings.HasPrefix(line, "-* ") || strings.HasPrefix(line, "-+ ") {
		return false
	}
	if !strings.HasPrefix(line, "- ") || len(line) < 3 || line[2] == ' ' || line[2] == '\t' {
		return false
	}
	item := strings.TrimSpace(strings.TrimPrefix(line, "- "))
	return item != "" && !strings.HasPrefix(item, "-")
}

func repairDeepSeekUpdateHunkPrefixes(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	inUpdateFile := false
	inHunk := false
	droppingAfterEndOfFile := false
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "*** Update File: "):
			inUpdateFile = true
			inHunk = false
			droppingAfterEndOfFile = false
			out = append(out, line)
			continue
		case inUpdateFile && inHunk && line == "*** End of File":
			droppingAfterEndOfFile = true
			out = append(out, line)
			continue
		case strings.HasPrefix(line, "*** "):
			inUpdateFile = false
			inHunk = false
			droppingAfterEndOfFile = false
			out = append(out, line)
			continue
		case inUpdateFile && strings.HasPrefix(line, "@@"):
			inHunk = true
			droppingAfterEndOfFile = false
			out = append(out, line)
			continue
		case droppingAfterEndOfFile:
			continue
		case inUpdateFile && inHunk && needsDeepSeekContextPrefix(line):
			out = append(out, " "+line)
			continue
		}
		out = append(out, line)
	}
	return removeNoOpDeepSeekUpdateHunks(strings.Join(out, "\n"))
}

func removeNoOpDeepSeekUpdateHunks(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	inUpdateFile := false
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "*** Update File: "):
			inUpdateFile = true
			out = append(out, line)
		case strings.HasPrefix(line, "*** "):
			inUpdateFile = false
			out = append(out, line)
		case inUpdateFile && line == "@@":
			hunkEnd := nextDeepSeekHunkBoundary(lines, i+1)
			if !hunkHasMutation(lines[i+1 : hunkEnd]) {
				i = hunkEnd - 1
				continue
			}
			out = append(out, line)
		default:
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func nextDeepSeekHunkBoundary(lines []string, start int) int {
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "*** ") || strings.HasPrefix(lines[i], "@@") {
			return i
		}
	}
	return len(lines)
}

func hunkHasMutation(lines []string) bool {
	for _, line := range lines {
		if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") {
			return true
		}
	}
	return false
}

func needsDeepSeekContextPrefix(line string) bool {
	if line == "" {
		return true
	}
	return !strings.HasPrefix(line, " ") &&
		!strings.HasPrefix(line, "+") &&
		!strings.HasPrefix(line, "-") &&
		!strings.HasPrefix(line, `\`)
}

func isPatchContextLine(line string) bool {
	return strings.HasPrefix(line, " ") && !strings.HasPrefix(line, " ***")
}

func looksLikeReplacementLine(oldLine string, newLine string) bool {
	oldTrimmed := strings.TrimSpace(oldLine)
	newTrimmed := strings.TrimSpace(newLine)
	if oldTrimmed == "" || newTrimmed == "" || oldTrimmed == newTrimmed {
		return false
	}
	commonPrefix := 0
	for commonPrefix < len(oldTrimmed) && commonPrefix < len(newTrimmed) && oldTrimmed[commonPrefix] == newTrimmed[commonPrefix] {
		commonPrefix++
	}
	commonSuffix := 0
	for commonSuffix < len(oldTrimmed)-commonPrefix && commonSuffix < len(newTrimmed)-commonPrefix &&
		oldTrimmed[len(oldTrimmed)-1-commonSuffix] == newTrimmed[len(newTrimmed)-1-commonSuffix] {
		commonSuffix++
	}
	shorter := len(oldTrimmed)
	if len(newTrimmed) < shorter {
		shorter = len(newTrimmed)
	}
	return shorter >= 12 && (commonPrefix+commonSuffix)*100/shorter >= 70
}

func lineIndent(line string) string {
	var b strings.Builder
	for _, r := range line {
		if r != ' ' && r != '\t' {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

func minimizeSingleReplacementHunks(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if !strings.HasPrefix(line, "@@") {
			out = append(out, line)
			continue
		}
		hunk := []string{line}
		j := i + 1
		for ; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], "@@") || strings.HasPrefix(lines[j], "*** ") {
				break
			}
			hunk = append(hunk, lines[j])
		}
		if minimized, ok := singleReplacementHunk(hunk); ok {
			out = append(out, minimized...)
		} else {
			out = append(out, hunk...)
		}
		i = j - 1
	}
	return strings.Join(out, "\n")
}

func singleReplacementHunk(hunk []string) ([]string, bool) {
	if len(hunk) < 3 {
		return nil, false
	}
	var removed string
	var added string
	removedCount := 0
	addedCount := 0
	for _, line := range hunk[1:] {
		switch {
		case strings.HasPrefix(line, "-"):
			removed = line
			removedCount++
		case strings.HasPrefix(line, "+"):
			added = line
			addedCount++
		}
	}
	if removedCount != 1 || addedCount != 1 {
		return nil, false
	}
	if !looksLikeReplacementLine(strings.TrimPrefix(removed, "-"), strings.TrimPrefix(added, "+")) {
		return nil, false
	}
	return []string{hunk[0], removed, added}, true
}
