package server

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/capabilities"
	"codex-bridge/internal/codex"
	"codex-bridge/internal/config"
	"codex-bridge/internal/toollog"
	"codex-bridge/internal/tools"
)

func TestPrepareProjectedResponseRequestRemovesUnsupportedCodexOptions(t *testing.T) {
	request, removed := prepareProjectedResponseRequest(map[string]any{
		"client_metadata": map[string]any{"session_id": "session"},
		"reasoning":       map[string]any{"effort": "high"},
		"text":            map[string]any{"verbosity": "high", "format": map[string]any{"type": "json_schema"}},
		"include":         []any{"reasoning.encrypted_content", "message.output_text.logprobs"},
	}, false, true)

	if _, ok := request["client_metadata"]; ok {
		t.Fatalf("client_metadata was not removed")
	}
	if _, ok := request["reasoning"]; ok {
		t.Fatalf("reasoning was not removed")
	}
	text := request["text"].(map[string]any)
	if _, ok := text["verbosity"]; ok {
		t.Fatalf("text.verbosity was not removed")
	}
	if _, ok := text["format"]; !ok {
		t.Fatalf("text.format should be preserved")
	}
	include := request["include"].([]any)
	if len(include) != 1 || include[0] != "message.output_text.logprobs" {
		t.Fatalf("include = %#v", include)
	}
	if len(removed) < 3 {
		t.Fatalf("removed fields = %#v", removed)
	}
}

func TestPrepareProjectedResponseRequestRemovesUnsupportedStructuredFormatOnly(t *testing.T) {
	request, removed := prepareProjectedResponseRequest(map[string]any{
		"reasoning": map[string]any{"effort": "high"},
		"text":      map[string]any{"verbosity": "high", "format": map[string]any{"type": "json_schema"}},
	}, true, false)

	if _, ok := request["reasoning"]; !ok {
		t.Fatalf("reasoning should be preserved")
	}
	text := request["text"].(map[string]any)
	if text["verbosity"] != "high" {
		t.Fatalf("text.verbosity = %#v, want high", text["verbosity"])
	}
	if _, ok := text["format"]; ok {
		t.Fatalf("text.format was not removed")
	}
	if len(removed) != 1 || removed[0] != "text.format" {
		t.Fatalf("removed fields = %#v", removed)
	}
}

func TestProjectedInternalToolFollowUpStaysOnResponses(t *testing.T) {
	toolCtx := tools.Context{}
	tools.AddWebSearchProxy(nil, &toolCtx)
	server := &Server{
		cfg: &config.Config{Capabilities: config.CapabilitiesConfig{
			Search: config.SearchCapabilityConfig{MaxResults: 5},
		}},
		runtime: capabilities.Runtime{Search: staticSearchProvider{}},
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	request := map[string]any{
		"model": "kimi-for-coding",
		"input": []any{map[string]any{"type": "message", "role": "user", "content": "search"}},
		"tools": []any{map[string]any{"type": "function", "name": tools.WebSearchProxyToolName}},
	}
	response := map[string]any{"output": []any{map[string]any{
		"id":      "reasoning_search",
		"type":    "reasoning",
		"summary": []any{},
	}, map[string]any{
		"type":      "function_call",
		"call_id":   "call_search",
		"name":      tools.WebSearchProxyToolName,
		"arguments": `{"action":"search","query":"bridge"}`,
	}}}

	followUp, ok := server.projectedInternalToolFollowUpRequest(
		context.Background(),
		request,
		response,
		toolCtx,
		adapters.Get(adapters.KimiName),
		toollog.OutputContext{RequestID: "req_test", Model: "gpt-5.3-codex", UpstreamModel: "kimi-for-coding", Profile: "kimi"},
	)
	if !ok {
		t.Fatalf("projected internal tool follow-up was not created")
	}
	if followUp["tool_choice"] != "auto" {
		t.Fatalf("tool_choice = %#v, want auto", followUp["tool_choice"])
	}
	if parallel, _ := followUp["parallel_tool_calls"].(bool); parallel {
		t.Fatalf("parallel_tool_calls = true, want false")
	}
	input := followUp["input"].([]any)
	reasoning := input[len(input)-3].(map[string]any)
	if reasoning["type"] != "reasoning" || reasoning["id"] != "reasoning_search" {
		t.Fatalf("reasoning input item = %#v", reasoning)
	}
	last := input[len(input)-1].(map[string]any)
	if last["type"] != "function_call_output" || last["call_id"] != "call_search" || last["output"] != "search result" {
		t.Fatalf("last input item = %#v", last)
	}
}

func TestEnforceProjectedResponseStructuredOutputHandlesDecodedResponseContent(t *testing.T) {
	response := map[string]any{
		"output": []any{map[string]any{
			"id":   "msg_title",
			"type": "message",
			"role": "assistant",
			"content": []any{map[string]any{
				"type": "output_text",
				"text": "Bridge compatibility title",
			}},
		}},
	}
	responseFormat := map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"schema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"title": map[string]any{"type": "string"}},
				"required":   []any{"title"},
			},
		},
	}

	enforceProjectedResponseStructuredOutput(response, responseFormat)

	output := response["output"].([]any)
	message := codex.ResponseItem(output[0].(map[string]any))
	if got := messageOutputText(message); got != `{"title":"Bridge compatibility title"}` {
		t.Fatalf("message output text = %q", got)
	}
}

type staticSearchProvider struct{}

func (staticSearchProvider) Search(context.Context, string, int) (capabilities.SearchResult, error) {
	return capabilities.SearchResult{RawText: "search result"}, nil
}

func (staticSearchProvider) Read(context.Context, string) (string, error) {
	return "read result", nil
}
