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
	KindReadFile    = "read_file"
	KindListFiles   = "list_files"
	KindFileSearch  = "file_search"
	KindWebSearch   = "web_search"
	KindShell       = "shell"
	KindHarnessUI   = "harness_ui"
	KindImageView   = "image_view"

	InputModeJSON     = "json"
	InputModeFreeform = "freeform"
	InputModeAction   = "action"

	SideEffectNone       = "none"
	SideEffectRead       = "read"
	SideEffectWriteFiles = "write_files"
	SideEffectExecute    = "execute"
	SideEffectStatus     = "status"

	ArgumentModeIdentity      = "identity"
	ArgumentModeKwargs        = "kwargs_envelope"
	SchemaQualityStrong       = "strong"
	SchemaQualityOpenObject   = "open_object"
	SchemaQualityEnvelopeOnly = "envelope_only"
)

var (
	emptyObjectParameters = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":true}`)
)

const (
	imageViewOriginalToolName = "view_image"
	imageViewChatToolName     = "inspect_local_image"
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
			if registerConvertedTool(&ctx, item) {
				out = append(out, item.tool)
			}
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
				if registerConvertedTool(ctx, converted) {
					out = append(out, converted.tool)
				}
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
	if mcpResourceActionForFunction(name) != "" {
		if _, ok := ctx.Tools[mcpResourceProxyToolName]; ok {
			return newEntry(name, KindMCPResource, InputModeJSON, SideEffectRead, KindFunction, "", nil)
		}
	}
	return newEntry(name, KindFunction, InputModeJSON, SideEffectNone, KindFunction, "", nil)
}

func (ctx Context) EntryByOriginalName(name string) (Entry, bool) {
	if ctx.Tools == nil {
		return Entry{}, false
	}
	if entry, ok := ctx.Tools[name]; ok {
		return entry, true
	}
	var found Entry
	matched := false
	for _, entry := range ctx.Tools {
		if entry.OriginalName() == name {
			if matched {
				return Entry{}, false
			}
			found = entry
			matched = true
		}
	}
	return found, matched
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

func IsNativeCommandProxyToolName(name string) bool {
	switch name {
	case ReadFileToolName, ListFilesToolName, FileSearchToolName:
		return true
	default:
		return false
	}
}

type convertedTool struct {
	tool  providers.ChatTool
	entry Entry
}

func registerConvertedTool(ctx *Context, converted convertedTool) bool {
	if ctx.Tools == nil {
		ctx.Tools = map[string]Entry{}
	}
	name := converted.entry.Name()
	if _, exists := ctx.Tools[name]; exists {
		return false
	}
	ctx.Tools[name] = converted.entry
	return true
}

func convertTool(tool codex.ResponseTool, adapter adapters.Adapter) []convertedTool {
	toolType := rawString(tool.Raw, "type", tool.Type)
	switch toolType {
	case "namespace":
		return convertNamespace(tool, adapter)
	case "function":
		name := rawString(tool.Raw, "name", tool.Name)
		if converted, ok := convertExternalTool(name); ok {
			return converted
		}
		if name == "exec_command" {
			return convertExecCommand(tool, adapter)
		}
		if adapters.UseMCPResourceProxy(adapter) && isMCPResourceTool(name) {
			return convertMCPResourceProxy()
		}
		if IsHarnessUITool(name) {
			return convertFunctionWithSideEffect(tool, adapter, "", KindHarnessUI, SideEffectStatus)
		}
		if RequiresImageInputTool(name) {
			return convertImageView(tool)
		}
		return convertFunction(tool, adapter, "", KindFunction)
	case "custom":
		return convertCustom(tool, adapter)
	case "apply_patch":
		tool.Name = "apply_patch"
		return convertCustom(tool, adapter)
	case "tool_search":
		return convertToolSearch(tool.Description, tool.Raw)
	case "file_search":
		return convertFileSearch(tool)
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
	return convertFunctionWithSideEffect(tool, adapter, namespace, kind, SideEffectNone)
}

func convertFunctionWithSideEffect(tool codex.ResponseTool, adapter adapters.Adapter, namespace string, kind string, sideEffect string) []convertedTool {
	name := rawString(tool.Raw, "name", tool.Name)
	if name == "" {
		return nil
	}
	params := tool.Parameters
	if len(params) == 0 {
		params = objectParameters()
	}
	description := normalizeToolDescription(mcpResourceToolDescription(name, tool.Description))
	entry := newEntry(name, kind, InputModeJSON, sideEffect, "function", description, tool.Raw)
	entry.Namespace = namespace
	entry.SchemaQuality = schemaQuality(params)
	return []convertedTool{chatFunction(entry, params)}
}

func IsHarnessUITool(name string) bool {
	switch strings.TrimSpace(name) {
	case "create_goal", "get_goal", "update_goal", "update_plan", "request_user_input":
		return true
	default:
		return false
	}
}

func IsPlanModeOnlyHarnessUITool(name string) bool {
	switch strings.TrimSpace(name) {
	case "create_goal", "get_goal", "update_goal", "request_user_input":
		return true
	default:
		return false
	}
}

func IsHarnessUIEntry(entry Entry) bool {
	return entry.Kind() == KindHarnessUI || entry.Descriptor.SideEffect == SideEffectStatus || IsHarnessUITool(entry.Name()) || IsHarnessUITool(entry.OriginalName())
}

func RequiresImageInputTool(name string) bool {
	name = strings.TrimSpace(name)
	return name == imageViewOriginalToolName || name == imageViewChatToolName
}

func RequiresImageInputEntry(entry Entry) bool {
	return entry.Kind() == KindImageView || RequiresImageInputTool(entry.Name()) || RequiresImageInputTool(entry.OriginalName())
}

func convertImageView(tool codex.ResponseTool) []convertedTool {
	name := rawString(tool.Raw, "name", tool.Name)
	if name == "" {
		return nil
	}
	params := tool.Parameters
	if len(params) == 0 {
		params = objectParameters()
	}
	entry := newEntry(name, KindImageView, InputModeJSON, SideEffectRead, "function", imageViewDescription(), tool.Raw)
	entry.UpstreamName = imageViewChatToolName
	entry.SchemaQuality = schemaQuality(params)
	return []convertedTool{chatFunction(entry, params)}
}

func imageViewDescription() string {
	return "Inspect a local image file from the filesystem when visual inspection is required. Use only for image files such as PNG, JPG, JPEG, WEBP, GIF, or SVG. Do not use this tool for Markdown, source code, JSON, CSS, Vue, JavaScript, TypeScript, TOML, YAML, or other text files; read text files with read_file instead."
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

func normalizeToolDescription(description string) string {
	return normalizeExternalToolDescription(description)
}
