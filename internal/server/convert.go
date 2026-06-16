package server

import (
	"encoding/json"
	"log/slog"
	"strings"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
	"codex-bridge/internal/providers"
	"codex-bridge/internal/toollog"
	"codex-bridge/internal/tools"
)

type responseConversionOptions struct {
	patchCooldownFiles []string
}

func responseItemsFromMessage(message providers.ChatMessage, toolCtx tools.Context, adapter adapters.Adapter, requestID string, logger *slog.Logger) []codex.ResponseItem {
	return responseItemsFromMessageWithOptions(message, toolCtx, adapter, requestID, logger, responseConversionOptions{})
}

func responseItemsFromMessageWithOptions(message providers.ChatMessage, toolCtx tools.Context, adapter adapters.Adapter, requestID string, logger *slog.Logger, options responseConversionOptions) []codex.ResponseItem {
	if len(message.ToolCalls) > 0 {
		items := make([]codex.ResponseItem, 0, len(message.ToolCalls))
		if item := reasoningItem(message.ReasoningContent); item != nil {
			items = append(items, item)
		}
		for _, call := range message.ToolCalls {
			entry := toolCtx.Entry(call.Function.Name)
			item := responseItemFromToolCallWithOptions(call.ID, entry, call.Function.Arguments, adapter, options)
			items = append(items, item)
			logToolTranslation(logger, requestID, entry, item["type"].(string))
			logPatchWriteToolCall(requestID, call.ID, entry, call.Function.Arguments, item)
		}
		return collapseBlockedPatchCooldownItems(items)
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
	options   responseConversionOptions
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
	deferred  bool
}

func newStreamState(toolCtx tools.Context, adapter adapters.Adapter, requestID string, logger *slog.Logger) *streamState {
	return newStreamStateWithOptions(toolCtx, adapter, requestID, logger, responseConversionOptions{})
}

func newStreamStateWithOptions(toolCtx tools.Context, adapter adapters.Adapter, requestID string, logger *slog.Logger, options responseConversionOptions) *streamState {
	return &streamState{
		toolCtx:   toolCtx,
		adapter:   adapter,
		requestID: requestID,
		logger:    logger,
		options:   options,
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
				if isPatchWriteEntry(entry) && len(s.options.patchCooldownFiles) > 0 {
					call.deferred = true
				} else {
					call.added = true
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
				if event := argumentDeltaEvent(call.id, entry, delta.Function.Arguments); event != nil {
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
			item := responseItemFromToolCallWithOptions(call.id, entry, call.arguments, s.adapter, s.options)
			item["id"] = call.id
			if call.deferred {
				item["_deferred_added"] = true
			}
			items = append(items, item)
			logToolTranslation(s.logger, s.requestID, entry, item["type"].(string))
			logPatchWriteToolCall(s.requestID, call.id, entry, call.arguments, item)
		}
		return collapseBlockedPatchCooldownItems(items)
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
	return responseItemFromToolCallWithOptions(callID, entry, arguments, adapter, responseConversionOptions{})
}

func responseItemFromToolCallWithOptions(callID string, entry tools.Entry, arguments string, adapter adapters.Adapter, options responseConversionOptions) codex.ResponseItem {
	if rewritten, ok := adapter.ToolPolicy().RewriteBlockedToolCall(entry.Name(), arguments); ok {
		toollog.BlockedToolRewrite(callID, entry, arguments, rewritten)
		arguments = rewritten
	}
	switch entry.Kind() {
	case tools.KindCustom, tools.KindPatch, tools.KindTextEditor:
		input := tools.ExtractCustomToolInput(entry, arguments, adapter)
		if isPatchWriteEntry(entry) && adapters.PatchFilesOverlap(adapters.PatchTouchedFiles(input), options.patchCooldownFiles) {
			return blockedPatchCooldownMessage(options.patchCooldownFiles)
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

func isPatchWriteEntry(entry tools.Entry) bool {
	return entry.Kind() == tools.KindPatch || entry.Kind() == tools.KindTextEditor
}

func logPatchWriteToolCall(requestID string, callID string, entry tools.Entry, arguments string, item codex.ResponseItem) {
	if isPatchWriteEntry(entry) {
		toollog.PatchToolCall(requestID, callID, entry, arguments, item)
	}
}

func blockedPatchCooldownMessage(files []string) codex.ResponseItem {
	return codex.ResponseItem{
		"type": "message",
		"role": "assistant",
		"content": []map[string]string{{
			"type": "output_text",
			"text": "已跳过重复的同文件编辑：上一轮已经成功修改 " + strings.Join(files, ", ") + "。这次没有再次写入，避免把文件改脏；如需确认，请用只读命令查看 `git diff` 或目标片段。",
		}},
	}
}

func collapseBlockedPatchCooldownItems(items []codex.ResponseItem) []codex.ResponseItem {
	if len(items) == 0 {
		return items
	}
	var blocked []codex.ResponseItem
	var kept []codex.ResponseItem
	for _, item := range items {
		if isBlockedPatchCooldownItem(item) {
			blocked = append(blocked, item)
			continue
		}
		kept = append(kept, item)
	}
	if len(blocked) == 0 {
		return items
	}
	if len(kept) > 0 {
		return kept
	}
	return []codex.ResponseItem{blocked[0]}
}

func isBlockedPatchCooldownItem(item codex.ResponseItem) bool {
	if item["type"] != "message" || item["role"] != "assistant" {
		return false
	}
	content, ok := item["content"].([]map[string]string)
	if !ok || len(content) == 0 {
		return false
	}
	return strings.Contains(content[0]["text"], "已跳过重复的同文件编辑")
}

func inProgressItem(callID string, entry tools.Entry) map[string]any {
	item := responseItemFromToolCall(callID, entry, "{}", adapters.Get(adapters.DefaultName))
	item["id"] = callID
	item["status"] = "in_progress"
	delete(item, "input")
	delete(item, "arguments")
	return item
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
