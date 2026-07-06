package tools

import (
	"encoding/json"
	"strings"

	"codex-bridge/internal/providers"
)

const mcpResourceProxyToolName = "codex_context_resource"

var mcpResourceParameters = json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["list_mcp","list_mcp_templates","read_mcp","read_local_file","list","list_templates","read"],"description":"Use list_mcp/list_mcp_templates/read_mcp for MCP resources. Use read_local_file for local repository files, skill files, and file:// paths. list/list_templates/read are accepted aliases."},"server":{"type":"string","description":"Exact MCP server name returned by a prior list_mcp/list_mcp_templates action. Only used for MCP resource reads."},"uri":{"type":"string","description":"Exact MCP resource URI returned by list_mcp/list_mcp_templates, or a local file path/file:// URI when action=read_local_file."},"path":{"type":"string","description":"Local file path for action=read_local_file."}},"required":["action"],"additionalProperties":false}`)

func convertMCPResourceProxy() []convertedTool {
	entry := newEntry(mcpResourceProxyToolName, KindMCPResource, InputModeJSON, SideEffectRead, "function", mcpResourceProxyDescription(), nil)
	return []convertedTool{chatFunction(entry, mcpResourceParameters)}
}

func forcedMCPResourceTool(nativeName string) (providers.ChatTool, string, bool) {
	switch nativeName {
	case "list_mcp_resources":
		return providers.ChatTool{
			Type: "function",
			Function: providers.ChatFunction{
				Name:        "list_mcp",
				Description: "List MCP resources declared by connected MCP servers. Call with empty arguments.",
				Parameters:  emptyObjectParameters,
			},
		}, "list_mcp", true
	case "list_mcp_resource_templates":
		return providers.ChatTool{
			Type: "function",
			Function: providers.ChatFunction{
				Name:        "list_mcp_templates",
				Description: "List MCP resource templates declared by connected MCP servers. Call with empty arguments.",
				Parameters:  emptyObjectParameters,
			},
		}, "list_mcp_templates", true
	case "read_mcp_resource":
		return providers.ChatTool{
			Type: "function",
			Function: providers.ChatFunction{
				Name:        "read_mcp",
				Description: "Read an MCP resource using exact server and uri values returned by MCP resource listing.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"server":{"type":"string"},"uri":{"type":"string"}},"required":["server","uri"],"additionalProperties":false}`),
			},
		}, "read_mcp", true
	default:
		return providers.ChatTool{}, "", false
	}
}

func isMCPResourceTool(name string) bool {
	switch name {
	case "list_mcp_resources", "list_mcp_resource_templates", "read_mcp_resource":
		return true
	default:
		return false
	}
}

func mcpResourceFunctionAlias(name string) string {
	switch name {
	case "list", "list_mcp":
		return "list_mcp"
	case "list_templates", "list_mcp_templates":
		return "list_mcp_templates"
	case "read", "read_mcp":
		return "read_mcp"
	default:
		return ""
	}
}

func mcpResourceToolDescription(name string, description string) string {
	switch name {
	case "list_mcp_resources", "list_mcp_resource_templates":
		return "List MCP resources declared by connected MCP servers. This lists MCP resources, not callable MCP tools. Use this before read_mcp_resource when the exact URI is unknown. Do not infer MCP resources from prompt markup, URL-like strings, local files, file:// URIs, skill names, repository paths, or user-provided <skill><path> values. For MCP tool discovery, use tool_search."
	case "read_mcp_resource":
		return "Read an MCP resource declared by a connected MCP server. Read only exact server and URI values returned by list_mcp_resources or list_mcp_resource_templates. MCP resource URIs are opaque identifiers, not local filesystem paths. Do not invent or infer server/URI values from prompt markup, URL-like strings, local files, file:// URIs, skill names, repository paths, or user-provided <skill><path> values. If content is in a local skill file or repository file, read it with available filesystem/read-only shell tools instead."
	default:
		return description
	}
}

func MCPResourceCall(arguments string) (string, string) {
	return MCPResourceCallWithContext(arguments, Context{})
}

func MCPResourceCallWithContext(arguments string, ctx Context) (string, string) {
	return MCPResourceCallForTool("", arguments, ctx)
}

func MCPResourceCallForTool(toolName string, arguments string, ctx Context) (string, string) {
	var obj map[string]any
	if err := json.Unmarshal([]byte(arguments), &obj); err != nil {
		return "list_mcp_resources", "{}"
	}
	if action := mcpResourceFunctionAlias(toolName); action != "" {
		if current, _ := obj["action"].(string); strings.TrimSpace(current) == "" {
			obj["action"] = action
		}
	}
	action, _ := obj["action"].(string)
	action = strings.TrimSpace(strings.ToLower(action))
	if path := localResourcePath(obj); path != "" && ctx.Has("exec_command") {
		tool := toolName
		if strings.TrimSpace(tool) == "" {
			tool = mcpResourceProxyToolName
		}
		return "exec_command", marshalObject(map[string]any{"cmd": localFileReadCommandForTool(path, tool, arguments)})
	}
	out := map[string]any{}
	if server, ok := obj["server"].(string); ok && strings.TrimSpace(server) != "" {
		out["server"] = server
	}
	switch action {
	case "read", "read_mcp":
		if uri, ok := obj["uri"].(string); ok {
			out["uri"] = uri
		}
		return "read_mcp_resource", marshalObject(out)
	case "list_templates", "list_mcp_templates":
		return "list_mcp_resource_templates", marshalObject(out)
	default:
		return "list_mcp_resources", marshalObject(out)
	}
}

func MCPResourceLocalPath(arguments string) string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(arguments), &obj); err != nil {
		return ""
	}
	return localResourcePath(obj)
}

func mcpResourceProxyDescription() string {
	return strings.Join([]string{
		"Read context resources through Codex Bridge.",
		"Use action=list_mcp to inspect MCP resources, action=list_mcp_templates to inspect MCP resource templates, and action=read_mcp only with exact server and uri values returned by a prior list result.",
		"Use action=read_local_file with path, or with uri set to an absolute path or file:// URI, for local skill files, repository files, and other filesystem paths.",
		"MCP resources are readable data entries, not callable MCP tools. Discover callable MCP tools with tool_search.",
		"Do not infer MCP server or URI values from prompt markup, skill names, local paths, file:// URIs, or tool names.",
	}, "\n")
}

func localResourcePath(obj map[string]any) string {
	action, _ := obj["action"].(string)
	action = strings.TrimSpace(strings.ToLower(action))
	if action != "read" && action != "read_mcp" && action != "read_local_file" {
		return ""
	}
	if action == "read_local_file" {
		if path, ok := obj["path"].(string); ok {
			return normalizeLocalResourcePath(path)
		}
	}
	for _, key := range []string{"path", "uri"} {
		text, _ := obj[key].(string)
		if path := normalizeLocalResourcePath(text); path != "" {
			return path
		}
	}
	return ""
}

func normalizeLocalResourcePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if path, ok := strings.CutPrefix(value, "file://"); ok {
		value = path
	}
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../") {
		return value
	}
	return ""
}

func localFileReadCommand(path string) string {
	return "sed -n '1,240p' " + shellQuote(path) + " 2>&1 | head -c 20000"
}

func localFileReadCommandForTool(path string, toolName string, arguments string) string {
	return localFileReadCommand(path) + "; printf '\\n%s\\n' " + shellQuote(RuntimeLocalResultEnvelopeWithoutOutput(toolName, arguments))
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func marshalObject(value map[string]any) string {
	if len(value) == 0 {
		return "{}"
	}
	data, _ := json.Marshal(value)
	return string(data)
}
