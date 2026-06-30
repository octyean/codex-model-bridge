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
	KindFunction   = "function"
	KindCustom     = "custom"
	KindPatch      = "patch"
	KindTextEditor = "text_editor_patch"
	KindToolSearch = "tool_search"
	KindShell      = "shell"

	InputModeJSON     = "json"
	InputModeFreeform = "freeform"
	InputModeAction   = "action"

	SideEffectNone       = "none"
	SideEffectRead       = "read"
	SideEffectWriteFiles = "write_files"
	SideEffectExecute    = "execute"
)

var (
	applyPatchParameters  = json.RawMessage(`{"type":"object","properties":{"input":{"type":"string"}},"required":["input"],"additionalProperties":false}`)
	textEditorParameters  = json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","description":"One of create, str_replace, insert_after, move_file, or delete_file."},"path":{"type":"string"},"destination_path":{"type":"string","description":"Destination path for move_file."},"new_path":{"type":"string","description":"Alias for destination_path when using move_file."},"old_str":{"type":"string","description":"Exact existing text for str_replace, insert_after anchor text, or optional exact text to replace while moving a file."},"new_str":{"type":"string","description":"Replacement text for str_replace, inserted text for insert_after, optional replacement text for move_file, or destination path for move_file when destination_path/new_path is absent."},"insert_after":{"type":"string","description":"Exact existing anchor text after which new_str/text should be inserted."},"text":{"type":"string","description":"Inserted text, or file content for create."},"file_text":{"type":"string","description":"Full file content for create."}},"required":["command","path"],"additionalProperties":false}`)
	customParameters      = json.RawMessage(`{"type":"object","properties":{"input":{"type":"string"}},"required":["input"],"additionalProperties":false}`)
	toolSearchParameters  = json.RawMessage(`{"type":"object","properties":{"goal":{"type":"string"},"paths":{"type":"array","items":{"type":"string"}}},"additionalProperties":true}`)
	shellParameters       = json.RawMessage(`{"type":"object","properties":{"command":{"type":["string","array"],"items":{"type":"string"}},"workdir":{"type":"string"},"timeout_ms":{"type":"integer"},"max_output_length":{"type":"integer"}},"required":["command"],"additionalProperties":true}`)
	emptyObjectParameters = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":true}`)
)

type Context struct {
	Tools map[string]Entry
}

type Entry struct {
	Descriptor   adapters.ToolDescriptor
	Namespace    string
	UpstreamName string
}

func (e Entry) Name() string {
	if e.UpstreamName != "" {
		return e.UpstreamName
	}
	return e.Descriptor.Name
}

func (e Entry) Kind() string {
	return e.Descriptor.Kind
}

func (e Entry) OriginalName() string {
	return e.Descriptor.Name
}

func (e Entry) OriginalType() string {
	return e.Descriptor.OriginalType
}

func FromCodex(responseTools []codex.ResponseTool, adapter adapters.Adapter) ([]providers.ChatTool, Context) {
	ctx := Context{Tools: map[string]Entry{}}
	out := make([]providers.ChatTool, 0, len(responseTools))
	for _, tool := range responseTools {
		converted := convertTool(tool, adapter)
		for _, item := range converted {
			name := item.entry.Name()
			if _, exists := ctx.Tools[name]; exists {
				continue
			}
			ctx.Tools[name] = item.entry
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
				name := converted.entry.Name()
				if _, exists := ctx.Tools[name]; exists {
					continue
				}
				ctx.Tools[name] = converted.entry
				out = append(out, converted.tool)
			}
		}
	}
	return out
}

func (ctx Context) Entry(name string) Entry {
	if ctx.Tools == nil {
		return newEntry(name, KindFunction, InputModeJSON, SideEffectNone, KindFunction, "", nil)
	}
	if entry, ok := ctx.Tools[name]; ok {
		return entry
	}
	return newEntry(name, KindFunction, InputModeJSON, SideEffectNone, KindFunction, "", nil)
}

func (ctx Context) IsCustom(name string) bool {
	entry := ctx.Entry(name)
	return entry.Kind() == KindCustom || entry.Kind() == KindPatch || entry.Kind() == KindTextEditor
}

func (ctx Context) IsEmpty() bool {
	return len(ctx.Tools) == 0
}

func (ctx Context) HasFileWriteTool() bool {
	for _, entry := range ctx.Tools {
		if entry.Descriptor.SideEffect == SideEffectWriteFiles {
			return true
		}
	}
	return false
}

func ExtractCustomInput(arguments string) string {
	return extractCustomInputValue(arguments, []string{"input"})
}

func ExtractCustomToolInput(entry Entry, arguments string, adapter adapters.Adapter) string {
	if entry.Kind() == KindPatch {
		return adapter.NormalizePatchInput(extractCustomInputValue(arguments, []string{"input", "patch", "content"}))
	}
	if entry.Kind() == KindTextEditor {
		input, err := TextEditorPatchInput(arguments)
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
		upstreamName, ok := ctx.upstreamName(name)
		if !ok {
			return nil
		}
		return map[string]any{"type": "function", "function": map[string]any{"name": upstreamName}}
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
		if upstreamName, exists := ctx.upstreamName(name); exists {
			allowed = append(allowed, map[string]any{"type": "function", "function": map[string]any{"name": upstreamName}})
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

func (ctx Context) upstreamName(name string) (string, bool) {
	if _, ok := ctx.Tools[name]; ok {
		return name, true
	}
	for upstreamName, entry := range ctx.Tools {
		if entry.OriginalName() == name {
			return upstreamName, true
		}
	}
	return "", false
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
		entry := newEntry("tool_search", KindToolSearch, InputModeJSON, SideEffectRead, "tool_search", descriptionOrDefault(tool.Description, "Search for deferred tools to load before continuing."), tool.Raw)
		return []convertedTool{chatFunction(entry, toolSearchParameters)}
	case "local_shell", "shell":
		description := adapter.ToolPolicy().ToolDescription("shell", descriptionOrDefault(tool.Description, "Run a local shell command through Codex."))
		entry := newEntry("shell", KindShell, InputModeAction, SideEffectExecute, toolType, description, tool.Raw)
		return []convertedTool{chatFunction(entry, shellParameters)}
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
			converted.entry.UpstreamName = namespacedToolName(namespace, converted.entry.OriginalName())
			converted.tool.Function.Name = converted.entry.Name()
			out = append(out, converted)
		}
	}
	return out
}

func convertFunction(tool codex.ResponseTool, adapter adapters.Adapter, namespace string, kind string) []convertedTool {
	name := rawString(tool.Raw, "name", tool.Name)
	if name == "" {
		return nil
	}
	params := tool.Parameters
	if len(params) == 0 {
		params = objectParameters()
	}
	description := adapter.ToolPolicy().ToolDescription(name, tool.Description)
	entry := newEntry(name, kind, InputModeJSON, SideEffectNone, "function", description, tool.Raw)
	entry.Namespace = namespace
	return []convertedTool{chatFunction(entry, params)}
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
	inputMode := InputModeFreeform
	sideEffect := SideEffectNone
	if name == "apply_patch" {
		kind = KindPatch
		params = applyPatchParameters
		sideEffect = SideEffectWriteFiles
	}
	if name == "apply_patch" && adapters.UseTextEditorForApplyPatch(adapter) {
		kind = KindTextEditor
		params = textEditorParameters
		inputMode = InputModeJSON
		sideEffect = SideEffectWriteFiles
	}
	entry := newEntry(name, kind, inputMode, sideEffect, rawString(tool.Raw, "type", tool.Type), tool.Description, tool.Raw)
	if kind == KindTextEditor {
		entry.UpstreamName = "codex_text_editor"
	}
	entry.Descriptor.Description = adapter.CustomToolDescription(entry.Descriptor)
	return []convertedTool{chatFunction(entry, params)}
}

func chatFunction(entry Entry, parameters json.RawMessage) convertedTool {
	return convertedTool{
		tool: providers.ChatTool{
			Type: "function",
			Function: providers.ChatFunction{
				Name:        entry.Name(),
				Description: entry.Descriptor.Description,
				Parameters:  parameters,
			},
		},
		entry: entry,
	}
}

func newEntry(name string, kind string, inputMode string, sideEffect string, originalType string, description string, raw map[string]any) Entry {
	return Entry{Descriptor: adapters.ToolDescriptor{
		Name:         name,
		Kind:         kind,
		InputMode:    inputMode,
		SideEffect:   sideEffect,
		OriginalType: originalType,
		Description:  description,
		Raw:          raw,
	}}
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
