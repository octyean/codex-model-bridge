package transcript

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/capabilities"
	"codex-bridge/internal/codex"
	"codex-bridge/internal/providers"
	"codex-bridge/internal/tools"
)

type Result struct {
	Messages []providers.ChatMessage
	Items    []map[string]any
}

func ToChatMessages(req codex.ResponsesRequest, adapter adapters.Adapter) (Result, error) {
	return ToChatMessagesWithRuntime(context.Background(), req, adapter, capabilities.Runtime{})
}

func ToChatMessagesWithRuntime(ctx context.Context, req codex.ResponsesRequest, adapter adapters.Adapter, runtime capabilities.Runtime) (Result, error) {
	var messages []providers.ChatMessage
	if strings.TrimSpace(req.Instructions) != "" {
		messages = append(messages, providers.ChatMessage{
			Role:    "system",
			Content: req.Instructions,
		})
	}
	if note := tools.UnsupportedToolNote(req.Tools, runtime.HasSearch()); note != "" {
		messages = append(messages, providers.ChatMessage{Role: "system", Content: note})
	}

	items, err := parseInputItems(req.Input)
	if err != nil {
		return Result{}, err
	}
	var pendingToolCalls []providers.ChatToolCall
	for _, item := range items {
		itemType, _ := item["type"].(string)
		switch itemType {
		case "message":
			if len(pendingToolCalls) > 0 {
				messages = append(messages, providers.ChatMessage{Role: "assistant", ToolCalls: pendingToolCalls})
				pendingToolCalls = nil
			}
			role, _ := item["role"].(string)
			if role == "" {
				role = "user"
			}
			messages = append(messages, providers.ChatMessage{
				Role:    normalizeRole(role),
				Content: contentParts(ctx, item["content"], adapters.HasImageInput(adapter.Capabilities()), runtime),
			})
		case "function_call":
			pendingToolCalls = append(pendingToolCalls, functionToolCall(item))
		case "custom_tool_call":
			pendingToolCalls = append(pendingToolCalls, customToolCall(item, adapter))
		case "apply_patch_call":
			pendingToolCalls = append(pendingToolCalls, applyPatchToolCall(item, adapter))
		case "tool_search_call":
			pendingToolCalls = append(pendingToolCalls, toolSearchCall(item))
		case "shell_call", "local_shell_call":
			pendingToolCalls = append(pendingToolCalls, shellToolCall(item))
		case "function_call_output", "custom_tool_call_output", "apply_patch_call_output", "tool_search_output", "shell_call_output", "local_shell_call_output":
			if len(pendingToolCalls) > 0 {
				messages = append(messages, providers.ChatMessage{Role: "assistant", ToolCalls: pendingToolCalls})
				pendingToolCalls = nil
			}
			callID, _ := item["call_id"].(string)
			messages = append(messages, providers.ChatMessage{
				Role:       "tool",
				ToolCallID: callID,
				Content:    outputText(item),
			})
		case "additional_tools", "reasoning":
			continue
		}
	}
	if len(pendingToolCalls) > 0 {
		messages = append(messages, providers.ChatMessage{Role: "assistant", ToolCalls: pendingToolCalls})
	}
	if len(messages) == 0 {
		return Result{}, fmt.Errorf("responses input did not contain messages")
	}
	return Result{Messages: messages, Items: items}, nil
}

func parseInputItems(input json.RawMessage) ([]map[string]any, error) {
	if len(input) == 0 || string(input) == "null" {
		return nil, nil
	}
	var items []map[string]any
	if err := json.Unmarshal(input, &items); err == nil {
		return items, nil
	}
	var text string
	if err := json.Unmarshal(input, &text); err == nil {
		return []map[string]any{{
			"type":    "message",
			"role":    "user",
			"content": []any{map[string]any{"type": "input_text", "text": text}},
		}}, nil
	}
	return nil, fmt.Errorf("unsupported responses input shape")
}

func normalizeRole(role string) string {
	switch role {
	case "developer":
		return "system"
	case "assistant", "system", "user", "tool":
		return role
	default:
		return "user"
	}
}

func contentParts(ctx context.Context, value any, allowImage bool, runtime capabilities.Runtime) any {
	switch v := value.(type) {
	case string:
		return v
	case []any:
		if allowImage {
			parts := chatContentParts(v)
			if len(parts) > 0 {
				return parts
			}
		}
		return flattenedContent(ctx, v, runtime)
	default:
		return ""
	}
}

func chatContentParts(items []any) []map[string]any {
	parts := make([]map[string]any, 0, len(items))
	hasText := false
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		switch obj["type"] {
		case "input_text", "output_text", "text":
			if text, ok := obj["text"].(string); ok {
				hasText = hasText || strings.TrimSpace(text) != ""
				parts = append(parts, map[string]any{"type": "text", "text": text})
			}
		case "input_image", "image_url":
			image := map[string]any{}
			if url, ok := obj["image_url"].(string); ok {
				image["url"] = url
			} else if imageURL, ok := obj["image_url"].(map[string]any); ok {
				for key, value := range imageURL {
					image[key] = value
				}
			}
			if detail, ok := obj["detail"].(string); ok {
				image["detail"] = detail
			}
			if _, hasURL := image["url"]; hasURL {
				parts = append(parts, map[string]any{"type": "image_url", "image_url": image})
			} else if fileID, ok := obj["file_id"].(string); ok {
				hasText = true
				parts = append(parts, map[string]any{"type": "text", "text": "[image file input omitted: " + fileID + "]"})
			}
		}
	}
	if len(parts) > 0 && !hasText {
		parts = append([]map[string]any{{"type": "text", "text": "Please inspect the attached image."}}, parts...)
	}
	return parts
}

func flattenedContent(ctx context.Context, items []any, runtime capabilities.Runtime) string {
	var b strings.Builder
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		switch obj["type"] {
		case "input_text", "output_text", "text":
			if text, ok := obj["text"].(string); ok {
				b.WriteString(text)
			}
		case "input_image", "image_url":
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			if text := imageAnalysisText(ctx, obj, runtime); text != "" {
				b.WriteString("[image analysis]\n")
				b.WriteString(text)
			} else {
				b.WriteString("[image input omitted: upstream model profile is text-only]")
			}
		case "input_file":
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			name, _ := obj["filename"].(string)
			if name == "" {
				name = "unnamed file"
			}
			b.WriteString("[file input omitted: " + name + "]")
		}
	}
	return b.String()
}

func imageAnalysisText(ctx context.Context, obj map[string]any, runtime capabilities.Runtime) string {
	if runtime.Vision == nil {
		return ""
	}
	imageURL, _ := obj["image_url"].(string)
	if imageURL == "" {
		if image, ok := obj["image_url"].(map[string]any); ok {
			imageURL, _ = image["url"].(string)
		}
	}
	if imageURL == "" {
		return ""
	}
	detail, _ := obj["detail"].(string)
	result, err := runtime.Vision.Analyze(ctx, capabilities.ImageInput{URL: imageURL, Detail: detail}, "describe")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(result.Text)
}

func functionToolCall(item map[string]any) providers.ChatToolCall {
	name, _ := item["name"].(string)
	callID, _ := item["call_id"].(string)
	arguments, _ := item["arguments"].(string)
	return providers.ChatToolCall{
		ID:   callID,
		Type: "function",
		Function: providers.ChatCallFunction{
			Name:      name,
			Arguments: arguments,
		},
	}
}

func customToolCall(item map[string]any, adapter adapters.Adapter) providers.ChatToolCall {
	name, _ := item["name"].(string)
	callID, _ := item["call_id"].(string)
	input, _ := item["input"].(string)
	input = adapter.NormalizeCustomInput(name, input)
	arguments, _ := json.Marshal(map[string]string{"input": input})
	return providers.ChatToolCall{
		ID:   callID,
		Type: "function",
		Function: providers.ChatCallFunction{
			Name:      name,
			Arguments: string(arguments),
		},
	}
}

func applyPatchToolCall(item map[string]any, adapter adapters.Adapter) providers.ChatToolCall {
	callID, _ := item["call_id"].(string)
	input := ""
	if text, ok := item["input"].(string); ok {
		input = text
	} else if operation, ok := item["operation"].(map[string]any); ok {
		data, _ := json.Marshal(operation)
		input = string(data)
	}
	input = adapter.NormalizeCustomInput("apply_patch", input)
	arguments, _ := json.Marshal(map[string]string{"input": input})
	return providers.ChatToolCall{
		ID:   callID,
		Type: "function",
		Function: providers.ChatCallFunction{
			Name:      "apply_patch",
			Arguments: string(arguments),
		},
	}
}

func toolSearchCall(item map[string]any) providers.ChatToolCall {
	callID, _ := item["call_id"].(string)
	arguments, _ := json.Marshal(item["arguments"])
	return providers.ChatToolCall{
		ID:   callID,
		Type: "function",
		Function: providers.ChatCallFunction{
			Name:      "tool_search",
			Arguments: string(arguments),
		},
	}
}

func shellToolCall(item map[string]any) providers.ChatToolCall {
	callID, _ := item["call_id"].(string)
	action, _ := json.Marshal(item["action"])
	return providers.ChatToolCall{
		ID:   callID,
		Type: "function",
		Function: providers.ChatCallFunction{
			Name:      "shell",
			Arguments: string(action),
		},
	}
}

func outputText(item map[string]any) string {
	itemType, _ := item["type"].(string)
	switch itemType {
	case "tool_search_output":
		data, _ := json.Marshal(item["tools"])
		return string(data)
	case "shell_call_output", "local_shell_call_output":
		return tools.ShellOutputText(item["output"])
	default:
		return valueText(item["output"])
	}
}

func valueText(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case map[string]any:
		if text, ok := v["content"].(string); ok {
			return text
		}
	case []any:
		return flattenedContent(context.Background(), v, capabilities.Runtime{})
	}
	data, _ := json.Marshal(value)
	return string(data)
}
