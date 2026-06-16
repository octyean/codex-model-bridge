package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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

type OpenAIChatClient struct {
	baseURL   string
	chatURL   string
	respURL   string
	modelsURL string
	apiKey    string
	client    *http.Client
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
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.modelsURL, nil)
	if err != nil {
		return nil, err
	}
	c.applyHeaders(httpReq)
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, readHTTPError(resp)
	}
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
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.respURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.applyHeaders(httpReq)
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, readHTTPError(resp)
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
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.chatURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.applyHeaders(httpReq)
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, readHTTPError(resp)
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

func (c *OpenAIChatClient) doJSON(ctx context.Context, req ChatCompletionRequest, out any) error {
	body, err := json.Marshal(prepareRequest(req))
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.chatURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.applyHeaders(httpReq)
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return readHTTPError(resp)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *OpenAIChatClient) doResponseJSON(ctx context.Context, req map[string]any, out any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.respURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.applyHeaders(httpReq)
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return readHTTPError(resp)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *OpenAIChatClient) applyHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
}

func readHTTPError(resp *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("upstream status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
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
	outputTokens := intValue(usage, "completion_tokens")
	totalTokens := intValue(usage, "total_tokens")
	cachedTokens := intValue(usage, "prompt_cache_hit_tokens")
	freshTokens := intValue(usage, "prompt_cache_miss_tokens")
	reasoningTokens := 0
	if details, ok := usage["prompt_tokens_details"].(map[string]any); ok && cachedTokens == 0 {
		cachedTokens = intValue(details, "cached_tokens")
	}
	if details, ok := usage["completion_tokens_details"].(map[string]any); ok {
		reasoningTokens = intValue(details, "reasoning_tokens")
	}
	if freshTokens == 0 && cachedTokens > 0 && inputTokens > cachedTokens {
		freshTokens = inputTokens - cachedTokens
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
