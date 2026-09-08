package server

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/toolruntime"
	"codex-bridge/internal/tools"
)

func TestResponseItemFromToolCallProjectsToolKinds(t *testing.T) {
	tests := []struct {
		name      string
		entry     tools.Entry
		arguments string
		wantType  string
		wantName  string
	}{
		{
			name:      "function",
			entry:     tools.Entry{Descriptor: adapters.ToolDescriptor{Name: "lookup", Kind: tools.KindFunction}},
			arguments: `{"id":1}`,
			wantType:  "function_call",
			wantName:  "lookup",
		},
		{
			name:      "tool search",
			entry:     tools.Entry{Descriptor: adapters.ToolDescriptor{Name: "tool_search", Kind: tools.KindToolSearch}},
			arguments: `{"query":"docs"}`,
			wantType:  "tool_search_call",
		},
		{
			name: "text editor",
			entry: tools.Entry{
				Descriptor:   adapters.ToolDescriptor{Name: "apply_patch", Kind: tools.KindTextEditor},
				UpstreamName: tools.TextEditorWriteToolName,
			},
			arguments: `{"path":"a.txt","file_text":"ok"}`,
			wantType:  "custom_tool_call",
			wantName:  "apply_patch",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requestID := "request-" + t.Name()
			t.Cleanup(func() { toolruntime.ForgetRequest(requestID) })
			item := responseItemFromToolCall(
				context.Background(),
				"call-test",
				test.entry,
				test.arguments,
				tools.Context{},
				adapters.Get(adapters.DefaultName),
				requestID,
				"model",
				"default",
				slog.New(slog.NewTextHandler(io.Discard, nil)),
				nil,
			)
			if item["type"] != test.wantType {
				t.Fatalf("item = %#v", item)
			}
			if test.wantName != "" && item["name"] != test.wantName {
				t.Fatalf("item name = %#v", item)
			}
			if test.wantType == "custom_tool_call" && !strings.Contains(item["input"].(string), "*** Add File: a.txt") {
				t.Fatalf("custom input = %#v", item)
			}
		})
	}
}
