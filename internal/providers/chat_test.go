package providers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCreateResponseRetriesOneTransientStatus(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "temporary", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_test","output":[]}`))
	}))
	defer server.Close()

	client := NewOpenAIChatClient(server.URL, "test")
	response, err := client.CreateResponse(context.Background(), map[string]any{"model": "test"})
	if err != nil {
		t.Fatal(err)
	}
	if response["id"] != "resp_test" || calls.Load() != 2 {
		t.Fatalf("response = %#v, calls = %d", response, calls.Load())
	}
}

func TestCreateResponseDoesNotRetryBadRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	client := NewOpenAIChatClient(server.URL, "test")
	_, err := client.CreateResponse(context.Background(), map[string]any{"model": "test"})
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("error = %#v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}

func TestNormalizeUsageSupportsChatAndResponsesSchemas(t *testing.T) {
	tests := []struct {
		name string
		raw  map[string]any
		want NormalizedUsage
	}{
		{
			name: "chat completions",
			raw: map[string]any{
				"prompt_tokens":     120,
				"completion_tokens": 15,
				"total_tokens":      135,
				"prompt_tokens_details": map[string]any{
					"cached_tokens": 100,
				},
				"completion_tokens_details": map[string]any{
					"reasoning_tokens": 8,
				},
			},
			want: NormalizedUsage{
				InputTokens:       120,
				CachedInputTokens: 100,
				FreshInputTokens:  20,
				OutputTokens:      15,
				ReasoningTokens:   8,
				TotalTokens:       135,
			},
		},
		{
			name: "responses",
			raw: map[string]any{
				"input_tokens":  4599,
				"output_tokens": 107,
				"total_tokens":  4706,
				"input_tokens_details": map[string]any{
					"cached_tokens": 4352,
				},
				"output_tokens_details": map[string]any{
					"reasoning_tokens": 99,
				},
			},
			want: NormalizedUsage{
				InputTokens:       4599,
				CachedInputTokens: 4352,
				FreshInputTokens:  247,
				OutputTokens:      107,
				ReasoningTokens:   99,
				TotalTokens:       4706,
			},
		},
		{
			name: "derived totals without cache",
			raw: map[string]any{
				"input_tokens":  10,
				"output_tokens": 2,
			},
			want: NormalizedUsage{
				InputTokens:      10,
				FreshInputTokens: 10,
				OutputTokens:     2,
				TotalTokens:      12,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := NormalizeUsage(test.raw); got != test.want {
				t.Fatalf("NormalizeUsage() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestStreamRequiresTerminalEvent(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantError bool
	}{
		{
			name:      "accept finish reason without done marker",
			body:      "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n",
			wantError: false,
		},
		{
			name:      "reject clean eof without terminal event",
			body:      "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n",
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()

			stream, err := NewOpenAIChatClient(server.URL, "").Stream(context.Background(), ChatCompletionRequest{Model: "test"})
			if err != nil {
				t.Fatal(err)
			}
			var streamErr error
			done := false
			for event := range stream {
				if event.Err != nil {
					streamErr = event.Err
				}
				done = done || event.Done
			}
			if test.wantError {
				if streamErr == nil || !strings.Contains(streamErr.Error(), "before terminal event") {
					t.Fatalf("stream error = %v", streamErr)
				}
				return
			}
			if streamErr != nil || !done {
				t.Fatalf("stream error = %v, done = %v", streamErr, done)
			}
		})
	}
}

func TestStreamResponseRequiresTypedTerminalEvent(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantTerminal string
		wantError    bool
	}{
		{
			name:         "accept completed followed by eof",
			body:         "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"status\":\"completed\"}}\n\n",
			wantTerminal: "response.completed",
		},
		{
			name:         "accept failed followed by eof",
			body:         "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_test\",\"status\":\"failed\"}}\n\n",
			wantTerminal: "response.failed",
		},
		{
			name:         "accept incomplete followed by eof",
			body:         "data: {\"type\":\"response.incomplete\",\"response\":{\"id\":\"resp_test\",\"status\":\"incomplete\"}}\n\n",
			wantTerminal: "response.incomplete",
		},
		{
			name:      "reject partial followed by eof",
			body:      "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n",
			wantError: true,
		},
		{
			name:      "reject done marker without terminal event",
			body:      "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\ndata: [DONE]\n\n",
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()

			stream, err := NewOpenAIChatClient(server.URL, "").StreamResponse(context.Background(), map[string]any{"model": "test"})
			if err != nil {
				t.Fatal(err)
			}
			var streamErr error
			done := false
			terminal := ""
			for event := range stream {
				if event.Err != nil {
					streamErr = event.Err
				}
				if eventType, _ := event.Data["type"].(string); isResponseTerminalEvent(event.Data) {
					terminal = eventType
				}
				done = done || event.Done
			}
			if test.wantError {
				if streamErr == nil || !strings.Contains(streamErr.Error(), "before terminal event") {
					t.Fatalf("stream error = %v", streamErr)
				}
				if done {
					t.Fatal("truncated stream reported done")
				}
				return
			}
			if streamErr != nil || !done || terminal != test.wantTerminal {
				t.Fatalf("stream error = %v, done = %v, terminal = %q", streamErr, done, terminal)
			}
		})
	}
}
