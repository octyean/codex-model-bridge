package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"codex-bridge/internal/adapters"
)

const maxTextEditorReadBytes = 4 * 1024 * 1024

type textEditorCommand struct {
	Command     string `json:"command"`
	Path        string `json:"path"`
	DestPath    string `json:"destination_path"`
	NewPath     string `json:"new_path"`
	OldStr      string `json:"old_str"`
	NewStr      string `json:"new_str"`
	InsertAfter string `json:"insert_after"`
	Text        string `json:"text"`
	FileText    string `json:"file_text"`
	Content     string `json:"content"`
}

func TextEditorPatchInput(arguments string) (string, error) {
	command, err := parseTextEditorCommand(arguments)
	if err != nil {
		return "", err
	}
	path := normalizeEditorPath(command.Path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	switch normalizeEditorCommand(command.Command) {
	case "create":
		content := firstNonEmpty(command.FileText, command.Content, command.Text, command.NewStr)
		if content == "" {
			return "", fmt.Errorf("create requires file_text or text")
		}
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return existingFileCreateResult(path), nil
		}
		return addFilePatch(path, content), nil
	case "str_replace":
		if command.OldStr == "" {
			return "", fmt.Errorf("str_replace requires old_str")
		}
		newText := alignReplacementIndent(command.OldStr, command.NewStr)
		if result, ok := alreadyAppliedReplaceResult(path, command.OldStr, newText); ok {
			return result, nil
		}
		return replacePatch(path, command.OldStr, newText), nil
	case "insert_after":
		anchor := firstNonEmpty(command.InsertAfter, command.OldStr)
		text := firstNonEmpty(command.Text, command.NewStr, command.Content)
		if anchor == "" {
			return "", fmt.Errorf("insert_after requires insert_after or old_str")
		}
		if text == "" {
			return "", fmt.Errorf("insert_after requires text or new_str")
		}
		return insertAfterPatch(path, anchor, text), nil
	case "move_file":
		destPath := normalizeEditorPath(firstNonEmpty(command.DestPath, command.NewPath, command.NewStr))
		if destPath == "" {
			return "", fmt.Errorf("move_file requires destination_path or new_path")
		}
		if destPath == path {
			return samePathMoveResult(path), nil
		}
		if command.OldStr != "" {
			return moveFilePatch(path, destPath, command.OldStr, alignReplacementIndent(command.OldStr, command.NewStr)), nil
		}
		return moveFilePatch(path, destPath, "", ""), nil
	case "delete_file":
		return "*** Begin Patch\n*** Delete File: " + path + "\n*** End Patch", nil
	default:
		return "", fmt.Errorf("unsupported command %q", command.Command)
	}
}

func TextEditorArgumentsFromPatch(input string) (string, bool) {
	lines := strings.Split(adapters.NormalizePatchInput(input), "\n")
	if len(lines) < 3 || lines[0] != "*** Begin Patch" || lines[len(lines)-1] != "*** End Patch" {
		return "", false
	}
	if path, ok := strings.CutPrefix(lines[1], "*** Add File: "); ok {
		content, ok := unprefixedPatchLines(lines[2:len(lines)-1], "+")
		if !ok {
			return "", false
		}
		return textEditorArguments(map[string]string{
			"command":   "create",
			"path":      normalizeEditorPath(path),
			"file_text": content,
		})
	}
	if path, ok := strings.CutPrefix(lines[1], "*** Delete File: "); ok && len(lines) == 3 {
		return textEditorArguments(map[string]string{
			"command": "delete_file",
			"path":    normalizeEditorPath(path),
		})
	}
	if path, ok := strings.CutPrefix(lines[1], "*** Update File: "); ok {
		return textEditorUpdateArguments(normalizeEditorPath(path), lines[2:len(lines)-1])
	}
	return "", false
}

func textEditorUpdateArguments(path string, lines []string) (string, bool) {
	if path == "" {
		return "", false
	}
	if len(lines) >= 1 {
		if destPath, ok := strings.CutPrefix(lines[0], "*** Move to: "); ok {
			if len(lines) == 1 {
				return textEditorArguments(map[string]string{
					"command":          "move_file",
					"path":             path,
					"destination_path": normalizeEditorPath(destPath),
				})
			}
			if len(lines) >= 3 && lines[1] == "@@" {
				if oldText, newText, ok := textEditorReplaceFromHunk(lines[2:]); ok {
					return textEditorArguments(map[string]string{
						"command":          "move_file",
						"path":             path,
						"destination_path": normalizeEditorPath(destPath),
						"old_str":          oldText,
						"new_str":          newText,
					})
				}
			}
			return "", false
		}
	}
	if len(lines) == 1 {
		return "", false
	}
	if len(lines) < 2 || lines[0] != "@@" {
		return "", false
	}
	body := lines[1:]
	if oldText, newText, ok := textEditorReplaceFromHunk(body); ok {
		return textEditorArguments(map[string]string{
			"command": "str_replace",
			"path":    path,
			"old_str": oldText,
			"new_str": newText,
		})
	}
	if anchor, text, ok := textEditorInsertAfterFromHunk(body); ok {
		return textEditorArguments(map[string]string{
			"command":      "insert_after",
			"path":         path,
			"insert_after": anchor,
			"text":         text,
		})
	}
	return "", false
}

func textEditorReplaceFromHunk(lines []string) (string, string, bool) {
	if len(lines) < 2 {
		return "", "", false
	}
	i := 0
	var oldLines []string
	for i < len(lines) && strings.HasPrefix(lines[i], "-") {
		oldLines = append(oldLines, strings.TrimPrefix(lines[i], "-"))
		i++
	}
	var newLines []string
	for i < len(lines) && strings.HasPrefix(lines[i], "+") {
		newLines = append(newLines, strings.TrimPrefix(lines[i], "+"))
		i++
	}
	if i != len(lines) || len(oldLines) == 0 || len(newLines) == 0 {
		return "", "", false
	}
	return strings.Join(oldLines, "\n"), strings.Join(newLines, "\n"), true
}

func textEditorInsertAfterFromHunk(lines []string) (string, string, bool) {
	if len(lines) < 2 {
		return "", "", false
	}
	i := 0
	var anchorLines []string
	for i < len(lines) && strings.HasPrefix(lines[i], " ") {
		anchorLines = append(anchorLines, strings.TrimPrefix(lines[i], " "))
		i++
	}
	var textLines []string
	for i < len(lines) && strings.HasPrefix(lines[i], "+") {
		textLines = append(textLines, strings.TrimPrefix(lines[i], "+"))
		i++
	}
	if i != len(lines) || len(anchorLines) == 0 || len(textLines) == 0 {
		return "", "", false
	}
	return strings.Join(anchorLines, "\n"), strings.Join(textLines, "\n"), true
}

func unprefixedPatchLines(lines []string, prefix string) (string, bool) {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if !strings.HasPrefix(line, prefix) {
			return "", false
		}
		out = append(out, strings.TrimPrefix(line, prefix))
	}
	return strings.Join(out, "\n"), true
}

func textEditorArguments(values map[string]string) (string, bool) {
	data, err := json.Marshal(values)
	if err != nil {
		return "", false
	}
	return string(data), true
}

func parseTextEditorCommand(arguments string) (textEditorCommand, error) {
	var command textEditorCommand
	if err := json.Unmarshal([]byte(arguments), &command); err == nil && (command.Command != "" || command.Path != "") {
		command.Command = normalizeEditorCommand(command.Command)
		return command, nil
	}
	var wrapped map[string]json.RawMessage
	if err := json.Unmarshal([]byte(arguments), &wrapped); err != nil {
		return command, fmt.Errorf("arguments must be a JSON object")
	}
	for _, key := range []string{"input", "arguments"} {
		raw, ok := wrapped[key]
		if !ok {
			continue
		}
		var nested string
		if err := json.Unmarshal(raw, &nested); err == nil {
			return parseTextEditorCommand(nested)
		}
		if err := json.Unmarshal(raw, &command); err == nil {
			command.Command = normalizeEditorCommand(command.Command)
			return command, nil
		}
	}
	return command, fmt.Errorf("arguments must include command and path")
}

func normalizeEditorCommand(command string) string {
	switch strings.TrimSpace(strings.ToLower(command)) {
	case "create":
		return "create"
	case "str_replace", "replace":
		return "str_replace"
	case "insert", "insert_after":
		return "insert_after"
	case "move", "rename", "move_file", "rename_file":
		return "move_file"
	case "delete", "delete_file":
		return "delete_file"
	default:
		return strings.TrimSpace(strings.ToLower(command))
	}
}

func normalizeEditorPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "." {
		return ""
	}
	return strings.TrimPrefix(path, "./")
}

func addFilePatch(path string, content string) string {
	lines := prefixedLines("+", content)
	return "*** Begin Patch\n*** Add File: " + path + "\n" + strings.Join(lines, "\n") + "\n*** End Patch"
}

func existingFileCreateResult(path string) string {
	return strings.Join([]string{
		"TEXT_EDITOR_CREATE_TARGET_ALREADY_EXISTS",
		"path: " + path,
		"file_edit_state: not_modified",
		"required_next_action: inspect_current_file_then_use_str_replace_or_summarize",
		"forbidden_next_action: retry_create_same_path",
		"recovery: the target file already exists. Do not use create for existing files; inspect the current file, then use str_replace or insert_after only if a real change is still missing.",
	}, "\n")
}

func samePathMoveResult(path string) string {
	return strings.Join([]string{
		"TEXT_EDITOR_MOVE_TARGET_SAME_AS_SOURCE",
		"path: " + path,
		"file_edit_state: not_modified",
		"required_next_action: use_str_replace_for_same_file_content_edits",
		"forbidden_next_action: retry_move_file_same_path",
		"recovery: source and destination are the same file. Do not use move_file for same-path edits; use str_replace or insert_after on the existing path.",
	}, "\n")
}

func replacePatch(path string, oldText string, newText string) string {
	if expandedOld, expandedNew, ok := expandReplacementFromFile(path, oldText, newText); ok {
		oldText = expandedOld
		newText = expandedNew
	}
	lines := []string{"*** Begin Patch", "*** Update File: " + path, "@@"}
	lines = append(lines, prefixedLines("-", oldText)...)
	lines = append(lines, prefixedLines("+", newText)...)
	lines = append(lines, "*** End Patch")
	return strings.Join(lines, "\n")
}

func alreadyAppliedReplaceResult(path string, oldText string, newText string) (string, bool) {
	if strings.TrimSpace(newText) == "" {
		return "", false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() > maxTextEditorReadBytes {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	content := string(data)
	if countExactLineBlock(content, oldText) > 0 || countExactLineBlock(content, newText) != 1 {
		return "", false
	}
	return alreadyAppliedResult(path), true
}

func alreadyAppliedResult(path string) string {
	return strings.Join([]string{
		"TEXT_EDITOR_ALREADY_APPLIED",
		"path: " + path,
		"file_edit_state: already_applied",
		"required_next_action: read_only_verify_current_file_or_summarize",
		"forbidden_next_action: repeat_same_text_editor_edit",
		"recovery: the requested content is already present. Do not send the same text editor edit again; inspect current file content, then edit a different missing change or summarize.",
	}, "\n")
}

func countExactLineBlock(content string, block string) int {
	if block == "" {
		return 0
	}
	lines := normalizedEditorLines(content)
	blockLines := normalizedEditorLines(block)
	if len(blockLines) == 0 || len(blockLines) > len(lines) {
		return 0
	}
	count := 0
	for i := 0; i <= len(lines)-len(blockLines); i++ {
		matched := true
		for j, blockLine := range blockLines {
			if lines[i+j] != blockLine {
				matched = false
				break
			}
		}
		if matched {
			count++
		}
	}
	return count
}

func expandReplacementFromFile(path string, oldText string, newText string) (string, string, bool) {
	if oldText == "" {
		return "", "", false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() > maxTextEditorReadBytes {
		return "", "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", false
	}
	content := string(data)
	if strings.Count(content, oldText) != 1 {
		return "", "", false
	}
	start := strings.Index(content, oldText)
	end := start + len(oldText)
	lineStart := strings.LastIndex(content[:start], "\n") + 1
	lineEnd := len(content)
	if nextNewline := strings.Index(content[end:], "\n"); nextNewline >= 0 {
		lineEnd = end + nextNewline
	}
	oldSegment := content[lineStart:lineEnd]
	if strings.Contains(oldSegment, newText) {
		return "", "", false
	}
	newSegment := content[lineStart:start] + newText + content[end:lineEnd]
	return oldSegment, newSegment, true
}

func insertAfterPatch(path string, anchor string, text string) string {
	lines := []string{"*** Begin Patch", "*** Update File: " + path, "@@"}
	lines = append(lines, prefixedLines(" ", anchor)...)
	lines = append(lines, prefixedLines("+", text)...)
	lines = append(lines, "*** End Patch")
	return strings.Join(lines, "\n")
}

func moveFilePatch(path string, destPath string, oldText string, newText string) string {
	lines := []string{"*** Begin Patch", "*** Update File: " + path, "*** Move to: " + destPath}
	if oldText != "" {
		lines = append(lines, "@@")
		lines = append(lines, prefixedLines("-", oldText)...)
		lines = append(lines, prefixedLines("+", newText)...)
	}
	lines = append(lines, "*** End Patch")
	return strings.Join(lines, "\n")
}

func prefixedLines(prefix string, text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	parts := strings.Split(text, "\n")
	if len(parts) > 1 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	out := make([]string, 0, len(parts))
	for _, line := range parts {
		out = append(out, prefix+line)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func alignReplacementIndent(oldText string, newText string) string {
	oldLines := normalizedEditorLines(oldText)
	newLines := normalizedEditorLines(newText)
	if len(oldLines) != len(newLines) {
		return newText
	}
	changed := false
	for i, oldLine := range oldLines {
		newLine := newLines[i]
		oldIndent := editorLineIndent(oldLine)
		if oldIndent == "" || strings.HasPrefix(newLine, oldIndent) || strings.TrimSpace(newLine) == "" {
			continue
		}
		newIndent := editorLineIndent(newLine)
		if newIndent != "" && !sameEditorIndentFamily(oldIndent, newIndent) {
			continue
		}
		newLines[i] = oldIndent + strings.TrimLeft(newLine, " \t")
		changed = true
	}
	if !changed {
		return newText
	}
	return strings.Join(newLines, "\n")
}

func sameEditorIndentFamily(a string, b string) bool {
	if strings.Contains(a+b, "\t") {
		return strings.Trim(a+b, "\t") == ""
	}
	return strings.Trim(a+b, " ") == ""
}

func normalizedEditorLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func editorLineIndent(line string) string {
	var b strings.Builder
	for _, r := range line {
		if r != ' ' && r != '\t' {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}
