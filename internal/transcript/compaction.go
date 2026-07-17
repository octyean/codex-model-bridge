package transcript

import (
	"fmt"
	"strings"

	"codex-bridge/internal/providers"
)

const environmentContextStart = "<environment_context>"
const environmentContextEnd = "</environment_context>"

func compactChatTranscript(messages []providers.ChatMessage) []providers.ChatMessage {
	return keepLatestEnvironmentContext(messages)
}

func keepLatestEnvironmentContext(messages []providers.ChatMessage) []providers.ChatMessage {
	total := 0
	for _, message := range messages {
		total += countEnvironmentContextBlocks(message.Content)
	}
	if total <= 1 {
		return messages
	}

	seen := 0
	out := make([]providers.ChatMessage, 0, len(messages))
	for _, message := range messages {
		message.Content = stripSupersededEnvironmentContexts(message.Content, total, &seen)
		if message.Role == "user" && emptyChatContent(message.Content) && len(message.ToolCalls) == 0 && strings.TrimSpace(message.ReasoningContent) == "" {
			continue
		}
		out = append(out, message)
	}
	return out
}

func countEnvironmentContextBlocks(content any) int {
	switch value := content.(type) {
	case string:
		return countEnvironmentContextBlocksInText(value)
	case []map[string]any:
		count := 0
		for _, part := range value {
			if text, ok := part["text"].(string); ok {
				count += countEnvironmentContextBlocksInText(text)
			}
		}
		return count
	case []any:
		count := 0
		for _, part := range value {
			if obj, ok := part.(map[string]any); ok {
				if text, ok := obj["text"].(string); ok {
					count += countEnvironmentContextBlocksInText(text)
				}
			}
		}
		return count
	default:
		return 0
	}
}

func countEnvironmentContextBlocksInText(text string) int {
	count := 0
	pos := 0
	for {
		start := strings.Index(text[pos:], environmentContextStart)
		if start < 0 {
			return count
		}
		start += pos
		end := strings.Index(text[start:], environmentContextEnd)
		if end < 0 {
			return count
		}
		pos = start + end + len(environmentContextEnd)
		count++
	}
}

func stripSupersededEnvironmentContexts(content any, total int, seen *int) any {
	switch value := content.(type) {
	case string:
		return stripSupersededEnvironmentContextsFromText(value, total, seen)
	case []map[string]any:
		out := make([]map[string]any, 0, len(value))
		for _, part := range value {
			next := copyStringAnyMap(part)
			if text, ok := next["text"].(string); ok {
				next["text"] = stripSupersededEnvironmentContextsFromText(text, total, seen)
				if strings.TrimSpace(next["text"].(string)) == "" && textContentPart(next) {
					continue
				}
			}
			out = append(out, next)
		}
		return out
	case []any:
		out := make([]any, 0, len(value))
		for _, part := range value {
			obj, ok := part.(map[string]any)
			if !ok {
				out = append(out, part)
				continue
			}
			next := copyStringAnyMap(obj)
			if text, ok := next["text"].(string); ok {
				next["text"] = stripSupersededEnvironmentContextsFromText(text, total, seen)
				if strings.TrimSpace(next["text"].(string)) == "" && textContentPart(next) {
					continue
				}
			}
			out = append(out, next)
		}
		return out
	default:
		return content
	}
}

func stripSupersededEnvironmentContextsFromText(text string, total int, seen *int) string {
	var b strings.Builder
	pos := 0
	for {
		start := strings.Index(text[pos:], environmentContextStart)
		if start < 0 {
			b.WriteString(text[pos:])
			break
		}
		start += pos
		end := strings.Index(text[start:], environmentContextEnd)
		if end < 0 {
			b.WriteString(text[pos:])
			break
		}
		end = start + end + len(environmentContextEnd)
		*seen++
		if *seen == total {
			b.WriteString(text[pos:end])
		} else {
			b.WriteString(text[pos:start])
		}
		pos = end
	}
	return strings.TrimSpace(b.String())
}

func copyStringAnyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func textContentPart(part map[string]any) bool {
	kind, _ := part["type"].(string)
	return kind == "" || kind == "text" || kind == "input_text" || kind == "output_text"
}

func emptyChatContent(content any) bool {
	switch value := content.(type) {
	case string:
		return strings.TrimSpace(value) == ""
	case []map[string]any:
		for _, part := range value {
			if text, ok := part["text"].(string); ok && strings.TrimSpace(text) != "" {
				return false
			}
		}
		return true
	case []any:
		for _, part := range value {
			if obj, ok := part.(map[string]any); ok {
				if text, ok := obj["text"].(string); ok && strings.TrimSpace(text) != "" {
					return false
				}
				continue
			}
			if strings.TrimSpace(fmt.Sprint(part)) != "" {
				return false
			}
		}
		return true
	default:
		return content == nil
	}
}
