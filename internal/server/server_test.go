package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/capabilities"
	"codex-bridge/internal/config"
	"codex-bridge/internal/providers"
)

type fakeProvider struct {
	req           providers.ChatCompletionRequest
	reqs          []providers.ChatCompletionRequest
	streamReq     providers.ChatCompletionRequest
	streamReqs    []providers.ChatCompletionRequest
	streamEvents  []providers.StreamEvent
	streamBatches [][]providers.StreamEvent
	responses     []providers.ChatCompletionResponse
}

type fakeResponsesProvider struct {
	fakeProvider
	responseReq       map[string]any
	streamResponseReq map[string]any
	streamEvents      []providers.ResponseStreamEvent
}

func (p *fakeResponsesProvider) CreateResponse(_ context.Context, req map[string]any) (map[string]any, error) {
	p.responseReq = req
	return map[string]any{
		"id":         "resp_test",
		"object":     "response",
		"created_at": float64(123),
		"model":      req["model"],
		"status":     "completed",
		"output":     []any{},
	}, nil
}

func (p *fakeResponsesProvider) StreamResponse(_ context.Context, req map[string]any) (<-chan providers.ResponseStreamEvent, error) {
	p.streamResponseReq = req
	out := make(chan providers.ResponseStreamEvent, len(p.streamEvents)+1)
	go func() {
		defer close(out)
		for _, event := range p.streamEvents {
			out <- event
		}
		out <- providers.ResponseStreamEvent{Done: true}
	}()
	return out, nil
}

func (p *fakeProvider) Create(_ context.Context, req providers.ChatCompletionRequest) (*providers.ChatCompletionResponse, error) {
	p.req = req
	p.reqs = append(p.reqs, req)
	if len(p.responses) > 0 {
		resp := p.responses[0]
		p.responses = p.responses[1:]
		return &resp, nil
	}
	toolName := "apply_patch"
	toolArguments := `{"input":"*** Begin Patch\n*** Add File: hello.txt\n+hello\n*** End Patch\n"}`
	for _, tool := range req.Tools {
		if tool.Function.Name == "codex_text_editor" {
			toolName = "codex_text_editor"
			toolArguments = `{"command":"create","path":"hello.txt","file_text":"hello"}`
			break
		}
	}
	return &providers.ChatCompletionResponse{
		ID:    "chatcmpl_test",
		Model: req.Model,
		Choices: []struct {
			Index        int                   `json:"index"`
			Message      providers.ChatMessage `json:"message"`
			FinishReason string                `json:"finish_reason"`
		}{{
			Index: 0,
			Message: providers.ChatMessage{ToolCalls: []providers.ChatToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: providers.ChatCallFunction{
					Name:      toolName,
					Arguments: toolArguments,
				},
			}}},
			FinishReason: "tool_calls",
		}},
	}, nil
}

func TestResponsesEndpointForwardsNativeResponsesForAutoGPTModel(t *testing.T) {
	provider := &fakeResponsesProvider{}
	cfg := testConfig()
	cfg.Providers["fake"] = config.ProviderConfig{Profile: adapters.DefaultName, Protocol: "auto"}
	cfg.Models["deepseek-v4-flash"] = config.ModelConfig{
		Provider:           "fake",
		UpstreamModel:      "gpt-5.4",
		ApplyPatchToolType: "freeform",
	}
	handler := New(cfg, map[string]providers.ChatProvider{"fake": provider}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := []byte(`{
		"model":"deepseek-v4-flash",
		"input":"think",
		"reasoning":{"effort":"high","summary":"auto"},
		"stream":false
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if provider.responseReq == nil {
		t.Fatalf("native responses request was not sent")
	}
	if provider.responseReq["model"] != "gpt-5.4" {
		t.Fatalf("upstream model = %#v", provider.responseReq["model"])
	}
	reasoning, ok := provider.responseReq["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "high" {
		t.Fatalf("reasoning = %#v", provider.responseReq["reasoning"])
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["model"] != "deepseek-v4-flash" {
		t.Fatalf("response model = %#v", resp["model"])
	}
}

func TestResponsesEndpointPureForwardsNativeGPTResponses(t *testing.T) {
	provider := &fakeResponsesProvider{}
	cfg := testConfig()
	cfg.Providers["fake"] = config.ProviderConfig{Profile: adapters.DefaultName, Protocol: "responses"}
	cfg.Models["deepseek-v4-flash"] = config.ModelConfig{
		Provider:           "fake",
		Profile:            adapters.DefaultName,
		UpstreamModel:      "gpt-5.4",
		ApplyPatchToolType: "freeform",
	}
	handler := New(cfg, map[string]providers.ChatProvider{"fake": provider}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := []byte(`{
		"model":"deepseek-v4-flash",
		"instructions":"Keep native tools untouched.",
		"input":"edit a file",
		"tools":[{"type":"custom","name":"apply_patch","input_format":{"type":"text"}}],
		"stream":false
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if provider.responseReq["model"] != "gpt-5.4" {
		t.Fatalf("upstream model = %#v", provider.responseReq["model"])
	}
	if provider.responseReq["instructions"] != "Keep native tools untouched." {
		t.Fatalf("instructions were changed: %#v", provider.responseReq["instructions"])
	}
	tools, ok := provider.responseReq["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v", provider.responseReq["tools"])
	}
	tool, ok := tools[0].(map[string]any)
	if !ok || tool["type"] != "custom" || tool["name"] != "apply_patch" {
		t.Fatalf("tool was changed: %#v", tools[0])
	}
}

func TestShouldForwardResponsesOnlyForOpenAIAdapter(t *testing.T) {
	if !shouldForwardResponses("responses", adapters.Get(adapters.OpenAIName)) {
		t.Fatalf("openai responses should be forwarded")
	}
	if shouldForwardResponses("responses", adapters.Get(adapters.KimiName)) {
		t.Fatalf("kimi responses should use chat tool adapter")
	}
	if shouldForwardResponses("chat_completions", adapters.Get(adapters.OpenAIName)) {
		t.Fatalf("chat completions should not be forwarded as responses")
	}
}

func TestResponsesEndpointPreparesNativeKimiResponses(t *testing.T) {
	provider := &fakeResponsesProvider{}
	cfg := testConfig()
	cfg.Providers["fake"] = config.ProviderConfig{Profile: adapters.KimiName, Protocol: "responses"}
	cfg.Models["deepseek-v4-flash"] = config.ModelConfig{
		Provider:           "fake",
		Profile:            adapters.KimiName,
		UpstreamModel:      "kimi-for-coding",
		ApplyPatchToolType: "freeform",
	}
	handler := New(cfg, map[string]providers.ChatProvider{"fake": provider}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := []byte(`{
		"model":"deepseek-v4-flash",
		"instructions":"Follow the user request.",
		"input":"edit a file",
		"tools":[{"type":"function","name":"codex_text_editor"}],
		"stream":false
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if provider.responseReq != nil {
		t.Fatalf("kimi should use chat tool adapter, got native responses req = %#v", provider.responseReq)
	}
	if provider.req.Model != "kimi-for-coding" {
		t.Fatalf("upstream model = %q", provider.req.Model)
	}
	if len(provider.req.Tools) != 1 || provider.req.Tools[0].Function.Name != "codex_text_editor" {
		t.Fatalf("tools = %#v", provider.req.Tools)
	}
	if len(provider.req.Messages) == 0 || provider.req.Messages[0].Role != "system" {
		t.Fatalf("missing system discipline note: %#v", provider.req.Messages)
	}
	text, _ := provider.req.Messages[0].Content.(string)
	if !strings.Contains(text, "KIMI_CODEX_TOOL_DISCIPLINE") {
		t.Fatalf("discipline note = %q", text)
	}
}

func (p *fakeProvider) Stream(_ context.Context, req providers.ChatCompletionRequest) (<-chan providers.StreamEvent, error) {
	p.streamReq = req
	p.streamReqs = append(p.streamReqs, req)
	events := p.streamEvents
	if len(p.streamBatches) > 0 {
		events = p.streamBatches[0]
		p.streamBatches = p.streamBatches[1:]
	}
	out := make(chan providers.StreamEvent, len(events)+1)
	go func() {
		defer close(out)
		for _, event := range events {
			out <- event
		}
		out <- providers.StreamEvent{Done: true}
	}()
	return out, nil
}

func (p *fakeProvider) ListModels(_ context.Context) (*providers.ModelsResponse, error) {
	return &providers.ModelsResponse{Object: "list", Data: []providers.ModelInfo{{
		ID:      "upstream-model",
		Object:  "model",
		Created: 123,
		OwnedBy: "fake",
	}}}, nil
}

func TestV1RootAndModels(t *testing.T) {
	handler := New(testConfig(), map[string]providers.ChatProvider{"fake": &fakeProvider{}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rootReq := httptest.NewRequest(http.MethodGet, "/v1", nil)
	rootRec := httptest.NewRecorder()
	handler.ServeHTTP(rootRec, rootReq)
	if rootRec.Code != http.StatusOK {
		t.Fatalf("/v1 status = %d", rootRec.Code)
	}

	modelReq := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	modelReq.Header.Set("Authorization", "Bearer local-token")
	modelRec := httptest.NewRecorder()
	handler.ServeHTTP(modelRec, modelReq)
	if modelRec.Code != http.StatusOK {
		t.Fatalf("/v1/models status = %d, body = %s", modelRec.Code, modelRec.Body.String())
	}
	var resp providers.ModelsResponse
	if err := json.Unmarshal(modelRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode models: %v", err)
	}
	if resp.Object != "list" || len(resp.Data) != 1 || resp.Data[0].ID != "deepseek-v4-flash" {
		t.Fatalf("models = %#v", resp)
	}
}

func TestDiscoveredModelIsRoutable(t *testing.T) {
	provider := &fakeProvider{}
	cfg := testConfig()
	cfg.Models = nil
	cfg.ModelDiscovery = config.ModelDiscoveryConfig{Enabled: true, Mode: "upstream"}
	cfg.AddDiscoveredModels("fake", []string{"upstream-model"})
	handler := New(cfg, map[string]providers.ChatProvider{"fake": provider}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := []byte(`{
		"model":"gpt-5.3-codex",
		"input":"create hello.txt",
		"tools":[{"type":"custom","name":"apply_patch"}],
		"stream":false
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if provider.req.Model != "upstream-model" {
		t.Fatalf("upstream model = %q", provider.req.Model)
	}
}

func TestResponsesEndpointReturnsApplyPatchCustomToolCall(t *testing.T) {
	provider := &fakeProvider{}
	cfg := &config.Config{
		Codex: config.CodexConfig{LocalToken: "local-token"},
		Providers: map[string]config.ProviderConfig{
			"fake": {Profile: adapters.DeepSeekName},
		},
		Models: map[string]config.ModelConfig{
			"deepseek-v4-flash": {
				Provider:           "fake",
				UpstreamModel:      "deepseek-v4-flash",
				ApplyPatchToolType: "freeform",
			},
		},
	}
	handler := New(cfg, map[string]providers.ChatProvider{"fake": provider}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := []byte(`{
		"model":"deepseek-v4-flash",
		"input":"create hello.txt",
		"tools":[{"type":"custom","name":"apply_patch"}],
		"stream":false
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !provider.req.AssistantToolContentNull {
		t.Fatalf("deepseek adapter should request null assistant tool content")
	}
	if len(provider.req.Tools) != 1 || provider.req.Tools[0].Function.Name != "codex_text_editor" {
		t.Fatalf("chat tools = %#v", provider.req.Tools)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	output := resp["output"].([]any)
	item := output[0].(map[string]any)
	if item["type"] != "custom_tool_call" || item["name"] != "apply_patch" {
		t.Fatalf("output item = %#v", item)
	}
	if item["input"] != "*** Begin Patch\n*** Add File: hello.txt\n+hello\n*** End Patch" {
		t.Fatalf("input = %q", item["input"])
	}
}

func TestResponsesEndpointAllowsDifferentFilePatchAfterPatchSuccess(t *testing.T) {
	provider := &fakeProvider{responses: []providers.ChatCompletionResponse{{
		ID: "chatcmpl_test",
		Choices: []struct {
			Index        int                   `json:"index"`
			Message      providers.ChatMessage `json:"message"`
			FinishReason string                `json:"finish_reason"`
		}{{
			Message: providers.ChatMessage{ToolCalls: []providers.ChatToolCall{{
				ID:   "call_2",
				Type: "function",
				Function: providers.ChatCallFunction{
					Name:      "codex_text_editor",
					Arguments: `{"command":"str_replace","path":"b.vue","old_str":"old","new_str":"new"}`,
				},
			}}},
			FinishReason: "tool_calls",
		}},
	}}}
	handler := New(testConfig(), map[string]providers.ChatProvider{"fake": provider}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := []byte(`{
		"model":"deepseek-v4-flash",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"edit two files"}]},
			{"type":"custom_tool_call","call_id":"call_1","name":"apply_patch","input":"*** Begin Patch\n*** Update File: a.java\n@@\n-old\n+new\n*** End Patch"},
			{"type":"custom_tool_call_output","call_id":"call_1","output":"Success. Updated the following files:\nM a.java\n\nAPPLY_PATCH_SUCCEEDED\nfile_edit_state: completed\nchanged_files: a.java"}
		],
		"tools":[{"type":"custom","name":"apply_patch"}],
		"stream":false
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(provider.req.Tools) != 1 || provider.req.Tools[0].Function.Name != "codex_text_editor" {
		t.Fatalf("text editor should stay available: %#v", provider.req.Tools)
	}
	for _, message := range provider.req.Messages {
		text, _ := message.Content.(string)
		if message.Role == "system" && strings.Contains(text, "TEXT_EDITOR_SUCCESS_STOP") && strings.Contains(text, "a.java") {
			t.Fatalf("unexpected cooldown note: %#v", provider.req.Messages)
		}
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	output := resp["output"].([]any)
	item := output[0].(map[string]any)
	if item["type"] != "custom_tool_call" || !strings.Contains(item["input"].(string), "b.vue") {
		t.Fatalf("output item = %#v", item)
	}
}

func TestResponsesEndpointAllowsSameFilePatchAfterPatchSuccess(t *testing.T) {
	provider := &fakeProvider{responses: []providers.ChatCompletionResponse{{
		ID: "chatcmpl_test",
		Choices: []struct {
			Index        int                   `json:"index"`
			Message      providers.ChatMessage `json:"message"`
			FinishReason string                `json:"finish_reason"`
		}{{
			Message: providers.ChatMessage{ToolCalls: []providers.ChatToolCall{{
				ID:   "call_2",
				Type: "function",
				Function: providers.ChatCallFunction{
					Name:      "codex_text_editor",
					Arguments: `{"command":"str_replace","path":"./a.java","old_str":"next old","new_str":"next new"}`,
				},
			}}},
			FinishReason: "tool_calls",
		}},
	}}}
	handler := New(testConfig(), map[string]providers.ChatProvider{"fake": provider}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := []byte(`{
		"model":"deepseek-v4-flash",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"edit two files"}]},
			{"type":"custom_tool_call","call_id":"call_1","name":"apply_patch","input":"*** Begin Patch\n*** Update File: a.java\n@@\n-old\n+new\n*** End Patch"},
			{"type":"custom_tool_call_output","call_id":"call_1","output":"Success. Updated the following files:\nM a.java\n\nAPPLY_PATCH_SUCCEEDED\nfile_edit_state: completed\nchanged_files: a.java"}
		],
		"tools":[{"type":"custom","name":"apply_patch"}],
		"stream":false
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	output := resp["output"].([]any)
	item := output[0].(map[string]any)
	if item["type"] != "custom_tool_call" {
		t.Fatalf("output item = %#v", item)
	}
	if !strings.Contains(item["input"].(string), "a.java") || !strings.Contains(item["input"].(string), "next old") {
		t.Fatalf("output item = %#v", item)
	}
}

func TestResponsesEndpointReplaysPatchHistoryAsTextEditorToolCall(t *testing.T) {
	provider := &fakeProvider{responses: []providers.ChatCompletionResponse{{
		ID: "chatcmpl_test",
		Choices: []struct {
			Index        int                   `json:"index"`
			Message      providers.ChatMessage `json:"message"`
			FinishReason string                `json:"finish_reason"`
		}{{
			Message: providers.ChatMessage{ToolCalls: []providers.ChatToolCall{{
				ID:   "call_2",
				Type: "function",
				Function: providers.ChatCallFunction{
					Name:      "codex_text_editor",
					Arguments: `{"command":"str_replace","path":"a.java","old_str":"old","new_str":"new"}`,
				},
			}}},
			FinishReason: "tool_calls",
		}},
	}}}
	handler := New(testConfig(), map[string]providers.ChatProvider{"fake": provider}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := []byte(`{
		"model":"deepseek-v4-flash",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"edit again"}]},
			{"type":"custom_tool_call","call_id":"call_1","name":"apply_patch","input":"*** Begin Patch\n*** Update File: a.java\n@@\n-old\n+new\n*** End Patch"},
			{"type":"custom_tool_call_output","call_id":"call_1","output":[{"type":"output_text","text":"Success. Updated the following files:\nM a.java"}]}
		],
		"tools":[{"type":"custom","name":"apply_patch"}],
		"stream":false
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	call, ok := findToolCall(provider.req.Messages, "codex_text_editor")
	if !ok {
		t.Fatalf("messages = %#v", provider.req.Messages)
	}
	if call.Function.Name != "codex_text_editor" ||
		!strings.Contains(call.Function.Arguments, `"command":"str_replace"`) ||
		!strings.Contains(call.Function.Arguments, `"old_str":"old"`) ||
		!strings.Contains(call.Function.Arguments, `"new_str":"new"`) {
		t.Fatalf("history tool call = %#v", call)
	}
	historyOutput, ok := findToolOutput(provider.req.Messages, call.ID)
	if !ok ||
		!strings.Contains(historyOutput, "TEXT_EDITOR_EDIT_SUCCEEDED") {
		t.Fatalf("history tool output = %#v", provider.req.Messages)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	output := resp["output"].([]any)
	item := output[0].(map[string]any)
	if item["type"] != "custom_tool_call" || !strings.Contains(item["input"].(string), "a.java") {
		t.Fatalf("output item = %#v", item)
	}
}

func TestResponsesEndpointKeepsToolChoice(t *testing.T) {
	provider := &fakeProvider{}
	handler := New(testConfig(), map[string]providers.ChatProvider{"fake": provider}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := []byte(`{
		"model":"deepseek-v4-flash",
		"input":"answer without tools",
		"tool_choice":"none",
		"tools":[{"type":"custom","name":"apply_patch"}],
		"stream":false
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if provider.req.ToolChoice != "none" {
		t.Fatalf("tool_choice = %#v", provider.req.ToolChoice)
	}
}

func TestResponsesEndpointDisablesParallelToolCallsForFileWrites(t *testing.T) {
	provider := &fakeProvider{}
	handler := New(testConfig(), map[string]providers.ChatProvider{"fake": provider}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := []byte(`{
		"model":"deepseek-v4-flash",
		"input":"edit file",
		"tools":[{"type":"custom","name":"apply_patch"}],
		"parallel_tool_calls":true,
		"stream":false
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if provider.req.ParallelToolCalls == nil || *provider.req.ParallelToolCalls {
		t.Fatalf("parallel tool calls = %#v", provider.req.ParallelToolCalls)
	}
}

func TestResponsesEndpointKeepsParallelToolCallsForReadOnlyTools(t *testing.T) {
	provider := &fakeProvider{}
	handler := New(testConfig(), map[string]providers.ChatProvider{"fake": provider}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := []byte(`{
		"model":"deepseek-v4-flash",
		"input":"search tools",
		"tools":[{"type":"tool_search"}],
		"parallel_tool_calls":true,
		"stream":false
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if provider.req.ParallelToolCalls == nil || !*provider.req.ParallelToolCalls {
		t.Fatalf("parallel tool calls = %#v", provider.req.ParallelToolCalls)
	}
}

func TestResponsesEndpointResolvesInternalWebSearch(t *testing.T) {
	provider := &fakeProvider{responses: []providers.ChatCompletionResponse{
		{
			ID: "first",
			Choices: []struct {
				Index        int                   `json:"index"`
				Message      providers.ChatMessage `json:"message"`
				FinishReason string                `json:"finish_reason"`
			}{{
				Message: providers.ChatMessage{ToolCalls: []providers.ChatToolCall{{
					ID:   "call_search",
					Type: "function",
					Function: providers.ChatCallFunction{
						Name:      bridgeWebSearchTool,
						Arguments: `{"query":"codex bridge"}`,
					},
				}}},
			}},
		},
		{
			ID: "second",
			Choices: []struct {
				Index        int                   `json:"index"`
				Message      providers.ChatMessage `json:"message"`
				FinishReason string                `json:"finish_reason"`
			}{{
				Message: providers.ChatMessage{Role: "assistant", Content: "search result used"},
			}},
		},
	}}
	cfg := testConfig()
	cfg.Capabilities.Search.Enabled = true
	cfg.Capabilities.Search.Providers = []string{"static"}
	cfg.SearchProviders = map[string]config.SearchProvider{"static": {Type: "jina"}}
	handler := NewWithRuntime(cfg, map[string]providers.ChatProvider{"fake": provider}, capabilities.Runtime{
		Search: capabilities.StaticSearchProvider{Result: capabilities.SearchResult{RawText: "static search result"}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := []byte(`{
		"model":"deepseek-v4-flash",
		"input":"search web",
		"tools":[{"type":"web_search_preview"}],
		"stream":false
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	output := resp["output"].([]any)
	message := output[0].(map[string]any)
	content := message["content"].([]any)[0].(map[string]any)
	if content["text"] != "search result used" {
		t.Fatalf("response = %#v", resp)
	}
	if len(provider.reqs) != 2 {
		t.Fatalf("request count = %d", len(provider.reqs))
	}
	firstReq := provider.reqs[0]
	if len(firstReq.Tools) != 1 || firstReq.Tools[0].Function.Name != "web_search" {
		t.Fatalf("internal search tool = %#v", firstReq.Tools)
	}
	followUpReq := provider.reqs[1]
	if len(followUpReq.Tools) != 0 || followUpReq.ToolChoice != "none" {
		t.Fatalf("follow-up request = %#v", followUpReq)
	}
}

func TestResponsesEndpointResolvesMultipleInternalWebSearchRounds(t *testing.T) {
	const internalSearchRounds = 5
	responses := make([]providers.ChatCompletionResponse, 0, internalSearchRounds+1)
	for i := 1; i <= internalSearchRounds; i++ {
		responses = append(responses, providers.ChatCompletionResponse{
			ID: fmt.Sprintf("search_%d", i),
			Choices: []struct {
				Index        int                   `json:"index"`
				Message      providers.ChatMessage `json:"message"`
				FinishReason string                `json:"finish_reason"`
			}{{
				Message: providers.ChatMessage{ToolCalls: []providers.ChatToolCall{{
					ID:   fmt.Sprintf("call_search_%d", i),
					Type: "function",
					Function: providers.ChatCallFunction{
						Name:      "web_search",
						Arguments: fmt.Sprintf(`{"query":"round-%d"}`, i),
					},
				}}},
			}},
		})
	}
	responses = append(responses, providers.ChatCompletionResponse{
		ID: "final",
		Choices: []struct {
			Index        int                   `json:"index"`
			Message      providers.ChatMessage `json:"message"`
			FinishReason string                `json:"finish_reason"`
		}{{
			Message: providers.ChatMessage{Role: "assistant", Content: "multi search result"},
		}},
	})
	provider := &fakeProvider{responses: responses}
	cfg := testConfig()
	cfg.Capabilities.Search.Enabled = true
	cfg.Capabilities.Search.Providers = []string{"static"}
	handler := NewWithRuntime(cfg, map[string]providers.ChatProvider{"fake": provider}, capabilities.Runtime{
		Search: capabilities.StaticSearchProvider{Result: capabilities.SearchResult{RawText: "static search result"}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := []byte(`{
		"model":"deepseek-v4-flash",
		"input":"search web twice",
		"tools":[{"type":"web_search_preview"}],
		"stream":false
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(provider.reqs) != internalSearchRounds+1 {
		t.Fatalf("request count = %d", len(provider.reqs))
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	output := resp["output"].([]any)
	message := output[0].(map[string]any)
	content := message["content"].([]any)[0].(map[string]any)
	if content["text"] != "multi search result" {
		t.Fatalf("response = %#v", resp)
	}
}

func TestResponsesEndpointStreamsInternalWebSearchAsFinalMessage(t *testing.T) {
	provider := &fakeProvider{streamBatches: [][]providers.StreamEvent{
		{
			{Chunk: chatChunk(t, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_search","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"codex bridge\"}"}}]}}]}`)},
		},
		{
			{Chunk: chatChunk(t, `{"choices":[{"index":0,"delta":{"content":"stream search result used"}}]}`)},
		},
	}}
	cfg := testConfig()
	cfg.Capabilities.Search.Enabled = true
	cfg.Capabilities.Search.Providers = []string{"static"}
	cfg.SearchProviders = map[string]config.SearchProvider{"static": {Type: "jina"}}
	handler := NewWithRuntime(cfg, map[string]providers.ChatProvider{"fake": provider}, capabilities.Runtime{
		Search: capabilities.StaticSearchProvider{Result: capabilities.SearchResult{RawText: "static search result"}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := []byte(`{
		"model":"deepseek-v4-flash",
		"input":"search web",
		"tools":[{"type":"web_search_preview"}],
		"stream":true
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(provider.streamReqs) != 2 {
		t.Fatalf("stream request count = %d", len(provider.streamReqs))
	}
	if !provider.streamReqs[0].Stream || !provider.streamReqs[1].Stream {
		t.Fatalf("internal search should use upstream streaming: %#v", provider.streamReqs)
	}
	events := sseEvents(t, rec.Body.String())
	completed := events[len(events)-1]["response"].(map[string]any)
	output := completed["output"].([]any)
	item := output[0].(map[string]any)
	content := item["content"].([]any)[0].(map[string]any)
	if content["text"] != "stream search result used" {
		t.Fatalf("events = %#v", events)
	}
}

func TestResponsesEndpointStreamsMultipleInternalWebSearchRounds(t *testing.T) {
	const internalSearchRounds = 5
	streamBatches := make([][]providers.StreamEvent, 0, internalSearchRounds+1)
	for i := 1; i <= internalSearchRounds; i++ {
		streamBatches = append(streamBatches, []providers.StreamEvent{
			{Chunk: chatChunk(t, fmt.Sprintf(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_search_%d","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"round-%d\"}"}}]}}]}`, i, i))},
		})
	}
	streamBatches = append(streamBatches, []providers.StreamEvent{
		{Chunk: chatChunk(t, `{"choices":[{"index":0,"delta":{"content":"multi search result"}}]}`)},
	})
	provider := &fakeProvider{streamBatches: streamBatches}
	cfg := testConfig()
	cfg.Capabilities.Search.Enabled = true
	cfg.Capabilities.Search.Providers = []string{"static"}
	handler := NewWithRuntime(cfg, map[string]providers.ChatProvider{"fake": provider}, capabilities.Runtime{
		Search: capabilities.StaticSearchProvider{Result: capabilities.SearchResult{RawText: "static search result"}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := []byte(`{
		"model":"deepseek-v4-flash",
		"input":"search web twice",
		"tools":[{"type":"web_search_preview"}],
		"stream":true
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(provider.streamReqs) != internalSearchRounds+1 {
		t.Fatalf("stream request count = %d", len(provider.streamReqs))
	}
	events := sseEvents(t, rec.Body.String())
	for _, event := range events {
		if item, _ := event["item"].(map[string]any); item["name"] == "web_search" {
			t.Fatalf("internal search leaked to client: %#v", events)
		}
	}
	completed := events[len(events)-1]["response"].(map[string]any)
	output := completed["output"].([]any)
	item := output[0].(map[string]any)
	content := item["content"].([]any)[0].(map[string]any)
	if content["text"] != "multi search result" {
		t.Fatalf("events = %#v", events)
	}
}

func TestResponsesEndpointStreamsInternalWebSearchDirectTextDeltas(t *testing.T) {
	provider := &fakeProvider{streamEvents: []providers.StreamEvent{
		{Chunk: chatChunk(t, `{"choices":[{"index":0,"delta":{"content":"tool "}}]}`)},
		{Chunk: chatChunk(t, `{"choices":[{"index":0,"delta":{"content":"list"}}]}`)},
	}}
	cfg := testConfig()
	cfg.Capabilities.Search.Enabled = true
	handler := NewWithRuntime(cfg, map[string]providers.ChatProvider{"fake": provider}, capabilities.Runtime{
		Search: capabilities.StaticSearchProvider{Result: capabilities.SearchResult{RawText: "unused"}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := []byte(`{
		"model":"deepseek-v4-flash",
		"input":"请详细列举你能够使用的工具。",
		"tools":[{"type":"web_search_preview"}],
		"stream":true
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	events := sseEvents(t, rec.Body.String())
	var deltas []string
	for _, event := range events {
		if event["type"] == "response.output_text.delta" {
			deltas = append(deltas, event["delta"].(string))
		}
	}
	if strings.Join(deltas, "") != "tool list" {
		t.Fatalf("deltas = %#v, events = %#v", deltas, events)
	}
}

func TestResponsesEndpointStreamsApplyPatchAndUsage(t *testing.T) {
	provider := &fakeProvider{streamEvents: []providers.StreamEvent{
		{Chunk: chatChunk(t, `{"choices":[{"index":0,"delta":{"reasoning_content":"think "}}]}`)},
		{Chunk: chatChunk(t, `{"choices":[{"index":0,"delta":{"reasoning_content":"more","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"codex_text_editor","arguments":"{\"command\":\"create\","}}]}}]}`)},
		{Chunk: chatChunk(t, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"path\":\"hello.txt\",\"file_text\":\"hello\"}"}}]}}]}`)},
		{Chunk: chatChunk(t, `{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120,"prompt_cache_hit_tokens":80,"prompt_cache_miss_tokens":20,"completion_tokens_details":{"reasoning_tokens":5}}}`)},
	}}
	handler := New(testConfig(), map[string]providers.ChatProvider{"fake": provider}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := []byte(`{
		"model":"deepseek-v4-flash",
		"input":"create hello.txt",
		"tools":[{"type":"custom","name":"apply_patch"}],
		"stream":true
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !provider.streamReq.Stream {
		t.Fatalf("stream request should be true")
	}
	events := sseEvents(t, rec.Body.String())
	eventTypes := make([]string, 0, len(events))
	for _, event := range events {
		eventTypes = append(eventTypes, event["type"].(string))
	}
	wantTypes := []string{
		"response.created",
		"response.in_progress",
		"response.output_item.added",
		"response.custom_tool_call_input.delta",
		"response.output_item.done",
		"response.custom_tool_call_input.done",
		"response.output_item.done",
		"response.completed",
	}
	if strings.Join(eventTypes, ",") != strings.Join(wantTypes, ",") {
		t.Fatalf("event types = %v", eventTypes)
	}
	addedItem := events[2]["item"].(map[string]any)
	if addedItem["type"] != "custom_tool_call" || addedItem["name"] != "apply_patch" || addedItem["status"] != "in_progress" {
		t.Fatalf("added item = %#v", addedItem)
	}
	if !strings.Contains(events[3]["delta"].(string), "*** Begin Patch") {
		t.Fatalf("delta = %#v", events[3])
	}
	doneItem := events[6]["item"].(map[string]any)
	if doneItem["type"] != "custom_tool_call" || doneItem["name"] != "apply_patch" {
		t.Fatalf("done item = %#v", doneItem)
	}
	if doneItem["input"] != "*** Begin Patch\n*** Add File: hello.txt\n+hello\n*** End Patch" {
		t.Fatalf("input = %q", doneItem["input"])
	}
	completed := events[7]["response"].(map[string]any)
	output := completed["output"].([]any)
	reasoningItem := output[0].(map[string]any)
	if reasoningItem["type"] != "reasoning" || reasoningItem["reasoning_content"] != "think more" {
		t.Fatalf("reasoning item = %#v", reasoningItem)
	}
	toolItem := output[1].(map[string]any)
	if toolItem["type"] != "custom_tool_call" {
		t.Fatalf("tool item = %#v", toolItem)
	}
	usage := completed["usage"].(map[string]any)
	if usage["input_tokens"] != float64(100) || usage["total_tokens"] != float64(120) {
		t.Fatalf("usage = %#v", usage)
	}
	outputDetails := usage["output_tokens_details"].(map[string]any)
	if outputDetails["reasoning_tokens"] != float64(5) {
		t.Fatalf("output details = %#v", outputDetails)
	}
}

func TestDeepSeekUsageLogIncludesCacheDiagnostics(t *testing.T) {
	provider := &fakeProvider{streamEvents: []providers.StreamEvent{
		{Chunk: chatChunk(t, `{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120,"prompt_cache_hit_tokens":80,"prompt_cache_miss_tokens":20}}`)},
	}}
	var logs bytes.Buffer
	handler := New(testConfig(), map[string]providers.ChatProvider{"fake": provider}, slog.New(slog.NewJSONHandler(&logs, nil)))
	body := []byte(`{
		"model":"deepseek-v4-flash",
		"input":"hello",
		"tools":[{"type":"function","name":"z_tool","parameters":{"type":"object","properties":{"b":{"type":"string"},"a":{"type":"string"}}}}],
		"stream":true
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	text := logs.String()
	for _, want := range []string{
		`"msg":"upstream_usage"`,
		`"cached_input_tokens":80`,
		`"fresh_input_tokens":20`,
		`"prefix_hash"`,
		`"tools_hash"`,
		`"cache_hit_rate_permille":800`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("logs missing %s: %s", want, text)
		}
	}
}

func TestResponsesEndpointStreamsDifferentFilePatchAfterSuccess(t *testing.T) {
	provider := &fakeProvider{streamEvents: []providers.StreamEvent{
		{Chunk: chatChunk(t, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_2","type":"function","function":{"name":"codex_text_editor","arguments":"{\"command\":\"str_replace\",\"path\":\"b.vue\","}}]}}]}`)},
		{Chunk: chatChunk(t, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"old_str\":\"old\",\"new_str\":\"new\"}"}}]}}]}`)},
	}}
	handler := New(testConfig(), map[string]providers.ChatProvider{"fake": provider}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := []byte(`{
		"model":"deepseek-v4-flash",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"edit two files"}]},
			{"type":"custom_tool_call","call_id":"call_1","name":"apply_patch","input":"*** Begin Patch\n*** Update File: a.java\n@@\n-old\n+new\n*** End Patch"},
			{"type":"custom_tool_call_output","call_id":"call_1","output":"Success. Updated the following files:\nM a.java\n\nAPPLY_PATCH_SUCCEEDED\nfile_edit_state: completed\nchanged_files: a.java"}
		],
		"tools":[{"type":"custom","name":"apply_patch"}],
		"stream":true
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	events := sseEvents(t, rec.Body.String())
	completed := events[len(events)-1]["response"].(map[string]any)
	output := completed["output"].([]any)
	item := output[0].(map[string]any)
	if item["type"] != "custom_tool_call" || !strings.Contains(item["input"].(string), "b.vue") {
		t.Fatalf("output item = %#v", item)
	}
}

func TestResponsesEndpointStreamsSameFilePatchAfterSuccess(t *testing.T) {
	provider := &fakeProvider{streamEvents: []providers.StreamEvent{
		{Chunk: chatChunk(t, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_2","type":"function","function":{"name":"codex_text_editor","arguments":"{\"command\":\"str_replace\",\"path\":\"a.java\","}}]}}]}`)},
		{Chunk: chatChunk(t, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"old_str\":\"next old\",\"new_str\":\"next new\"}"}}]}}]}`)},
	}}
	handler := New(testConfig(), map[string]providers.ChatProvider{"fake": provider}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := []byte(`{
		"model":"deepseek-v4-flash",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"edit two files"}]},
			{"type":"custom_tool_call","call_id":"call_1","name":"apply_patch","input":"*** Begin Patch\n*** Update File: a.java\n@@\n-old\n+new\n*** End Patch"},
			{"type":"custom_tool_call_output","call_id":"call_1","output":"Success. Updated the following files:\nM a.java\n\nAPPLY_PATCH_SUCCEEDED\nfile_edit_state: completed\nchanged_files: a.java"}
		],
		"tools":[{"type":"custom","name":"apply_patch"}],
		"stream":true
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	events := sseEvents(t, rec.Body.String())
	completed := events[len(events)-1]["response"].(map[string]any)
	output := completed["output"].([]any)
	item := output[0].(map[string]any)
	if item["type"] != "custom_tool_call" {
		t.Fatalf("output item = %#v", item)
	}
	if !strings.Contains(item["input"].(string), "a.java") || !strings.Contains(item["input"].(string), "next old") {
		t.Fatalf("output item = %#v", item)
	}
}

func TestResponsesEndpointStreamsWithPatchHistoryAsTextEditorToolCall(t *testing.T) {
	provider := &fakeProvider{streamEvents: []providers.StreamEvent{
		{Chunk: chatChunk(t, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_2","type":"function","function":{"name":"codex_text_editor","arguments":"{\"command\":\"str_replace\",\"path\":\"a.java\","}}]}}]}`)},
		{Chunk: chatChunk(t, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"old_str\":\"old\",\"new_str\":\"new\"}"}}]}}]}`)},
	}}
	handler := New(testConfig(), map[string]providers.ChatProvider{"fake": provider}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := []byte(`{
		"model":"deepseek-v4-flash",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"edit again"}]},
			{"type":"custom_tool_call","call_id":"call_1","name":"apply_patch","input":"*** Begin Patch\n*** Update File: a.java\n@@\n-old\n+new\n*** End Patch"},
			{"type":"custom_tool_call_output","call_id":"call_1","output":"Success. Updated the following files:\nM a.java"}
		],
		"tools":[{"type":"custom","name":"apply_patch"}],
		"stream":true
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	call, ok := findToolCall(provider.streamReq.Messages, "codex_text_editor")
	if !ok {
		t.Fatalf("messages = %#v", provider.streamReq.Messages)
	}
	if call.Function.Name != "codex_text_editor" ||
		!strings.Contains(call.Function.Arguments, `"command":"str_replace"`) ||
		!strings.Contains(call.Function.Arguments, `"old_str":"old"`) ||
		!strings.Contains(call.Function.Arguments, `"new_str":"new"`) {
		t.Fatalf("history tool call = %#v", call)
	}
	events := sseEvents(t, rec.Body.String())
	completed := events[len(events)-1]["response"].(map[string]any)
	output := completed["output"].([]any)
	item := output[0].(map[string]any)
	if item["type"] != "custom_tool_call" || !strings.Contains(item["input"].(string), "a.java") {
		t.Fatalf("output item = %#v", item)
	}
}

func TestResponsesEndpointTurnsAlreadyAppliedTextEditorIntoExecCommand(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/a.java"
	if err := os.WriteFile(path, []byte("old done\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	arguments, _ := json.Marshal(map[string]string{
		"command": "str_replace",
		"path":    path,
		"old_str": "old",
		"new_str": "old done",
	})
	provider := &fakeProvider{responses: []providers.ChatCompletionResponse{{
		ID: "chatcmpl_test",
		Choices: []struct {
			Index        int                   `json:"index"`
			Message      providers.ChatMessage `json:"message"`
			FinishReason string                `json:"finish_reason"`
		}{{
			Message: providers.ChatMessage{ToolCalls: []providers.ChatToolCall{{
				ID:   "call_2",
				Type: "function",
				Function: providers.ChatCallFunction{
					Name:      "codex_text_editor",
					Arguments: string(arguments),
				},
			}}},
			FinishReason: "tool_calls",
		}},
	}}}
	handler := New(testConfig(), map[string]providers.ChatProvider{"fake": provider}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := []byte(`{
		"model":"deepseek-v4-flash",
		"input":"edit two files",
		"tools":[{"type":"custom","name":"apply_patch"}],
		"stream":false
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	output := resp["output"].([]any)
	item := output[0].(map[string]any)
	if item["type"] != "shell_call" {
		t.Fatalf("output item = %#v", item)
	}
	action := item["action"].(map[string]any)
	commands := action["commands"].([]any)
	if !strings.Contains(commands[0].(string), "TEXT_EDITOR_ALREADY_APPLIED") {
		t.Fatalf("output item = %#v", item)
	}
}

func TestResponsesEndpointStreamsAlreadyAppliedTextEditorAsExecCommand(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/a.java"
	if err := os.WriteFile(path, []byte("old done\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	arguments, _ := json.Marshal(map[string]string{
		"command": "str_replace",
		"path":    path,
		"old_str": "old",
		"new_str": "old done",
	})
	escapedArguments, _ := json.Marshal(string(arguments))
	provider := &fakeProvider{streamEvents: []providers.StreamEvent{
		{Chunk: chatChunk(t, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_2","type":"function","function":{"name":"codex_text_editor","arguments":`+string(escapedArguments)+`}}]}}]}`)},
	}}
	handler := New(testConfig(), map[string]providers.ChatProvider{"fake": provider}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := []byte(`{
		"model":"deepseek-v4-flash",
		"input":"edit two files",
		"tools":[{"type":"custom","name":"apply_patch"}],
		"stream":true
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	events := sseEvents(t, rec.Body.String())
	completed := events[len(events)-1]["response"].(map[string]any)
	output := completed["output"].([]any)
	item := output[0].(map[string]any)
	if item["type"] != "shell_call" {
		t.Fatalf("output item = %#v", item)
	}
	action := item["action"].(map[string]any)
	commands := action["commands"].([]any)
	if !strings.Contains(commands[0].(string), "TEXT_EDITOR_ALREADY_APPLIED") {
		t.Fatalf("output item = %#v", item)
	}
}

func TestResponsesEndpointStreamsBlockedDeepSeekExecCommand(t *testing.T) {
	provider := &fakeProvider{streamEvents: []providers.StreamEvent{
		{Chunk: chatChunk(t, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"exec_command","arguments":"{\"cmd\":\"cat > README.md << 'EOF'\\n"}}]}}]}`)},
		{Chunk: chatChunk(t, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"hello\\nEOF\",\"workdir\":\"/tmp/test\"}"}}]}}]}`)},
		{Chunk: chatChunk(t, `{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120}}`)},
	}}
	handler := New(testConfig(), map[string]providers.ChatProvider{"fake": provider}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := []byte(`{
		"model":"deepseek-v4-flash",
		"input":"create README.md",
		"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"]}}],
		"stream":true
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	events := sseEvents(t, rec.Body.String())
	completed := events[len(events)-1]["response"].(map[string]any)
	output := completed["output"].([]any)
	item := output[0].(map[string]any)
	if item["type"] != "shell_call" {
		t.Fatalf("item = %#v", item)
	}
	action := item["action"].(map[string]any)
	commands := action["commands"].([]any)
	if !strings.Contains(commands[0].(string), "SHELL_FILE_WRITE_BLOCKED") || strings.Contains(commands[0].(string), "cat > README.md") {
		t.Fatalf("item = %#v", item)
	}
}

func testConfig() *config.Config {
	return &config.Config{
		Codex: config.CodexConfig{LocalToken: "local-token"},
		Providers: map[string]config.ProviderConfig{
			"fake": {Profile: adapters.DeepSeekName},
		},
		Models: map[string]config.ModelConfig{
			"deepseek-v4-flash": {
				Provider:           "fake",
				UpstreamModel:      "deepseek-v4-flash",
				ApplyPatchToolType: "freeform",
			},
		},
	}
}

func chatChunk(t *testing.T, raw string) providers.ChatCompletionChunk {
	t.Helper()
	var chunk providers.ChatCompletionChunk
	if err := json.Unmarshal([]byte(raw), &chunk); err != nil {
		t.Fatalf("decode chunk: %v", err)
	}
	return chunk
}

func sseEvents(t *testing.T, body string) []map[string]any {
	t.Helper()
	var events []map[string]any
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			t.Fatalf("decode event: %v", err)
		}
		events = append(events, event)
	}
	return events
}

func findToolCall(messages []providers.ChatMessage, name string) (providers.ChatToolCall, bool) {
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			if call.Function.Name == name {
				return call, true
			}
		}
	}
	return providers.ChatToolCall{}, false
}

func findToolOutput(messages []providers.ChatMessage, callID string) (string, bool) {
	for _, message := range messages {
		if message.Role != "tool" || message.ToolCallID != callID {
			continue
		}
		text, ok := message.Content.(string)
		return text, ok
	}
	return "", false
}
