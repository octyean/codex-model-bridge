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
