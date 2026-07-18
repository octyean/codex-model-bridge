package upstreamprobe

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	ProbeVersion             = 3
	ProbeOutcomeSupported    = "supported"
	ProbeOutcomeUnsupported  = "unsupported"
	ProbeOutcomeInconclusive = "inconclusive"
)

type Result struct {
	ProbeVersion                int               `json:"probe_version"`
	Outcome                     string            `json:"outcome"`
	BaseURL                     string            `json:"base_url"`
	ModelsURL                   string            `json:"models_url"`
	ResponsesURL                string            `json:"responses_url"`
	ChatCompletionsURL          string            `json:"chat_completions_url"`
	ProbeModel                  string            `json:"probe_model,omitempty"`
	Models                      []string          `json:"models"`
	ModelsOK                    bool              `json:"models_ok"`
	ResponsesStreamOK           bool              `json:"responses_stream_ok"`
	ResponsesToolsOK            bool              `json:"responses_tools_ok"`
	ResponsesToolStreamOK       bool              `json:"responses_tool_stream_ok"`
	ResponsesToolContinuationOK bool              `json:"responses_tool_continuation_ok"`
	ResponsesOptionsOK          bool              `json:"responses_options_ok"`
	ResponsesStructuredOutputOK bool              `json:"responses_structured_output_ok"`
	ResponsesFirstEventMS       int64             `json:"responses_first_event_ms,omitempty"`
	ChatStreamOK                bool              `json:"chat_stream_ok"`
	ChatToolsOK                 bool              `json:"chat_tools_ok"`
	ChatToolStreamOK            bool              `json:"chat_tool_stream_ok"`
	ChatFirstEventMS            int64             `json:"chat_first_event_ms,omitempty"`
	RecommendedProtocol         string            `json:"recommended_protocol"`
	Failures                    map[string]string `json:"failures,omitempty"`
	Inconclusive                map[string]string `json:"inconclusive,omitempty"`
	Error                       string            `json:"error,omitempty"`
}

type Options struct {
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
}

func Run(ctx context.Context, options Options) Result {
	timeout := options.Timeout
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	baseURL := strings.TrimRight(strings.TrimSpace(options.BaseURL), "/")
	result := Result{
		ProbeVersion:       ProbeVersion,
		BaseURL:            baseURL,
		ModelsURL:          modelsURL(baseURL),
		ResponsesURL:       responsesURL(baseURL),
		ChatCompletionsURL: chatCompletionsURL(baseURL),
	}
	models, err := listModels(ctx, client, result.ModelsURL, options.APIKey)
	if err == nil {
		result.ModelsOK = true
		result.Models = models
	} else {
		result.recordFailure("models", err)
	}
	probeModel := strings.TrimSpace(options.Model)
	if probeModel == "" && len(models) > 0 {
		probeModel = models[0]
	}
	result.ProbeModel = probeModel
	if probeModel != "" {
		if ms, err := probeResponsesStream(ctx, client, result.ResponsesURL, options.APIKey, probeModel); err == nil {
			result.ResponsesStreamOK = true
			result.ResponsesFirstEventMS = ms
		} else {
			result.recordFailure("responses_stream", err)
		}
		call, err := probeResponsesTools(ctx, client, result.ResponsesURL, options.APIKey, probeModel)
		if err == nil {
			result.ResponsesToolsOK = true
		} else {
			result.recordFailure("responses_tools", err)
		}
		if err := probeResponsesToolStream(ctx, client, result.ResponsesURL, options.APIKey, probeModel); err == nil {
			result.ResponsesToolStreamOK = true
		} else {
			result.recordFailure("responses_tool_stream", err)
		}
		if call != nil {
			if err := probeResponsesToolContinuation(ctx, client, result.ResponsesURL, options.APIKey, probeModel, call); err == nil {
				result.ResponsesToolContinuationOK = true
			} else {
				result.recordFailure("responses_tool_continuation", err)
			}
		}
		if err := probeResponsesOptions(ctx, client, result.ResponsesURL, options.APIKey, probeModel); err == nil {
			result.ResponsesOptionsOK = true
		} else {
			result.recordFailure("responses_options", err)
		}
		if err := probeResponsesStructuredOutput(ctx, client, result.ResponsesURL, options.APIKey, probeModel); err == nil {
			result.ResponsesStructuredOutputOK = true
		} else {
			result.recordFailure("responses_structured_output", err)
		}
		if ms, err := probeChatStream(ctx, client, result.ChatCompletionsURL, options.APIKey, probeModel); err == nil {
			result.ChatStreamOK = true
			result.ChatFirstEventMS = ms
		} else {
			result.recordFailure("chat_stream", err)
		}
		if err := probeChatTools(ctx, client, result.ChatCompletionsURL, options.APIKey, probeModel); err == nil {
			result.ChatToolsOK = true
		} else {
			result.recordFailure("chat_tools", err)
		}
		if err := probeChatToolStream(ctx, client, result.ChatCompletionsURL, options.APIKey, probeModel); err == nil {
			result.ChatToolStreamOK = true
		} else {
			result.recordFailure("chat_tool_stream", err)
		}
	} else {
		result.recordFailure("probe_model", fmt.Errorf("no model was provided and /models returned no usable model"))
	}
	if result.ResponsesReady() {
		result.RecommendedProtocol = "responses"
	} else if result.ChatReady() {
		result.RecommendedProtocol = "chat_completions"
	} else {
		result.RecommendedProtocol = "chat_completions"
	}
	result.Outcome = result.outcome()
	if result.Outcome != ProbeOutcomeSupported {
		result.Error = result.failureSummary()
	}
	return result
}

func (r Result) ResponsesReady() bool {
	return r.ResponsesStreamOK && r.ResponsesToolsOK && r.ResponsesToolStreamOK && r.ResponsesToolContinuationOK
}

func (r Result) ChatReady() bool {
	return r.ChatStreamOK && r.ChatToolsOK && r.ChatToolStreamOK
}

func (r Result) Cacheable() bool {
	return strings.TrimSpace(r.ProbeModel) != "" && len(r.Inconclusive) == 0
}

func (r *Result) recordFailure(stage string, err error) {
	if err == nil {
		return
	}
	if isInconclusiveError(err) {
		if r.Inconclusive == nil {
			r.Inconclusive = map[string]string{}
		}
		r.Inconclusive[stage] = err.Error()
		return
	}
	if r.Failures == nil {
		r.Failures = map[string]string{}
	}
	r.Failures[stage] = err.Error()
}

func (r Result) outcome() string {
	if len(r.Inconclusive) > 0 {
		return ProbeOutcomeInconclusive
	}
	if r.ResponsesReady() || r.ChatReady() {
		return ProbeOutcomeSupported
	}
	return ProbeOutcomeUnsupported
}

func (r Result) failureSummary() string {
	failures := make(map[string]string, len(r.Failures)+len(r.Inconclusive))
	for stage, message := range r.Failures {
		failures[stage] = message
	}
	for stage, message := range r.Inconclusive {
		failures[stage] = message
	}
	if len(failures) == 0 {
		return "no compatible upstream protocol was detected"
	}
	stages := make([]string, 0, len(failures))
	for stage := range failures {
		stages = append(stages, stage)
	}
	sort.Strings(stages)
	stage := stages[0]
	return stage + ": " + failures[stage]
}

func listModels(ctx context.Context, client *http.Client, targetURL string, apiKey string) ([]string, error) {
	resp, err := doProbeRequest(ctx, client, http.MethodGet, targetURL, apiKey, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, readHTTPError(resp)
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(payload.Data))
	for _, item := range payload.Data {
		if id := strings.TrimSpace(item.ID); id != "" {
			models = append(models, id)
		}
	}
	sort.Strings(models)
	return models, nil
}

func probeResponsesStream(ctx context.Context, client *http.Client, targetURL string, apiKey string, model string) (int64, error) {
	body := map[string]any{
		"model":  model,
		"stream": true,
		"input":  "Reply with ok.",
	}
	completed := false
	firstEventMS, _, err := streamJSONEvents(ctx, client, targetURL, apiKey, body, func(event map[string]any) error {
		switch event["type"] {
		case "response.completed":
			completed = true
		case "response.failed":
			return fmt.Errorf("responses stream returned response.failed")
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if !completed {
		return 0, fmt.Errorf("responses stream ended without response.completed")
	}
	return firstEventMS, nil
}

func probeChatStream(ctx context.Context, client *http.Client, targetURL string, apiKey string, model string) (int64, error) {
	body := map[string]any{
		"model":  model,
		"stream": true,
		"messages": []map[string]string{{
			"role":    "user",
			"content": "Reply with ok.",
		}},
	}
	firstEventMS, sawDone, err := streamJSONEvents(ctx, client, targetURL, apiKey, body, nil)
	if err != nil {
		return 0, err
	}
	if !sawDone {
		return 0, fmt.Errorf("chat stream ended without [DONE]")
	}
	return firstEventMS, nil
}

func probeResponsesTools(ctx context.Context, client *http.Client, targetURL string, apiKey string, model string) (map[string]any, error) {
	body := map[string]any{
		"model":       model,
		"input":       "Call probe_tool with value ok. After receiving the tool output, reply with probe-complete.",
		"tools":       responsesProbeTools(),
		"tool_choice": map[string]any{"type": "function", "name": "probe_tool"},
	}
	var response map[string]any
	if err := postJSON(ctx, client, targetURL, apiKey, body, &response); err != nil {
		return nil, err
	}
	call := responseFunctionCall(response)
	if call == nil {
		return nil, fmt.Errorf("responses probe returned no function_call")
	}
	return call, nil
}

func probeChatTools(ctx context.Context, client *http.Client, targetURL string, apiKey string, model string) error {
	body := map[string]any{
		"model": model,
		"messages": []map[string]string{{
			"role":    "user",
			"content": "Call probe_tool with value ok. Do not answer in normal text.",
		}},
		"tools":       chatProbeTools(),
		"tool_choice": map[string]any{"type": "function", "function": map[string]any{"name": "probe_tool"}},
	}
	var response map[string]any
	if err := postJSON(ctx, client, targetURL, apiKey, body, &response); err != nil {
		return err
	}
	choices, _ := response["choices"].([]any)
	for _, rawChoice := range choices {
		choice, _ := rawChoice.(map[string]any)
		message, _ := choice["message"].(map[string]any)
		toolCalls, _ := message["tool_calls"].([]any)
		for _, rawCall := range toolCalls {
			call, _ := rawCall.(map[string]any)
			function, _ := call["function"].(map[string]any)
			if function["name"] == "probe_tool" {
				return nil
			}
		}
	}
	return fmt.Errorf("chat probe returned no tool_call")
}

func probeResponsesToolStream(ctx context.Context, client *http.Client, targetURL string, apiKey string, model string) error {
	body := map[string]any{
		"model":       model,
		"stream":      true,
		"input":       "Call probe_tool with value ok. Do not answer in normal text.",
		"tools":       responsesProbeTools(),
		"tool_choice": map[string]any{"type": "function", "name": "probe_tool"},
	}
	var call map[string]any
	completed := false
	_, _, err := streamJSONEvents(ctx, client, targetURL, apiKey, body, func(event map[string]any) error {
		switch event["type"] {
		case "response.output_item.done":
			item, _ := event["item"].(map[string]any)
			if item["type"] == "function_call" && item["name"] == "probe_tool" {
				call = item
			}
		case "response.completed":
			completed = true
			if response, ok := event["response"].(map[string]any); ok && call == nil {
				call = responseFunctionCall(response)
			}
		case "response.failed":
			return fmt.Errorf("responses tool stream returned response.failed")
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !completed {
		return fmt.Errorf("responses tool stream ended without response.completed")
	}
	if call == nil {
		return fmt.Errorf("responses tool stream returned no complete function_call")
	}
	if strings.TrimSpace(responseCallArguments(call)) == "" {
		return fmt.Errorf("responses tool stream returned empty function_call arguments")
	}
	return nil
}

func probeResponsesToolContinuation(ctx context.Context, client *http.Client, targetURL string, apiKey string, model string, call map[string]any) error {
	callID := responseCallID(call)
	if callID == "" {
		return fmt.Errorf("responses function_call has no call_id")
	}
	body := map[string]any{
		"model": model,
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": "Call probe_tool with value ok. After receiving the tool output, reply with probe-complete."},
			map[string]any{
				"type":      "function_call",
				"call_id":   callID,
				"name":      "probe_tool",
				"arguments": responseCallArguments(call),
				"status":    "completed",
			},
			map[string]any{"type": "function_call_output", "call_id": callID, "output": "ok"},
		},
		"tools": responsesProbeTools(),
	}
	var response map[string]any
	if err := postJSON(ctx, client, targetURL, apiKey, body, &response); err != nil {
		return err
	}
	if strings.TrimSpace(responseOutputText(response)) == "" {
		return fmt.Errorf("responses continuation returned no assistant text")
	}
	return nil
}

func probeResponsesOptions(ctx context.Context, client *http.Client, targetURL string, apiKey string, model string) error {
	body := map[string]any{
		"model":     model,
		"input":     "Reply with ok.",
		"reasoning": map[string]any{"effort": "low", "summary": "auto"},
		"text":      map[string]any{"verbosity": "low"},
	}
	var response map[string]any
	if err := postJSON(ctx, client, targetURL, apiKey, body, &response); err != nil {
		return err
	}
	if strings.TrimSpace(responseOutputText(response)) == "" {
		return fmt.Errorf("responses options probe returned no assistant text")
	}
	return nil
}

func probeResponsesStructuredOutput(ctx context.Context, client *http.Client, targetURL string, apiKey string, model string) error {
	body := map[string]any{
		"model": model,
		"input": "Return JSON with ok set to true.",
		"text": map[string]any{"format": map[string]any{
			"type":   "json_schema",
			"name":   "probe_result",
			"strict": true,
			"schema": map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"ok": map[string]any{"type": "boolean"}},
				"required":             []string{"ok"},
				"additionalProperties": false,
			},
		}},
	}
	var response map[string]any
	if err := postJSON(ctx, client, targetURL, apiKey, body, &response); err != nil {
		return err
	}
	text := strings.TrimSpace(responseOutputText(response))
	var value map[string]any
	if text == "" || json.Unmarshal([]byte(text), &value) != nil {
		return fmt.Errorf("responses structured output probe returned invalid JSON")
	}
	if ok, _ := value["ok"].(bool); !ok {
		return fmt.Errorf("responses structured output probe returned unexpected JSON")
	}
	return nil
}

func probeChatToolStream(ctx context.Context, client *http.Client, targetURL string, apiKey string, model string) error {
	body := map[string]any{
		"model":  model,
		"stream": true,
		"messages": []map[string]string{{
			"role":    "user",
			"content": "Call probe_tool with value ok. Do not answer in normal text.",
		}},
		"tools":       chatProbeTools(),
		"tool_choice": map[string]any{"type": "function", "function": map[string]any{"name": "probe_tool"}},
	}
	name := ""
	arguments := ""
	_, sawDone, err := streamJSONEvents(ctx, client, targetURL, apiKey, body, func(event map[string]any) error {
		choices, _ := event["choices"].([]any)
		for _, rawChoice := range choices {
			choice, _ := rawChoice.(map[string]any)
			delta, _ := choice["delta"].(map[string]any)
			toolCalls, _ := delta["tool_calls"].([]any)
			for _, rawCall := range toolCalls {
				call, _ := rawCall.(map[string]any)
				function, _ := call["function"].(map[string]any)
				if value, _ := function["name"].(string); value != "" {
					name = value
				}
				if value, _ := function["arguments"].(string); value != "" {
					arguments += value
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !sawDone {
		return fmt.Errorf("chat tool stream ended without [DONE]")
	}
	if name != "probe_tool" || strings.TrimSpace(arguments) == "" {
		return fmt.Errorf("chat tool stream returned no complete tool_call")
	}
	return nil
}

func responsesProbeTools() []map[string]any {
	return []map[string]any{{
		"type":        "function",
		"name":        "probe_tool",
		"description": "Protocol capability probe with no side effects.",
		"parameters": map[string]any{
			"type":                 "object",
			"properties":           map[string]any{"value": map[string]any{"type": "string"}},
			"required":             []string{"value"},
			"additionalProperties": false,
		},
	}}
}

func chatProbeTools() []map[string]any {
	return []map[string]any{{
		"type": "function",
		"function": map[string]any{
			"name":        "probe_tool",
			"description": "Protocol capability probe with no side effects.",
			"parameters": map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"value": map[string]any{"type": "string"}},
				"required":             []string{"value"},
				"additionalProperties": false,
			},
		},
	}}
}

func responseFunctionCall(response map[string]any) map[string]any {
	output, _ := response["output"].([]any)
	for _, rawItem := range output {
		item, _ := rawItem.(map[string]any)
		if item["type"] == "function_call" && item["name"] == "probe_tool" {
			return item
		}
	}
	return nil
}

func responseCallID(call map[string]any) string {
	if value, _ := call["call_id"].(string); value != "" {
		return value
	}
	value, _ := call["id"].(string)
	return value
}

func responseCallArguments(call map[string]any) string {
	if value, _ := call["arguments"].(string); value != "" {
		return value
	}
	data, _ := json.Marshal(call["arguments"])
	if string(data) == "null" {
		return ""
	}
	return string(data)
}

func responseOutputText(response map[string]any) string {
	output, _ := response["output"].([]any)
	var text strings.Builder
	for _, rawItem := range output {
		item, _ := rawItem.(map[string]any)
		content, _ := item["content"].([]any)
		for _, rawPart := range content {
			part, _ := rawPart.(map[string]any)
			if value, _ := part["text"].(string); value != "" {
				text.WriteString(value)
			}
		}
	}
	return text.String()
}

func postJSON(ctx context.Context, client *http.Client, targetURL string, apiKey string, body map[string]any, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := doProbeRequest(ctx, client, http.MethodPost, targetURL, apiKey, data, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return readHTTPError(resp)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func streamJSONEvents(ctx context.Context, client *http.Client, targetURL string, apiKey string, body map[string]any, visit func(map[string]any) error) (int64, bool, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return 0, false, err
	}
	start := time.Now()
	resp, err := doProbeRequest(ctx, client, http.MethodPost, targetURL, apiKey, data, "text/event-stream")
	if err != nil {
		return 0, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, false, readHTTPError(resp)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	firstEventMS := int64(-1)
	sawDone := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if firstEventMS < 0 {
			firstEventMS = time.Since(start).Milliseconds()
		}
		if data == "[DONE]" {
			sawDone = true
			break
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return 0, false, err
		}
		if visit != nil {
			if err := visit(event); err != nil {
				return 0, false, err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, false, err
	}
	if firstEventMS < 0 {
		return 0, sawDone, fmt.Errorf("no SSE data event")
	}
	return firstEventMS, sawDone, nil
}

func applyHeaders(req *http.Request, apiKey string) {
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
}

type probeHTTPError struct {
	StatusCode int
	Body       string
}

func (e *probeHTTPError) Error() string {
	return fmt.Sprintf("upstream status %d: %s", e.StatusCode, e.Body)
}

func readHTTPError(resp *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return &probeHTTPError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(data))}
}

func doProbeRequest(ctx context.Context, client *http.Client, method string, targetURL string, apiKey string, body []byte, accept string) (*http.Response, error) {
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, targetURL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		applyHeaders(req, apiKey)
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		resp, err := client.Do(req)
		if attempt == 1 || !shouldRetryProbe(ctx, resp, err) {
			return resp, err
		}
		if resp != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, errors.New("probe request retry exhausted")
}

func shouldRetryProbe(ctx context.Context, resp *http.Response, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	if err != nil {
		var networkError net.Error
		return errors.As(err, &networkError)
	}
	if resp == nil {
		return false
	}
	return resp.StatusCode == http.StatusRequestTimeout ||
		resp.StatusCode == http.StatusTooEarly ||
		resp.StatusCode == http.StatusTooManyRequests ||
		resp.StatusCode >= http.StatusInternalServerError
}

func isInconclusiveError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var httpErr *probeHTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case http.StatusBadRequest, http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusUnprocessableEntity:
			return false
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests:
			return true
		default:
			return httpErr.StatusCode >= http.StatusInternalServerError || httpErr.StatusCode >= http.StatusBadRequest
		}
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return true
	}
	var urlError *url.Error
	return errors.As(err, &urlError)
}

func chatCompletionsURL(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	if strings.HasSuffix(base, "/responses") {
		base = strings.TrimSuffix(base, "/responses")
	}
	return base + "/chat/completions"
}

func responsesURL(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/responses") {
		return base
	}
	if strings.HasSuffix(base, "/chat/completions") {
		base = strings.TrimSuffix(base, "/chat/completions")
	}
	return base + "/responses"
}

func modelsURL(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/chat/completions") {
		base = strings.TrimSuffix(base, "/chat/completions")
	}
	if strings.HasSuffix(base, "/responses") {
		base = strings.TrimSuffix(base, "/responses")
	}
	if strings.HasSuffix(base, "/models") {
		return base
	}
	return base + "/models"
}
