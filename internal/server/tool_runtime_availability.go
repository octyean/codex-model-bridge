package server

import (
	"strings"

	"codex-bridge/internal/providers"
	"codex-bridge/internal/tools"
)

func filterUnavailableRuntimeTools(chatTools []providers.ChatTool, toolCtx *tools.Context, messages []providers.ChatMessage) []providers.ChatTool {
	if toolCtx == nil || collaborationMode(messages) == "plan" {
		return chatTools
	}
	out := chatTools[:0]
	for _, tool := range chatTools {
		if tool.Function.Name == "request_user_input" {
			delete(toolCtx.Tools, tool.Function.Name)
			continue
		}
		out = append(out, tool)
	}
	return out
}

func collaborationMode(messages []providers.ChatMessage) string {
	for _, message := range messages {
		text := messageText(message.Content)
		if !strings.Contains(text, "Collaboration Mode:") {
			continue
		}
		switch {
		case strings.Contains(text, "Collaboration Mode: Plan"):
			return "plan"
		case strings.Contains(text, "Collaboration Mode: Default"):
			return "default"
		}
	}
	return ""
}
