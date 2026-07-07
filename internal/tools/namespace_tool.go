package tools

import (
	"encoding/json"
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
		if converted, ok := convertExternalTool(rawString(child.Raw, "name", child.Name)); ok {
			out = append(out, converted...)
			continue
		}
		for _, converted := range convertTool(child, adapter) {
			converted.entry.Namespace = namespace
			converted.entry.UpstreamName = namespacedToolName(namespace, converted.entry.OriginalName())
			converted.tool.Function.Name = converted.entry.Name()
			if modelParameters, promoted, ok := pseudoKwargsModelParameters(converted.tool.Function.Parameters); ok {
				converted.entry.PseudoKwargs = true
				converted.entry.ArgumentMode = ArgumentModeKwargs
				converted.tool.Function.Parameters = modelParameters
				if promoted {
					converted.entry.SchemaQuality = schemaQuality(modelParameters)
					converted.tool.Function.Description = strings.TrimSpace(converted.tool.Function.Description + "\nCall this as a normal Chat Completions function with top-level JSON fields matching the schema; do not wrap them in kwargs.")
				} else {
					converted.entry.SchemaQuality = SchemaQualityEnvelopeOnly
					converted.tool.Function.Description = strings.TrimSpace(converted.tool.Function.Description + "\nCall this as a normal Chat Completions function. Put the tool's native arguments inside the required kwargs object.")
				}
			}
			out = append(out, converted)
		}
	}
	return out
}

func CanonicalArguments(entry Entry, arguments string) string {
	if entry.Kind() == KindTextEditor {
		arguments = TextEditorCanonicalArguments(entry.Name(), arguments)
	}
	if !entry.PseudoKwargs {
		return arguments
	}
	return canonicalPseudoKwargsArguments(arguments)
}

func RuntimeArguments(entry Entry, arguments string) string {
	if entry.Kind() == KindTextEditor {
		arguments = TextEditorCanonicalArguments(entry.Name(), arguments)
	}
	if !entry.PseudoKwargs {
		return arguments
	}
	return canonicalPseudoKwargsArguments(arguments)
}

func ModelHistoryArguments(entry Entry, arguments string) string {
	if entry.Kind() == KindTextEditor {
		arguments = TextEditorModelArguments(entry.Name(), arguments)
	}
	if !entry.PseudoKwargs {
		return arguments
	}
	canonical := canonicalPseudoKwargsArguments(arguments)
	if entry.SchemaQuality == SchemaQualityEnvelopeOnly {
		return wrapPseudoKwargsArguments(canonical)
	}
	return canonical
}

func NativeHistoryFunctionCall(name string, namespace string, arguments string, ctx Context) (string, string) {
	if namespace == "" {
		if entry, ok := ctx.Tools[name]; ok {
			return entry.Name(), ModelHistoryArguments(entry, arguments)
		}
		if entry, ok := ctx.EntryByOriginalName(name); ok {
			return entry.Name(), ModelHistoryArguments(entry, arguments)
		}
		entry := ctx.Entry(name)
		return entry.Name(), ModelHistoryArguments(entry, arguments)
	}
	toolName := namespacedToolName(namespace, name)
	entry, ok := ctx.Tools[toolName]
	if !ok {
		return toolName, arguments
	}
	return toolName, ModelHistoryArguments(entry, arguments)
}

func pseudoKwargsModelParameters(parameters json.RawMessage) (json.RawMessage, bool, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(parameters, &obj); err != nil {
		return nil, false, false
	}
	propertiesRaw, ok := obj["properties"]
	if !ok {
		return nil, false, false
	}
	var properties map[string]json.RawMessage
	if err := json.Unmarshal(propertiesRaw, &properties); err != nil {
		return nil, false, false
	}
	requiredRaw, ok := obj["required"]
	if !ok {
		return nil, false, false
	}
	var required []string
	if err := json.Unmarshal(requiredRaw, &required); err != nil {
		return nil, false, false
	}
	if len(properties) != 1 || len(required) != 1 || required[0] != "kwargs" {
		return nil, false, false
	}
	var kwargs map[string]json.RawMessage
	if err := json.Unmarshal(properties["kwargs"], &kwargs); err != nil {
		return nil, false, false
	}
	if rawType, ok := kwargs["type"]; ok {
		var typ string
		if err := json.Unmarshal(rawType, &typ); err != nil || typ != "object" {
			return nil, false, false
		}
	}
	if weakKwargsSchema(kwargs) {
		return kwargsEnvelopeParameters(), false, true
	}
	promoted := map[string]json.RawMessage{}
	for key, value := range kwargs {
		promoted[key] = value
	}
	promoted["type"] = json.RawMessage(`"object"`)
	if _, hasProperties := promoted["properties"]; !hasProperties {
		if _, hasAdditional := promoted["additionalProperties"]; !hasAdditional {
			promoted["additionalProperties"] = json.RawMessage(`true`)
		}
	}
	data, err := json.Marshal(promoted)
	if err != nil {
		return objectParameters(), true, true
	}
	return data, true, true
}

func kwargsEnvelopeParameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"kwargs":{"type":"object","description":"Native arguments for this tool.","additionalProperties":true}},"required":["kwargs"],"additionalProperties":false}`)
}

func weakKwargsSchema(kwargs map[string]json.RawMessage) bool {
	if len(kwargs) == 0 {
		return true
	}
	propertiesRaw, hasProperties := kwargs["properties"]
	if !hasProperties {
		return true
	}
	var properties map[string]json.RawMessage
	if err := json.Unmarshal(propertiesRaw, &properties); err != nil {
		return true
	}
	return len(properties) == 0
}

func canonicalPseudoKwargsArguments(arguments string) string {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(arguments), &obj); err != nil {
		return arguments
	}
	if raw, ok := obj["kwargs"]; ok && len(obj) == 1 && json.Valid(raw) {
		var nested map[string]any
		if err := json.Unmarshal(raw, &nested); err == nil {
			return marshalJSON(nested, arguments)
		}
	}
	canonical := map[string]any{}
	if raw, ok := obj["kwargs"]; ok && json.Valid(raw) {
		var nested map[string]any
		if err := json.Unmarshal(raw, &nested); err == nil {
			for key, value := range nested {
				canonical[key] = value
			}
		}
	}
	for key, raw := range obj {
		if key == "kwargs" {
			continue
		}
		var value any
		if err := json.Unmarshal(raw, &value); err == nil {
			canonical[key] = value
		}
	}
	return marshalJSON(canonical, arguments)
}

func wrapPseudoKwargsArguments(arguments string) string {
	var value any
	if err := json.Unmarshal([]byte(arguments), &value); err != nil {
		return arguments
	}
	return marshalJSON(map[string]any{"kwargs": value}, arguments)
}

func marshalJSON(value any, fallback string) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fallback
	}
	return string(data)
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
