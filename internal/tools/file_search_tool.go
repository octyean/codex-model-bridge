package tools

import (
	"encoding/json"
	"strconv"
	"strings"

	"codex-bridge/internal/codex"
	"codex-bridge/internal/providers"
)

const FileSearchToolName = "search_files"

var fileSearchParameters = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"Literal text, identifier, phrase, or keyword to search for in local files."},"path":{"type":"string","description":"Optional workspace-relative or absolute file/directory path. Omit to search the current workspace."},"glob":{"type":"string","description":"Optional rg-style file glob, such as *.go or internal/**/*.go."},"max_results":{"type":"integer","description":"Maximum matching lines to return. Omit for 50; maximum 100."}},"required":["query"],"additionalProperties":false}`)

type fileSearchSpec struct {
	Query      string
	Path       string
	Glob       string
	MaxResults int
}

func AddFileSearchProxy(chatTools []providers.ChatTool, ctx *Context) []providers.ChatTool {
	if ctx.Tools == nil || !ctx.Has("exec_command") {
		return chatTools
	}
	if _, exists := ctx.Tools[FileSearchToolName]; exists {
		return chatTools
	}
	converted := fileSearchConvertedTool("file_search", "file_search", nil)
	if registerConvertedTool(ctx, converted) {
		return append(chatTools, converted.tool)
	}
	return chatTools
}

func convertFileSearch(tool codex.ResponseTool) []convertedTool {
	return []convertedTool{fileSearchConvertedTool("file_search", rawString(tool.Raw, "type", tool.Type), tool.Raw)}
}

func fileSearchConvertedTool(originalName string, originalType string, raw map[string]any) convertedTool {
	entry := newEntry(originalName, KindFileSearch, InputModeJSON, SideEffectRead, originalType, fileSearchDescription(), raw)
	entry.UpstreamName = FileSearchToolName
	entry.SchemaQuality = schemaQuality(fileSearchParameters)
	return chatFunction(entry, fileSearchParameters)
}

func fileSearchDescription() string {
	return strings.Join([]string{
		"Search local workspace files by literal text through Codex Bridge.",
		"Use this to find candidate files or matching lines before reading or editing.",
		"This does not read complete files. After a hit, call codex_context_resource with action=read_local_file and the returned path to inspect full content.",
		"Do not use this for web search, MCP resources, code execution, or generated content that is not already on disk.",
	}, "\n")
}

func FileSearchCallForTool(arguments string, ctx Context) (string, string, bool) {
	if !ctx.Has("exec_command") {
		return "", "", false
	}
	spec := fileSearchSpecFromArguments(arguments, ctx)
	return "exec_command", marshalObject(map[string]any{"cmd": fileSearchCommandForTool(spec, FileSearchToolName, arguments)}), true
}

func fileSearchSpecFromArguments(arguments string, ctx Context) fileSearchSpec {
	spec := fileSearchSpec{Query: strings.TrimSpace(arguments), Path: ".", MaxResults: 50}
	var obj map[string]any
	if err := json.Unmarshal([]byte(arguments), &obj); err != nil {
		return spec
	}
	if query, ok := obj["query"].(string); ok {
		spec.Query = strings.TrimSpace(query)
	}
	if path, ok := obj["path"].(string); ok && strings.TrimSpace(path) != "" {
		if resolved := ctx.ResolveLocalResourcePath(path, true); resolved != "" {
			spec.Path = resolved
		} else {
			spec.Path = strings.TrimSpace(path)
		}
	}
	if glob, ok := obj["glob"].(string); ok {
		spec.Glob = strings.TrimSpace(glob)
	}
	spec.MaxResults = boundedPositiveInt(obj["max_results"], 50, 100)
	return spec
}

func fileSearchCommandForTool(spec fileSearchSpec, toolName string, arguments string) string {
	return fileSearchCommand(spec) + "\nprintf '\\n%s\\n' " + shellQuote(RuntimeLocalResultEnvelopeWithoutOutput(toolName, arguments))
}

func fileSearchCommand(spec fileSearchSpec) string {
	return strings.Join([]string{
		"query=" + shellQuote(spec.Query),
		"target=" + shellQuote(spec.Path),
		"glob=" + shellQuote(spec.Glob),
		"max=" + strconv.Itoa(spec.MaxResults),
		"tmp=$(mktemp)",
		"err=$(mktemp)",
		"printf '%s\n' 'FILE_SEARCH_RESULTS'",
		"printf 'query: %s\n' \"$query\"",
		"printf 'path: %s\n' \"$target\"",
		"if [ -n \"$glob\" ]; then printf 'glob: %s\n' \"$glob\"; fi",
		"printf '%s\n' 'next_action: read matched files with codex_context_resource action=read_local_file'",
		"if [ -z \"$query\" ]; then",
		"  printf '%s\n' 'search_failed: true'",
		"  printf '%s\n' 'reason: query_required'",
		"elif [ -n \"$glob\" ]; then",
		"  rg --line-number --no-heading --color never --smart-case --trim --fixed-strings --glob \"$glob\" -- \"$query\" \"$target\" >\"$tmp\" 2>\"$err\"",
		"  status=$?",
		"else",
		"  rg --line-number --no-heading --color never --smart-case --trim --fixed-strings -- \"$query\" \"$target\" >\"$tmp\" 2>\"$err\"",
		"  status=$?",
		"fi",
		"if [ -n \"${status:-}\" ]; then",
		"  if [ \"$status\" -eq 0 ] || [ \"$status\" -eq 1 ]; then",
		"    total=$(wc -l < \"$tmp\" | tr -d ' ')",
		"    shown=$total",
		"    if [ \"$shown\" -gt \"$max\" ]; then shown=$max; fi",
		"    printf 'match_count: %s\n' \"$total\"",
		"    printf 'shown_count: %s\n' \"$shown\"",
		"    if [ \"$total\" -gt \"$max\" ]; then printf '%s\n' 'more_results_omitted: true'; fi",
		"    printf '%s\n' 'results:'",
		"    if [ \"$shown\" -gt 0 ]; then head -n \"$max\" \"$tmp\" | head -c 30000; fi",
		"  else",
		"    printf '%s\n' 'search_failed: true'",
		"    printf 'exit_code: %s\n' \"$status\"",
		"    if [ -s \"$err\" ]; then printf '%s\n' 'error:'; cat \"$err\"; fi",
		"  fi",
		"fi",
		"rm -f \"$tmp\" \"$err\"",
	}, "\n")
}
