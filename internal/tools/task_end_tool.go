package tools

import (
	"encoding/json"
	"errors"
	"strings"

	"codex-bridge/internal/providers"
)

const TaskEndToolName = "codex_bridge_task_end"

var taskEndParameters = json.RawMessage(`{"type":"object","properties":{"status":{"type":"string","enum":["completed","blocked"],"description":"Use completed only when the requested task is finished. Use blocked only when specific user input or an external condition is required."},"result":{"type":"string","description":"The exact final response to deliver to the user, or the exact blocker that requires user action."}},"required":["status","result"],"additionalProperties":false}`)

func AddTaskEndTool(chatTools []providers.ChatTool, ctx *Context) []providers.ChatTool {
	if ctx.Tools == nil {
		ctx.Tools = map[string]Entry{}
	}
	if _, exists := ctx.Tools[TaskEndToolName]; exists {
		return chatTools
	}
	entry := newEntry(
		TaskEndToolName,
		KindTaskEnd,
		InputModeJSON,
		SideEffectStatus,
		"function",
		"End the current Codex task. If work remains, call the next normal task tool instead. Use status=completed only after the requested work and required verification are finished. Use status=blocked only when specific user input or an external condition is required. Plain assistant text does not end the task.",
		nil,
	)
	ctx.Tools[TaskEndToolName] = entry
	return append(chatTools, chatFunction(entry, taskEndParameters).tool)
}

func ParseTaskEndArguments(arguments string) (string, string, error) {
	var payload struct {
		Status string `json:"status"`
		Result string `json:"result"`
	}
	if err := json.Unmarshal([]byte(arguments), &payload); err != nil {
		return "", "", err
	}
	payload.Status = strings.TrimSpace(payload.Status)
	payload.Result = strings.TrimSpace(payload.Result)
	if payload.Status != "completed" && payload.Status != "blocked" {
		return "", "", errors.New("status must be completed or blocked")
	}
	if payload.Result == "" {
		return "", "", errors.New("result must not be empty")
	}
	return payload.Status, payload.Result, nil
}
