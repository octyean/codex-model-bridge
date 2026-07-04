package tools

import (
	"strings"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
)

func convertNamespace(tool codex.ResponseTool, adapter adapters.Adapter) []convertedTool {
	namespace := rawString(tool.Raw, "name", tool.Name)
	rawTools, ok := tool.Raw["tools"].([]any)
	if !ok {
		return nil
	}
	var out []convertedTool
	for _, rawTool := range rawTools {
		toolMap, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		child, ok := responseToolFromMap(toolMap)
		if !ok {
			continue
		}
		for _, converted := range convertTool(child, adapter) {
			converted.entry.Namespace = namespace
			converted.entry.UpstreamName = namespacedToolName(namespace, converted.entry.OriginalName())
			converted.tool.Function.Name = converted.entry.Name()
			out = append(out, converted)
		}
	}
	return out
}

func namespacedToolName(namespace string, name string) string {
	if namespace == "" {
		return name
	}
	return sanitizeToolName(namespace) + "__" + sanitizeToolName(name)
}

func sanitizeToolName(value string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		if ok {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "tool"
	}
	return out
}
