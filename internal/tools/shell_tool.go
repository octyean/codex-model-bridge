package tools

import (
	"encoding/json"
	"strconv"
	"strings"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
)

var shellParameters = json.RawMessage(`{"type":"object","properties":{"command":{"type":["string","array"],"items":{"type":"string"}},"workdir":{"type":"string"},"timeout_ms":{"type":"integer"},"max_output_length":{"type":"integer"}},"required":["command"],"additionalProperties":true}`)

func convertExecCommand(tool codex.ResponseTool, adapter adapters.Adapter) []convertedTool {
	name := rawString(tool.Raw, "name", tool.Name)
	params := tool.Parameters
	if len(params) == 0 {
		params = objectParameters()
	}
	entry := newEntry(name, KindFunction, InputModeJSON, SideEffectExecute, "function", tool.Description, tool.Raw)
	return []convertedTool{chatFunction(entry, params)}
}

func convertShell(tool codex.ResponseTool, adapter adapters.Adapter, originalType string) []convertedTool {
	description := descriptionOrDefault(tool.Description, "Run a local shell command through Codex.")
	entry := newEntry("shell", KindShell, InputModeAction, SideEffectExecute, originalType, description, tool.Raw)
	if originalType == "exec_command" {
		entry.UpstreamName = "exec_command"
	}
	return []convertedTool{chatFunction(entry, shellParameters)}
}

func ShellArguments(arguments string) map[string]any {
	var obj map[string]any
	if err := json.Unmarshal([]byte(arguments), &obj); err == nil {
		return obj
	}
	var commands []string
	if err := json.Unmarshal([]byte(arguments), &commands); err == nil {
		return map[string]any{"commands": commands}
	}
	return map[string]any{"command": arguments}
}

func ShellOutputText(value any) string {
	switch v := value.(type) {
	case string:
		return normalizeCodexShellOutput(v)
	case map[string]any:
		if output, ok := v["output"].(string); ok {
			return normalizeCodexShellOutput(output)
		}
		if stdout, ok := v["stdout"].(string); ok {
			return normalizeCodexShellOutput(stdout)
		}
	}
	data, _ := json.Marshal(value)
	return string(data)
}

func CommandOutputBodyText(value any) string {
	text := ShellOutputText(value)
	exitCode, body, ok := parseCodexShellOutput(text)
	if !ok {
		return text
	}
	body = strings.TrimRight(body, "\n")
	if exitCode == 0 {
		return body
	}
	if body == "" {
		return "exit_code: " + strconv.Itoa(exitCode)
	}
	return "exit_code: " + strconv.Itoa(exitCode) + "\n" + body
}

func normalizeCodexShellOutput(output string) string {
	exitCode, body, ok := parseCodexShellOutput(output)
	if !ok {
		return output
	}
	body = strings.TrimRight(body, "\n")
	if body == "" {
		return "Exit code: " + strconv.Itoa(exitCode)
	}
	return "Exit code: " + strconv.Itoa(exitCode) + "\nOutput:\n" + body
}

func parseCodexShellOutput(output string) (int, string, bool) {
	lines := strings.Split(output, "\n")
	exitCode := 0
	hasExitCode := false
	outputIndex := -1
	for index, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Process exited with code") {
			value := strings.TrimSpace(strings.TrimPrefix(line, "Process exited with code"))
			code, err := strconv.Atoi(value)
			if err != nil {
				return 0, "", false
			}
			exitCode = code
			hasExitCode = true
			continue
		}
		if strings.HasPrefix(line, "Exit code:") {
			value := strings.TrimSpace(strings.TrimPrefix(line, "Exit code:"))
			code, err := strconv.Atoi(value)
			if err != nil {
				return 0, "", false
			}
			exitCode = code
			hasExitCode = true
			continue
		}
		if line == "Output:" && outputIndex < 0 {
			outputIndex = index
		}
	}
	if !hasExitCode || outputIndex < 0 || outputIndex+1 > len(lines) {
		return 0, "", false
	}
	return exitCode, strings.Join(lines[outputIndex+1:], "\n"), true
}
