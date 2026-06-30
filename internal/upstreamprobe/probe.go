package upstreamprobe

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

type Result struct {
	BaseURL               string   `json:"base_url"`
	ModelsURL             string   `json:"models_url"`
	ResponsesURL          string   `json:"responses_url"`
	ChatCompletionsURL    string   `json:"chat_completions_url"`
	Models                []string `json:"models"`
	ModelsOK              bool     `json:"models_ok"`
	ResponsesStreamOK     bool     `json:"responses_stream_ok"`
	ResponsesFirstEventMS int64    `json:"responses_first_event_ms,omitempty"`
	ChatStreamOK          bool     `json:"chat_stream_ok"`
	ChatFirstEventMS      int64    `json:"chat_first_event_ms,omitempty"`
	RecommendedProtocol   string   `json:"recommended_protocol"`
	Error                 string   `json:"error,omitempty"`
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
		result.Error = err.Error()
	}
	probeModel := strings.TrimSpace(options.Model)
	if probeModel == "" && len(models) > 0 {
		probeModel = models[0]
	}
	if probeModel != "" {
		if ms, err := probeResponsesStream(ctx, client, result.ResponsesURL, options.APIKey, probeModel); err == nil {
			result.ResponsesStreamOK = true
			result.ResponsesFirstEventMS = ms
		} else if result.Error == "" {
			result.Error = err.Error()
		}
		if ms, err := probeChatStream(ctx, client, result.ChatCompletionsURL, options.APIKey, probeModel); err == nil {
			result.ChatStreamOK = true
			result.ChatFirstEventMS = ms
		} else if result.Error == "" {
			result.Error = err.Error()
		}
	}
	if result.ResponsesStreamOK {
		result.RecommendedProtocol = "responses"
	} else if result.ChatStreamOK {
		result.RecommendedProtocol = "chat_completions"
	} else {
		result.RecommendedProtocol = "chat_completions"
	}
	return result
}

func listModels(ctx context.Context, client *http.Client, targetURL string, apiKey string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, err
	}
	applyHeaders(req, apiKey)
	resp, err := client.Do(req)
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
	return models, nil
}

func probeResponsesStream(ctx context.Context, client *http.Client, targetURL string, apiKey string, model string) (int64, error) {
	body := map[string]any{
		"model":  model,
		"stream": true,
		"input":  "Reply with ok.",
	}
	return firstSSEEvent(ctx, client, targetURL, apiKey, body)
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
	return firstSSEEvent(ctx, client, targetURL, apiKey, body)
}

func firstSSEEvent(ctx context.Context, client *http.Client, targetURL string, apiKey string, body map[string]any) (int64, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	applyHeaders(req, apiKey)
	req.Header.Set("Accept", "text/event-stream")
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, readHTTPError(resp)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			return time.Since(start).Milliseconds(), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("no SSE data event")
}

func applyHeaders(req *http.Request, apiKey string) {
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
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
