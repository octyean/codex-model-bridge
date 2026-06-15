package adapters

import (
	"encoding/json"
	"regexp"
	"strings"
)

const ShellFileWriteBlockedOutput = `SHELL_FILE_WRITE_BLOCKED
This model must create, edit, and delete source, document, and config files with apply_patch.
Shell is still available for reading files, searching, building, testing, formatting, and running real generators.
Read the target file if needed, then call apply_patch with a small patch.`

var shellFileWritePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?s)(^|[;&|]\s*)cat\s+(?:<<\S+\s*)?>\s*\S+`),
	regexp.MustCompile(`(?s)(^|[;&|]\s*)printf\s+.+>\s*\S+`),
	regexp.MustCompile(`(?s)(^|[;&|]\s*)echo\s+.+>{1,2}\s*\S+`),
	regexp.MustCompile(`(?s)(^|[;&|]\s*)tee\s+(?:-[a-zA-Z]+\s+)*\S+`),
	regexp.MustCompile(`(?s)\bopen\s*\([^)]*,\s*["'](?:w|a|x|w\+|a\+)["']`),
	regexp.MustCompile(`(?s)(?:\.|\b)writeFile(?:Sync|sync)?\s*\(`),
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
	for _, pattern := range shellFileWritePatterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
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
