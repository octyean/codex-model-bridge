package providers

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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type ChatProvider interface {
	Create(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error)
	Stream(ctx context.Context, req ChatCompletionRequest) (<-chan StreamEvent, error)
	ListModels(ctx context.Context) (*ModelsResponse, error)
}

type ResponsesProvider interface {
	CreateResponse(ctx context.Context, req map[string]any) (map[string]any, error)
	StreamResponse(ctx context.Context, req map[string]any) (<-chan ResponseStreamEvent, error)
}

type ResponseStreamEvent struct {
	Data map[string]any
	Done bool
	Err  error
}

type HTTPError struct {
	StatusCode int
	Body       string
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("upstream status %d: %s", e.StatusCode, e.Body)
}

type OpenAIChatClient struct {
	baseURL    string
	chatURL    string
	respURL    string
	modelsURL  string
	apiKey     string
	client     *http.Client
	observerMu sync.RWMutex
	observer   func(RetryEvent)
	requests   atomic.Int64
	retried    atomic.Int64
	failures   atomic.Int64
}

type RetryEvent struct {
	Action            string
	Method            string
	URL               string
	RetryCount        int
	Wait              time.Duration
	TotalWait         time.Duration
	StatusCode        int
	Error             string
	TotalRequests     int64
	RetriedRequests   int64
	FailedRequests    int64
	ErrorRatePermille int64
}

type PreparedChatRequestStats struct {
	BodyBytes       int
	EstimatedTokens int
	MessageCount    int
	ToolCount       int
}

func NewOpenAIChatClient(baseURL string, apiKey string) *OpenAIChatClient {
	baseURL = strings.TrimRight(baseURL, "/")
	return &OpenAIChatClient{
		baseURL:   baseURL,
		chatURL:   chatCompletionsURL(baseURL),
		respURL:   responsesURL(baseURL),
		modelsURL: modelsURL(baseURL),
		apiKey:    apiKey,
		client: &http.Client{
			Timeout: 10 * time.Minute,
		},
	}
}

func (c *OpenAIChatClient) SetRetryObserver(observer func(RetryEvent)) {
	c.observerMu.Lock()
	c.observer = observer
	c.observerMu.Unlock()
}

type ModelsResponse struct {
	Object string      `json:"object"`
	Data   []ModelInfo `json:"data"`
}

type ModelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type ChatCompletionRequest struct {
	Model                    string         `json:"model"`
	Messages                 []ChatMessage  `json:"messages"`
	Tools                    []ChatTool     `json:"tools,omitempty"`
	ToolChoice               any            `json:"tool_choice,omitempty"`
	ResponseFormat           any            `json:"response_format,omitempty"`
	Stream                   bool           `json:"stream"`
	StreamOptions            *StreamOptions `json:"stream_options,omitempty"`
	ParallelToolCalls        *bool          `json:"parallel_tool_calls,omitempty"`
	AssistantToolContentNull bool           `json:"-"`
}

type ChatMessage struct {
	Role             string         `json:"role"`
	Content          any            `json:"content,omitempty"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCalls        []ChatToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
}

type ChatTool struct {
	Type     string       `json:"type"`
	Function ChatFunction `json:"function"`
}

type ChatFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type ChatToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ChatCallFunction `json:"function"`
}

type ChatCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ChatCompletionResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int         `json:"index"`
		Message      ChatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage any `json:"usage,omitempty"`
}

type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type NormalizedUsage struct {
	InputTokens       int `json:"input_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
	FreshInputTokens  int `json:"fresh_input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	ReasoningTokens   int `json:"reasoning_tokens"`
	TotalTokens       int `json:"total_tokens"`
}

type StreamEvent struct {
	Chunk ChatCompletionChunk
	Done  bool
	Err   error
}

type ChatCompletionChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role             string `json:"role"`
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage any `json:"usage,omitempty"`
}

func (c *OpenAIChatClient) Create(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	req.Stream = false
	var resp ChatCompletionResponse
	if err := c.doJSON(ctx, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *OpenAIChatClient) CreateResponse(ctx context.Context, req map[string]any) (map[string]any, error) {
	req = cloneMap(req)
	req["stream"] = false
	var resp map[string]any
	if err := c.doResponseJSON(ctx, req, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *OpenAIChatClient) ListModels(ctx context.Context) (*ModelsResponse, error) {
	resp, err := c.doWithRetry(ctx, http.MethodGet, c.modelsURL, nil, "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out ModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *OpenAIChatClient) StreamResponse(ctx context.Context, req map[string]any) (<-chan ResponseStreamEvent, error) {
	req = cloneMap(req)
	req["stream"] = true
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := c.doWithRetry(ctx, http.MethodPost, c.respURL, body, "text/event-stream")
	if err != nil {
		return nil, err
	}

	out := make(chan ResponseStreamEvent, 32)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				out <- ResponseStreamEvent{Done: true}
				return
			}
			var event map[string]any
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				out <- ResponseStreamEvent{Err: err}
				return
			}
			out <- ResponseStreamEvent{Data: event}
		}
		if err := scanner.Err(); err != nil {
			out <- ResponseStreamEvent{Err: err}
		}
	}()
	return out, nil
}

func (c *OpenAIChatClient) Stream(ctx context.Context, req ChatCompletionRequest) (<-chan StreamEvent, error) {
	req.Stream = true
	body, err := json.Marshal(prepareRequest(req))
	if err != nil {
		return nil, err
	}
	resp, err := c.doWithRetry(ctx, http.MethodPost, c.chatURL, body, "text/event-stream")
	if err != nil {
		return nil, err
	}

	out := make(chan StreamEvent, 32)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				out <- StreamEvent{Done: true}
				return
			}
			var chunk ChatCompletionChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				out <- StreamEvent{Err: err}
				return
			}
			out <- StreamEvent{Chunk: chunk}
		}
		if err := scanner.Err(); err != nil {
			out <- StreamEvent{Err: err}
		}
	}()
	return out, nil
}

func ChatRequestStats(req ChatCompletionRequest) PreparedChatRequestStats {
	body, _ := json.Marshal(prepareRequest(req))
	return PreparedChatRequestStats{
		BodyBytes:       len(body),
		EstimatedTokens: estimateWireTokens(body),
		MessageCount:    len(req.Messages),
		ToolCount:       len(req.Tools),
	}
}

func PreparedChatRequest(req ChatCompletionRequest) any {
	return prepareRequest(req)
}

func estimateWireTokens(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	return len(data) / 4
}

func (c *OpenAIChatClient) doJSON(ctx context.Context, req ChatCompletionRequest, out any) error {
	body, err := json.Marshal(prepareRequest(req))
	if err != nil {
		return err
	}
	resp, err := c.doWithRetry(ctx, http.MethodPost, c.chatURL, body, "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *OpenAIChatClient) doResponseJSON(ctx context.Context, req map[string]any, out any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	resp, err := c.doWithRetry(ctx, http.MethodPost, c.respURL, body, "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *OpenAIChatClient) applyHeaders(req *http.Request, accept string) {
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", accept)
}

func (c *OpenAIChatClient) doWithRetry(ctx context.Context, method string, url string, body []byte, accept string) (*http.Response, error) {
	c.requests.Add(1)
	retryCount := 0
	totalWait := time.Duration(0)
	for attempt := 0; attempt < 2; attempt++ {
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, reader)
		if err != nil {
			return nil, err
		}
		c.applyHeaders(req, accept)
		resp, err := c.client.Do(req)
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if retryCount > 0 {
				c.emitRetryEvent(RetryEvent{
					Action:     "recovered",
					Method:     method,
					URL:        url,
					RetryCount: retryCount,
					TotalWait:  totalWait,
				})
			}
			return resp, nil
		}

		var requestErr error
		if err != nil {
			requestErr = err
		} else {
			requestErr = readHTTPError(resp)
			_ = resp.Body.Close()
		}
		if attempt == 1 || !retryableUpstreamError(ctx, requestErr) {
			c.failures.Add(1)
			c.emitRetryEvent(RetryEvent{
				Action:     "failed",
				Method:     method,
				URL:        url,
				RetryCount: retryCount,
				TotalWait:  totalWait,
				StatusCode: upstreamStatusCode(requestErr),
				Error:      requestErr.Error(),
			})
			return nil, requestErr
		}
		delay := retryDelay(requestErr)
		if retryCount == 0 {
			c.retried.Add(1)
		}
		retryCount++
		totalWait += delay
		c.emitRetryEvent(RetryEvent{
			Action:     "retry",
			Method:     method,
			URL:        url,
			RetryCount: retryCount,
			Wait:       delay,
			TotalWait:  totalWait,
			StatusCode: upstreamStatusCode(requestErr),
			Error:      requestErr.Error(),
		})
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, fmt.Errorf("upstream request retry exhausted")
}

func (c *OpenAIChatClient) emitRetryEvent(event RetryEvent) {
	event.TotalRequests = c.requests.Load()
	event.RetriedRequests = c.retried.Load()
	event.FailedRequests = c.failures.Load()
	if event.TotalRequests > 0 {
		event.ErrorRatePermille = event.FailedRequests * 1000 / event.TotalRequests
	}
	c.observerMu.RLock()
	observer := c.observer
	c.observerMu.RUnlock()
	if observer != nil {
		observer(event)
	}
}

func upstreamStatusCode(err error) int {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode
	}
	return 0
}

func readHTTPError(resp *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return &HTTPError{
		StatusCode: resp.StatusCode,
		Body:       strings.TrimSpace(string(data)),
		RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
	}
}

func retryableUpstreamError(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true
		default:
			return false
		}
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

func retryDelay(err error) time.Duration {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) && httpErr.RetryAfter > 0 {
		if httpErr.RetryAfter > 2*time.Second {
			return 2 * time.Second
		}
		return httpErr.RetryAfter
	}
	return 250 * time.Millisecond
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(value); err == nil {
		return time.Until(retryAt)
	}
	return 0
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

type chatCompletionWireRequest struct {
	Model             string            `json:"model"`
	Messages          []chatWireMessage `json:"messages"`
	Tools             []ChatTool        `json:"tools,omitempty"`
	ToolChoice        any               `json:"tool_choice,omitempty"`
	ResponseFormat    any               `json:"response_format,omitempty"`
	Stream            bool              `json:"stream"`
	StreamOptions     *StreamOptions    `json:"stream_options,omitempty"`
	ParallelToolCalls *bool             `json:"parallel_tool_calls,omitempty"`
}

type chatWireMessage struct {
	Role             string         `json:"role"`
	Content          any            `json:"content"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCalls        []ChatToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
}

func prepareRequest(req ChatCompletionRequest) chatCompletionWireRequest {
	return chatCompletionWireRequest{
		Model:             req.Model,
		Messages:          wireMessages(req.Messages, req.AssistantToolContentNull),
		Tools:             req.Tools,
		ToolChoice:        req.ToolChoice,
		ResponseFormat:    req.ResponseFormat,
		Stream:            req.Stream,
		StreamOptions:     req.StreamOptions,
		ParallelToolCalls: req.ParallelToolCalls,
	}
}

func wireMessages(messages []ChatMessage, assistantToolContentNull bool) []chatWireMessage {
	out := make([]chatWireMessage, 0, len(messages))
	for _, message := range messages {
		var content any = message.Content
		if content == nil {
			content = ""
		}
		if assistantToolContentNull && message.Role == "assistant" && len(message.ToolCalls) > 0 && emptyContent(message.Content) {
			content = nil
		}
		out = append(out, chatWireMessage{
			Role:             message.Role,
			Content:          content,
			ReasoningContent: message.ReasoningContent,
			ToolCalls:        message.ToolCalls,
			ToolCallID:       message.ToolCallID,
		})
	}
	return out
}

func emptyContent(content any) bool {
	if content == nil {
		return true
	}
	text, ok := content.(string)
	return ok && text == ""
}

func NormalizeUsage(raw any) NormalizedUsage {
	usage := usageObject(raw)
	inputTokens := intValue(usage, "prompt_tokens")
	if inputTokens == 0 {
		inputTokens = intValue(usage, "input_tokens")
	}
	outputTokens := intValue(usage, "completion_tokens")
	if outputTokens == 0 {
		outputTokens = intValue(usage, "output_tokens")
	}
	totalTokens := intValue(usage, "total_tokens")
	cachedTokens := intValue(usage, "prompt_cache_hit_tokens")
	freshTokens := intValue(usage, "prompt_cache_miss_tokens")
	reasoningTokens := 0
	if details, ok := usage["prompt_tokens_details"].(map[string]any); ok && cachedTokens == 0 {
		cachedTokens = intValue(details, "cached_tokens")
	}
	if details, ok := usage["input_tokens_details"].(map[string]any); ok && cachedTokens == 0 {
		cachedTokens = intValue(details, "cached_tokens")
	}
	if details, ok := usage["completion_tokens_details"].(map[string]any); ok {
		reasoningTokens = intValue(details, "reasoning_tokens")
	}
	if details, ok := usage["output_tokens_details"].(map[string]any); ok && reasoningTokens == 0 {
		reasoningTokens = intValue(details, "reasoning_tokens")
	}
	if freshTokens == 0 && inputTokens >= cachedTokens {
		freshTokens = inputTokens - cachedTokens
	}
	if totalTokens == 0 {
		totalTokens = inputTokens + outputTokens
	}
	return NormalizedUsage{
		InputTokens:       inputTokens,
		CachedInputTokens: cachedTokens,
		FreshInputTokens:  freshTokens,
		OutputTokens:      outputTokens,
		ReasoningTokens:   reasoningTokens,
		TotalTokens:       totalTokens,
	}
}

func usageObject(raw any) map[string]any {
	switch value := raw.(type) {
	case map[string]any:
		return value
	case nil:
		return map[string]any{}
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return map[string]any{}
		}
		var obj map[string]any
		if err := json.Unmarshal(data, &obj); err != nil {
			return map[string]any{}
		}
		return obj
	}
}

func intValue(obj map[string]any, key string) int {
	switch value := obj[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case json.Number:
		i, _ := value.Int64()
		return int(i)
	default:
		return 0
	}
}

func cloneMap(value map[string]any) map[string]any {
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}
