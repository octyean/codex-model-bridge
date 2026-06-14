package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
	"codex-bridge/internal/providers"
)

const (
	KindFunction    = "function"
	KindCustom      = "custom"
	KindApplyPatch  = "apply_patch"
	KindToolSearch  = "tool_search"
	KindShell       = "shell"
	KindUnsupported = "unsupported"
)

var (
	applyPatchParameters  = json.RawMessage(`{"type":"object","properties":{"input":{"type":"string"}},"required":["input"],"additionalProperties":false}`)
	customParameters      = json.RawMessage(`{"type":"object","properties":{"input":{"type":"string"}},"required":["input"],"additionalProperties":false}`)
	toolSearchParameters  = json.RawMessage(`{"type":"object","properties":{"goal":{"type":"string"},"paths":{"type":"array","items":{"type":"string"}}},"additionalProperties":true}`)
	shellParameters       = json.RawMessage(`{"type":"object","properties":{"command":{"type":["string","array"],"items":{"type":"string"}},"workdir":{"type":"string"},"timeout_ms":{"type":"integer"},"max_output_length":{"type":"integer"}},"required":["command"],"additionalProperties":true}`)
	emptyObjectParameters = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":true}`)
)

type Context struct {
	Tools map[string]Entry
}

type Entry struct {
	Name         string
	Kind         string
	Namespace    string
	OriginalName string
	OriginalType string
}

func FromCodex(responseTools []codex.ResponseTool, adapter adapters.Adapter) ([]providers.ChatTool, Context) {
	ctx := Context{Tools: map[string]Entry{}}
	out := make([]providers.ChatTool, 0, len(responseTools))
	for _, tool := range responseTools {
		converted := convertTool(tool, adapter)
		for _, item := range converted {
			if _, exists := ctx.Tools[item.entry.Name]; exists {
				continue
			}
			ctx.Tools[item.entry.Name] = item.entry
			out = append(out, item.tool)
		}
	}
	return out, ctx
}

func FromAdditionalTools(items []map[string]any, adapter adapters.Adapter, ctx *Context) []providers.ChatTool {
	var out []providers.ChatTool
	for _, item := range items {
		itemType, _ := item["type"].(string)
		if itemType != "additional_tools" && itemType != "tool_search_output" {
			continue
		}
		rawTools, ok := item["tools"].([]any)
		if !ok {
			continue
		}
		for _, rawTool := range rawTools {
			toolMap, ok := rawTool.(map[string]any)
			if !ok {
				continue
			}
			tool, ok := responseToolFromMap(toolMap)
			if !ok {
				continue
			}
			for _, converted := range convertTool(tool, adapter) {
				if _, exists := ctx.Tools[converted.entry.Name]; exists {
					continue
				}
				ctx.Tools[converted.entry.Name] = converted.entry
				out = append(out, converted.tool)
			}
		}
	}
	return out
}

func (ctx Context) Entry(name string) Entry {
	if ctx.Tools == nil {
		return Entry{Name: name, Kind: KindFunction, OriginalName: name, OriginalType: KindFunction}
	}
	if entry, ok := ctx.Tools[name]; ok {
		return entry
	}
	return Entry{Name: name, Kind: KindFunction, OriginalName: name, OriginalType: KindFunction}
}

func (ctx Context) IsCustom(name string) bool {
	entry := ctx.Entry(name)
	return entry.Kind == KindCustom || entry.Kind == KindApplyPatch
}

func (ctx Context) IsEmpty() bool {
	return len(ctx.Tools) == 0
}

func ExtractCustomInput(arguments string) string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(arguments), &obj); err == nil {
		if input, ok := obj["input"].(string); ok {
			return input
		}
	}
	var text string
	if err := json.Unmarshal([]byte(arguments), &text); err == nil {
		return text
	}
	return arguments
}

func ExtractCustomToolInput(entry Entry, arguments string, adapter adapters.Adapter) string {
	return adapter.NormalizeCustomInput(entry.OriginalName, ExtractCustomInput(arguments))
}

func ToolChoice(value any, ctx Context) any {
	if ctx.IsEmpty() || value == nil {
		if ctx.IsEmpty() {
			return nil
		}
		return "auto"
	}
	if text, ok := value.(string); ok {
		if text == "none" || text == "auto" || text == "required" {
			return text
		}
		return value
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return value
	}
	toolType, _ := obj["type"].(string)
	if toolType == "allowed_tools" {
		return allowedToolsChoice(obj, ctx)
	}
	name, _ := obj["name"].(string)
	if name == "" {
		if function, ok := obj["function"].(map[string]any); ok {
			name, _ = function["name"].(string)
		}
	}
	if toolType == "function" && name != "" {
		if _, ok := ctx.Tools[name]; !ok {
			return nil
		}
		return map[string]any{"type": "function", "function": map[string]any{"name": name}}
	}
	return value
}

func allowedToolsChoice(obj map[string]any, ctx Context) any {
	rawTools, ok := obj["tools"].([]any)
	if !ok {
		return obj
	}
	allowed := make([]any, 0, len(rawTools))
	for _, rawTool := range rawTools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		toolType, _ := tool["type"].(string)
		name, _ := tool["name"].(string)
		if toolType != "function" || name == "" {
			continue
		}
		if _, exists := ctx.Tools[name]; exists {
			allowed = append(allowed, map[string]any{"type": "function", "function": map[string]any{"name": name}})
		}
	}
	if len(allowed) == 0 {
		return nil
	}
	mode, _ := obj["mode"].(string)
	if mode == "" {
		mode = "auto"
	}
	return map[string]any{"type": "allowed_tools", "mode": mode, "tools": allowed}
}

type convertedTool struct {
	tool  providers.ChatTool
	entry Entry
}

func convertTool(tool codex.ResponseTool, adapter adapters.Adapter) []convertedTool {
	toolType := rawString(tool.Raw, "type", tool.Type)
	switch toolType {
	case "namespace":
		return convertNamespace(tool, adapter)
	case "function":
		return convertFunction(tool, adapter, "", KindFunction)
	case "custom":
		return convertCustom(tool, adapter)
	case "apply_patch":
		tool.Name = "apply_patch"
		return convertCustom(tool, adapter)
	case "tool_search":
		return []convertedTool{chatFunction("tool_search", descriptionOrDefault(tool.Description, "Search for deferred tools to load before continuing."), toolSearchParameters, Entry{
			Name:         "tool_search",
			Kind:         KindToolSearch,
			OriginalName: "tool_search",
			OriginalType: "tool_search",
		})}
	case "local_shell", "shell":
		return []convertedTool{chatFunction("shell", descriptionOrDefault(tool.Description, "Run a local shell command through Codex."), shellParameters, Entry{
			Name:         "shell",
			Kind:         KindShell,
			OriginalName: "shell",
			OriginalType: toolType,
		})}
	default:
		name := rawString(tool.Raw, "name", tool.Name)
		if name == "" {
			return nil
		}
		if strings.HasPrefix(toolType, "web_search") || toolType == "file_search" || toolType == "mcp" || toolType == "computer" || toolType == "image_generation" || toolType == "code_interpreter" {
			return nil
		}
		return convertFunction(tool, adapter, "", KindFunction)
	}
}

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
			out = append(out, converted)
		}
	}
	return out
}

func convertFunction(tool codex.ResponseTool, _ adapters.Adapter, namespace string, kind string) []convertedTool {
	name := rawString(tool.Raw, "name", tool.Name)
	if name == "" {
		return nil
	}
	params := tool.Parameters
	if len(params) == 0 {
		params = objectParameters()
	}
	return []convertedTool{chatFunction(name, tool.Description, params, Entry{
		Name:         name,
		Kind:         kind,
		Namespace:    namespace,
		OriginalName: name,
		OriginalType: "function",
	})}
}

func objectParameters() json.RawMessage {
	return emptyObjectParameters
}

func convertCustom(tool codex.ResponseTool, adapter adapters.Adapter) []convertedTool {
	name := rawString(tool.Raw, "name", tool.Name)
	if name == "" {
		name = "apply_patch"
	}
	kind := KindCustom
	params := customParameters
	if name == "apply_patch" {
		kind = KindApplyPatch
		params = applyPatchParameters
	}
	return []convertedTool{chatFunction(name, adapter.CustomToolDescription(name, tool), params, Entry{
		Name:         name,
		Kind:         kind,
		OriginalName: name,
		OriginalType: rawString(tool.Raw, "type", tool.Type),
	})}
}

func chatFunction(name string, description string, parameters json.RawMessage, entry Entry) convertedTool {
	return convertedTool{
		tool: providers.ChatTool{
			Type: "function",
			Function: providers.ChatFunction{
				Name:        name,
				Description: description,
				Parameters:  parameters,
			},
		},
		entry: entry,
	}
}

func responseToolFromMap(toolMap map[string]any) (codex.ResponseTool, bool) {
	data, err := json.Marshal(toolMap)
	if err != nil {
		return codex.ResponseTool{}, false
	}
	var tool codex.ResponseTool
	if err := json.Unmarshal(data, &tool); err != nil {
		return codex.ResponseTool{}, false
	}
	return tool, true
}

func rawString(raw map[string]any, key string, fallback string) string {
	if raw != nil {
		if value, ok := raw[key].(string); ok {
			return value
		}
	}
	return fallback
}

func descriptionOrDefault(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
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

func ToolSearchArguments(arguments string) any {
	var obj map[string]any
	if err := json.Unmarshal([]byte(arguments), &obj); err == nil {
		return obj
	}
	return map[string]any{"goal": arguments}
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

func UnsupportedToolNote(responseTools []codex.ResponseTool, searchEnabled bool) string {
	var names []string
	for _, tool := range responseTools {
		toolType := rawString(tool.Raw, "type", tool.Type)
		if toolType == "" {
			continue
		}
		name := rawString(tool.Raw, "name", tool.Name)
		if name == "" {
			name = toolType
		}
		if strings.HasPrefix(toolType, "web_search") {
			if !searchEnabled {
				names = append(names, fmt.Sprintf("%s(%s)", name, toolType))
			}
			continue
		}
		switch toolType {
		case "file_search", "mcp", "computer", "image_generation", "code_interpreter":
			name := rawString(tool.Raw, "name", tool.Name)
			if name == "" {
				name = toolType
			}
			names = append(names, fmt.Sprintf("%s(%s)", name, toolType))
		}
	}
	if len(names) == 0 {
		return ""
	}
	return "The upstream model is connected through Chat Completions and cannot directly execute these Responses hosted tools: " + strings.Join(names, ", ") + ". Do not pretend to call them. Use available function, shell, or local project context instead."
}
