package transcript

import (
	"encoding/json"
	"fmt"
	"strings"

	"codex-bridge/internal/providers"
)

const rawToolExchangeWindow = 6
const environmentContextStart = "<environment_context>"
const environmentContextEnd = "</environment_context>"

type completedToolExchange struct {
	assistantIndex int
	endIndex       int
	calls          []providers.ChatToolCall
	outputs        map[string]providers.ChatMessage
}

func compactChatTranscript(messages []providers.ChatMessage) []providers.ChatMessage {
	messages = keepLatestEnvironmentContext(messages)
	exchanges := completedToolExchanges(messages)
	compactCount := len(exchanges) - rawToolExchangeWindow
	if compactCount <= 0 {
		return messages
	}

	compacted := make(map[int]completedToolExchange, compactCount)
	for _, exchange := range exchanges[:compactCount] {
		compacted[exchange.assistantIndex] = exchange
	}

	out := make([]providers.ChatMessage, 0, len(messages)-compactCount)
	for i := 0; i < len(messages); {
		exchange, ok := compacted[i]
		if !ok {
			out = append(out, messages[i])
			i++
			continue
		}

		lines := []string{
			"CHAT_TRANSCRIPT_COMPACTED",
			"Older completed tool exchanges are summarized to keep the Chat conversation usable. Exact historical tool arguments and outputs are omitted; inspect the current workspace again when exact content matters.",
		}
		for {
			lines = append(lines, summarizeToolExchange(exchange)...)
			i = exchange.endIndex + 1
			next, ok := compacted[i]
			if !ok {
				break
			}
			exchange = next
		}
		out = append(out, providers.ChatMessage{Role: "system", Content: strings.Join(lines, "\n")})
	}
	return out
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

func completedToolExchanges(messages []providers.ChatMessage) []completedToolExchange {
	var exchanges []completedToolExchange
	for i := 0; i < len(messages); i++ {
		if len(messages[i].ToolCalls) == 0 {
			continue
		}

		pending := make(map[string]providers.ChatToolCall, len(messages[i].ToolCalls))
		for _, call := range messages[i].ToolCalls {
			if call.ID != "" {
				pending[call.ID] = call
			}
		}
		if len(pending) == 0 {
			continue
		}

		outputs := make(map[string]providers.ChatMessage, len(pending))
		end := i
		for j := i + 1; j < len(messages) && len(pending) > 0; j++ {
			if messages[j].Role != "tool" {
				break
			}
			if _, ok := pending[messages[j].ToolCallID]; !ok {
				break
			}
			outputs[messages[j].ToolCallID] = messages[j]
			delete(pending, messages[j].ToolCallID)
			end = j
		}
		if len(pending) > 0 {
			continue
		}

		exchanges = append(exchanges, completedToolExchange{
			assistantIndex: i,
			endIndex:       end,
			calls:          messages[i].ToolCalls,
			outputs:        outputs,
		})
		i = end
	}
	return exchanges
}

func summarizeToolExchange(exchange completedToolExchange) []string {
	lines := make([]string, 0, len(exchange.calls))
	for _, call := range exchange.calls {
		output := exchange.outputs[call.ID]
		lines = append(lines, fmt.Sprintf("- %s %s -> %s",
			call.Function.Name,
			summarizeToolArguments(call.Function.Name, call.Function.Arguments),
			summarizeToolOutput(valueText(output.Content)),
		))
	}
	return lines
}

func summarizeToolArguments(name string, raw string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return clipForSummary(raw, 180)
	}

	switch name {
	case "write_file", "replace_text", "insert_text_at_line", "insert_text_after_match", "move_file", "delete_file":
		return keyValueSummary(args, "path", "destination_path")
	case "exec_command":
		return keyValueSummary(args, "cmd")
	case "codex_context_resource":
		return keyValueSummary(args, "action", "path", "start_line", "line_limit", "server", "uri", "name")
	case "tool_search":
		return keyValueSummary(args, "query")
	case "inspect_local_image":
		return keyValueSummary(args, "path")
	default:
		return keyValueSummary(args, "command", "action", "path", "query", "name")
	}
}

func keyValueSummary(args map[string]any, keys ...string) string {
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value, ok := args[key]
		if !ok {
			continue
		}
		text := strings.TrimSpace(fmt.Sprint(value))
		if text == "" {
			continue
		}
		parts = append(parts, key+"="+clipForSummary(text, 140))
	}
	if len(parts) == 0 {
		data, _ := json.Marshal(args)
		return clipForSummary(string(data), 180)
	}
	return strings.Join(parts, " ")
}

func summarizeToolOutput(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return "completed"
	}

	var kept []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if shouldKeepOutputSummaryLine(line) {
			kept = append(kept, clipForSummary(line, 220))
		}
		if len(kept) >= 4 {
			break
		}
	}
	if len(kept) > 0 {
		return strings.Join(kept, "; ")
	}
	return clipForSummary(output, 260)
}

func shouldKeepOutputSummaryLine(line string) bool {
	switch {
	case strings.HasPrefix(line, "Exit code:"):
		return true
	case strings.HasPrefix(line, "TEXT_EDITOR_"):
		return true
	case strings.HasPrefix(line, "LOCAL_FILE_READ_"):
		return true
	case strings.HasPrefix(line, "line_range:"):
		return true
	case strings.HasPrefix(line, "total_lines:"):
		return true
	case strings.HasPrefix(line, "truncated:"):
		return true
	case strings.HasPrefix(line, "next_start_line:"):
		return true
	case strings.HasPrefix(line, "file_edit_state:"):
		return true
	case strings.HasPrefix(line, "changed_files:"):
		return true
	case strings.HasPrefix(line, "required_next_action:"):
		return true
	case strings.Contains(line, "error"):
		return true
	case strings.Contains(line, "failed"):
		return true
	case strings.Contains(line, "rejected"):
		return true
	}
	return false
}

func clipForSummary(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	return text[:limit-3] + "..."
}
