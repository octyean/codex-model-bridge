package tools

import (
	"encoding/json"
	"strings"

	"codex-bridge/internal/codex"
	"codex-bridge/internal/providers"
)

const WebSearchProxyToolName = "codex_web_search"

var webSearchParameters = json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["search","read"]},"query":{"type":"string","description":"Search query for action=search."},"url":{"type":"string","description":"URL to read for action=read."}},"required":["action"],"additionalProperties":false}`)

func WebSearchChatTool() providers.ChatTool {
	return chatFunction(webSearchEntry(), webSearchParameters).tool
}

func AddWebSearchProxy(chatTools []providers.ChatTool, ctx *Context) []providers.ChatTool {
	if ctx.Tools == nil {
		ctx.Tools = map[string]Entry{}
	}
	if _, exists := ctx.Tools[WebSearchProxyToolName]; exists {
		return chatTools
	}
	ctx.Tools[WebSearchProxyToolName] = webSearchEntry()
	return append(chatTools, WebSearchChatTool())
}

func HasWebSearch(responseTools []codex.ResponseTool) bool {
	for _, tool := range responseTools {
		if IsWebSearchToolType(rawString(tool.Raw, "type", tool.Type)) {
			return true
		}
	}
	return false
}

func IsWebSearchToolType(toolType string) bool {
	return strings.HasPrefix(toolType, "web_search")
}

func WebSearchArguments(arguments string) (string, string) {
	var args struct {
		Action string `json:"action"`
		Query  string `json:"query"`
		URL    string `json:"url"`
	}
	_ = json.Unmarshal([]byte(arguments), &args)
	if strings.TrimSpace(strings.ToLower(args.Action)) == "read" {
		return "", args.URL
	}
	return args.Query, ""
}

func webSearchProxyDescription() string {
	return strings.Join([]string{
		"Search or read current web content through Codex Bridge.",
		"Use action=search with query for current web information.",
		"Use action=read with url when a specific web page must be fetched.",
		"Do not use this for local files, repository files, MCP resources, or callable MCP tool discovery.",
	}, "\n")
}

func webSearchEntry() Entry {
	return newEntry(WebSearchProxyToolName, KindWebSearch, InputModeJSON, SideEffectRead, "web_search_preview", webSearchProxyDescription(), nil)
}
