package tools

import (
	"encoding/json"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
)

var (
	applyPatchParameters = json.RawMessage(`{"type":"object","properties":{"input":{"type":"string"}},"required":["input"],"additionalProperties":false}`)
	customParameters     = json.RawMessage(`{"type":"object","properties":{"input":{"type":"string"}},"required":["input"],"additionalProperties":false}`)
)

func ExtractCustomInput(arguments string) string {
	return extractCustomInputValue(arguments, []string{"input"})
}

func ExtractCustomToolInput(entry Entry, arguments string, adapter adapters.Adapter) string {
	return ExtractCustomToolInputWithWorkspace(entry, arguments, adapter, "")
}

func ExtractCustomToolInputWithWorkspace(entry Entry, arguments string, adapter adapters.Adapter, workspace string) string {
	if entry.Kind() == KindPatch {
		return adapter.NormalizePatchInput(extractCustomInputValue(arguments, []string{"input", "patch", "content"}))
	}
	if entry.Kind() == KindTextEditor {
		input, err := TextEditorPatchInputWithWorkspace(TextEditorCanonicalArguments(entry.Name(), arguments), workspace)
		if err != nil {
			return ""
		}
		return adapter.NormalizePatchInput(input)
	}
	return adapter.NormalizeCustomInput(entry.OriginalName(), ExtractCustomInput(arguments))
}

func extractCustomInputValue(arguments string, keys []string) string {
	var value any
	if err := json.Unmarshal([]byte(arguments), &value); err == nil {
		if input, ok := customInputFromValue(value, keys); ok {
			return input
		}
	}
	return arguments
}

func customInputFromValue(value any, keys []string) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case map[string]any:
		for _, key := range keys {
			if text, ok := customInputFromValue(v[key], keys); ok {
				return text, true
			}
		}
		if nested, ok := v["arguments"]; ok {
			if text, ok := customInputFromValue(nested, keys); ok {
				return text, true
			}
		}
	}
	return "", false
}

func convertCustom(tool codex.ResponseTool, adapter adapters.Adapter) []convertedTool {
	name := rawString(tool.Raw, "name", tool.Name)
	if name == "" {
		name = "apply_patch"
	}
	if name == "apply_patch" && adapters.UseTextEditorForApplyPatch(adapter) {
		return convertTextEditor(tool, adapter)
	}
	kind := KindCustom
	params := customParameters
	inputMode := InputModeFreeform
	sideEffect := SideEffectNone
	if name == "apply_patch" {
		kind = KindPatch
		params = applyPatchParameters
		sideEffect = SideEffectWriteFiles
	}
	entry := newEntry(name, kind, inputMode, sideEffect, rawString(tool.Raw, "type", tool.Type), tool.Description, tool.Raw)
	entry.Descriptor.Description = adapter.CustomToolDescription(entry.Descriptor)
	return []convertedTool{chatFunction(entry, params)}
}

func convertTextEditor(tool codex.ResponseTool, adapter adapters.Adapter) []convertedTool {
	specs := TextEditorToolSpecs()
	out := make([]convertedTool, 0, len(specs))
	for _, spec := range specs {
		entry := newEntry("apply_patch", KindTextEditor, InputModeJSON, SideEffectWriteFiles, rawString(tool.Raw, "type", tool.Type), spec.Description, tool.Raw)
		entry.UpstreamName = spec.Name
		entry.SchemaQuality = schemaQuality(spec.Parameters)
		entry.Descriptor.Description = adapter.CustomToolDescription(entry.Descriptor)
		out = append(out, chatFunction(entry, spec.Parameters))
	}
	return out
}
