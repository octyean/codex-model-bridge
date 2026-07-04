package tools

import (
	"encoding/json"

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
	description := adapter.ToolPolicy().ToolDescription(name, tool.Description)
	entry := newEntry(name, KindFunction, InputModeJSON, SideEffectExecute, "function", description, tool.Raw)
	return []convertedTool{chatFunction(entry, params)}
}

func convertShell(tool codex.ResponseTool, adapter adapters.Adapter, originalType string) []convertedTool {
	description := adapter.ToolPolicy().ToolDescription("shell", descriptionOrDefault(tool.Description, "Run a local shell command through Codex."))
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
		return v
	case map[string]any:
		if output, ok := v["output"].(string); ok {
			return output
		}
		if stdout, ok := v["stdout"].(string); ok {
			return stdout
		}
	}
	data, _ := json.Marshal(value)
	return string(data)
}
