package upstreamprobe

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestResultSeparatesUnsupportedAndInconclusiveFailures(t *testing.T) {
	result := Result{}
	result.recordFailure("bad_request", &probeHTTPError{StatusCode: http.StatusBadRequest, Body: "unsupported"})
	result.recordFailure("rate_limit", &probeHTTPError{StatusCode: http.StatusTooManyRequests, Body: "retry later"})
	result.recordFailure("server_error", &probeHTTPError{StatusCode: http.StatusServiceUnavailable, Body: "temporary"})
	result.recordFailure("timeout", context.DeadlineExceeded)

	if len(result.Failures) != 1 || result.Failures["bad_request"] == "" {
		t.Fatalf("failures = %#v", result.Failures)
	}
	for _, stage := range []string{"rate_limit", "server_error", "timeout"} {
		if result.Inconclusive[stage] == "" {
			t.Fatalf("inconclusive = %#v", result.Inconclusive)
		}
	}
	if result.outcome() != ProbeOutcomeInconclusive || result.Cacheable() {
		t.Fatalf("outcome = %q, cacheable = %v", result.outcome(), result.Cacheable())
	}
}

func TestPostJSONRetriesOneTransientStatus(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "temporary", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()

	var response map[string]any
	client := &http.Client{Timeout: time.Second}
	if err := postJSON(context.Background(), client, server.URL, "", map[string]any{"probe": true}, &response); err != nil {
		t.Fatal(err)
	}
	if response["ok"] != true || calls.Load() != 2 {
		t.Fatalf("response = %#v, calls = %d", response, calls.Load())
	}
}

func TestToolProbesForceNamedToolChoice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		assertProbeToolChoice(t, r.URL.Path, body["tool_choice"])
		stream, _ := body["stream"].(bool)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/responses" && stream:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"name\":\"probe_tool\",\"call_id\":\"call_test\",\"arguments\":\"{\\\"value\\\":\\\"ok\\\"}\"}}\n\n")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n")
		case r.URL.Path == "/responses":
			_, _ = io.WriteString(w, `{"output":[{"type":"function_call","name":"probe_tool","call_id":"call_test","arguments":"{\"value\":\"ok\"}"}]}`)
		case r.URL.Path == "/chat/completions" && stream:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"function\":{\"name\":\"probe_tool\",\"arguments\":\"{\\\"value\\\":\\\"ok\\\"}\"}}]}}]}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		default:
			_, _ = io.WriteString(w, `{"choices":[{"message":{"tool_calls":[{"function":{"name":"probe_tool","arguments":"{\"value\":\"ok\"}"}}]}}]}`)
		}
	}))
	defer server.Close()

	client := &http.Client{Timeout: time.Second}
	if _, err := probeResponsesTools(context.Background(), client, server.URL+"/responses", "", "test"); err != nil {
		t.Fatal(err)
	}
	if err := probeResponsesToolStream(context.Background(), client, server.URL+"/responses", "", "test"); err != nil {
		t.Fatal(err)
	}
	if err := probeChatTools(context.Background(), client, server.URL+"/chat/completions", "", "test"); err != nil {
		t.Fatal(err)
	}
	if err := probeChatToolStream(context.Background(), client, server.URL+"/chat/completions", "", "test"); err != nil {
		t.Fatal(err)
	}
}

func assertProbeToolChoice(t *testing.T, path string, raw any) {
	t.Helper()
	choice, _ := raw.(map[string]any)
	if choice["type"] != "function" {
		t.Fatalf("%s tool_choice = %#v", path, raw)
	}
	if path == "/responses" {
		if choice["name"] != "probe_tool" {
			t.Fatalf("%s tool_choice = %#v", path, raw)
		}
		return
	}
	function, _ := choice["function"].(map[string]any)
	if function["name"] != "probe_tool" {
		t.Fatalf("%s tool_choice = %#v", path, raw)
	}
}
