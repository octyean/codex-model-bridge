package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
	"codex-bridge/internal/providers"
	"codex-bridge/internal/toollog"
	"codex-bridge/internal/toolruntime"
	"codex-bridge/internal/tools"
)

type toolCallLocalResolver func(ctx context.Context, callID string, entry tools.Entry, modelArguments string, canonicalArguments string, runtimeArguments string) (codex.ResponseItem, bool)

func responseItemsFromMessage(ctx context.Context, message providers.ChatMessage, toolCtx tools.Context, adapter adapters.Adapter, requestID string, model string, profile string, logger *slog.Logger, localResolver toolCallLocalResolver) []codex.ResponseItem {
	if len(message.ToolCalls) > 0 {
		items := make([]codex.ResponseItem, 0, len(message.ToolCalls)+2)
		if item := reasoningItem(message.ReasoningContent); item != nil {
			items = append(items, item)
		}
		if strings.TrimSpace(messageText(message.Content)) != "" {
			items = append(items, assistantMessageItem(message.Content))
		}
		for _, call := range message.ToolCalls {
			entry := toolCtx.Entry(call.Function.Name)
			toollog.ToolCall(requestID, model, profile, call.ID, entry, call.Function.Arguments, message.ReasoningContent)
			item := responseItemFromToolCall(ctx, call.ID, entry, call.Function.Arguments, toolCtx, adapter, requestID, model, profile, logger, localResolver)
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
	items = append(items, assistantMessageItem(message.Content))
	return items
}

func assistantMessageItem(content any) codex.ResponseItem {
	return codex.ResponseItem{
		"type":    "message",
		"role":    "assistant",
		"content": []map[string]string{{"type": "output_text", "text": messageText(content)}},
	}
}

type streamState struct {
	toolCtx         tools.Context
	adapter         adapters.Adapter
	requestID       string
	model           string
	profile         string
	logger          *slog.Logger
	requestCtx      context.Context
	localResolver   toolCallLocalResolver
	textItemID      string
	textAdded       bool
	textIndex       int
	text            string
	reasoningItemID string
	reasoning       string
	reasoningAdded  bool
	reasoningIndex  int
	toolCalls       map[int]*streamToolCall
	nextOutputIndex int
	eventsEmitted   bool
}

type streamToolCall struct {
	id          string
	name        string
	arguments   string
	outputIndex int
}

func newStreamState(ctx context.Context, toolCtx tools.Context, adapter adapters.Adapter, requestID string, model string, profile string, logger *slog.Logger, localResolver toolCallLocalResolver) *streamState {
	if ctx == nil {
		ctx = context.Background()
	}
	return &streamState{
		toolCtx:         toolCtx,
		adapter:         adapter,
		requestID:       requestID,
		model:           model,
		profile:         profile,
		logger:          logger,
		requestCtx:      ctx,
		localResolver:   localResolver,
		textItemID:      "msg_" + requestID,
		reasoningItemID: "rs_" + requestID,
		textIndex:       -1,
		reasoningIndex:  -1,
		toolCalls:       map[int]*streamToolCall{},
	}
}

func (s *streamState) AddChunk(chunk providers.ChatCompletionChunk) []map[string]any {
	var events []map[string]any
	for _, choice := range chunk.Choices {
		reasoningDelta := choice.Delta.ReasoningContent
		contentDelta := choice.Delta.Content
		if s.toolCtx.Has(tools.TaskEndToolName) {
			reasoningDelta = sanitizeTaskProtocolText(reasoningDelta)
			contentDelta = sanitizeTaskProtocolText(contentDelta)
		}
		if reasoningDelta != "" {
			if !s.reasoningAdded {
				s.reasoningAdded = true
				s.reasoningIndex = s.nextOutputIndex
				s.nextOutputIndex++
				events = append(events, map[string]any{
					"type":         "response.output_item.added",
					"item":         map[string]any{"id": s.reasoningItemID, "type": "reasoning", "status": "in_progress"},
					"output_index": s.reasoningIndex,
				})
			}
			s.reasoning += reasoningDelta
		}
		if contentDelta != "" {
			if !s.textAdded {
				s.textAdded = true
				s.textIndex = s.nextOutputIndex
				s.nextOutputIndex++
				events = append(events, map[string]any{
					"type":         "response.output_item.added",
					"item":         map[string]any{"id": s.textItemID, "type": "message", "role": "assistant", "content": []any{}},
					"output_index": s.textIndex,
				})
				events = append(events, contentPartAddedEvent(s.textItemID, s.textIndex))
			}
			s.text += contentDelta
			events = append(events, map[string]any{
				"type":          "response.output_text.delta",
				"item_id":       s.textItemID,
				"output_index":  s.textIndex,
				"content_index": 0,
				"delta":         contentDelta,
			})
		}
		for _, delta := range choice.Delta.ToolCalls {
			call := s.toolCalls[delta.Index]
			if call == nil {
				call = &streamToolCall{outputIndex: -1}
				s.toolCalls[delta.Index] = call
			}
			if delta.ID != "" {
				call.id = delta.ID
			}
			if delta.Function.Name != "" {
				call.name = delta.Function.Name
				if call.outputIndex < 0 {
					call.outputIndex = s.nextOutputIndex
					s.nextOutputIndex++
				}
			}
			if delta.Function.Arguments != "" {
				call.arguments += delta.Function.Arguments
			}
		}
	}
	return events
}

func (s *streamState) Done() []codex.ResponseItem {
	var items []indexedResponseItem
	if len(s.toolCalls) > 0 {
		if item := reasoningItem(s.reasoning); item != nil {
			if s.reasoningAdded {
				item["id"] = s.reasoningItemID
			}
			items = append(items, indexedResponseItem{index: s.itemIndex(s.reasoningIndex), item: item})
		}
		if s.textAdded || strings.TrimSpace(s.text) != "" {
			items = append(items, indexedResponseItem{index: s.itemIndex(s.textIndex), item: codex.ResponseItem{
				"id":      s.textItemID,
				"type":    "message",
				"role":    "assistant",
				"content": []map[string]string{{"type": "output_text", "text": s.text}},
			}})
		}
		for i := 0; i < len(s.toolCalls); i++ {
			call, ok := s.toolCalls[i]
			if !ok {
				continue
			}
			entry := s.toolCtx.Entry(call.name)
			toollog.ToolCall(s.requestID, s.model, s.profile, call.id, entry, call.arguments, s.reasoning)
			item := responseItemFromToolCall(s.requestCtx, call.id, entry, call.arguments, s.toolCtx, s.adapter, s.requestID, s.model, s.profile, s.logger, s.localResolver)
			items = append(items, indexedResponseItem{index: s.itemIndex(call.outputIndex), item: item})
			logToolTranslation(s.logger, s.requestID, entry, item["type"].(string))
			logPatchWriteToolCall(s.requestID, call.id, entry, call.arguments, item)
		}
		return sortedResponseItems(items)
	}
	if item := reasoningItem(s.reasoning); item != nil {
		if s.reasoningAdded {
			item["id"] = s.reasoningItemID
		}
		items = append(items, indexedResponseItem{index: s.itemIndex(s.reasoningIndex), item: item})
	}
	if s.textAdded || s.text != "" {
		items = append(items, indexedResponseItem{index: s.itemIndex(s.textIndex), item: codex.ResponseItem{
			"id":      s.textItemID,
			"type":    "message",
			"role":    "assistant",
			"content": []map[string]string{{"type": "output_text", "text": s.text}},
		}})
	}
	return sortedResponseItems(items)
}

type indexedResponseItem struct {
	index int
	item  codex.ResponseItem
}

func (s *streamState) itemIndex(index int) int {
	if index >= 0 {
		return index
	}
	out := s.nextOutputIndex
	s.nextOutputIndex++
	return out
}

func sortedResponseItems(items []indexedResponseItem) []codex.ResponseItem {
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].index < items[j].index
	})
	out := make([]codex.ResponseItem, 0, len(items))
	for _, item := range items {
		out = append(out, item.item)
	}
	return out
}

func (s *streamState) ToolCallCount() int {
	return len(s.toolCalls)
}

func responseItemFromToolCall(ctx context.Context, callID string, entry tools.Entry, arguments string, toolCtx tools.Context, adapter adapters.Adapter, requestID string, model string, profile string, logger *slog.Logger, localResolver toolCallLocalResolver) codex.ResponseItem {
	modelArguments := arguments
	canonicalArguments := tools.CanonicalArguments(entry, modelArguments)
	runtimeArguments := tools.RuntimeArguments(entry, canonicalArguments)
	customInput := ""
	if entry.Kind() == tools.KindCustom || entry.Kind() == tools.KindPatch || entry.Kind() == tools.KindTextEditor {
		customInput = tools.ExtractCustomToolInputWithWorkspace(entry, canonicalArguments, adapter, toolCtx.Workspace)
		if strings.TrimSpace(customInput) != "" {
			runtimeArguments = customInput
		}
	}
	toollog.ToolCallFrame(requestID, model, profile, callID, entry, modelArguments, canonicalArguments, runtimeArguments)
	if decision := toolruntime.Decide(toolruntime.CallContext{
		RequestID:          requestID,
		Model:              model,
		Profile:            profile,
		CallID:             callID,
		Tool:               runtimeToolInfo(entry, canonicalArguments),
		CanReturnLocalText: false,
	}); decision.ShouldRecord {
		toollog.BrokerDecision(requestID, model, profile, callID, entry, canonicalArguments, decision)
		if decision.Action == toolruntime.DecisionStop {
			return localToolResultMessageItem(callID, decision.LocalText)
		}
	}
	if localResolver != nil {
		if item, ok := localResolver(ctx, callID, entry, modelArguments, canonicalArguments, runtimeArguments); ok {
			return item
		}
	}
	switch entry.Kind() {
	case tools.KindCustom, tools.KindPatch, tools.KindTextEditor:
		input := customInput
		if entry.Kind() == tools.KindTextEditor {
			if strings.TrimSpace(input) == "" {
				return localToolResultMessageItem(callID, textEditorInvalidArgumentsResult())
			}
			if strings.HasPrefix(strings.TrimSpace(input), "TEXT_EDITOR_") {
				return localToolResultMessageItem(callID, input)
			}
		}
		return codex.ResponseItem{
			"id":      toolItemID("custom_tool_call", callID),
			"type":    "custom_tool_call",
			"call_id": callID,
			"name":    entry.OriginalName(),
			"input":   input,
			"status":  "completed",
		}
	case tools.KindToolSearch:
		return codex.ResponseItem{
			"id":        toolItemID("tool_search_call", callID),
			"type":      "tool_search_call",
			"execution": "client",
			"call_id":   callID,
			"status":    "completed",
			"arguments": tools.ToolSearchArguments(canonicalArguments),
		}
	case tools.KindMCPResource:
		name, nativeArguments := tools.MCPResourceCallForTool(entry.Name(), canonicalArguments, toolCtx)
		if name != "read_mcp_resource" && name != "list_mcp_resources" && name != "list_mcp_resource_templates" {
			toollog.ToolCallRerouted(requestID, model, profile, callID, entry, canonicalArguments, name, nativeArguments, "local_context_resource")
		}
		return codex.ResponseItem{
			"id":        toolItemID("function_call", callID),
			"type":      "function_call",
			"call_id":   callID,
			"name":      name,
			"arguments": nativeArguments,
			"status":    "completed",
		}
	case tools.KindReadFile:
		command := tools.ReadFileCommand(canonicalArguments, toolCtx)
		toollog.ToolCallRerouted(requestID, model, profile, callID, entry, canonicalArguments, "exec_command", execCommandArguments(command), "native_command_execution")
		return execCommandItem(callID, command)
	case tools.KindListFiles:
		command := tools.ListFilesCommand(canonicalArguments, toolCtx)
		toollog.ToolCallRerouted(requestID, model, profile, callID, entry, canonicalArguments, "exec_command", execCommandArguments(command), "native_command_execution")
		return execCommandItem(callID, command)
	case tools.KindFileSearch:
		command := tools.FileSearchCommand(canonicalArguments, toolCtx)
		toollog.ToolCallRerouted(requestID, model, profile, callID, entry, canonicalArguments, "exec_command", execCommandArguments(command), "native_command_execution")
		return execCommandItem(callID, command)
	case tools.KindShell:
		return codex.ResponseItem{
			"id":      toolItemID("shell_call", callID),
			"type":    "shell_call",
			"call_id": callID,
			"action":  shellAction(canonicalArguments),
			"status":  "completed",
		}
	default:
		item := codex.ResponseItem{
			"id":        toolItemID("function_call", callID),
			"type":      "function_call",
			"call_id":   callID,
			"name":      entry.OriginalName(),
			"arguments": runtimeArguments,
			"status":    "completed",
		}
		if entry.Namespace != "" {
			item["namespace"] = entry.Namespace
		}
		return item
	}
}

func execCommandItem(callID string, command tools.ExecCommand) codex.ResponseItem {
	return codex.ResponseItem{
		"id":        toolItemID("function_call", callID),
		"type":      "function_call",
		"call_id":   callID,
		"name":      "exec_command",
		"arguments": execCommandArguments(command),
		"status":    "completed",
	}
}

func execCommandArguments(command tools.ExecCommand) string {
	args := map[string]any{
		"cmd":               command.Cmd,
		"yield_time_ms":     1000,
		"max_output_tokens": command.MaxOutputTokens,
	}
	if command.Workdir != "" {
		args["workdir"] = command.Workdir
	}
	data, _ := json.Marshal(args)
	return string(data)
}

func localToolResultMessageItem(callID string, input string) codex.ResponseItem {
	item := codex.ResponseItem{
		"id":   "msg_" + strings.TrimPrefix(callID, "call_"),
		"type": "message",
		"role": "assistant",
		"content": []map[string]string{{
			"type": "output_text",
			"text": input,
		}},
	}
	return item
}

func runtimeToolInfo(entry tools.Entry, arguments string) toolruntime.ToolInfo {
	return toolruntime.ToolInfo{
		Name:         entry.Name(),
		Kind:         entry.Kind(),
		OriginalType: entry.OriginalType(),
		Description:  entry.Descriptor.Description,
		SideEffect:   entry.Descriptor.SideEffect,
		Arguments:    arguments,
	}
}

func toolItemID(itemType string, callID string) string {
	if callID == "" {
		callID = "unknown"
	}
	prefix := "fc"
	switch itemType {
	case "custom_tool_call":
		prefix = "ctc"
	case "tool_search_call":
		prefix = "tsc"
	case "shell_call", "local_shell_call":
		prefix = "sc"
	case "function_call":
		prefix = "fc"
	}
	return prefix + "_" + strings.TrimPrefix(callID, prefix+"_")
}

func textEditorInvalidArgumentsResult() string {
	return tools.TextEditorInvalidArgumentsResult("")
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func isPatchWriteEntry(entry tools.Entry, arguments string) bool {
	return entry.Kind() == tools.KindPatch || entry.Kind() == tools.KindTextEditor
}

func logPatchWriteToolCall(requestID string, callID string, entry tools.Entry, arguments string, item codex.ResponseItem) {
	if isPatchWriteEntry(entry, arguments) {
		toollog.PatchToolCall(requestID, callID, entry, arguments, item)
	}
}

func outputDoneEvents(item codex.ResponseItem, outputIndex int, alreadyAdded bool) []map[string]any {
	events := []map[string]any{}
	if !alreadyAdded {
		events = append(events, map[string]any{
			"type":         "response.output_item.added",
			"item":         inProgressOutputItem(item),
			"output_index": outputIndex,
		})
	}
	itemType, _ := item["type"].(string)
	switch itemType {
	case "message":
		if !alreadyAdded {
			itemID, _ := item["id"].(string)
			events = append(events, contentPartAddedEvent(itemID, outputIndex))
		}
		text := messageOutputText(item)
		events = append(events, map[string]any{
			"type":          "response.output_text.done",
			"item_id":       item["id"],
			"output_index":  outputIndex,
			"content_index": 0,
			"text":          text,
		})
		events = append(events, map[string]any{
			"type":          "response.content_part.done",
			"item_id":       item["id"],
			"output_index":  outputIndex,
			"content_index": 0,
			"part":          outputTextPart(text),
		})
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
		"output_index": outputIndex,
	})
	return events
}

func contentPartAddedEvent(itemID string, outputIndex int) map[string]any {
	return map[string]any{
		"type":          "response.content_part.added",
		"item_id":       itemID,
		"output_index":  outputIndex,
		"content_index": 0,
		"part":          outputTextPart(""),
	}
}

func outputTextPart(text string) map[string]any {
	return map[string]any{"type": "output_text", "text": text, "annotations": []any{}}
}

func ensureStreamItemIDs(items []codex.ResponseItem, requestID string) {
	for i, item := range items {
		if _, ok := item["id"]; ok {
			continue
		}
		itemType, _ := item["type"].(string)
		switch itemType {
		case "message":
			item["id"] = "msg_" + requestID + "_" + strconv.Itoa(i)
		case "reasoning":
			item["id"] = "rs_" + requestID + "_" + strconv.Itoa(i)
		}
	}
}

func messageOutputText(item codex.ResponseItem) string {
	switch content := item["content"].(type) {
	case []map[string]string:
		var text strings.Builder
		for _, part := range content {
			text.WriteString(part["text"])
		}
		return text.String()
	case []any:
		var text strings.Builder
		for _, rawPart := range content {
			part, _ := rawPart.(map[string]any)
			value, _ := part["text"].(string)
			text.WriteString(value)
		}
		return text.String()
	default:
		return ""
	}
}

func inProgressOutputItem(item codex.ResponseItem) codex.ResponseItem {
	out := make(codex.ResponseItem, len(item)+1)
	for key, value := range item {
		out[key] = value
	}
	out["status"] = "in_progress"
	delete(out, "input")
	delete(out, "arguments")
	return out
}

func shellAction(arguments string) map[string]any {
	obj := tools.ShellArguments(arguments)
	if commands, ok := obj["commands"]; ok {
		obj["commands"] = commands
	} else if command, ok := obj["command"]; ok {
		obj["commands"] = shellCommands(command)
		delete(obj, "command")
	} else if command, ok := obj["cmd"]; ok {
		obj["commands"] = shellCommands(command)
		delete(obj, "cmd")
	}
	return obj
}

func shellCommands(command any) any {
	switch v := command.(type) {
	case []any, []string:
		return v
	default:
		return []any{command}
	}
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
