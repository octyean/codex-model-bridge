package providers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
