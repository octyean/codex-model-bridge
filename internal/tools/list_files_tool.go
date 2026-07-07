package tools

import (
	"encoding/json"
	"strconv"
	"strings"

	"codex-bridge/internal/providers"
)

const ListFilesToolName = "list_files"

var listFilesParameters = json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Optional absolute, $HOME, ~, file://, or workspace-relative directory/file path. Omit for current workspace."},"recursive":{"type":"boolean","description":"Whether to list recursively. Omit for false."},"max_results":{"type":"integer","description":"Maximum entries to return. Omit for 100; maximum 300."}},"additionalProperties":false}`)

type listFilesSpec struct {
	Path       string
	Recursive  bool
	MaxResults int
}

func AddListFilesProxy(chatTools []providers.ChatTool, ctx *Context) []providers.ChatTool {
	if ctx.Tools == nil || !ctx.Has("exec_command") || ctx.Has(ListFilesToolName) {
		return chatTools
	}
	converted := listFilesConvertedTool()
	if registerConvertedTool(ctx, converted) {
		return append(chatTools, converted.tool)
	}
	return chatTools
}

func listFilesConvertedTool() convertedTool {
	entry := newEntry(ListFilesToolName, KindListFiles, InputModeJSON, SideEffectRead, "function", listFilesDescription(), nil)
	entry.SchemaQuality = schemaQuality(listFilesParameters)
	return chatFunction(entry, listFilesParameters)
}

func listFilesDescription() string {
	return strings.Join([]string{
		"List local file and directory paths through Codex Bridge.",
		"Use this instead of shell ls/find when you need to inspect repository structure, check whether paths exist, or see entries under a directory.",
		"For finding files by name/path fragment or content, use search_files. For reading file content, use read_file.",
		"Do not use this for web search, MCP resources, code execution, or file edits.",
	}, "\n")
}

func ListFilesCommand(arguments string, ctx Context) ExecCommand {
	spec := listFilesSpecFromArguments(arguments, ctx)
	depth := " -maxdepth 1 -mindepth 1"
	if spec.Recursive {
		depth = ""
	}
	return ExecCommand{
		Cmd:             "find " + shellQuote(spec.Path) + depth + " -print 2>&1 | sort | head -n " + strconv.Itoa(spec.MaxResults) + " | head -c 30000",
		Workdir:         ctx.Workspace,
		MaxOutputTokens: 12000,
	}
}

func listFilesSpecFromArguments(arguments string, ctx Context) listFilesSpec {
	spec := listFilesSpec{Path: ".", MaxResults: 100}
	var obj map[string]any
	if err := json.Unmarshal([]byte(arguments), &obj); err == nil {
		if path, ok := obj["path"].(string); ok && strings.TrimSpace(path) != "" {
			spec.Path = path
		}
		if recursive, ok := obj["recursive"].(bool); ok {
			spec.Recursive = recursive
		}
		spec.MaxResults = boundedPositiveInt(obj["max_results"], 100, 300)
	}
	if path := ctx.ResolveLocalResourcePath(spec.Path, true); path != "" {
		spec.Path = path
	}
	return spec
}
