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
	result, err := ToChatMessages(codex.ResponsesRequest{Input: input}, adapters.Get(adapters.OpenAIName))
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

func TestNonGPTTranscriptReplaysSimpleApplyPatchAsTextEditor(t *testing.T) {
	input := json.RawMessage(`[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"edit"}]},
		{"type":"custom_tool_call","call_id":"call_1","name":"apply_patch","input":"*** Begin Patch\n*** Update File: a.java\n@@\n-old\n+new\n*** End Patch\n"},
		{"type":"custom_tool_call_output","call_id":"call_1","output":"Success. Updated the following files:\nM a.java"}
	]`)
	result, err := ToChatMessages(codex.ResponsesRequest{Input: input}, adapters.Get(adapters.MimoName))
	if err != nil {
		t.Fatalf("to chat messages: %v", err)
	}
	if len(result.Messages) != 3 {
		t.Fatalf("messages len = %d", len(result.Messages))
	}
	if len(result.Messages[1].ToolCalls) != 1 {
		t.Fatalf("history should replay as a tool call: %#v", result.Messages)
	}
	call := result.Messages[1].ToolCalls[0]
	if call.Function.Name != "codex_text_editor" ||
		!strings.Contains(call.Function.Arguments, `"command":"str_replace"`) ||
		!strings.Contains(call.Function.Arguments, `"old_str":"old"`) ||
		!strings.Contains(call.Function.Arguments, `"new_str":"new"`) {
		t.Fatalf("tool call = %#v", call)
	}
	output, _ := result.Messages[2].Content.(string)
	if result.Messages[2].Role != "tool" || result.Messages[2].ToolCallID != "call_1" ||
		!strings.Contains(output, "TEXT_EDITOR_EDIT_SUCCEEDED") ||
		strings.Contains(output, "APPLY_PATCH_SUCCEEDED") {
		t.Fatalf("output = %s", output)
	}
}

func TestNonGPTTranscriptHidesIrreversibleApplyPatchHistory(t *testing.T) {
	input := json.RawMessage(`[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"edit"}]},
		{"type":"custom_tool_call","call_id":"call_1","name":"apply_patch","input":"*** Begin Patch\n*** Update File: a.java\n*** Move to: b.java\n@@\n-old\n+new\n*** End Patch\n"},
		{"type":"custom_tool_call_output","call_id":"call_1","output":"Success. Updated the following files:\nM b.java"}
	]`)
	result, err := ToChatMessages(codex.ResponsesRequest{Input: input}, adapters.Get(adapters.MimoName))
	if err != nil {
		t.Fatalf("to chat messages: %v", err)
	}
	if len(result.Messages) != 3 {
		t.Fatalf("messages len = %d", len(result.Messages))
	}
	if result.Messages[1].Role != "system" || result.Messages[2].Role != "system" {
		t.Fatalf("history should be hidden as system summaries: %#v", result.Messages)
	}
	callSummary, _ := result.Messages[1].Content.(string)
	if !strings.Contains(callSummary, "TEXT_EDITOR_HISTORY_HIDDEN") || strings.Contains(callSummary, "old_str") || strings.Contains(callSummary, "new_str") {
		t.Fatalf("call summary leaked editable arguments: %s", callSummary)
	}
}

func TestNonGPTTranscriptMarksAlreadyAppliedTextEditorHistory(t *testing.T) {
	input := json.RawMessage(`[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"edit"}]},
		{"type":"custom_tool_call","call_id":"call_1","name":"apply_patch","input":"*** Begin Patch\n*** Add File: a.java\n+TEXT_EDITOR_ALREADY_APPLIED\n*** End Patch"},
		{"type":"custom_tool_call_output","call_id":"call_1","output":"text editor edit failed: file already exists\nTEXT_EDITOR_ALREADY_APPLIED"}
	]`)
	result, err := ToChatMessages(codex.ResponsesRequest{Input: input}, adapters.Get(adapters.DeepSeekName))
	if err != nil {
		t.Fatalf("to chat messages: %v", err)
	}
	if len(result.Messages) != 3 {
		t.Fatalf("messages len = %d", len(result.Messages))
	}
	callSummary, _ := result.Messages[1].Content.(string)
	if !strings.Contains(callSummary, "TEXT_EDITOR_ALREADY_APPLIED") ||
		!strings.Contains(callSummary, "Do not repeat that edit") ||
		strings.Contains(callSummary, "old_str") ||
		strings.Contains(callSummary, "new_str") {
		t.Fatalf("call summary = %s", callSummary)
	}
	outputSummary, _ := result.Messages[2].Content.(string)
	for _, want := range []string{
		"TEXT_EDITOR_HISTORY_OUTPUT_HIDDEN",
		"TEXT_EDITOR_ALREADY_APPLIED",
		"required_next_action: read_only_verify_current_file_or_summarize",
		"forbidden_next_action: repeat_same_text_editor_edit",
	} {
		if !strings.Contains(outputSummary, want) {
			t.Fatalf("output summary missing %q: %s", want, outputSummary)
		}
	}
}

func TestDeepSeekReasoningItemAttachesToFollowingToolCall(t *testing.T) {
	input := json.RawMessage(`[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"edit"}]},
		{"type":"reasoning","reasoning_content":"think before tool"},
		{"type":"function_call","call_id":"call_1","name":"record_result","arguments":"{\"ok\":true}"},
		{"type":"custom_tool_call_output","call_id":"call_1","output":"ok"}
	]`)
	result, err := ToChatMessages(codex.ResponsesRequest{Input: input}, adapters.Get(adapters.DeepSeekName))
	if err != nil {
		t.Fatalf("to chat messages: %v", err)
	}
	if len(result.Messages) != 3 {
		t.Fatalf("messages len = %d", len(result.Messages))
	}
	if result.Messages[1].ReasoningContent != "think before tool" {
		t.Fatalf("reasoning_content = %q", result.Messages[1].ReasoningContent)
	}
	if len(result.Messages[1].ToolCalls) != 1 {
		t.Fatalf("assistant tool calls len = %d", len(result.Messages[1].ToolCalls))
	}
}

func TestDefaultAdapterIgnoresReasoningItemForChatCompletions(t *testing.T) {
	input := json.RawMessage(`[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"edit"}]},
		{"type":"reasoning","reasoning_content":"think before tool"},
		{"type":"custom_tool_call","call_id":"call_1","name":"apply_patch","input":"*** Begin Patch\n*** End Patch\n"}
	]`)
	result, err := ToChatMessages(codex.ResponsesRequest{Input: input}, adapters.Get(adapters.DefaultName))
	if err != nil {
		t.Fatalf("to chat messages: %v", err)
	}
	if result.Messages[1].ReasoningContent != "" {
		t.Fatalf("default adapter should not forward reasoning_content: %#v", result.Messages[1])
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

func TestNonGPTApplyPatchOutputUsesTextEditorRecoverySemantics(t *testing.T) {
	input := json.RawMessage(`[
		{"type":"apply_patch_call_output","call_id":"call_1","output":"Failed to find context"}
	]`)
	result, err := ToChatMessages(codex.ResponsesRequest{Input: input}, adapters.Get(adapters.MimoName))
	if err != nil {
		t.Fatalf("to chat messages: %v", err)
	}
	if len(result.Messages) != 1 || result.Messages[0].Role != "tool" {
		t.Fatalf("messages = %#v", result.Messages)
	}
	content, _ := result.Messages[0].Content.(string)
	if !strings.Contains(content, "TEXT_EDITOR_CONTEXT_MISMATCH") ||
		!strings.Contains(content, "required_next_action: inspect_current_file") ||
		!strings.Contains(content, "forbidden_next_action: retry_same_edit") {
		t.Fatalf("tool output = %q", content)
	}
}

func TestNonGPTApplyPatchExpectedLinesFailureUsesTextEditorRecoveryProtocol(t *testing.T) {
	input := json.RawMessage(`[
		{"type":"apply_patch_call_output","call_id":"call_1","output":"apply_patch verification failed: Failed to find expected lines in /tmp/file:\n   <view"}
	]`)
	result, err := ToChatMessages(codex.ResponsesRequest{Input: input}, adapters.Get(adapters.MimoName))
	if err != nil {
		t.Fatalf("to chat messages: %v", err)
	}
	content, _ := result.Messages[0].Content.(string)
	if !strings.Contains(content, "TEXT_EDITOR_CONTEXT_MISMATCH") ||
		!strings.Contains(content, "required_next_action: inspect_current_file") ||
		!strings.Contains(content, "forbidden_next_action: retry_same_edit") {
		t.Fatalf("tool output = %q", content)
	}
}

func TestNonGPTCustomApplyPatchOutputUsesTextEditorRecoverySemantics(t *testing.T) {
	input := json.RawMessage(`[
		{"type":"custom_tool_call","call_id":"call_1","name":"apply_patch","input":"*** Begin Patch\n*** End Patch\n"},
		{"type":"custom_tool_call_output","call_id":"call_1","output":"apply_patch verification failed: Failed to find expected lines in /tmp/file:\n   <view"}
	]`)
	result, err := ToChatMessages(codex.ResponsesRequest{Input: input}, adapters.Get(adapters.MimoName))
	if err != nil {
		t.Fatalf("to chat messages: %v", err)
	}
	if len(result.Messages) != 2 || result.Messages[1].Role != "tool" {
		if len(result.Messages) != 2 || result.Messages[1].Role != "system" {
			t.Fatalf("messages = %#v", result.Messages)
		}
	}
	content, _ := result.Messages[1].Content.(string)
	if !strings.Contains(content, "TEXT_EDITOR_HISTORY_OUTPUT_HIDDEN") ||
		!strings.Contains(content, "TEXT_EDITOR_CONTEXT_MISMATCH") ||
		!strings.Contains(content, "required_next_action: inspect_current_file") ||
		!strings.Contains(content, "forbidden_next_action: retry_same_edit") {
		t.Fatalf("tool output = %q", content)
	}
}

func TestNonGPTPlainCustomOutputDoesNotUsePatchRecoveryProtocol(t *testing.T) {
	input := json.RawMessage(`[
		{"type":"custom_tool_call","call_id":"call_1","name":"custom_tool","input":"x"},
		{"type":"custom_tool_call_output","call_id":"call_1","output":"Failed to find expected lines"}
	]`)
	result, err := ToChatMessages(codex.ResponsesRequest{Input: input}, adapters.Get(adapters.MimoName))
	if err != nil {
		t.Fatalf("to chat messages: %v", err)
	}
	content, _ := result.Messages[1].Content.(string)
	if strings.Contains(content, "APPLY_PATCH_CONTEXT_MISMATCH") {
		t.Fatalf("plain custom output should not be treated as patch: %q", content)
	}
}
