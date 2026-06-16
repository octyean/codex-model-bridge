package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxTextEditorReadBytes = 4 * 1024 * 1024

type textEditorCommand struct {
	Command     string `json:"command"`
	Path        string `json:"path"`
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
		return addFilePatch(path, content), nil
	case "str_replace":
		if command.OldStr == "" {
			return "", fmt.Errorf("str_replace requires old_str")
		}
		return replacePatch(path, command.OldStr, alignReplacementIndent(command.OldStr, command.NewStr)), nil
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
	case "delete_file":
		return "*** Begin Patch\n*** Delete File: " + path + "\n*** End Patch", nil
	default:
		return "", fmt.Errorf("unsupported command %q", command.Command)
	}
}

func parseTextEditorCommand(arguments string) (textEditorCommand, error) {
	var command textEditorCommand
	if err := json.Unmarshal([]byte(arguments), &command); err == nil && (command.Command != "" || command.Path != "") {
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
