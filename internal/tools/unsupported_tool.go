package tools

import (
	"fmt"
	"strings"

	"codex-bridge/internal/codex"
)

func isUnsupportedHostedTool(toolType string) bool {
	return toolType == "mcp" ||
		toolType == "computer" ||
		toolType == "image_generation" ||
		toolType == "code_interpreter"
}

func UnsupportedToolNote(responseTools []codex.ResponseTool, searchEnabled bool) string {
	var names []string
	for _, tool := range responseTools {
		toolType := rawString(tool.Raw, "type", tool.Type)
		if toolType == "" {
			continue
		}
		name := rawString(tool.Raw, "name", tool.Name)
		if name == "" {
			name = toolType
		}
		if IsWebSearchToolType(toolType) {
			if !searchEnabled {
				names = append(names, fmt.Sprintf("%s(%s)", name, toolType))
			}
			continue
		}
		switch toolType {
		case "mcp", "computer", "image_generation", "code_interpreter":
			name := rawString(tool.Raw, "name", tool.Name)
			if name == "" {
				name = toolType
			}
			names = append(names, fmt.Sprintf("%s(%s)", name, toolType))
		}
	}
	if len(names) == 0 {
		return ""
	}
	return "The upstream model is connected through Chat Completions and cannot directly execute these Responses hosted tools: " + strings.Join(names, ", ") + ". Do not pretend to call them. Use available function, shell, or local project context instead."
}
