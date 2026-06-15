package transcript

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/capabilities"
	"codex-bridge/internal/codex"
)

func TestToolOutputFollowsAssistantToolCall(t *testing.T) {
	input := json.RawMessage(`[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"edit"}]},
		{"type":"custom_tool_call","call_id":"call_1","name":"apply_patch","input":"*** Begin Patch\n*** End Patch\n"},
		{"type":"custom_tool_call_output","call_id":"call_1","output":"ok"}
	]`)
	result, err := ToChatMessages(codex.ResponsesRequest{Input: input}, adapters.Get(adapters.DefaultName))
	if err != nil {
		t.Fatalf("to chat messages: %v", err)
	}
	if len(result.Messages) != 3 {
		t.Fatalf("messages len = %d", len(result.Messages))
	}
	if len(result.Messages[1].ToolCalls) != 1 {
		t.Fatalf("assistant tool calls len = %d", len(result.Messages[1].ToolCalls))
	}
	if result.Messages[2].Role != "tool" || result.Messages[2].ToolCallID != "call_1" {
		t.Fatalf("tool output message = %#v", result.Messages[2])
	}
}

func TestImageInputFallsBackToTextOnlyPlaceholder(t *testing.T) {
	input := json.RawMessage(`[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"inspect"},{"type":"input_image","image_url":"https://x/y.png"}]}
	]`)
	result, err := ToChatMessages(codex.ResponsesRequest{Input: input}, adapters.Get(adapters.DefaultName))
	if err != nil {
		t.Fatalf("to chat messages: %v", err)
	}
	got, ok := result.Messages[0].Content.(string)
	if !ok || !strings.Contains(got, "[image input omitted") {
		t.Fatalf("content = %#v", result.Messages[0].Content)
	}
}

func TestTextOnlyImageInputUsesVisionCapability(t *testing.T) {
	input := json.RawMessage(`[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"inspect"},{"type":"input_image","image_url":"https://x/y.png"}]}
	]`)
	result, err := ToChatMessagesWithRuntime(context.Background(), codex.ResponsesRequest{Input: input}, adapters.Get(adapters.DefaultName), capabilities.Runtime{
		Vision: capabilities.StaticVisionProvider{Result: capabilities.VisionResult{Text: "a blue icon"}},
	})
	if err != nil {
		t.Fatalf("to chat messages: %v", err)
	}
	got, ok := result.Messages[0].Content.(string)
	if !ok || !strings.Contains(got, "[image analysis]") || !strings.Contains(got, "a blue icon") {
		t.Fatalf("content = %#v", result.Messages[0].Content)
	}
}

func TestVisionInputKeepsImageParts(t *testing.T) {
	input := json.RawMessage(`[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"inspect"},{"type":"input_image","image_url":"https://x/y.png","detail":"original"}]}
	]`)
	result, err := ToChatMessages(codex.ResponsesRequest{Input: input}, visionAdapter{Adapter: adapters.Get(adapters.DefaultName)})
	if err != nil {
		t.Fatalf("to chat messages: %v", err)
	}
	parts, ok := result.Messages[0].Content.([]map[string]any)
	if !ok {
		t.Fatalf("content = %#v", result.Messages[0].Content)
	}
	if len(parts) != 2 {
		t.Fatalf("parts = %#v", parts)
	}
}

func TestMimoInputKeepsImageParts(t *testing.T) {
	input := json.RawMessage(`[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"inspect"},{"type":"input_image","image_url":"https://x/y.png","detail":"original"}]}
	]`)
	result, err := ToChatMessages(codex.ResponsesRequest{Input: input}, adapters.Get(adapters.MimoName))
	if err != nil {
		t.Fatalf("to chat messages: %v", err)
	}
	parts, ok := result.Messages[0].Content.([]map[string]any)
	if !ok {
		t.Fatalf("content = %#v", result.Messages[0].Content)
	}
	if len(parts) != 2 {
		t.Fatalf("parts = %#v", parts)
	}
}

type visionAdapter struct {
	adapters.Adapter
}

func (visionAdapter) Capabilities() adapters.Capabilities {
	return adapters.Capabilities{InputModalities: []string{"text", "image"}}
}

func TestToolSearchOutputIsKeptForRegistry(t *testing.T) {
	input := json.RawMessage(`[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"continue"}]},
		{"type":"tool_search_call","execution":"client","call_id":"call_1","status":"completed","arguments":{"goal":"open"}},
		{"type":"tool_search_output","execution":"client","call_id":"call_1","status":"completed","tools":[{"type":"function","name":"open","description":"open url","parameters":{"type":"object"}}]}
	]`)
	result, err := ToChatMessages(codex.ResponsesRequest{Input: input}, adapters.Get(adapters.DefaultName))
	if err != nil {
		t.Fatalf("to chat messages: %v", err)
	}
	found := false
	for _, item := range result.Items {
		found = found || item["type"] == "tool_search_output"
	}
	if !found {
		t.Fatalf("items = %#v", result.Items)
	}
	if result.Messages[2].Role != "tool" || result.Messages[2].ToolCallID != "call_1" {
		t.Fatalf("tool search output message = %#v", result.Messages)
	}
}

func TestSupportedWebSearchDoesNotAddUnsupportedNote(t *testing.T) {
	req := codex.ResponsesRequest{
		Input: json.RawMessage(`[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"search"}]}
		]`),
		Tools: []codex.ResponseTool{{Type: "web_search", Raw: map[string]any{"type": "web_search"}}},
	}
	result, err := ToChatMessagesWithRuntime(context.Background(), req, adapters.Get(adapters.DefaultName), capabilities.Runtime{
		Search: capabilities.StaticSearchProvider{},
	})
	if err != nil {
		t.Fatalf("to chat messages: %v", err)
	}
	for _, message := range result.Messages {
		if strings.Contains(valueAsString(message.Content), "cannot directly execute") {
			t.Fatalf("unexpected unsupported note: %#v", result.Messages)
		}
	}
}

func TestUnsupportedWebSearchAddsUnsupportedNoteWhenSearchDisabled(t *testing.T) {
	req := codex.ResponsesRequest{
		Input: json.RawMessage(`[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"search"}]}
		]`),
		Tools: []codex.ResponseTool{{Type: "web_search", Raw: map[string]any{"type": "web_search"}}},
	}
	result, err := ToChatMessages(req, adapters.Get(adapters.DefaultName))
	if err != nil {
		t.Fatalf("to chat messages: %v", err)
	}
	if len(result.Messages) < 2 || !strings.Contains(valueAsString(result.Messages[0].Content), "cannot directly execute") {
		t.Fatalf("messages = %#v", result.Messages)
	}
}

func TestImageFileIDFallsBackToTextPart(t *testing.T) {
	input := json.RawMessage(`[
		{"type":"message","role":"user","content":[{"type":"input_image","file_id":"file_123"}]}
	]`)
	result, err := ToChatMessages(codex.ResponsesRequest{Input: input}, visionAdapter{Adapter: adapters.Get(adapters.DefaultName)})
	if err != nil {
		t.Fatalf("to chat messages: %v", err)
	}
	parts, ok := result.Messages[0].Content.([]map[string]any)
	if !ok {
		t.Fatalf("content = %#v", result.Messages[0].Content)
	}
	if parts[0]["type"] != "text" {
		t.Fatalf("parts = %#v", parts)
	}
}

func valueAsString(value any) string {
	text, _ := value.(string)
	return text
}

func TestApplyPatchCallOutputRoundsTrip(t *testing.T) {
	input := json.RawMessage(`[
		{"type":"apply_patch_call_output","call_id":"call_1","output":"done"}
	]`)
	result, err := ToChatMessages(codex.ResponsesRequest{Input: input}, adapters.Get(adapters.DefaultName))
	if err != nil {
		t.Fatalf("to chat messages: %v", err)
	}
	if len(result.Messages) != 1 || result.Messages[0].Role != "tool" {
		t.Fatalf("messages = %#v", result.Messages)
	}
}

func TestDeepSeekApplyPatchContextFailureCarriesRecoverySemantics(t *testing.T) {
	input := json.RawMessage(`[
		{"type":"apply_patch_call_output","call_id":"call_1","output":"Failed to find context"}
	]`)
	result, err := ToChatMessages(codex.ResponsesRequest{Input: input}, adapters.Get(adapters.DeepSeekName))
	if err != nil {
		t.Fatalf("to chat messages: %v", err)
	}
	if len(result.Messages) != 1 || result.Messages[0].Role != "tool" {
		t.Fatalf("messages = %#v", result.Messages)
	}
	content, _ := result.Messages[0].Content.(string)
	if !strings.Contains(content, "APPLY_PATCH_CONTEXT_MISMATCH") ||
		!strings.Contains(content, "required_next_action: inspect_current_file") ||
		!strings.Contains(content, "forbidden_next_action: retry_same_patch") {
		t.Fatalf("tool output = %q", content)
	}
}

func TestDeepSeekApplyPatchExpectedLinesFailureCarriesRecoveryProtocol(t *testing.T) {
	input := json.RawMessage(`[
		{"type":"apply_patch_call_output","call_id":"call_1","output":"apply_patch verification failed: Failed to find expected lines in /tmp/file:\n   <view"}
	]`)
	result, err := ToChatMessages(codex.ResponsesRequest{Input: input}, adapters.Get(adapters.DeepSeekName))
	if err != nil {
		t.Fatalf("to chat messages: %v", err)
	}
	content, _ := result.Messages[0].Content.(string)
	if !strings.Contains(content, "APPLY_PATCH_CONTEXT_MISMATCH") ||
		!strings.Contains(content, "required_next_action: inspect_current_file") ||
		!strings.Contains(content, "forbidden_next_action: retry_same_patch") {
		t.Fatalf("tool output = %q", content)
	}
}

func TestDeepSeekCustomApplyPatchOutputCarriesRecoverySemantics(t *testing.T) {
	input := json.RawMessage(`[
		{"type":"custom_tool_call","call_id":"call_1","name":"apply_patch","input":"*** Begin Patch\n*** End Patch\n"},
		{"type":"custom_tool_call_output","call_id":"call_1","output":"apply_patch verification failed: Failed to find expected lines in /tmp/file:\n   <view"}
	]`)
	result, err := ToChatMessages(codex.ResponsesRequest{Input: input}, adapters.Get(adapters.DeepSeekName))
	if err != nil {
		t.Fatalf("to chat messages: %v", err)
	}
	if len(result.Messages) != 2 || result.Messages[1].Role != "tool" {
		t.Fatalf("messages = %#v", result.Messages)
	}
	content, _ := result.Messages[1].Content.(string)
	if !strings.Contains(content, "APPLY_PATCH_CONTEXT_MISMATCH") ||
		!strings.Contains(content, "required_next_action: inspect_current_file") ||
		!strings.Contains(content, "forbidden_next_action: retry_same_patch") {
		t.Fatalf("tool output = %q", content)
	}
}

func TestDeepSeekPlainCustomOutputDoesNotUsePatchRecoveryProtocol(t *testing.T) {
	input := json.RawMessage(`[
		{"type":"custom_tool_call","call_id":"call_1","name":"custom_tool","input":"x"},
		{"type":"custom_tool_call_output","call_id":"call_1","output":"Failed to find expected lines"}
	]`)
	result, err := ToChatMessages(codex.ResponsesRequest{Input: input}, adapters.Get(adapters.DeepSeekName))
	if err != nil {
		t.Fatalf("to chat messages: %v", err)
	}
	content, _ := result.Messages[1].Content.(string)
	if strings.Contains(content, "APPLY_PATCH_CONTEXT_MISMATCH") {
		t.Fatalf("plain custom output should not be treated as patch: %q", content)
	}
}
