package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"codex-bridge/internal/providers"
)

const ReadFileToolName = "read_file"

var readFileParameters = json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Absolute, $HOME, ~, file://, or workspace-relative text file path."},"start_line":{"type":"integer","description":"1-based first line to read. Omit for 1."},"line_limit":{"type":"integer","description":"Maximum lines to read. Omit for 240; maximum 400."}},"required":["path"],"additionalProperties":false}`)

func AddReadFileProxy(chatTools []providers.ChatTool, ctx *Context) []providers.ChatTool {
	if ctx.Tools == nil || !ctx.Has("exec_command") || ctx.Has(ReadFileToolName) {
		return chatTools
	}
	converted := readFileConvertedTool()
	if registerConvertedTool(ctx, converted) {
		return append(chatTools, converted.tool)
	}
	return chatTools
}

func readFileConvertedTool() convertedTool {
	entry := newEntry(ReadFileToolName, KindReadFile, InputModeJSON, SideEffectRead, "function", readFileDescription(), nil)
	entry.SchemaQuality = schemaQuality(readFileParameters)
	return chatFunction(entry, readFileParameters)
}

func readFileDescription() string {
	return strings.Join([]string{
		"Read a local text file through Codex Bridge.",
		"Use this for repository files, skill files, absolute paths, $HOME paths, ~/ paths, file:// URIs, and workspace-relative paths.",
		"The result is the requested line range. If you still need later content, continue with a larger start_line; do not repeat the same path and start_line.",
		"Do not use this for MCP resources, web pages, shell commands, image inspection, or file edits.",
	}, "\n")
}

func ReadFileCommand(arguments string, ctx Context) ExecCommand {
	spec := readFileSpecFromArguments(arguments, ctx)
	end := spec.StartLine + spec.LineLimit - 1
	return ExecCommand{
		Cmd:             "rtk sed -n " + shellQuote(fmt.Sprintf("%d,%dp", spec.StartLine, end)) + " " + shellQuote(spec.Path) + " 2>&1 | head -c 30000",
		Workdir:         ctx.Workspace,
		MaxOutputTokens: 12000,
	}
}

func readFileSpecFromArguments(arguments string, ctx Context) localResourceRead {
	obj := map[string]any{}
	if err := json.Unmarshal([]byte(arguments), &obj); err != nil {
		var path string
		if json.Unmarshal([]byte(arguments), &path) != nil {
			path = arguments
		}
		obj["path"] = strings.TrimSpace(path)
	}
	path, _ := obj["path"].(string)
	if strings.TrimSpace(path) == "" {
		path, _ = obj["uri"].(string)
	}
	return localResourceRead{
		Path:      ctx.ResolveLocalResourcePath(path, true),
		StartLine: positiveInt(obj["start_line"], 1),
		LineLimit: boundedPositiveInt(obj["line_limit"], 240, 400),
	}
}
