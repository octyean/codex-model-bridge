package tools

import (
	"encoding/json"
	"strings"
)

const (
	runtimeLocalResultMarker = "CODEX_BRIDGE_RUNTIME_LOCAL_RESULT"
)

func RuntimeLocalResultEnvelope(toolName string, canonicalArguments string, output string) string {
	data, _ := json.Marshal(map[string]string{
		"tool":      toolName,
		"arguments": canonicalArguments,
		"output":    output,
	})
	return output + "\n" + runtimeLocalResultMarker + "\n" + string(data)
}

func RuntimeLocalResultEnvelopeWithoutOutput(toolName string, canonicalArguments string) string {
	data, _ := json.Marshal(map[string]string{
		"tool":      toolName,
		"arguments": canonicalArguments,
	})
	return runtimeLocalResultMarker + "\n" + string(data)
}

func ParseRuntimeLocalResultEnvelope(text string) (string, string, string, bool) {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != runtimeLocalResultMarker || i+1 >= len(lines) {
			continue
		}
		var obj struct {
			Tool      string `json:"tool"`
			Arguments string `json:"arguments"`
			Output    string `json:"output"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(lines[i+1])), &obj); err != nil {
			continue
		}
		if strings.TrimSpace(obj.Tool) == "" {
			continue
		}
		if strings.TrimSpace(obj.Output) == "" {
			obj.Output = runtimeLocalOutput(lines[:i])
		}
		if strings.TrimSpace(obj.Output) == "" {
			continue
		}
		return obj.Tool, obj.Arguments, obj.Output, true
	}
	return "", "", "", false
}

func runtimeLocalOutput(lines []string) string {
	output := strings.TrimRight(strings.Join(lines, "\n"), "\n")
	if marker := "\nOutput:\n"; strings.Contains(output, "Process exited with code") {
		if index := strings.LastIndex(output, marker); index >= 0 {
			return strings.TrimRight(output[index+len(marker):], "\n")
		}
	}
	return output
}
