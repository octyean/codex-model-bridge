package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

var toolSearchParameters = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"paths":{"type":"array","items":{"type":"string"}}},"required":["query"],"additionalProperties":true}`)

func convertToolSearch(toolDescription string, raw map[string]any) []convertedTool {
	description := descriptionOrDefault(toolDescription, "Search for deferred tools to load before continuing.")
	description = toolSearchDescription(description)
	entry := newEntry("tool_search", KindToolSearch, InputModeJSON, SideEffectRead, "tool_search", description, raw)
	return []convertedTool{chatFunction(entry, toolSearchParameters)}
}

func toolSearchDescription(description string) string {
	const boundary = "Use tool_search only when the needed callable tool is not already visible. Do not use it to read files, find local paths, inspect repositories, or choose between already visible tools."
	if description == "" {
		return boundary
	}
	return description + "\n" + boundary
}

func ToolSearchArguments(arguments string) any {
	var obj map[string]any
	if err := json.Unmarshal([]byte(arguments), &obj); err == nil {
		if _, ok := obj["query"].(string); !ok {
			if goal, ok := obj["goal"].(string); ok {
				obj["query"] = goal
				delete(obj, "goal")
			}
		}
		return obj
	}
	return map[string]any{"query": arguments}
}

func ToolSearchOutputSummary(raw any) string {
	return ToolSearchOutputSummaryForCall(raw, "", Context{})
}

func ToolSearchOutputSummaryForCall(raw any, arguments string, ctx Context) string {
	data, _ := json.Marshal(raw)
	var results []map[string]any
	if err := json.Unmarshal(data, &results); err != nil {
		return string(data)
	}
	lines := []string{"TOOL_SEARCH_RESULTS"}
	lines = append(lines, visibleToolSearchGuidance(arguments, ctx)...)
	if len(results) == 0 {
		if len(lines) == 1 {
			return "[]"
		}
		return strings.Join(lines, "\n")
	}
	count := 0
	for _, result := range results {
		count += appendToolSearchResultSummary(&lines, result)
		if count >= 30 {
			lines = append(lines, "more_results_omitted: true")
			break
		}
	}
	return strings.Join(lines, "\n")
}

func visibleToolSearchGuidance(arguments string, ctx Context) []string {
	if arguments == "" || ctx.Tools == nil {
		return nil
	}
	text := toolSearchQueryText(arguments)
	var lines []string
	if ctx.Has(mcpResourceProxyToolName) && looksLikeLocalReadQuery(text) {
		lines = append(lines, "- codex_context_resource: already visible. Use action=read_local_file with path, start_line, and line_limit to read local files or skill files.")
	}
	if ctx.Has(TextEditorWriteToolName) && looksLikeFileEditQuery(text) {
		lines = append(lines, "- write_file/replace_text/insert_text_at_line/insert_text_after_match/move_file/delete_file: already visible file editing tools. Use their schemas directly instead of searching again.")
	}
	return lines
}

func toolSearchQueryText(arguments string) string {
	var value any
	if err := json.Unmarshal([]byte(arguments), &value); err == nil {
		return strings.ToLower(toolSearchQueryTextFromValue(value))
	}
	return strings.ToLower(arguments)
}

func toolSearchQueryTextFromValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, item := range v {
			parts = append(parts, toolSearchQueryTextFromValue(item))
		}
		return strings.Join(parts, " ")
	case map[string]any:
		var parts []string
		for _, key := range []string{"query", "goal", "path", "file", "uri"} {
			parts = append(parts, toolSearchQueryTextFromValue(v[key]))
		}
		return strings.Join(parts, " ")
	default:
		return ""
	}
}

func looksLikeLocalReadQuery(text string) bool {
	return containsAny(text, "read", "open", "inspect", "view", "load", "context", "读", "读取", "查看", "打开") &&
		containsAny(text, "file", "path", "local", "repo", "repository", "workspace", "skill", "context", "文件", "路径", "本地", "仓库", "技能", "上下文")
}

func looksLikeFileEditQuery(text string) bool {
	return containsAny(text, "write", "edit", "replace", "insert", "move", "delete", "patch", "modify", "写", "编辑", "替换", "插入", "移动", "删除", "修改") &&
		containsAny(text, "file", "path", "repo", "workspace", "文件", "路径", "仓库")
}

func containsAny(text string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}

func appendToolSearchResultSummary(lines *[]string, result map[string]any) int {
	name, _ := result["name"].(string)
	description, _ := result["description"].(string)
	if nested, ok := result["tools"].([]any); ok {
		count := 0
		for _, rawTool := range nested {
			tool, ok := rawTool.(map[string]any)
			if !ok {
				continue
			}
			toolName, _ := tool["name"].(string)
			if name != "" && !strings.Contains(toolName, "__") {
				toolName = name + "__" + toolName
			}
			toolDesc, _ := tool["description"].(string)
			toolName, toolDesc = normalizedToolSearchSummary(toolName, toolDesc)
			*lines = append(*lines, "- "+toolName+": "+clipToolSearchText(toolDesc, 220))
			count++
			if count >= 30 {
				break
			}
		}
		return count
	}
	if name == "" {
		return 0
	}
	name, description = normalizedToolSearchSummary(name, description)
	*lines = append(*lines, fmt.Sprintf("- %s: %s", name, clipToolSearchText(description, 220)))
	return 1
}

func normalizedToolSearchSummary(name string, description string) (string, string) {
	return normalizeExternalToolSummary(name, description)
}

func clipToolSearchText(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= limit {
		return text
	}
	return text[:limit-3] + "..."
}

func ToolSearchCallWithContext(arguments string, ctx Context) (string, string, bool) {
	if !ctx.Has("exec_command") {
		return "", "", false
	}
	if path := toolSearchLocalPath(arguments, ctx); path != "" {
		return "exec_command", marshalObject(map[string]any{"cmd": localFileReadCommand(localResourceRead{Path: path, StartLine: 1, LineLimit: 240})}), true
	}
	return "", "", false
}

func toolSearchLocalPath(arguments string, ctx Context) string {
	var value any
	if err := json.Unmarshal([]byte(arguments), &value); err == nil {
		if path := toolSearchLocalPathFromValue(value, ctx); path != "" {
			return path
		}
	}
	return localPathFromText(arguments, ctx)
}

func toolSearchLocalPathFromValue(value any, ctx Context) string {
	switch v := value.(type) {
	case string:
		return localPathFromText(v, ctx)
	case []any:
		for _, item := range v {
			if path := toolSearchLocalPathFromValue(item, ctx); path != "" {
				return path
			}
		}
	case map[string]any:
		for _, key := range []string{"path", "file", "uri", "query", "goal"} {
			if path := toolSearchLocalPathFromValue(v[key], ctx); path != "" {
				return path
			}
		}
		for _, key := range []string{"paths", "files"} {
			if path := toolSearchLocalPathFromValue(v[key], ctx); path != "" {
				return path
			}
		}
	}
	return ""
}

func localPathFromText(text string, ctx Context) string {
	for _, field := range strings.FieldsFunc(text, localPathSeparator) {
		candidate := strings.Trim(field, "`'\"“”‘’,;:，。；：()（）[]【】{}<>")
		if path := normalizeToolSearchFilePath(candidate, ctx); path != "" {
			return path
		}
		if index := strings.Index(candidate, "/"); index >= 0 {
			if path := normalizeToolSearchFilePath(candidate[index:], ctx); path != "" {
				return path
			}
		}
	}
	return ""
}

func localPathSeparator(r rune) bool {
	return r == 0 || r == '\n' || r == '\r' || r == '\t' || r == ' '
}

func normalizeToolSearchFilePath(candidate string, ctx Context) string {
	path := ctx.ResolveLocalResourcePath(candidate, true)
	if path == "" {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return ""
	}
	return path
}
