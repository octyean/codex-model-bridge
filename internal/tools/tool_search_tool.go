package tools

import (
	"encoding/json"
	"os"
	"sort"
	"strconv"
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
	guidance := visibleToolSearchGuidance(arguments, ctx)
	if len(guidance) > 0 {
		lines = append(lines, guidance...)
		lines = append(lines, "search_results_hidden: already_visible_tool_covers_query")
		return strings.Join(lines, "\n")
	}
	if len(results) == 0 {
		if len(lines) == 1 {
			return "[]"
		}
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "result_count: "+strconv.Itoa(toolSearchResultCount(results)))
	lines = append(lines, "summary_limit: 30")
	lines = append(lines, "schema_source: next_chat_tools")
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
	if ctx.Has(mcpResourceProxyToolName) && (looksLikeLocalReadQuery(text) || localPathFromText(text, ctx) != "") {
		lines = append(lines, "- codex_context_resource: already visible. Use action=read_local_file with path, start_line, and line_limit to read local files or skill files.")
	}
	if ctx.Has(TextEditorWriteToolName) && looksLikeFileEditQuery(text) {
		lines = append(lines, "- write_file/replace_text/insert_text_at_line/insert_text_after_match/move_file/delete_file: already visible file editing tools. Use their schemas directly instead of searching again.")
	}
	if ctx.Has(FileSearchToolName) && looksLikeFileSearchQuery(text) {
		lines = append(lines, "- search_files: already visible. Use query plus optional path/glob to find matching local files, then read hits with codex_context_resource.")
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

func looksLikeFileSearchQuery(text string) bool {
	return containsAny(text, "search", "find", "grep", "rg", "match", "locate", "查找", "搜索", "检索", "匹配") &&
		containsAny(text, "file", "path", "repo", "repository", "workspace", "code", "文件", "路径", "仓库", "代码")
}

func containsAny(text string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}

func toolSearchResultCount(results []map[string]any) int {
	count := 0
	for _, result := range results {
		if nested, ok := result["tools"].([]any); ok {
			for _, rawTool := range nested {
				if _, ok := rawTool.(map[string]any); ok {
					count++
				}
			}
			continue
		}
		if name, _ := result["name"].(string); name != "" {
			count++
		}
	}
	return count
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
				toolName = namespacedToolName(name, toolName)
			}
			toolDesc, _ := tool["description"].(string)
			toolName, toolDesc = normalizedToolSearchSummary(toolName, toolDesc)
			appendToolSearchResultLines(lines, toolName, toolDesc, tool)
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
	appendToolSearchResultLines(lines, name, description, result)
	return 1
}

func normalizedToolSearchSummary(name string, description string) (string, string) {
	return normalizeExternalToolSummary(name, description)
}

func appendToolSearchResultLines(lines *[]string, name string, description string, tool map[string]any) {
	args, required, source := toolSearchSchemaFields(name, description, tool)
	*lines = append(*lines, "- name: "+name)
	*lines = append(*lines, "  summary: "+clipToolSearchText(description, 260))
	if len(args) > 0 {
		*lines = append(*lines, "  args: "+strings.Join(args, ", "))
	}
	if len(required) > 0 {
		*lines = append(*lines, "  required: "+strings.Join(required, ", "))
	}
	if effect := knownToolSearchSideEffect(name); effect != "" {
		*lines = append(*lines, "  side_effect: "+effect)
	}
	*lines = append(*lines, "  source: "+source)
	if hint := bridgeToolSearchHint(name); hint != "" {
		*lines = append(*lines, "  bridge_hint: "+hint)
	}
}

func toolSearchSchemaFields(name string, description string, tool map[string]any) ([]string, []string, string) {
	if parameters := knownToolSearchParameters(name); parameters != nil {
		return schemaFields(parameters, description, "next_chat_tools_schema")
	}
	parameters := toolSearchParametersMap(tool)
	return schemaFields(parameters, description, "schema")
}

func schemaFields(parameters map[string]any, description string, schemaSource string) ([]string, []string, string) {
	args := sortedStringKeys(parametersMap(parameters, "properties"))
	required := stringArray(parameters["required"])
	hasDescription := strings.TrimSpace(description) != ""
	hasSchema := len(args) > 0 || len(required) > 0
	source := "unknown"
	if hasDescription {
		source = "description"
	}
	if hasSchema {
		source = schemaSource
		if hasDescription {
			source = "description+" + schemaSource
		}
	}
	return args, required, source
}

func knownToolSearchParameters(name string) map[string]any {
	switch externalToolBaseName(name) {
	case mcpResourceProxyToolName:
		return rawSchemaObject(mcpResourceParameters)
	case FileSearchToolName:
		return rawSchemaObject(fileSearchParameters)
	case "tool_search":
		return rawSchemaObject(toolSearchParameters)
	case TextEditorWriteToolName, TextEditorReplaceToolName, TextEditorInsertLineToolName, TextEditorInsertMatchToolName, TextEditorMoveToolName, TextEditorDeleteToolName:
		for _, spec := range TextEditorToolSpecs() {
			if spec.Name == externalToolBaseName(name) {
				return rawSchemaObject(spec.Parameters)
			}
		}
	}
	return nil
}

func rawSchemaObject(raw json.RawMessage) map[string]any {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	return obj
}

func toolSearchParametersMap(tool map[string]any) map[string]any {
	if parameters, ok := tool["parameters"].(map[string]any); ok {
		return parameters
	}
	if function, ok := tool["function"].(map[string]any); ok {
		if parameters, ok := function["parameters"].(map[string]any); ok {
			return parameters
		}
	}
	return nil
}

func parametersMap(parameters map[string]any, key string) map[string]any {
	if parameters == nil {
		return nil
	}
	values, _ := parameters[key].(map[string]any)
	return values
}

func sortedStringKeys(values map[string]any) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func stringArray(value any) []string {
	if values, ok := value.([]string); ok {
		return values
	}
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if text, ok := item.(string); ok && text != "" {
			out = append(out, text)
		}
	}
	return out
}

func knownToolSearchSideEffect(name string) string {
	switch externalToolBaseName(name) {
	case mcpResourceProxyToolName, FileSearchToolName, "tool_search":
		return SideEffectRead
	case TextEditorWriteToolName, TextEditorReplaceToolName, TextEditorInsertLineToolName, TextEditorInsertMatchToolName, TextEditorMoveToolName, TextEditorDeleteToolName:
		return SideEffectWriteFiles
	default:
		return ""
	}
}

func bridgeToolSearchHint(name string) string {
	switch externalToolBaseName(name) {
	case mcpResourceProxyToolName:
		return "Read local files or MCP resources here; continue truncated local files with start_line=next_start_line."
	case FileSearchToolName:
		return "Search matching local files here, then read selected hits with codex_context_resource."
	case TextEditorWriteToolName, TextEditorReplaceToolName, TextEditorInsertLineToolName, TextEditorInsertMatchToolName, TextEditorMoveToolName, TextEditorDeleteToolName:
		return "Use this for file edits; inspect current file content first when replacing or inserting around existing text."
	case "tool_search":
		return "Use only for callable tools that are not already visible."
	default:
		return ""
	}
}

func clipToolSearchText(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= limit {
		return text
	}
	return text[:limit-3] + "..."
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
