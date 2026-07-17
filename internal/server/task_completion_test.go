package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
	"codex-bridge/internal/config"
	"codex-bridge/internal/optimization"
	"codex-bridge/internal/providers"
	"codex-bridge/internal/toollog"
	"codex-bridge/internal/tools"
)

func TestProjectedTaskEndResultAcceptsValidTermination(t *testing.T) {
	toolCtx := taskEndTestContext()
	response := projectedToolCallResponse(tools.TaskEndToolName, `{"status":"completed","result":"bridge-ok"}`)

	status, result, found, err := projectedTaskEndResult(response, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if !found || status != "completed" || result != "bridge-ok" {
		t.Fatalf("task end = found:%v status:%q result:%q", found, status, result)
	}
}

func TestProjectedTaskEndResultRejectsPlainText(t *testing.T) {
	_, _, _, err := projectedTaskEndResult(projectedAssistantResponse("I will continue later."), taskEndTestContext())
	if !errors.Is(err, errTaskProtocolMissingCall) {
		t.Fatalf("error = %v", err)
	}
}

func TestProjectedTaskEndResultKeepsWorkingWhenCallsAreMixed(t *testing.T) {
	response := map[string]any{"output": []any{
		projectedToolCallResponse(tools.TaskEndToolName, `{"status":"completed","result":"done"}`)["output"].([]any)[0],
		projectedToolCallResponse("read_file", `{}`)["output"].([]any)[0],
	}}
	_, _, ended, err := projectedTaskEndResult(response, taskEndTestContext())
	if err != nil || ended {
		t.Fatalf("mixed calls must continue work, ended=%v error=%v", ended, err)
	}
	filtered := projectedWithoutTaskEndCalls(response, taskEndTestContext())
	output := filtered["output"].([]any)
	if len(output) != 1 || output[0].(map[string]any)["name"] != "read_file" {
		t.Fatalf("filtered output = %#v", output)
	}
}

func TestProjectedTaskProtocolRetryRequiresTool(t *testing.T) {
	request := map[string]any{"input": []any{}, "tools": []any{map[string]any{"type": "function", "name": tools.TaskEndToolName}}}
	followUp := projectedTaskProtocolRetryRequest(request, projectedAssistantResponse("not done"), true)
	if followUp["tool_choice"] != "required" || followUp["parallel_tool_calls"] != false {
		t.Fatalf("retry request = %#v", followUp)
	}
	input := followUp["input"].([]any)
	last := input[len(input)-1].(map[string]any)
	if !strings.Contains(last["content"].(string), "TASK_PROTOCOL_RETRY") {
		t.Fatalf("retry instruction = %#v", last)
	}
}

func TestChatTaskEndResultAcceptsValidTermination(t *testing.T) {
	message := providers.ChatMessage{ToolCalls: []providers.ChatToolCall{{
		Function: providers.ChatCallFunction{Name: tools.TaskEndToolName, Arguments: `{"status":"blocked","result":"need token"}`},
	}}}
	status, result, found, err := chatTaskEndResult(message, taskEndTestContext())
	if err != nil {
		t.Fatal(err)
	}
	if !found || status != "blocked" || result != "need token" {
		t.Fatalf("task end = found:%v status:%q result:%q", found, status, result)
	}
}

func TestChatWithoutTaskEndCallsKeepsWorkCalls(t *testing.T) {
	message := providers.ChatMessage{
		Content:          "use " + tools.TaskEndToolName + " later",
		ReasoningContent: "call " + tools.TaskEndToolName + " when done",
		ToolCalls: []providers.ChatToolCall{
			{Function: providers.ChatCallFunction{Name: tools.TaskEndToolName}},
			{Function: providers.ChatCallFunction{Name: "read_file"}},
		},
	}
	filtered := chatWithoutTaskEndCalls(message, taskEndTestContext())
	if len(filtered.ToolCalls) != 1 || filtered.ToolCalls[0].Function.Name != "read_file" {
		t.Fatalf("tool calls = %#v", filtered.ToolCalls)
	}
	if strings.Contains(messageText(filtered.Content), tools.TaskEndToolName) ||
		strings.Contains(filtered.ReasoningContent, tools.TaskEndToolName) {
		t.Fatalf("internal task tool leaked in message: %#v", filtered)
	}
}

func TestChatTaskProtocolFailsAfterOneRetry(t *testing.T) {
	toolCtx := taskEndTestContext()
	chatTools := tools.AddTaskEndTool(nil, &toolCtx)
	provider := &taskProtocolProvider{
		chatResponses: []*providers.ChatCompletionResponse{
			chatAssistantResponse(t, "still plain text"),
		},
	}
	server := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	_, _, _, err := server.resolveInternalTools(
		context.Background(),
		provider,
		"",
		providers.ChatCompletionRequest{Model: "kimi-for-coding", Tools: chatTools},
		chatAssistantResponse(t, "plain text"),
		toolCtx,
		adapters.Get(adapters.KimiName),
		toollog.OutputContext{RequestID: "req_test", Model: "bridge-model", UpstreamModel: "kimi-for-coding", Profile: "kimi"},
	)
	if !errors.Is(err, errTaskProtocolViolation) {
		t.Fatalf("error = %v, want task protocol violation", err)
	}
	if len(provider.chatRequests) != maxTaskProtocolRetries {
		t.Fatalf("retry requests = %d, want %d", len(provider.chatRequests), maxTaskProtocolRetries)
	}
}

func TestChatStreamTaskProtocolReturnsModelBehaviorError(t *testing.T) {
	provider := &streamSequenceProvider{streams: [][]providers.StreamEvent{
		{streamTextChunk(t, "plain text")},
		{streamTextChunk(t, "still plain text")},
	}}
	server := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/v1/responses", nil)
	toolCtx := tools.Context{}
	chatTools := tools.AddTaskEndTool(nil, &toolCtx)

	server.streamInternalToolResponse(
		recorder,
		request,
		"req_test",
		"",
		codex.ResponsesRequest{Model: "bridge-model", Raw: map[string]any{"model": "bridge-model"}},
		providers.ChatCompletionRequest{Model: "kimi-for-coding", Tools: chatTools},
		provider,
		toolCtx,
		adapters.Get(adapters.KimiName),
		"kimi",
		optimization.Shape{},
		"",
	)

	body := recorder.Body.String()
	if !strings.Contains(body, `"type":"response.failed"`) || !strings.Contains(body, `"type":"model_behavior_error"`) {
		t.Fatalf("stream failure = %s", body)
	}
	if strings.Contains(body, `"type":"response.completed"`) {
		t.Fatalf("protocol violation completed successfully: %s", body)
	}
}

func TestProjectedTaskProtocolReturnsModelBehaviorError(t *testing.T) {
	provider := &taskProtocolProvider{
		responseCreates: []map[string]any{
			projectedAssistantResponse("plain text"),
			projectedAssistantResponse("still plain text"),
		},
	}
	server := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/v1/responses", nil)
	req := taskProtocolResponsesRequest(t, false)

	server.forwardProjectedResponses(
		recorder,
		request,
		"req_test",
		"",
		req,
		config.ModelConfig{UpstreamModel: "kimi-for-coding"},
		provider,
		adapters.Get(adapters.KimiName),
		config.ModelExecutionPlan{Mode: config.ExecutionModeProjectedResponses},
		"",
		"",
	)

	var failure codex.ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &failure); err != nil {
		t.Fatalf("decode failure: %v\n%s", err, recorder.Body.String())
	}
	if recorder.Code != 502 || failure.Error.Type != "model_behavior_error" {
		t.Fatalf("status=%d failure=%#v", recorder.Code, failure)
	}
	if len(provider.responseCreateRequests) != maxTaskProtocolRetries+1 {
		t.Fatalf("upstream calls = %d, want %d", len(provider.responseCreateRequests), maxTaskProtocolRetries+1)
	}
}

func TestProjectedStreamTaskProtocolReturnsModelBehaviorError(t *testing.T) {
	provider := &taskProtocolProvider{
		responseStreams: [][]providers.ResponseStreamEvent{
			projectedAssistantStream("resp_first", "plain text"),
			projectedAssistantStream("resp_second", "still plain text"),
		},
	}
	server := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/v1/responses", nil)
	req := taskProtocolResponsesRequest(t, true)
	toolCtx := tools.Context{}
	chatTools := tools.AddTaskEndTool(nil, &toolCtx)
	upstreamReq := map[string]any{
		"model":               "kimi-for-coding",
		"input":               []any{map[string]any{"type": "message", "role": "user", "content": "do the work"}},
		"tools":               projectedResponseTools(chatTools),
		"parallel_tool_calls": false,
	}

	server.streamProjectedResponses(
		recorder,
		request,
		"req_test",
		"",
		req,
		config.ModelConfig{UpstreamModel: "kimi-for-coding"},
		provider,
		adapters.Get(adapters.KimiName),
		toolCtx,
		upstreamReq,
		nil,
		false,
		"",
	)

	body := recorder.Body.String()
	if !strings.Contains(body, `"type":"response.failed"`) || !strings.Contains(body, `"type":"model_behavior_error"`) {
		t.Fatalf("stream failure = %s", body)
	}
	if strings.Contains(body, `"type":"response.completed"`) {
		t.Fatalf("protocol violation completed successfully: %s", body)
	}
	if len(provider.responseStreamRequests) != maxTaskProtocolRetries+1 {
		t.Fatalf("upstream calls = %d, want %d", len(provider.responseStreamRequests), maxTaskProtocolRetries+1)
	}
}

func taskEndTestContext() tools.Context {
	ctx := tools.Context{}
	tools.AddTaskEndTool(nil, &ctx)
	ctx.Tools["read_file"] = tools.Entry{}
	return ctx
}

func projectedToolCallResponse(name string, arguments string) map[string]any {
	return map[string]any{"output": []any{map[string]any{
		"type":      "function_call",
		"name":      name,
		"call_id":   "call_test",
		"arguments": arguments,
	}}}
}

func projectedAssistantResponse(text string) map[string]any {
	return map[string]any{"id": "resp_test", "status": "completed", "output": []any{map[string]any{
		"type": "message",
		"role": "assistant",
		"content": []any{map[string]any{
			"type": "output_text",
			"text": text,
		}},
	}}}
}

func chatAssistantResponse(t *testing.T, text string) *providers.ChatCompletionResponse {
	t.Helper()
	var response providers.ChatCompletionResponse
	if err := json.Unmarshal([]byte(`{"choices":[{"message":{"role":"assistant","content":`+string(mustJSON(t, text))+`}}]}`), &response); err != nil {
		t.Fatal(err)
	}
	return &response
}

func taskProtocolResponsesRequest(t *testing.T, stream bool) codex.ResponsesRequest {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"model":  "bridge-model",
		"stream": stream,
		"input": []any{map[string]any{
			"type":    "message",
			"role":    "user",
			"content": "do the work",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var req codex.ResponsesRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatal(err)
	}
	return req
}

func projectedAssistantStream(responseID string, text string) []providers.ResponseStreamEvent {
	response := projectedAssistantResponse(text)
	response["id"] = responseID
	return []providers.ResponseStreamEvent{
		{Data: map[string]any{"type": "response.created", "response": map[string]any{"id": responseID}}},
		{Data: map[string]any{"type": "response.completed", "response": response}},
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type taskProtocolProvider struct {
	chatResponses          []*providers.ChatCompletionResponse
	chatRequests           []providers.ChatCompletionRequest
	responseCreates        []map[string]any
	responseCreateRequests []map[string]any
	responseStreams        [][]providers.ResponseStreamEvent
	responseStreamRequests []map[string]any
}

func (p *taskProtocolProvider) Create(_ context.Context, req providers.ChatCompletionRequest) (*providers.ChatCompletionResponse, error) {
	p.chatRequests = append(p.chatRequests, req)
	if len(p.chatResponses) == 0 {
		return nil, errors.New("no chat response configured")
	}
	response := p.chatResponses[0]
	p.chatResponses = p.chatResponses[1:]
	return response, nil
}

func (p *taskProtocolProvider) Stream(context.Context, providers.ChatCompletionRequest) (<-chan providers.StreamEvent, error) {
	return nil, errors.New("chat stream not configured")
}

func (p *taskProtocolProvider) ListModels(context.Context) (*providers.ModelsResponse, error) {
	return &providers.ModelsResponse{}, nil
}

func (p *taskProtocolProvider) CreateResponse(_ context.Context, req map[string]any) (map[string]any, error) {
	p.responseCreateRequests = append(p.responseCreateRequests, req)
	if len(p.responseCreates) == 0 {
		return nil, errors.New("no response configured")
	}
	response := p.responseCreates[0]
	p.responseCreates = p.responseCreates[1:]
	return response, nil
}

func (p *taskProtocolProvider) StreamResponse(_ context.Context, req map[string]any) (<-chan providers.ResponseStreamEvent, error) {
	p.responseStreamRequests = append(p.responseStreamRequests, req)
	if len(p.responseStreams) == 0 {
		return nil, errors.New("no response stream configured")
	}
	events := p.responseStreams[0]
	p.responseStreams = p.responseStreams[1:]
	stream := make(chan providers.ResponseStreamEvent, len(events))
	for _, event := range events {
		stream <- event
	}
	close(stream)
	return stream, nil
}
