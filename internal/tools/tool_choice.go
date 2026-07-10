package tools

import "codex-bridge/internal/providers"

const toolChoiceOriginalNameKey = "_codex_original_name"

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
		choice := map[string]any{"type": "function", "function": map[string]any{"name": upstreamName}}
		if upstreamName != name {
			choice[toolChoiceOriginalNameKey] = name
		}
		return choice
	}
	return value
}

func SoftRequiredToolChoiceNote(value any, supportsRequired bool) string {
	if supportsRequired {
		return ""
	}
	if value == "required" {
		return "CHAT_REQUIRED_TOOL_CHOICE\nThis Codex turn requires one of the available tools. Call an appropriate tool instead of answering in normal text. Continue from the tool result in the next turn."
	}
	obj, ok := value.(map[string]any)
	toolType, _ := obj["type"].(string)
	if !ok {
		return ""
	}
	if toolType == "allowed_tools" && obj["mode"] == "required" {
		return "CHAT_REQUIRED_TOOL_CHOICE\nThis Codex turn requires one of the allowed tools. Call an appropriate allowed tool instead of answering in normal text. Continue from the tool result in the next turn."
	}
	if toolType != "function" {
		return ""
	}
	name := choiceFunctionName(obj)
	if name == "" {
		return ""
	}
	return "CHAT_FORCED_TOOL_CHOICE\nThis Codex turn selected one required tool: " + name + ". Call that tool with appropriate arguments instead of answering in normal text. Continue from the tool result in the next turn."
}

func allowedToolsChoice(obj map[string]any, ctx Context) any {
	rawTools, ok := obj["tools"].([]any)
	if !ok {
		return obj
	}
	seen := map[string]bool{}
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
		upstreamName, exists := ctx.upstreamName(name)
		if !exists || seen[upstreamName] {
			continue
		}
		seen[upstreamName] = true
		allowed = append(allowed, map[string]any{"type": "function", "function": map[string]any{"name": upstreamName}})
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
	if isMCPResourceTool(name) {
		if _, ok := ctx.Tools[mcpResourceProxyToolName]; ok {
			return mcpResourceProxyToolName, true
		}
	}
	if IsWebSearchToolType(name) {
		if _, ok := ctx.Tools[WebSearchProxyToolName]; ok {
			return WebSearchProxyToolName, true
		}
	}
	for upstreamName, entry := range ctx.Tools {
		if entry.OriginalName() == name {
			return upstreamName, true
		}
	}
	return "", false
}

func ApplyToolChoice(chatTools []providers.ChatTool, value any, supportsRequired bool) ([]providers.ChatTool, any) {
	if value == "required" && !supportsRequired {
		return chatTools, "auto"
	}
	obj, ok := value.(map[string]any)
	if !ok || len(chatTools) == 0 {
		return chatTools, value
	}
	switch obj["type"] {
	case "function":
		name := choiceFunctionName(obj)
		if name == "" {
			return chatTools, value
		}
		if originalName, _ := obj[toolChoiceOriginalNameKey].(string); originalName != "" && isMCPResourceTool(originalName) {
			if tool, forcedName, ok := forcedMCPResourceTool(originalName); ok {
				if supportsRequired {
					return []providers.ChatTool{tool}, "required"
				}
				return []providers.ChatTool{tool}, map[string]any{"type": "function", "function": map[string]any{"name": forcedName}}
			}
		}
		if supportsRequired {
			return filterChatTools(chatTools, map[string]bool{name: true}), "required"
		}
		return filterChatTools(chatTools, map[string]bool{name: true}), "auto"
	case "allowed_tools":
		names := allowedChoiceNames(obj)
		if len(names) == 0 {
			return chatTools, nil
		}
		mode, _ := obj["mode"].(string)
		if mode != "required" {
			mode = "auto"
		}
		if !supportsRequired {
			mode = "auto"
		}
		return filterChatTools(chatTools, names), mode
	default:
		return chatTools, value
	}
}

func ForcedToolChoiceLocalCall(value any, ctx Context) (string, string, bool) {
	if ctx.IsEmpty() {
		return "", "", false
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return "", "", false
	}
	if toolType, _ := obj["type"].(string); toolType != "function" {
		return "", "", false
	}
	name := choiceFunctionName(obj)
	if name == "" {
		return "", "", false
	}
	if _, ok := ctx.upstreamName(name); !ok {
		return "", "", false
	}
	switch name {
	case "list_mcp_resources", "list_mcp_resource_templates":
		return name, "{}", true
	default:
		return "", "", false
	}
}

func choiceFunctionName(obj map[string]any) string {
	if name, _ := obj["name"].(string); name != "" {
		return name
	}
	if function, ok := obj["function"].(map[string]any); ok {
		name, _ := function["name"].(string)
		return name
	}
	return ""
}

func allowedChoiceNames(obj map[string]any) map[string]bool {
	rawTools, ok := obj["tools"].([]any)
	if !ok {
		return nil
	}
	names := map[string]bool{}
	for _, rawTool := range rawTools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		if name := choiceFunctionName(tool); name != "" {
			names[name] = true
		}
	}
	return names
}

func filterChatTools(chatTools []providers.ChatTool, names map[string]bool) []providers.ChatTool {
	out := make([]providers.ChatTool, 0, len(chatTools))
	for _, tool := range chatTools {
		if names[tool.Function.Name] {
			out = append(out, tool)
		}
	}
	return out
}
