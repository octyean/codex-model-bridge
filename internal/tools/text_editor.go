package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"codex-bridge/internal/adapters"
)

const TextEditorToolName = "file_editor"

const (
	TextEditorWriteToolName       = "write_file"
	TextEditorReplaceToolName     = "replace_text"
	TextEditorInsertLineToolName  = "insert_text_at_line"
	TextEditorInsertMatchToolName = "insert_text_after_match"
	TextEditorMoveToolName        = "move_file"
	TextEditorDeleteToolName      = "delete_file"
)

const maxTextEditorReadBytes = 4 * 1024 * 1024

type TextEditorToolSpec struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

func TextEditorToolSpecs() []TextEditorToolSpec {
	return []TextEditorToolSpec{
		{
			Name:        TextEditorWriteToolName,
			Description: "Create a new text file or intentionally replace an entire file in the Codex workspace. Do not use this for small edits to an existing file; use replace_text or an insert tool after inspecting current content.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Workspace-relative or absolute target file path."},"file_text":{"type":"string","description":"Complete desired file content."}},"required":["path","file_text"],"additionalProperties":false}`),
		},
		{
			Name:        TextEditorReplaceToolName,
			Description: "Replace exact existing text in a workspace file. Read the file first unless the exact current text is already visible; old_str must be copied exactly from the current file.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Workspace-relative or absolute target file path."},"old_str":{"type":"string","description":"Exact existing text to replace. It must appear in the current file."},"new_str":{"type":"string","description":"Replacement text."}},"required":["path","old_str","new_str"],"additionalProperties":false}`),
		},
		{
			Name:        TextEditorInsertLineToolName,
			Description: "Insert text into an existing workspace file after a known current line number. Use insert_line=0 for the beginning; prefer insert_text_after_match when an exact anchor is safer.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Workspace-relative or absolute target file path."},"insert_line":{"type":"integer","description":"Line number after which to insert text. Use 0 for the beginning of the file."},"insert_text":{"type":"string","description":"Text to insert."}},"required":["path","insert_line","insert_text"],"additionalProperties":false}`),
		},
		{
			Name:        TextEditorInsertMatchToolName,
			Description: "Insert text into an existing workspace file after exact anchor text copied from the current file. Read the target first unless the anchor is already visible in this turn.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Workspace-relative or absolute target file path."},"old_str":{"type":"string","description":"Exact anchor text after which insert_text will be inserted."},"insert_text":{"type":"string","description":"Text to insert after old_str."}},"required":["path","old_str","insert_text"],"additionalProperties":false}`),
		},
		{
			Name:        TextEditorMoveToolName,
			Description: "Move or rename one workspace file. destination_path must include the final file name, not just a directory.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Workspace-relative or absolute source file path."},"destination_path":{"type":"string","description":"Complete destination file path, including the final file name."}},"required":["path","destination_path"],"additionalProperties":false}`),
		},
		{
			Name:        TextEditorDeleteToolName,
			Description: "Delete one workspace file only when the requested change requires removing that file.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Workspace-relative or absolute target file path."}},"required":["path"],"additionalProperties":false}`),
		},
	}
}

type textEditorCommand struct {
	Command    string `json:"command"`
	Path       string `json:"path"`
	DestPath   string `json:"destination_path"`
	OldStr     string `json:"old_str"`
	NewStr     string `json:"new_str"`
	FileText   string `json:"file_text"`
	InsertLine *int   `json:"insert_line"`
	InsertText string `json:"insert_text"`
}

func IsTextEditorToolName(name string) bool {
	switch name {
	case TextEditorWriteToolName, TextEditorReplaceToolName, TextEditorInsertLineToolName, TextEditorInsertMatchToolName, TextEditorMoveToolName, TextEditorDeleteToolName:
		return true
	default:
		return false
	}
}

func TextEditorCanonicalArguments(toolName string, arguments string) string {
	command := textEditorCommandForTool(toolName)
	if command == "" {
		return arguments
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(arguments), &obj); err != nil {
		return arguments
	}
	obj["command"] = command
	data, err := json.Marshal(obj)
	if err != nil {
		return arguments
	}
	return string(data)
}

func TextEditorModelArguments(toolName string, arguments string) string {
	if !IsTextEditorToolName(toolName) {
		return arguments
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(arguments), &obj); err != nil {
		return arguments
	}
	delete(obj, "command")
	data, err := json.Marshal(obj)
	if err != nil {
		return arguments
	}
	return string(data)
}

func TextEditorToolCallFromPatch(input string) (string, string, bool) {
	if adapters.PatchIsAlreadyApplied(input) {
		return "", "", false
	}
	arguments, ok := TextEditorArgumentsFromPatch(input)
	if !ok {
		return "", "", false
	}
	command, err := parseTextEditorCommand(arguments)
	if err != nil {
		return "", "", false
	}
	name := textEditorToolForCommand(command)
	if name == "" {
		return "", "", false
	}
	return name, TextEditorModelArguments(name, arguments), true
}

func textEditorCommandForTool(toolName string) string {
	switch toolName {
	case TextEditorWriteToolName:
		return "write_file"
	case TextEditorReplaceToolName:
		return "str_replace"
	case TextEditorInsertLineToolName, TextEditorInsertMatchToolName:
		return "insert"
	case TextEditorMoveToolName:
		return "move_file"
	case TextEditorDeleteToolName:
		return "delete_file"
	default:
		return ""
	}
}

func textEditorToolForCommand(command textEditorCommand) string {
	switch NormalizeTextEditorCommand(command.Command) {
	case "write_file":
		return TextEditorWriteToolName
	case "str_replace":
		return TextEditorReplaceToolName
	case "insert":
		if command.InsertLine != nil {
			return TextEditorInsertLineToolName
		}
		return TextEditorInsertMatchToolName
	case "move_file":
		return TextEditorMoveToolName
	case "delete_file":
		return TextEditorDeleteToolName
	default:
		return ""
	}
}

func TextEditorPatchInput(arguments string) (string, error) {
	return TextEditorPatchInputWithWorkspace(arguments, "")
}

func TextEditorPatchInputWithWorkspace(arguments string, workspace string) (string, error) {
	command, err := parseTextEditorCommand(arguments)
	if err != nil {
		return "", err
	}
	path := normalizeEditorPath(command.Path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	fsPath := editorFSPath(path, workspace)
	switch NormalizeTextEditorCommand(command.Command) {
	case "write_file":
		if command.FileText == "" {
			return "", fmt.Errorf("write_file requires file_text")
		}
		if result, ok := alreadyAppliedWriteResult(path, fsPath, command.FileText); ok {
			return result, nil
		}
		if info, err := os.Stat(fsPath); err == nil && !info.IsDir() {
			if info.Size() <= maxTextEditorReadBytes {
				if data, readErr := os.ReadFile(fsPath); readErr == nil && len(data) > 0 {
					return replacePatch(path, fsPath, string(data), command.FileText), nil
				}
			}
			return replaceWholeFilePatch(path, command.FileText), nil
		}
		return addFilePatch(path, command.FileText), nil
	case "str_replace":
		if command.OldStr == "" {
			return "", fmt.Errorf("str_replace requires old_str")
		}
		newText := alignReplacementIndent(command.OldStr, command.NewStr)
		if result, ok := alreadyAppliedReplaceResult(path, fsPath, command.OldStr, newText); ok {
			return result, nil
		}
		return replacePatch(path, fsPath, command.OldStr, newText), nil
	case "insert":
		if command.InsertLine != nil {
			if command.InsertText == "" {
				return "", fmt.Errorf("insert requires insert_text")
			}
			if result, ok := alreadyAppliedInsertLineResult(path, fsPath, *command.InsertLine, command.InsertText); ok {
				return result, nil
			}
			return insertLinePatch(path, fsPath, *command.InsertLine, command.InsertText)
		}
		if command.OldStr == "" {
			return "", fmt.Errorf("insert requires insert_line or old_str")
		}
		if command.InsertText == "" {
			return "", fmt.Errorf("insert requires insert_text")
		}
		if result, ok := alreadyAppliedInsertAfterResult(path, fsPath, command.OldStr, command.InsertText); ok {
			return result, nil
		}
		return insertAfterPatch(path, command.OldStr, command.InsertText), nil
	case "move_file":
		destPath := normalizeEditorPath(command.DestPath)
		if destPath == "" {
			return "", fmt.Errorf("move_file requires destination_path")
		}
		if isEditorDirectoryTarget(command.DestPath) {
			return directoryTargetMoveResult(path, destPath), nil
		}
		if destPath == path {
			return samePathMoveResult(path), nil
		}
		if command.OldStr != "" {
			return moveFilePatch(path, destPath, command.OldStr, alignReplacementIndent(command.OldStr, command.NewStr)), nil
		}
		return moveWholeFilePatch(path, destPath, fsPath)
	case "delete_file":
		return "*** Begin Patch\n*** Delete File: " + path + "\n*** End Patch", nil
	default:
		return "", fmt.Errorf("unsupported command %q", command.Command)
	}
}

func TextEditorInvalidArgumentsResult(reason string) string {
	lines := []string{
		"TEXT_EDITOR_INVALID_ARGUMENTS",
		"file_edit_state: rejected",
	}
	if reason = strings.TrimSpace(reason); reason != "" {
		lines = append(lines, "reason: "+reason)
	}
	lines = append(lines,
		"required_next_action: call the matching file editor tool with all required fields from the visible schema",
		"forbidden_next_action: retry_invalid_text_editor_command_or_send_empty_patch",
		"recovery: use write_file, replace_text, insert_text_at_line, insert_text_after_match, move_file, or delete_file with the exact required JSON fields for that tool.",
	)
	return strings.Join(lines, "\n")
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
			"command":   "write_file",
			"path":      normalizeEditorPath(path),
			"file_text": content,
		})
	}
	if path, ok := strings.CutPrefix(lines[1], "*** Delete File: "); ok {
		path = normalizeEditorPath(path)
		if len(lines) == 3 {
			return textEditorArguments(map[string]string{
				"command": "delete_file",
				"path":    path,
			})
		}
		if len(lines) > 4 {
			if addPath, ok := strings.CutPrefix(lines[2], "*** Add File: "); ok && normalizeEditorPath(addPath) == path {
				content, ok := unprefixedPatchLines(lines[3:len(lines)-1], "+")
				if !ok {
					return "", false
				}
				return textEditorArguments(map[string]string{
					"command":   "write_file",
					"path":      path,
					"file_text": content,
				})
			}
		}
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
			"command":     "insert",
			"path":        path,
			"old_str":     anchor,
			"insert_text": text,
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
	if err := json.Unmarshal([]byte(arguments), &command); err != nil {
		return command, fmt.Errorf("arguments must be a JSON object")
	}
	command.Command = NormalizeTextEditorCommand(command.Command)
	return command, nil
}

func NormalizeTextEditorCommand(command string) string {
	return strings.TrimSpace(strings.ToLower(command))
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

func editorFSPath(path string, workspace string) string {
	if path == "" || filepath.IsAbs(path) || strings.TrimSpace(workspace) == "" {
		return path
	}
	return filepath.Join(workspace, path)
}

func addFilePatch(path string, content string) string {
	lines := prefixedLines("+", content)
	return "*** Begin Patch\n*** Add File: " + path + "\n" + strings.Join(lines, "\n") + "\n*** End Patch"
}

func samePathMoveResult(path string) string {
	return strings.Join([]string{
		"TEXT_EDITOR_MOVE_TARGET_SAME_AS_SOURCE",
		"path: " + path,
		"file_edit_state: not_modified",
		"required_next_action: use_str_replace_for_same_file_content_edits",
		"forbidden_next_action: retry_move_file_same_path",
		"recovery: source and destination are the same file. Do not use move_file for same-path edits; use replace_text or insert_text_after_match on the existing path.",
	}, "\n")
}

func directoryTargetMoveResult(path string, destPath string) string {
	examplePath := destPath + "/<target-file-name" + filepath.Ext(path) + ">"
	return strings.Join([]string{
		"TEXT_EDITOR_MOVE_TARGET_IS_DIRECTORY",
		"path: " + path,
		"destination_path: " + destPath,
		"rejected_field: destination_path",
		"rejected_value: " + destPath,
		"file_edit_state: not_modified",
		"required_next_action: retry_move_file_with_complete_destination_file_path",
		"forbidden_next_action: retry_move_file_to_directory",
		"same_call_will_repeat_this_error: true",
		"required_destination_path_shape: " + examplePath,
		"next_call_template: {\"command\":\"move_file\",\"path\":\"" + path + "\",\"destination_path\":\"" + examplePath + "\"}",
		"recovery: destination_path must be changed to a complete destination file path including the target file name. Do not pass only a directory; choose the final file path from the user request, then retry move_file if the move is still needed.",
	}, "\n")
}

func isEditorDirectoryTarget(rawPath string) bool {
	return rawPath != "" && strings.HasSuffix(strings.TrimSpace(rawPath), "/")
}

func replacePatch(path string, fsPath string, oldText string, newText string) string {
	if expandedOld, expandedNew, ok := expandReplacementFromFile(fsPath, oldText, newText); ok {
		oldText = expandedOld
		newText = expandedNew
	}
	lines := []string{"*** Begin Patch", "*** Update File: " + path, "@@"}
	lines = append(lines, prefixedLines("-", oldText)...)
	lines = append(lines, prefixedLines("+", newText)...)
	lines = append(lines, "*** End Patch")
	return strings.Join(lines, "\n")
}

func alreadyAppliedReplaceResult(path string, fsPath string, oldText string, newText string) (string, bool) {
	if strings.TrimSpace(newText) == "" {
		return "", false
	}
	info, err := os.Stat(fsPath)
	if err != nil || info.IsDir() || info.Size() > maxTextEditorReadBytes {
		return "", false
	}
	data, err := os.ReadFile(fsPath)
	if err != nil {
		return "", false
	}
	content := string(data)
	if countExactLineBlock(content, oldText) > 0 || countExactLineBlock(content, newText) != 1 {
		return "", false
	}
	return alreadyAppliedResult(path), true
}

func alreadyAppliedWriteResult(path string, fsPath string, fileText string) (string, bool) {
	info, err := os.Stat(fsPath)
	if err != nil || info.IsDir() || info.Size() > maxTextEditorReadBytes {
		return "", false
	}
	data, err := os.ReadFile(fsPath)
	if err != nil || string(data) != fileText {
		return "", false
	}
	return alreadyAppliedResult(path), true
}

func alreadyAppliedInsertLineResult(path string, fsPath string, insertLine int, insertText string) (string, bool) {
	if strings.TrimSpace(insertText) == "" {
		return "", false
	}
	info, err := os.Stat(fsPath)
	if err != nil || info.IsDir() || info.Size() > maxTextEditorReadBytes {
		return "", false
	}
	data, err := os.ReadFile(fsPath)
	if err != nil {
		return "", false
	}
	lines := normalizedEditorLines(string(data))
	insertLines := normalizedEditorLines(insertText)
	if insertLine < 0 || insertLine > len(lines) || len(insertLines) == 0 {
		return "", false
	}
	if hasLineBlockAt(lines, insertLine, insertLines) {
		return alreadyAppliedResult(path), true
	}
	return "", false
}

func alreadyAppliedInsertAfterResult(path string, fsPath string, anchor string, insertText string) (string, bool) {
	if strings.TrimSpace(insertText) == "" || strings.TrimSpace(anchor) == "" {
		return "", false
	}
	info, err := os.Stat(fsPath)
	if err != nil || info.IsDir() || info.Size() > maxTextEditorReadBytes {
		return "", false
	}
	data, err := os.ReadFile(fsPath)
	if err != nil {
		return "", false
	}
	content := string(data)
	if countExactLineBlock(content, anchor) != 1 {
		return "", false
	}
	lines := normalizedEditorLines(content)
	anchorLines := normalizedEditorLines(anchor)
	insertLines := normalizedEditorLines(insertText)
	for i := 0; i <= len(lines)-len(anchorLines); i++ {
		if hasLineBlockAt(lines, i, anchorLines) && hasLineBlockAt(lines, i+len(anchorLines), insertLines) {
			return alreadyAppliedResult(path), true
		}
	}
	return "", false
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

func hasLineBlockAt(lines []string, start int, block []string) bool {
	if len(block) == 0 || start < 0 || start+len(block) > len(lines) {
		return false
	}
	for i, line := range block {
		if lines[start+i] != line {
			return false
		}
	}
	return true
}

func expandReplacementFromFile(fsPath string, oldText string, newText string) (string, string, bool) {
	if oldText == "" {
		return "", "", false
	}
	info, err := os.Stat(fsPath)
	if err != nil || info.IsDir() || info.Size() > maxTextEditorReadBytes {
		return "", "", false
	}
	data, err := os.ReadFile(fsPath)
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

func insertLinePatch(path string, fsPath string, insertLine int, text string) (string, error) {
	info, err := os.Stat(fsPath)
	if err != nil || info.IsDir() {
		return "", fmt.Errorf("insert requires an existing file")
	}
	if info.Size() > maxTextEditorReadBytes {
		return "", fmt.Errorf("insert target is too large")
	}
	data, err := os.ReadFile(fsPath)
	if err != nil {
		return "", err
	}
	content := string(data)
	lines := normalizedEditorLines(content)
	if insertLine < 0 || insertLine > len(lines) {
		return "", fmt.Errorf("insert_line out of range")
	}
	if len(lines) == 0 {
		return replaceWholeFilePatch(path, text), nil
	}
	if insertLine == 0 {
		for end := 1; end <= len(lines) && end <= 5; end++ {
			oldText := strings.Join(lines[:end], "\n")
			if countExactLineBlock(content, oldText) == 1 {
				return replacePatch(path, fsPath, oldText, text+"\n"+oldText), nil
			}
		}
		return replaceWholeFilePatch(path, text+"\n"+strings.Join(lines, "\n")), nil
	}
	for start := insertLine - 1; start >= 0 && insertLine-start <= 5; start-- {
		anchor := strings.Join(lines[start:insertLine], "\n")
		if countExactLineBlock(content, anchor) == 1 {
			return insertAfterPatch(path, anchor, text), nil
		}
	}
	return insertAfterPatch(path, strings.Join(lines[:insertLine], "\n"), text), nil
}

func replaceWholeFilePatch(path string, content string) string {
	lines := []string{"*** Begin Patch", "*** Delete File: " + path, "*** Add File: " + path}
	lines = append(lines, prefixedLines("+", content)...)
	lines = append(lines, "*** End Patch")
	return strings.Join(lines, "\n")
}

func moveWholeFilePatch(path string, destPath string, fsPath string) (string, error) {
	info, err := os.Stat(fsPath)
	if err != nil || info.IsDir() {
		return "", fmt.Errorf("move_file requires an existing source file")
	}
	if info.Size() > maxTextEditorReadBytes {
		return "", fmt.Errorf("move_file source is too large")
	}
	data, err := os.ReadFile(fsPath)
	if err != nil {
		return "", err
	}
	lines := []string{"*** Begin Patch", "*** Delete File: " + path, "*** Add File: " + destPath}
	lines = append(lines, prefixedLines("+", string(data))...)
	lines = append(lines, "*** End Patch")
	return strings.Join(lines, "\n"), nil
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
