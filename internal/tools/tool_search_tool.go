package tools

import (
	"encoding/json"
	"os"
	"strings"
)

var toolSearchParameters = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"paths":{"type":"array","items":{"type":"string"}}},"required":["query"],"additionalProperties":true}`)

func convertToolSearch(toolDescription string, raw map[string]any) []convertedTool {
	description := descriptionOrDefault(toolDescription, "Search for deferred tools to load before continuing.")
	description = toolSearchDescription(description)
	entry := newEntry("tool_search", KindToolSearch, InputModeJSON, SideEffectRead, "tool_search", description, raw)
	return []convertedTool{chatFunction(entry, toolSearchParameters)}
}

func toolSearchDescription(description string) string {
	const boundary = "Use tool_search only when the needed callable tool is not already visible. Do not use it to read files, find local paths, inspect repositories, or choose between already visible tools."
	if description == "" {
		return boundary
	}
	return description + "\n" + boundary
}

func ToolSearchArguments(arguments string) any {
	var obj map[string]any
	if err := json.Unmarshal([]byte(arguments), &obj); err == nil {
		if _, ok := obj["query"].(string); !ok {
			if goal, ok := obj["goal"].(string); ok {
				obj["query"] = goal
				delete(obj, "goal")
			}
		}
		return obj
	}
	return map[string]any{"query": arguments}
}

func ToolSearchCallWithContext(arguments string, ctx Context) (string, string, bool) {
	if !ctx.Has("exec_command") {
		return "", "", false
	}
	if path := toolSearchLocalPath(arguments); path != "" {
		return "exec_command", marshalObject(map[string]any{"cmd": localFileReadCommand(path)}), true
	}
	return "", "", false
}

func toolSearchLocalPath(arguments string) string {
	var value any
	if err := json.Unmarshal([]byte(arguments), &value); err == nil {
		if path := toolSearchLocalPathFromValue(value); path != "" {
			return path
		}
	}
	return localPathFromText(arguments)
}

func toolSearchLocalPathFromValue(value any) string {
	switch v := value.(type) {
	case string:
		return localPathFromText(v)
	case []any:
		for _, item := range v {
			if path := toolSearchLocalPathFromValue(item); path != "" {
				return path
			}
		}
	case map[string]any:
		for _, key := range []string{"path", "file", "uri", "query", "goal"} {
			if path := toolSearchLocalPathFromValue(v[key]); path != "" {
				return path
			}
		}
		for _, key := range []string{"paths", "files"} {
			if path := toolSearchLocalPathFromValue(v[key]); path != "" {
				return path
			}
		}
	}
	return ""
}

func localPathFromText(text string) string {
	for _, field := range strings.FieldsFunc(text, localPathSeparator) {
		candidate := strings.Trim(field, "`'\"“”‘’,;:，。；：()（）[]【】{}<>")
		if path := normalizeToolSearchFilePath(candidate); path != "" {
			return path
		}
		if index := strings.Index(candidate, "/"); index >= 0 {
			if path := normalizeToolSearchFilePath(candidate[index:]); path != "" {
				return path
			}
		}
	}
	return ""
}

func localPathSeparator(r rune) bool {
	return r == 0 || r == '\n' || r == '\r' || r == '\t' || r == ' '
}

func normalizeToolSearchFilePath(candidate string) string {
	path := normalizeLocalResourcePath(candidate)
	if path == "" {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return ""
	}
	return path
}
