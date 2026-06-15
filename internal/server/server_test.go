package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/capabilities"
	"codex-bridge/internal/config"
	"codex-bridge/internal/providers"
)

type fakeProvider struct {
	req          providers.ChatCompletionRequest
	reqs         []providers.ChatCompletionRequest
	streamReq    providers.ChatCompletionRequest
	streamEvents []providers.StreamEvent
	responses    []providers.ChatCompletionResponse
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
					Name:      "apply_patch",
					Arguments: `{"input":"*** Begin Patch\n*** Add File: hello.txt\n+hello\n*** End Patch\n"}`,
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

func (p *fakeProvider) Stream(_ context.Context, req providers.ChatCompletionRequest) (<-chan providers.StreamEvent, error) {
	p.streamReq = req
	out := make(chan providers.StreamEvent, len(p.streamEvents)+1)
	go func() {
		defer close(out)
		for _, event := range p.streamEvents {
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
	if len(provider.req.Tools) != 1 || provider.req.Tools[0].Function.Name != "apply_patch" {
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

func TestResponsesEndpointStreamsInternalWebSearchAsFinalMessage(t *testing.T) {
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
				Message: providers.ChatMessage{Role: "assistant", Content: "stream search result used"},
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
	content := item["content"].([]any)[0].(map[string]any)
	if content["text"] != "stream search result used" {
		t.Fatalf("events = %#v", events)
	}
}

func TestResponsesEndpointStreamsApplyPatchAndUsage(t *testing.T) {
	provider := &fakeProvider{streamEvents: []providers.StreamEvent{
		{Chunk: chatChunk(t, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"apply_patch","arguments":"{\"input\":\"*** Begin Patch\\n"}}]}}]}`)},
		{Chunk: chatChunk(t, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"*** Add File: hello.txt\\n+hello\\n*** End Patch\\n\"}"}}]}}]}`)},
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
		"response.custom_tool_call_input.done",
		"response.output_item.done",
		"response.completed",
	}
	if strings.Join(eventTypes, ",") != strings.Join(wantTypes, ",") {
		t.Fatalf("event types = %v", eventTypes)
	}
	doneItem := events[5]["item"].(map[string]any)
	if doneItem["type"] != "custom_tool_call" || doneItem["name"] != "apply_patch" {
		t.Fatalf("done item = %#v", doneItem)
	}
	if doneItem["input"] != "*** Begin Patch\n*** Add File: hello.txt\n+hello\n*** End Patch" {
		t.Fatalf("input = %q", doneItem["input"])
	}
	completed := events[6]["response"].(map[string]any)
	usage := completed["usage"].(map[string]any)
	if usage["input_tokens"] != float64(100) || usage["total_tokens"] != float64(120) {
		t.Fatalf("usage = %#v", usage)
	}
	outputDetails := usage["output_tokens_details"].(map[string]any)
	if outputDetails["reasoning_tokens"] != float64(5) {
		t.Fatalf("output details = %#v", outputDetails)
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
	if item["type"] != "function_call" {
		t.Fatalf("item = %#v", item)
	}
	arguments, _ := item["arguments"].(string)
	if !strings.Contains(arguments, "SHELL_FILE_WRITE_BLOCKED") || strings.Contains(arguments, "cat > README.md") {
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
