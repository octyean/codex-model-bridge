package tools

import (
	"encoding/json"
	"strconv"
	"strings"

	"codex-bridge/internal/codex"
	"codex-bridge/internal/providers"
)

const FileSearchToolName = "search_files"

var fileSearchParameters = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"Literal file name, path fragment, identifier, phrase, or keyword to search for in local files."},"path":{"type":"string","description":"Optional workspace-relative or absolute file/directory path. Omit to search the current workspace."},"glob":{"type":"string","description":"Optional rg-style file glob, such as *.go or internal/**/*.go."},"max_results":{"type":"integer","description":"Maximum matching paths or lines to return. Omit for 50; maximum 100."}},"required":["query"],"additionalProperties":false}`)

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
		"Search local workspace file paths and file contents through Codex Bridge.",
		"Use this to find candidate files by name/path or matching content lines before reading or editing.",
		"For filename searches, pass a distinctive literal filename or path fragment; this searches paths too.",
		"This does not read complete files. After a hit, call read_file with the returned path to inspect full content.",
		"Do not use this for web search, MCP resources, code execution, or generated content that is not already on disk.",
	}, "\n")
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

func FileSearchCommand(arguments string, ctx Context) ExecCommand {
	spec := fileSearchSpecFromArguments(arguments, ctx)
	contentArgs := []string{"rg", "--line-number", "--no-heading", "--color", "never", "--smart-case", "--trim", "--fixed-strings"}
	if spec.Glob != "" {
		contentArgs = append(contentArgs, "--glob", spec.Glob)
	}
	contentArgs = append(contentArgs, "--", spec.Query, spec.Path)
	fileArgs := []string{"rg", "--files"}
	if spec.Glob != "" {
		fileArgs = append(fileArgs, "--glob", spec.Glob)
	}
	fileArgs = append(fileArgs, spec.Path)
	pathArgs := []string{"rg", "--color", "never", "--smart-case", "--fixed-strings", "--", spec.Query}
	return ExecCommand{
		Cmd:             "( " + shellJoin(contentArgs) + " 2>&1 || true; " + shellJoin(fileArgs) + " 2>/dev/null | " + shellJoin(pathArgs) + " 2>&1 || true ) | head -n " + strconv.Itoa(spec.MaxResults) + " | head -c 30000",
		Workdir:         ctx.Workspace,
		MaxOutputTokens: 12000,
	}
}

func shellJoin(args []string) string {
	for i, arg := range args {
		args[i] = shellQuote(arg)
	}
	return strings.Join(args, " ")
}
