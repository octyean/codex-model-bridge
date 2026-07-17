package capabilities

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

func TestMCPProviderSharesSessionAcrossConcurrentCalls(t *testing.T) {
	const sessionID = "session-test"
	var initializeCalls atomic.Int32
	var toolCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", sessionID)
		switch payload.Method {
		case "initialize":
			initializeCalls.Add(1)
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
		case "tools/call":
			if r.Header.Get("Mcp-Session-Id") != sessionID {
				http.Error(w, "missing MCP session", http.StatusConflict)
				return
			}
			toolCalls.Add(1)
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"ok"}]}}`)
		default:
			http.Error(w, "unexpected method", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	provider := NewMCPProvider(server.URL, "", "", "", server.Client())
	const workers = 32
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := provider.Search(context.Background(), "bridge", 5)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if initializeCalls.Load() != 1 {
		t.Fatalf("initialize calls = %d, want 1", initializeCalls.Load())
	}
	if toolCalls.Load() != workers {
		t.Fatalf("tool calls = %d, want %d", toolCalls.Load(), workers)
	}
}
