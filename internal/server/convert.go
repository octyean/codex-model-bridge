package server

import (
	"encoding/json"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
	"codex-bridge/internal/providers"
	"codex-bridge/internal/toollog"
	"codex-bridge/internal/tools"
)

func responseItemsFromMessage(message providers.ChatMessage, toolCtx tools.Context, adapter adapters.Adapter, requestID string, logger *slog.Logger) []codex.ResponseItem {
	if len(message.ToolCalls) > 0 {
		items := make([]codex.ResponseItem, 0, len(message.ToolCalls))
		if item := reasoningItem(message.ReasoningContent); item != nil {
			items = append(items, item)
		}
		for _, call := range message.ToolCalls {
			entry := toolCtx.Entry(call.Function.Name)
			item := responseItemFromToolCall(call.ID, entry, call.Function.Arguments, adapter)
			items = append(items, item)
			logToolTranslation(logger, requestID, entry, item["type"].(string))
			logPatchWriteToolCall(requestID, call.ID, entry, call.Function.Arguments, item)
		}
		return items
	}
	items := make([]codex.ResponseItem, 0, 2)
	if item := reasoningItem(message.ReasoningContent); item != nil {
		items = append(items, item)
	}
	items = append(items, codex.ResponseItem{
		"type":    "message",
		"role":    "assistant",
		"content": []map[string]string{{"type": "output_text", "text": messageText(message.Content)}},
	})
	return items
}

type streamState struct {
	toolCtx   tools.Context
	adapter   adapters.Adapter
	requestID string
	logger    *slog.Logger
	textAdded bool
	text      string
	reasoning string
	toolCalls map[int]*streamToolCall
}

type streamToolCall struct {
	id        string
	name      string
	arguments string
	added     bool
	projector *textEditorStreamProjector
}

func newStreamState(toolCtx tools.Context, adapter adapters.Adapter, requestID string, logger *slog.Logger) *streamState {
	return &streamState{
		toolCtx:   toolCtx,
		adapter:   adapter,
		requestID: requestID,
		logger:    logger,
		toolCalls: map[int]*streamToolCall{},
	}
}

func (s *streamState) AddChunk(chunk providers.ChatCompletionChunk) []map[string]any {
	var events []map[string]any
	for _, choice := range chunk.Choices {
		if choice.Delta.ReasoningContent != "" {
			s.reasoning += choice.Delta.ReasoningContent
		}
		if choice.Delta.Content != "" {
			if !s.textAdded {
				s.textAdded = true
				events = append(events, map[string]any{
					"type":         "response.output_item.added",
					"item":         map[string]any{"id": "msg_0", "type": "message", "role": "assistant", "content": []any{}},
					"output_index": 0,
				})
			}
			s.text += choice.Delta.Content
			events = append(events, map[string]any{
				"type":    "response.output_text.delta",
				"item_id": "msg_0",
				"delta":   choice.Delta.Content,
			})
		}
		for _, delta := range choice.Delta.ToolCalls {
			call := s.toolCalls[delta.Index]
			if call == nil {
				call = &streamToolCall{}
				s.toolCalls[delta.Index] = call
			}
			if delta.ID != "" {
				call.id = delta.ID
			}
			if delta.Function.Name != "" {
				call.name = delta.Function.Name
			}
			if !call.added && call.name != "" {
				entry := s.toolCtx.Entry(call.name)
				call.added = true
				if entry.Kind() == tools.KindTextEditor {
					call.projector = newTextEditorStreamProjector(call.id, entry)
				} else {
					events = append(events, map[string]any{
						"type":         "response.output_item.added",
						"item":         inProgressItem(call.id, entry),
						"output_index": 0,
					})
				}
			}
			if delta.Function.Arguments != "" {
				call.arguments += delta.Function.Arguments
				entry := s.toolCtx.Entry(call.name)
				if call.projector != nil {
					events = append(events, call.projector.update(call.arguments, s.adapter)...)
				} else if event := argumentDeltaEvent(call.id, entry, delta.Function.Arguments); event != nil {
					events = append(events, event)
				}
			}
		}
	}
	return events
}

func (s *streamState) Done() []codex.ResponseItem {
	if len(s.toolCalls) > 0 {
		items := make([]codex.ResponseItem, 0, len(s.toolCalls))
		if item := reasoningItem(s.reasoning); item != nil {
			items = append(items, item)
		}
		for i := 0; i < len(s.toolCalls); i++ {
			call, ok := s.toolCalls[i]
			if !ok {
				continue
			}
			entry := s.toolCtx.Entry(call.name)
			item := responseItemFromToolCall(call.id, entry, call.arguments, s.adapter)
			item["id"] = call.id
			if call.projector != nil {
				item["_streamed_text_editor_projector"] = call.projector
			}
			items = append(items, item)
			logToolTranslation(s.logger, s.requestID, entry, item["type"].(string))
			logPatchWriteToolCall(s.requestID, call.id, entry, call.arguments, item)
		}
		return items
	}
	items := make([]codex.ResponseItem, 0, 2)
	if item := reasoningItem(s.reasoning); item != nil {
		items = append(items, item)
	}
	items = append(items, codex.ResponseItem{
		"id":      "msg_0",
		"type":    "message",
		"role":    "assistant",
		"content": []map[string]string{{"type": "output_text", "text": s.text}},
	})
	return items
}

func (s *streamState) ToolCallCount() int {
	return len(s.toolCalls)
}

func responseItemFromToolCall(callID string, entry tools.Entry, arguments string, adapter adapters.Adapter) codex.ResponseItem {
	if rewritten, ok := adapter.ToolPolicy().RewriteBlockedToolCall(entry.Name(), arguments); ok {
		toollog.BlockedToolRewrite(callID, entry, arguments, rewritten)
		arguments = rewritten
	}
	switch entry.Kind() {
	case tools.KindCustom, tools.KindPatch, tools.KindTextEditor:
		input := tools.ExtractCustomToolInput(entry, arguments, adapter)
		if entry.Kind() == tools.KindTextEditor {
			if strings.HasPrefix(strings.TrimSpace(input), "TEXT_EDITOR_") {
				return textEditorLocalResultExecCommandCall(callID, input)
			}
		}
		return codex.ResponseItem{
			"type":    "custom_tool_call",
			"call_id": callID,
			"name":    entry.OriginalName(),
			"input":   input,
			"status":  "completed",
		}
	case tools.KindToolSearch:
		return codex.ResponseItem{
			"type":      "tool_search_call",
			"execution": "client",
			"call_id":   callID,
			"status":    "completed",
			"arguments": tools.ToolSearchArguments(arguments),
		}
	case tools.KindShell:
		return codex.ResponseItem{
			"type":    "shell_call",
			"call_id": callID,
			"action":  shellAction(arguments),
			"status":  "completed",
		}
	default:
		item := codex.ResponseItem{
			"type":      "function_call",
			"call_id":   callID,
			"name":      entry.OriginalName(),
			"arguments": arguments,
			"status":    "completed",
		}
		if entry.Namespace != "" {
			item["namespace"] = entry.Namespace
		}
		return item
	}
}

func textEditorLocalResultExecCommandCall(callID string, input string) codex.ResponseItem {
	arguments, _ := json.Marshal(map[string]string{"cmd": textEditorLocalResultCommand(input)})
	return codex.ResponseItem{
		"type":      "function_call",
		"call_id":   callID,
		"name":      "exec_command",
		"arguments": string(arguments),
		"status":    "completed",
	}
}

type textEditorStreamProjector struct {
	callID string
	entry  tools.Entry
	input  string
	added  bool
	local  bool
}

func newTextEditorStreamProjector(callID string, entry tools.Entry) *textEditorStreamProjector {
	return &textEditorStreamProjector{callID: callID, entry: entry}
}

func (p *textEditorStreamProjector) addedEvent() map[string]any {
	return map[string]any{
		"type":         "response.output_item.added",
		"item":         inProgressTextEditorPatchItem(p.callID, p.entry),
		"output_index": 0,
	}
}

func (p *textEditorStreamProjector) update(arguments string, adapter adapters.Adapter) []map[string]any {
	input, local, ok := p.project(arguments, adapter)
	if !ok {
		return nil
	}
	if local {
		return p.startLocal(input)
	}
	return p.appendPatchInput(input)
}

func (p *textEditorStreamProjector) project(arguments string, adapter adapters.Adapter) (string, bool, bool) {
	if input := tools.ExtractCustomToolInput(p.entry, arguments, adapter); input != "" {
		return input, strings.HasPrefix(strings.TrimSpace(input), "TEXT_EDITOR_"), true
	}
	return p.projectPartial(arguments, adapter)
}

func (p *textEditorStreamProjector) projectPartial(arguments string, adapter adapters.Adapter) (string, bool, bool) {
	fields := parseTextEditorArgumentPrefix(arguments)
	command := normalizeTextEditorStreamCommand(fields.value("command"))
	path := fields.value("path")
	if command == "" || path == "" || !fields.complete("command") || !fields.complete("path") {
		return "", false, false
	}
	switch command {
	case "create":
		if textEditorStreamFileExists(path) {
			return "", false, false
		}
		text, ok := fields.firstValue("file_text", "content", "text", "new_str")
		if !ok {
			return "", false, false
		}
		return projectedPartialTextEditorInput(adapter, map[string]string{
			"command":   command,
			"path":      path,
			"file_text": text,
		})
	case "str_replace":
		oldText := fields.value("old_str")
		if oldText == "" || !fields.complete("old_str") || textEditorStreamFileMissingOldText(path, oldText) {
			return "", false, false
		}
		newText, ok := fields.firstValue("new_str", "text", "content")
		if !ok {
			return "", false, false
		}
		return projectedPartialTextEditorInput(adapter, map[string]string{
			"command": command,
			"path":    path,
			"old_str": oldText,
			"new_str": newText,
		})
	case "insert_after":
		anchor := fields.value("insert_after")
		if anchor == "" {
			anchor = fields.value("old_str")
		}
		if anchor == "" {
			return "", false, false
		}
		text, ok := fields.firstValue("text", "new_str", "content")
		if !ok {
			return "", false, false
		}
		return projectedPartialTextEditorInput(adapter, map[string]string{
			"command":      command,
			"path":         path,
			"insert_after": anchor,
			"text":         text,
		})
	case "delete_file":
		return projectedTextEditorInput(adapter, map[string]string{
			"command": command,
			"path":    path,
		})
	default:
		return "", false, false
	}
}

func (p *textEditorStreamProjector) appendPatchInput(input string) []map[string]any {
	if p.local || !strings.HasPrefix(input, p.input) {
		return nil
	}
	var events []map[string]any
	if !p.added {
		p.added = true
		events = append(events, p.addedEvent())
	}
	delta := strings.TrimPrefix(input, p.input)
	if delta == "" {
		return events
	}
	p.input = input
	return append(events, map[string]any{
		"type":    "response.custom_tool_call_input.delta",
		"item_id": p.callID,
		"call_id": p.callID,
		"delta":   delta,
	})
}

func (p *textEditorStreamProjector) startLocal(input string) []map[string]any {
	p.local = true
	if p.added {
		return nil
	}
	p.added = true
	item := textEditorLocalResultExecCommandCall(p.callID, input)
	item["id"] = p.callID
	item["status"] = "in_progress"
	delete(item, "arguments")
	return []map[string]any{{
		"type":         "response.output_item.added",
		"item":         item,
		"output_index": 0,
	}}
}

func (p *textEditorStreamProjector) doneEvents(item codex.ResponseItem) []map[string]any {
	if !p.added {
		if item["type"] == "custom_tool_call" {
			p.added = true
			return append([]map[string]any{p.addedEvent()}, p.doneEvents(item)...)
		}
		return []map[string]any{{
			"type":         "response.output_item.added",
			"item":         item,
			"output_index": 0,
		}}
	}
	if item["type"] != "custom_tool_call" {
		return nil
	}
	input, _ := item["input"].(string)
	var events []map[string]any
	if strings.HasPrefix(input, p.input) {
		if delta := strings.TrimPrefix(input, p.input); delta != "" {
			events = append(events, map[string]any{
				"type":    "response.custom_tool_call_input.delta",
				"item_id": item["id"],
				"call_id": item["call_id"],
				"delta":   delta,
			})
		}
	}
	events = append(events, map[string]any{
		"type":    "response.custom_tool_call_input.done",
		"item_id": item["id"],
		"call_id": item["call_id"],
		"input":   input,
	})
	return events
}

func textEditorLocalResultCommand(input string) string {
	command := "printf '%s\\n' " + shellSingleQuote(input)
	if path := textEditorLocalResultPath(input); path != "" {
		command += "; printf '%s\\n' '--- current file ---'; sed -n '1,200p' " + shellSingleQuote(path)
	}
	return command + "; exit 0"
}

func textEditorLocalResultPath(input string) string {
	for _, line := range strings.Split(input, "\n") {
		if path, ok := strings.CutPrefix(line, "path: "); ok {
			return strings.TrimSpace(path)
		}
	}
	return ""
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

type textEditorArgumentFields map[string]streamedJSONField

type streamedJSONField struct {
	value    string
	seen     bool
	complete bool
}

func (f textEditorArgumentFields) value(name string) string {
	return f[name].value
}

func (f textEditorArgumentFields) complete(name string) bool {
	field := f[name]
	return field.seen && field.complete
}

func (f textEditorArgumentFields) firstValue(names ...string) (string, bool) {
	for _, name := range names {
		field := f[name]
		if field.seen {
			return field.value, true
		}
	}
	return "", false
}

func parseTextEditorArgumentPrefix(arguments string) textEditorArgumentFields {
	fields := textEditorArgumentFields{}
	i := skipJSONSpace(arguments, 0)
	if i >= len(arguments) || arguments[i] != '{' {
		return fields
	}
	for i++; i < len(arguments); {
		i = skipJSONSpace(arguments, i)
		if i >= len(arguments) || arguments[i] == '}' {
			return fields
		}
		if arguments[i] != '"' {
			i++
			continue
		}
		key, next, complete := scanJSONStringPrefix(arguments, i)
		if !complete {
			return fields
		}
		i = skipJSONSpace(arguments, next)
		if i >= len(arguments) || arguments[i] != ':' {
			return fields
		}
		i = skipJSONSpace(arguments, i+1)
		if i >= len(arguments) {
			return fields
		}
		if arguments[i] == '"' {
			value, valueNext, valueComplete := scanJSONStringPrefix(arguments, i)
			if isTextEditorStreamField(key) {
				fields[key] = streamedJSONField{value: value, seen: true, complete: valueComplete}
			}
			if !valueComplete {
				return fields
			}
			i = valueNext
			continue
		}
		for i < len(arguments) && arguments[i] != ',' && arguments[i] != '}' {
			i++
		}
		if i < len(arguments) && arguments[i] == ',' {
			i++
		}
	}
	return fields
}

func scanJSONStringPrefix(text string, start int) (string, int, bool) {
	var out strings.Builder
	i := start + 1
	for i < len(text) {
		ch := text[i]
		switch ch {
		case '"':
			return out.String(), i + 1, true
		case '\\':
			decoded, next, ok := scanJSONEscapePrefix(text, i+1)
			if !ok {
				return out.String(), len(text), false
			}
			out.WriteString(decoded)
			i = next
		default:
			r, size := rune(ch), 1
			if ch >= utf8.RuneSelf {
				r, size = utf8.DecodeRuneInString(text[i:])
			}
			out.WriteRune(r)
			i += size
		}
	}
	return out.String(), len(text), false
}

func scanJSONEscapePrefix(text string, start int) (string, int, bool) {
	if start >= len(text) {
		return "", len(text), false
	}
	switch text[start] {
	case '"', '\\', '/':
		return string(text[start]), start + 1, true
	case 'b':
		return "\b", start + 1, true
	case 'f':
		return "\f", start + 1, true
	case 'n':
		return "\n", start + 1, true
	case 'r':
		return "\r", start + 1, true
	case 't':
		return "\t", start + 1, true
	case 'u':
		if start+5 > len(text) {
			return "", len(text), false
		}
		r, ok := decodeJSONUnicodeEscape(text[start+1 : start+5])
		if !ok {
			return "", start + 5, true
		}
		return string(r), start + 5, true
	default:
		return string(text[start]), start + 1, true
	}
}

func decodeJSONUnicodeEscape(hexText string) (rune, bool) {
	value, err := strconv.ParseInt(hexText, 16, 32)
	if err != nil {
		return unicode.ReplacementChar, false
	}
	r := rune(value)
	if utf16.IsSurrogate(r) {
		return unicode.ReplacementChar, false
	}
	return r, true
}

func skipJSONSpace(text string, index int) int {
	for index < len(text) {
		switch text[index] {
		case ' ', '\n', '\r', '\t':
			index++
		default:
			return index
		}
	}
	return index
}

func isTextEditorStreamField(name string) bool {
	switch name {
	case "command", "path", "old_str", "new_str", "insert_after", "text", "file_text", "content":
		return true
	default:
		return false
	}
}

func normalizeTextEditorStreamCommand(command string) string {
	switch strings.TrimSpace(strings.ToLower(command)) {
	case "create":
		return "create"
	case "str_replace", "replace":
		return "str_replace"
	case "insert", "insert_after":
		return "insert_after"
	case "delete", "delete_file":
		return "delete_file"
	default:
		return ""
	}
}

func projectedTextEditorInput(adapter adapters.Adapter, values map[string]string) (string, bool, bool) {
	data, _ := json.Marshal(values)
	input, err := tools.TextEditorPatchInput(string(data))
	if err != nil || input == "" {
		return "", false, false
	}
	input = adapter.NormalizePatchInput(input)
	return input, strings.HasPrefix(strings.TrimSpace(input), "TEXT_EDITOR_"), true
}

func projectedPartialTextEditorInput(adapter adapters.Adapter, values map[string]string) (string, bool, bool) {
	input, local, ok := projectedTextEditorInput(adapter, values)
	if !ok || local {
		return input, local, ok
	}
	return strings.TrimSuffix(input, "\n*** End Patch"), false, true
}

func textEditorStreamFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func textEditorStreamFileContains(path string, text string) bool {
	if text == "" {
		return false
	}
	data, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(data), text)
}

func textEditorStreamFileMissingOldText(path string, oldText string) bool {
	if oldText == "" {
		return true
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	data, err := os.ReadFile(path)
	return err == nil && !strings.Contains(string(data), oldText)
}

func isPatchWriteEntry(entry tools.Entry) bool {
	return entry.Kind() == tools.KindPatch || entry.Kind() == tools.KindTextEditor
}

func logPatchWriteToolCall(requestID string, callID string, entry tools.Entry, arguments string, item codex.ResponseItem) {
	if isPatchWriteEntry(entry) {
		toollog.PatchToolCall(requestID, callID, entry, arguments, item)
	}
}

func inProgressItem(callID string, entry tools.Entry) map[string]any {
	item := responseItemFromToolCall(callID, entry, "{}", adapters.Get(adapters.DefaultName))
	item["id"] = callID
	item["status"] = "in_progress"
	delete(item, "input")
	delete(item, "arguments")
	return item
}

func inProgressTextEditorPatchItem(callID string, entry tools.Entry) map[string]any {
	return map[string]any{
		"id":      callID,
		"type":    "custom_tool_call",
		"call_id": callID,
		"name":    entry.OriginalName(),
		"status":  "in_progress",
	}
}

func argumentDeltaEvent(callID string, entry tools.Entry, delta string) map[string]any {
	switch entry.Kind() {
	case tools.KindFunction:
		return map[string]any{
			"type":    "response.function_call_arguments.delta",
			"item_id": callID,
			"call_id": callID,
			"delta":   delta,
		}
	default:
		return nil
	}
}

func toolDoneEvents(item codex.ResponseItem) []map[string]any {
	if projector, _ := item["_streamed_text_editor_projector"].(*textEditorStreamProjector); projector != nil {
		delete(item, "_streamed_text_editor_projector")
		events := projector.doneEvents(item)
		if item["type"] == "function_call" {
			events = append(events, map[string]any{
				"type":      "response.function_call_arguments.done",
				"item_id":   item["id"],
				"call_id":   item["call_id"],
				"arguments": item["arguments"],
			})
		}
		events = append(events, map[string]any{
			"type":         "response.output_item.done",
			"item":         item,
			"output_index": 0,
		})
		return events
	}
	events := []map[string]any{}
	itemType, _ := item["type"].(string)
	switch itemType {
	case "custom_tool_call":
		events = append(events, map[string]any{
			"type":    "response.custom_tool_call_input.delta",
			"item_id": item["id"],
			"call_id": item["call_id"],
			"delta":   item["input"],
		})
		events = append(events, map[string]any{
			"type":    "response.custom_tool_call_input.done",
			"item_id": item["id"],
			"call_id": item["call_id"],
			"input":   item["input"],
		})
	case "function_call":
		events = append(events, map[string]any{
			"type":      "response.function_call_arguments.done",
			"item_id":   item["id"],
			"call_id":   item["call_id"],
			"arguments": item["arguments"],
		})
	}
	events = append(events, map[string]any{
		"type":         "response.output_item.done",
		"item":         item,
		"output_index": 0,
	})
	return events
}

func shellAction(arguments string) map[string]any {
	obj := tools.ShellArguments(arguments)
	if commands, ok := obj["commands"]; ok {
		obj["commands"] = commands
	} else if command, ok := obj["command"]; ok {
		obj["commands"] = []any{command}
		delete(obj, "command")
	}
	return obj
}

func messageText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []map[string]any:
		data, _ := json.Marshal(v)
		return string(data)
	case []any:
		data, _ := json.Marshal(v)
		return string(data)
	default:
		if content == nil {
			return ""
		}
		data, _ := json.Marshal(content)
		return string(data)
	}
}

func reasoningItem(text string) codex.ResponseItem {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return codex.ResponseItem{
		"type":              "reasoning",
		"reasoning_content": text,
	}
}

func logToolTranslation(logger *slog.Logger, requestID string, entry tools.Entry, itemType string) {
	logger.Info("tool_call_translated",
		slog.String("request_id", requestID),
		slog.String("tool", entry.Name()),
		slog.String("kind", entry.Kind()),
		slog.String("input_mode", entry.Descriptor.InputMode),
		slog.String("side_effect", entry.Descriptor.SideEffect),
		slog.String("from", "chat_function_call"),
		slog.String("to", itemType),
	)
}
