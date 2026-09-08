package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
	"codex-bridge/internal/config"
	"codex-bridge/internal/diagnostics"
	"codex-bridge/internal/optimization"
	"codex-bridge/internal/providers"
	"codex-bridge/internal/toollog"
)

func TestLogUsageIncludesExecutionContextAndSeparatesUpstreamModels(t *testing.T) {
	var logs bytes.Buffer
	server := &Server{
		logger:    slog.New(slog.NewJSONHandler(&logs, nil)),
		optimizer: optimization.NewTracker(),
	}
	usage := providers.NormalizedUsage{InputTokens: 10, OutputTokens: 2, TotalTokens: 12}
	adapter := adapters.Get(adapters.KimiName)
	firstShape := optimization.Shape{SystemHash: "system-a", ToolsHash: "tools-a", PrefixHash: "prefix-a"}
	secondShape := optimization.Shape{SystemHash: "system-b", ToolsHash: "tools-b", PrefixHash: "prefix-b"}

	server.logUsage("req_test", "gpt-5.3-codex", "upstream-a", "kimi", config.ExecutionModeProjectedResponses, "initial", 0, adapter, firstShape, usage)
	server.logUsage("req_test", "gpt-5.3-codex", "upstream-b", "kimi", config.ExecutionModeProjectedResponses, "initial", 0, adapter, secondShape, usage)
	server.logUsage("req_test", "gpt-5.3-codex", "upstream-a", "kimi", config.ExecutionModeProjectedResponses, "initial", 0, adapter, secondShape, usage)

	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("log lines = %d\n%s", len(lines), logs.String())
	}
	records := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	if records[0]["upstream_model"] != "upstream-a" ||
		records[0]["execution_mode"] != config.ExecutionModeProjectedResponses ||
		records[0]["stage"] != "initial" ||
		records[0]["sequence"] != float64(0) {
		t.Fatalf("first usage log = %#v", records[0])
	}
	if records[0]["prefix_changed"] != false || records[1]["prefix_changed"] != false {
		t.Fatalf("different upstream models shared tracker state: %#v", records)
	}
	if records[2]["prefix_changed"] != true {
		t.Fatalf("same upstream model did not retain tracker state: %#v", records[2])
	}
}

func TestIncidentRecordIncludesCodexSessionID(t *testing.T) {
	request := httptest.NewRequest("POST", "/v1/responses", nil)
	request.Header.Set("X-Codex-Thread-Id", "thread-test")
	server := &Server{cfg: &config.Config{
		Providers: map[string]config.ProviderConfig{
			"upstream": {Protocol: "responses"},
		},
		Models: map[string]config.ModelConfig{
			"gpt-5.3-codex": {
				Provider:      "upstream",
				Profile:       "kimi",
				UpstreamModel: "kimi-for-coding",
			},
		},
	}}
	record := server.incidentRecord(
		request,
		codex.ResponsesRequest{Model: "gpt-5.3-codex", Raw: map[string]any{}},
		"req_test",
		"kimi",
		"",
		nil,
	)

	if record["codex_session_id"] != "thread-test" {
		t.Fatalf("incident record = %#v", record)
	}
	if record["upstream_model"] != "kimi-for-coding" || record["execution_mode"] != config.ExecutionModeProjectedResponses {
		t.Fatalf("incident execution context = %#v", record)
	}
}

func TestNativeResponsesStreamWritesTerminalResponseLogs(t *testing.T) {
	dir := t.TempDir()
	toolLogPath := filepath.Join(dir, "tool-calls.jsonl")
	t.Setenv(toollog.EnvToolLogPath, toolLogPath)
	sessionID := "thread-native"
	response := map[string]any{
		"id":     "resp_test",
		"model":  "gpt-upstream",
		"status": "completed",
		"output": []any{},
	}
	provider := &taskProtocolProvider{responseStreams: [][]providers.ResponseStreamEvent{{
		{Data: map[string]any{"type": "response.completed", "response": response}},
	}}}
	server := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/v1/responses", nil)

	server.forwardResponses(
		recorder,
		request,
		"req_test",
		sessionID,
		codex.ResponsesRequest{Model: "bridge-model", Stream: true, Raw: map[string]any{"model": "bridge-model", "stream": true}},
		config.ModelConfig{UpstreamModel: "gpt-upstream"},
		provider,
		adapters.Get(adapters.OpenAIName),
		"",
	)

	for _, fileName := range []string{"prompt-responses.jsonl", "bridge-responses.jsonl"} {
		path := diagnostics.SessionLogPath(toolLogPath, sessionID, fileName)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &record); err != nil {
			t.Fatal(err)
		}
		if record["terminal_type"] != "response.completed" || record["event_count"] != float64(1) {
			t.Fatalf("%s record = %#v", fileName, record)
		}
		body, _ := record["body"].(map[string]any)
		if body["id"] != "resp_test" || body["model"] != "bridge-model" || body["status"] != "completed" {
			t.Fatalf("%s body = %#v", fileName, body)
		}
	}
	if body := recorder.Body.String(); !strings.Contains(body, `"type":"response.completed"`) || !strings.Contains(body, `"model":"bridge-model"`) {
		t.Fatalf("stream body = %s", body)
	}
}
