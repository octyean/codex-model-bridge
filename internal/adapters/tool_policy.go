package adapters

import (
	"encoding/json"
	"regexp"
	"strings"
)

const ShellFileWriteBlockedOutput = `SHELL_FILE_WRITE_BLOCKED
This model must create, edit, and delete source, document, and config files with the text editor tool.
Shell is still available for reading files, searching, building, testing, formatting, and running real generators.
Read the target file if needed, then call the text editor tool with a small exact edit.`

const shellFileWritePolicyDescription = `This shell is not a file editor. Do not use shell commands to create, edit, delete, rename, move, or copy source, document, or config files. Do not use redirects, tee, sed -i, perl -pi, Python/Node file writes, rm, mv, or cp for file changes. Use the text editor tool for file changes. Shell is for read-only inspection, search, build, test, formatting, and real project generators.`

var shellFileWritePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?s)(^|[;&|]\s*)tee\s+(?:-[a-zA-Z]+\s+)*\S+`),
	regexp.MustCompile(`(?s)\bopen\s*\([^)]*,\s*["'](?:w|a|x|w\+|a\+)["']`),
	regexp.MustCompile(`(?s)\.(?:write_text|write_bytes)\s*\(`),
	regexp.MustCompile(`(?s)(?:\.|\b)writeFile(?:Sync|sync)?\s*\(`),
	regexp.MustCompile(`(?s)(^|[;&|]\s*)sed\s+-i(?:\s|$)`),
	regexp.MustCompile(`(?s)(^|[;&|]\s*)perl\s+-pi(?:\s|$)`),
	regexp.MustCompile(`(?s)(^|[;&|]\s*)rm\s+(?:-[a-zA-Z]+\s+)*\S+`),
	regexp.MustCompile(`(?s)(^|[;&|]\s*)mv\s+(?:-[a-zA-Z]+\s+)*\S+\s+\S+`),
	regexp.MustCompile(`(?s)(^|[;&|]\s*)cp\s+(?:-[a-zA-Z]+\s+)*\S+\s+\S+`),
	regexp.MustCompile(`(?s)\brm\s+[^;&|]+&&\s*cat\s+.*>\s*\S+`),
}

var shellAllowedWriteToolPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?s)\bgofmt\s+.*\s-w(\s|$)`),
	regexp.MustCompile(`(?s)\bprettier\s+.*--write(\s|$)`),
	regexp.MustCompile(`(?s)\bgo\s+generate(\s|$)`),
	regexp.MustCompile(`(?s)\bnpm\s+run\s+(?:build|test|lint|format)(\s|$)`),
	regexp.MustCompile(`(?s)\bpnpm\s+(?:build|test|lint|format)(\s|$)`),
	regexp.MustCompile(`(?s)\byarn\s+(?:build|test|lint|format)(\s|$)`),
}

func (p ToolPolicy) BlockedShellOutput(command string) string {
	if !p.BlockShellFileWrites {
		return ""
	}
	if isAllowedShellWriteTool(command) {
		return ""
	}
	if isManualShellFileWrite(command) {
		return ShellFileWriteBlockedOutput
	}
	return ""
}

func (p ToolPolicy) BlockedToolOutput(toolName string, arguments string) string {
	if !p.BlockShellFileWrites {
		return ""
	}
	for _, command := range shellCommandsFromToolCall(toolName, arguments) {
		if output := p.BlockedShellOutput(command); output != "" {
			return output
		}
	}
	return ""
}

func (p ToolPolicy) RewriteBlockedToolCall(toolName string, arguments string) (string, bool) {
	output := p.BlockedToolOutput(toolName, arguments)
	if output == "" {
		return arguments, false
	}
	name := strings.TrimSpace(toolName)
	switch name {
	case "shell":
		return rewriteShellArguments(arguments, output)
	case "exec_command":
		return rewriteExecCommandArguments(arguments, output)
	default:
		return arguments, false
	}
}

func (p ToolPolicy) ToolDescription(toolName string, description string) string {
	if !p.BlockShellFileWrites || !isShellToolName(toolName) {
		return description
	}
	if strings.Contains(description, "This shell is not a file editor.") {
		return description
	}
	if strings.TrimSpace(description) == "" {
		return shellFileWritePolicyDescription
	}
	return description + "\n" + shellFileWritePolicyDescription
}

func isShellToolName(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "shell", "exec_command":
		return true
	default:
		return false
	}
}

func rewriteExecCommandArguments(arguments string, output string) (string, bool) {
	var obj map[string]any
	if err := json.Unmarshal([]byte(arguments), &obj); err != nil {
		return arguments, false
	}
	obj["cmd"] = blockedShellCommand(output)
	delete(obj, "command")
	delete(obj, "commands")
	data, err := json.Marshal(obj)
	if err != nil {
		return arguments, false
	}
	return string(data), true
}

func rewriteShellArguments(arguments string, output string) (string, bool) {
	var obj map[string]any
	if err := json.Unmarshal([]byte(arguments), &obj); err != nil {
		return jsonString(map[string]any{"command": blockedShellCommand(output)})
	}
	obj["command"] = blockedShellCommand(output)
	delete(obj, "commands")
	data, err := json.Marshal(obj)
	if err != nil {
		return arguments, false
	}
	return string(data), true
}

func blockedShellCommand(output string) string {
	return "printf '%s\\n' " + shellQuote(output) + "; exit 1"
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func jsonString(value any) (string, bool) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	return string(data), true
}

func shellCommandsFromToolCall(toolName string, arguments string) []string {
	name := strings.TrimSpace(toolName)
	switch name {
	case "shell", "exec_command":
	default:
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(arguments), &obj); err != nil {
		if name == "shell" {
			return []string{arguments}
		}
		return nil
	}
	for _, key := range []string{"command", "commands", "cmd"} {
		if commands := stringValues(obj[key]); len(commands) > 0 {
			return commands
		}
	}
	return nil
}

func stringValues(value any) []string {
	switch v := value.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			text, ok := item.(string)
			if ok && strings.TrimSpace(text) != "" {
				out = append(out, text)
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(v))
		for _, text := range v {
			if strings.TrimSpace(text) != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func isManualShellFileWrite(command string) bool {
	text := strings.TrimSpace(command)
	if text == "" {
		return false
	}
	if hasUnquotedRedirection(text) {
		return true
	}
	for _, pattern := range shellFileWritePatterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}

func hasUnquotedRedirection(command string) bool {
	command = stripHereDocBodies(command)
	inSingleQuote := false
	inDoubleQuote := false
	escaped := false
	for i := 0; i < len(command); i++ {
		ch := command[i]
		if escaped {
			escaped = false
			continue
		}
		switch ch {
		case '\\':
			if !inSingleQuote {
				escaped = true
			}
		case '\'':
			if !inDoubleQuote {
				inSingleQuote = !inSingleQuote
			}
		case '"':
			if !inSingleQuote {
				inDoubleQuote = !inDoubleQuote
			}
		case '>':
			if !inSingleQuote && !inDoubleQuote {
				if isSafeFdRedirection(command, i) {
					continue
				}
				return true
			}
		}
	}
	return false
}

func isSafeFdRedirection(command string, index int) bool {
	start := index - 1
	for start >= 0 && command[start] >= '0' && command[start] <= '9' {
		start--
	}
	fd := command[start+1 : index]
	if fd != "2" {
		return false
	}
	rest := strings.TrimLeft(command[index+1:], " \t")
	return strings.HasPrefix(rest, "&1") || strings.HasPrefix(rest, "/dev/null")
}

func stripHereDocBodies(command string) string {
	lines := strings.Split(command, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		out = append(out, line)
		delimiter, ok := hereDocDelimiter(line)
		if !ok {
			continue
		}
		for i+1 < len(lines) {
			i++
			if strings.TrimSpace(lines[i]) == delimiter {
				out = append(out, lines[i])
				break
			}
		}
	}
	return strings.Join(out, "\n")
}

func hereDocDelimiter(line string) (string, bool) {
	idx := strings.Index(line, "<<")
	if idx < 0 {
		return "", false
	}
	rest := strings.TrimSpace(line[idx+2:])
	if strings.HasPrefix(rest, "-") {
		rest = strings.TrimSpace(rest[1:])
	}
	if rest == "" || strings.HasPrefix(rest, "<") {
		return "", false
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return "", false
	}
	delimiter := strings.Trim(fields[0], `"'`)
	if delimiter == "" {
		return "", false
	}
	return delimiter, true
}

func isAllowedShellWriteTool(command string) bool {
	text := strings.TrimSpace(command)
	if text == "" {
		return false
	}
	for _, pattern := range shellAllowedWriteToolPatterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}
