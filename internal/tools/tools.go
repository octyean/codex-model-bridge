package tools

import (
	"encoding/json"
	"strings"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
	"codex-bridge/internal/providers"
)

const (
	KindFunction    = "function"
	KindCustom      = "custom"
	KindPatch       = "patch"
	KindTextEditor  = "text_editor_patch"
	KindToolSearch  = "tool_search"
	KindMCPResource = "mcp_resource"
	KindWebSearch   = "web_search"
	KindShell       = "shell"

	InputModeJSON     = "json"
	InputModeFreeform = "freeform"
	InputModeAction   = "action"

	SideEffectNone       = "none"
	SideEffectRead       = "read"
	SideEffectWriteFiles = "write_files"
	SideEffectExecute    = "execute"

	ArgumentModeIdentity      = "identity"
	ArgumentModeKwargs        = "kwargs_envelope"
	SchemaQualityStrong       = "strong"
	SchemaQualityOpenObject   = "open_object"
	SchemaQualityEnvelopeOnly = "envelope_only"
)

var (
	emptyObjectParameters = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":true}`)
)

type Context struct {
	Tools     map[string]Entry
	Workspace string
}

type Entry struct {
	Descriptor    adapters.ToolDescriptor
	Namespace     string
	UpstreamName  string
	PseudoKwargs  bool
	ArgumentMode  string
	SchemaQuality string
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

func (e Entry) Transformer() string {
	if e.PseudoKwargs {
		return "pseudo_kwargs"
	}
	return "identity"
}

func (e Entry) ContractID() string {
	parts := []string{e.Name(), e.ArgumentMode, e.SchemaQuality}
	if e.Namespace != "" {
		parts = append([]string{e.Namespace}, parts...)
	}
	return strings.Join(parts, "|")
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
	if mcpResourceFunctionAlias(name) != "" {
		if _, ok := ctx.Tools[mcpResourceProxyToolName]; ok {
			return newEntry(name, KindMCPResource, InputModeJSON, SideEffectRead, KindFunction, "", nil)
		}
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

func (ctx Context) Has(name string) bool {
	if ctx.Tools == nil {
		return false
	}
	_, ok := ctx.Tools[name]
	return ok
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
		if rawString(tool.Raw, "name", tool.Name) == "exec_command" {
			return convertExecCommand(tool, adapter)
		}
		if adapters.UseMCPResourceProxy(adapter) && isMCPResourceTool(rawString(tool.Raw, "name", tool.Name)) {
			return convertMCPResourceProxy()
		}
		return convertFunction(tool, adapter, "", KindFunction)
	case "custom":
		return convertCustom(tool, adapter)
	case "apply_patch":
		tool.Name = "apply_patch"
		return convertCustom(tool, adapter)
	case "tool_search":
		return convertToolSearch(tool.Description, tool.Raw)
	case "local_shell", "shell":
		return convertShell(tool, adapter, toolType)
	default:
		name := rawString(tool.Raw, "name", tool.Name)
		if name == "" {
			return nil
		}
		if isUnsupportedHostedTool(toolType) {
			return nil
		}
		return convertFunction(tool, adapter, "", KindFunction)
	}
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
	description = mcpResourceToolDescription(name, description)
	entry := newEntry(name, kind, InputModeJSON, SideEffectNone, "function", description, tool.Raw)
	entry.Namespace = namespace
	entry.SchemaQuality = schemaQuality(params)
	return []convertedTool{chatFunction(entry, params)}
}

func objectParameters() json.RawMessage {
	return emptyObjectParameters
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
	}, ArgumentMode: ArgumentModeIdentity, SchemaQuality: SchemaQualityStrong}
}

func schemaQuality(parameters json.RawMessage) string {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(parameters, &obj); err != nil {
		return SchemaQualityOpenObject
	}
	propertiesRaw, ok := obj["properties"]
	if !ok {
		return SchemaQualityOpenObject
	}
	var properties map[string]json.RawMessage
	if err := json.Unmarshal(propertiesRaw, &properties); err != nil {
		return SchemaQualityOpenObject
	}
	if len(properties) == 0 {
		return SchemaQualityOpenObject
	}
	return SchemaQualityStrong
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
