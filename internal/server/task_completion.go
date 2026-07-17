package server

import (
	"errors"
	"fmt"
	"strings"

	"codex-bridge/internal/diagnostics"
	"codex-bridge/internal/providers"
	"codex-bridge/internal/toollog"
	"codex-bridge/internal/tools"
)

const taskProtocolInstruction = `CODEX_BRIDGE_TASK_PROTOCOL
Every assistant turn must call a tool.
You may include concise progress text before normal Codex work tools.
If work remains, call the next normal Codex work tool.
Only when the requested task is fully finished, call codex_bridge_task_end with status=completed and put the exact final user-facing response in result.
If progress is impossible without specific user input or an external state change, call codex_bridge_task_end with status=blocked and explain the exact blocker in result.
Plain assistant text does not finish the task. Do not call codex_bridge_task_end together with another tool.`

const taskProtocolRetryInstruction = `CODEX_BRIDGE_TASK_PROTOCOL_RETRY
The previous assistant text was already delivered to the user as progress. Do not repeat it.
Call exactly one or more normal work tools if work remains, or call codex_bridge_task_end alone if the task is completed or genuinely blocked.
When ending, copy the previous assistant text verbatim into result.
Do not answer with plain assistant text.`

const maxTaskProtocolRetries = 1
const taskProtocolPublicToolName = "the task completion tool"

var errTaskProtocolMissingCall = errors.New("assistant returned text without a required tool call")
var errTaskProtocolViolation = errors.New("model did not follow the required task completion protocol")

func taskProtocolViolation(cause error) error {
	return fmt.Errorf("%w: %v", errTaskProtocolViolation, cause)
}

func responseFailureType(err error) string {
	if errors.Is(err, errTaskProtocolViolation) {
		return "model_behavior_error"
	}
	return "server_error"
}

func projectedTaskEndResult(response map[string]any, toolCtx tools.Context) (string, string, bool, error) {
	output, _ := response["output"].([]any)
	var taskEnd map[string]any
	otherCalls := 0
	for _, rawItem := range output {
		item, _ := rawItem.(map[string]any)
		if item["type"] != "function_call" {
			continue
		}
		name, _ := item["name"].(string)
		if toolCtx.Entry(name).Kind() != tools.KindTaskEnd {
			otherCalls++
			continue
		}
		if taskEnd != nil {
			return "", "", false, errors.New("multiple task termination calls in one response")
		}
		taskEnd = item
	}
	if taskEnd == nil {
		if otherCalls > 0 {
			return "", "", false, nil
		}
		return "", "", false, errTaskProtocolMissingCall
	}
	if otherCalls > 0 {
		return "", "", false, nil
	}
	status, result, err := tools.ParseTaskEndArguments(responseFunctionCallArguments(taskEnd))
	if err != nil {
		return "", "", false, fmt.Errorf("invalid task termination call: %w", err)
	}
	return status, result, true, nil
}

func projectedTaskProtocolRetryRequest(req map[string]any, response map[string]any, supportsRequired bool) map[string]any {
	input, _ := req["input"].([]any)
	nextInput := append([]any(nil), input...)
	output, _ := response["output"].([]any)
	for _, rawItem := range output {
		item, _ := rawItem.(map[string]any)
		if item["type"] == "message" {
			nextInput = append(nextInput, cloneResponseRequest(item))
		}
	}
	nextInput = append(nextInput, map[string]any{
		"type":    "message",
		"role":    "system",
		"content": taskProtocolRetryInstruction,
	})

	followUp := cloneResponseRequest(req)
	followUp["input"] = nextInput
	followUp["tool_choice"] = "auto"
	if supportsRequired {
		followUp["tool_choice"] = "required"
	}
	followUp["parallel_tool_calls"] = false
	return followUp
}

func projectedTaskEndResponse(response map[string]any, result string) map[string]any {
	final := cloneResponseRequest(response)
	final["status"] = "completed"
	final["output"] = []any{map[string]any{
		"id":     "msg_task_end",
		"type":   "message",
		"role":   "assistant",
		"status": "completed",
		"content": []any{map[string]any{
			"type": "output_text",
			"text": sanitizeTaskProtocolText(result),
		}},
	}}
	return final
}

func projectedWithoutTaskEndCalls(response map[string]any, toolCtx tools.Context) map[string]any {
	projected := cloneResponseRequest(response)
	output, _ := projected["output"].([]any)
	filtered := make([]any, 0, len(output))
	for _, rawItem := range output {
		item, _ := rawItem.(map[string]any)
		name, _ := item["name"].(string)
		if item["type"] == "function_call" && toolCtx.Entry(name).Kind() == tools.KindTaskEnd {
			continue
		}
		if item["type"] == "reasoning" || item["type"] == "message" {
			filtered = append(filtered, sanitizeTaskProtocolValue(item))
			continue
		}
		filtered = append(filtered, rawItem)
	}
	projected["output"] = filtered
	return projected
}

func projectedResponseAssistantText(response map[string]any) string {
	output, _ := response["output"].([]any)
	parts := make([]string, 0, len(output))
	for _, rawItem := range output {
		item, _ := rawItem.(map[string]any)
		if item["type"] != "message" {
			continue
		}
		if text := strings.TrimSpace(messageOutputText(item)); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func chatTaskEndResult(message providers.ChatMessage, toolCtx tools.Context) (string, string, bool, error) {
	var taskEnd *providers.ChatToolCall
	otherCalls := 0
	for index := range message.ToolCalls {
		call := &message.ToolCalls[index]
		if toolCtx.Entry(call.Function.Name).Kind() != tools.KindTaskEnd {
			otherCalls++
			continue
		}
		if taskEnd != nil {
			return "", "", false, errors.New("multiple task termination calls in one response")
		}
		taskEnd = call
	}
	if taskEnd == nil {
		if otherCalls > 0 {
			return "", "", false, nil
		}
		return "", "", false, errTaskProtocolMissingCall
	}
	if otherCalls > 0 {
		return "", "", false, nil
	}
	status, result, err := tools.ParseTaskEndArguments(taskEnd.Function.Arguments)
	if err != nil {
		return "", "", false, fmt.Errorf("invalid task termination call: %w", err)
	}
	return status, result, true, nil
}

func chatWithoutTaskEndCalls(message providers.ChatMessage, toolCtx tools.Context) providers.ChatMessage {
	filtered := make([]providers.ChatToolCall, 0, len(message.ToolCalls))
	for _, call := range message.ToolCalls {
		if toolCtx.Entry(call.Function.Name).Kind() == tools.KindTaskEnd {
			continue
		}
		filtered = append(filtered, call)
	}
	message.ToolCalls = filtered
	message.Content = sanitizeTaskProtocolValue(message.Content)
	message.ReasoningContent = sanitizeTaskProtocolText(message.ReasoningContent)
	return message
}

func sanitizeTaskProtocolEvent(event map[string]any) map[string]any {
	eventType, _ := event["type"].(string)
	switch {
	case strings.Contains(eventType, "reasoning"),
		strings.Contains(eventType, "output_text"),
		strings.Contains(eventType, "content_part"):
		return sanitizeTaskProtocolValue(event).(map[string]any)
	case eventType == "response.output_item.added", eventType == "response.output_item.done":
		out := cloneResponseRequest(event)
		item, _ := event["item"].(map[string]any)
		if item["type"] == "reasoning" || item["type"] == "message" {
			out["item"] = sanitizeTaskProtocolValue(item)
		}
		return out
	default:
		return cloneResponseRequest(event)
	}
}

func sanitizeTaskProtocolItem(item map[string]any) map[string]any {
	if item["type"] != "reasoning" && item["type"] != "message" {
		return cloneResponseRequest(item)
	}
	return sanitizeTaskProtocolValue(item).(map[string]any)
}

func sanitizeTaskProtocolValue(value any) any {
	switch current := value.(type) {
	case string:
		return sanitizeTaskProtocolText(current)
	case map[string]any:
		out := make(map[string]any, len(current))
		for key, item := range current {
			out[key] = sanitizeTaskProtocolValue(item)
		}
		return out
	case map[string]string:
		out := make(map[string]string, len(current))
		for key, item := range current {
			out[key] = sanitizeTaskProtocolText(item)
		}
		return out
	case []any:
		out := make([]any, len(current))
		for index, item := range current {
			out[index] = sanitizeTaskProtocolValue(item)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(current))
		for index, item := range current {
			out[index] = sanitizeTaskProtocolValue(item).(map[string]any)
		}
		return out
	case []map[string]string:
		out := make([]map[string]string, len(current))
		for index, item := range current {
			out[index] = sanitizeTaskProtocolValue(item).(map[string]string)
		}
		return out
	default:
		return value
	}
}

func sanitizeTaskProtocolText(text string) string {
	return strings.ReplaceAll(text, tools.TaskEndToolName, taskProtocolPublicToolName)
}

func taskEndResultToEmit(result string, visibleTexts ...string) string {
	sanitized := sanitizeTaskProtocolText(result)
	for _, visible := range visibleTexts {
		if strings.TrimSpace(sanitizeTaskProtocolText(visible)) == strings.TrimSpace(sanitized) {
			return ""
		}
	}
	return sanitized
}

func chatTaskProtocolRetryRequest(req providers.ChatCompletionRequest, message providers.ChatMessage, supportsRequired bool, adapterPrepare func(providers.ChatCompletionRequest) providers.ChatCompletionRequest) providers.ChatCompletionRequest {
	followUp := req
	if text := strings.TrimSpace(messageText(message.Content)); text != "" {
		followUp.Messages = append(followUp.Messages, providers.ChatMessage{Role: "assistant", Content: text})
	}
	followUp.Messages = append(followUp.Messages, providers.ChatMessage{Role: "system", Content: taskProtocolRetryInstruction})
	followUp.ToolChoice = "auto"
	if supportsRequired {
		followUp.ToolChoice = "required"
	}
	disabled := false
	followUp.ParallelToolCalls = &disabled
	return adapterPrepare(followUp)
}

func (s *Server) writeTaskProtocolRetry(sessionID string, requestID string, model string, upstreamModel string, profile string, detail string, text string, stream bool) {
	toollog.WriteRecovery(sessionID, map[string]any{
		"event":          "task_protocol_retry",
		"request_id":     requestID,
		"model":          model,
		"upstream_model": upstreamModel,
		"profile":        profile,
		"detail":         detail,
		"stream":         stream,
		"message":        diagnostics.TextSummary(text, 320),
	})
}

func (s *Server) writeTaskProtocolFailure(sessionID string, requestID string, model string, upstreamModel string, profile string, detail string, text string, stream bool) {
	toollog.WriteRecovery(sessionID, map[string]any{
		"event":          "task_protocol_failure",
		"request_id":     requestID,
		"model":          model,
		"upstream_model": upstreamModel,
		"profile":        profile,
		"detail":         detail,
		"stream":         stream,
		"message":        diagnostics.TextSummary(text, 320),
	})
}
