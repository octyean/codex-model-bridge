package toollog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/tools"
)

func TestRememberedToolStateIsScopedBySession(t *testing.T) {
	t.Setenv(EnvToolLogPath, "")
	requestA := "request-a-" + t.Name()
	requestB := "request-b-" + t.Name()
	sessionA := "session-a-" + t.Name()
	sessionB := "session-b-" + t.Name()
	callID := "shared-call-" + t.Name()
	entryA := tools.Entry{Descriptor: adapters.ToolDescriptor{Name: tools.ReadFileToolName}}
	entryB := tools.Entry{Descriptor: adapters.ToolDescriptor{Name: tools.ListFilesToolName}}

	RememberRequestSession(requestA, sessionA, "model", "upstream", "profile", nil)
	RememberRequestSession(requestB, sessionB, "model", "upstream", "profile", nil)
	t.Cleanup(func() {
		ForgetRequestSession(requestA)
		ForgetRequestSession(requestB)
	})
	ToolCall(requestA, "model", "profile", callID, entryA, `{"path":"/tmp/a"}`, "")
	ToolCall(requestB, "model", "profile", callID, entryB, `{"path":"/tmp/b"}`, "")

	logicalA, ok := RememberedLogicalToolCall(sessionA, callID)
	if !ok || logicalA.Name != tools.ReadFileToolName {
		t.Fatalf("session A logical call = %#v, %v", logicalA, ok)
	}
	logicalB, ok := RememberedLogicalToolCall(sessionB, callID)
	if !ok || logicalB.Name != tools.ListFilesToolName {
		t.Fatalf("session B logical call = %#v, %v", logicalB, ok)
	}

	modelA := takeRememberedToolCall(requestA, callID)
	if modelA["tool"] != tools.ReadFileToolName {
		t.Fatalf("session A model call = %#v", modelA)
	}
	modelB := takeRememberedToolCall(requestB, callID)
	if modelB["tool"] != tools.ListFilesToolName {
		t.Fatalf("session B model call = %#v", modelB)
	}

	if seenToolOutput(requestA, callID, "same output") {
		t.Fatal("first session A output should not be seen")
	}
	if seenToolOutput(requestB, callID, "same output") {
		t.Fatal("first session B output should not be seen")
	}
	if !seenToolOutput(requestA, callID, "same output") {
		t.Fatal("repeated session A output should be seen")
	}
}

func TestPatchToolCallIncludesNamedEventAndRequestContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tool-calls.jsonl")
	t.Setenv(EnvToolLogPath, path)
	requestID := "request-" + t.Name()
	RememberRequestSession(requestID, "session-"+t.Name(), "gpt-5.3-codex", "kimi-for-coding", "kimi", nil)
	t.Cleanup(func() { ForgetRequestSession(requestID) })

	entry := tools.Entry{Descriptor: adapters.ToolDescriptor{
		Name:         tools.TextEditorWriteToolName,
		Kind:         tools.KindTextEditor,
		OriginalType: "apply_patch",
	}}
	PatchToolCall(requestID, "call-"+t.Name(), entry, `{"path":"a.txt","file_text":"ok"}`, map[string]any{"type": "custom_tool_call"})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &record); err != nil {
		t.Fatal(err)
	}
	if record["event"] != "patch_tool_call" || record["profile"] != "kimi" || record["upstream_model"] != "kimi-for-coding" {
		t.Fatalf("patch tool log = %#v", record)
	}
}

func TestPruneRememberedStateRemovesExpiredEntries(t *testing.T) {
	now := time.Now()
	expiredKey := "expired-" + t.Name()
	activeKey := "active-" + t.Name()

	toolStateMu.Lock()
	logicalToolCalls[expiredKey] = rememberedStateEntry{
		Value:    LogicalToolCall{Name: tools.ReadFileToolName},
		LastSeen: now.Add(-toolStateTTL - time.Minute),
	}
	logicalToolCalls[activeKey] = rememberedStateEntry{
		Value:    LogicalToolCall{Name: tools.ListFilesToolName},
		LastSeen: now,
	}
	toolStateMu.Unlock()
	t.Cleanup(func() {
		toolStateMu.Lock()
		delete(logicalToolCalls, expiredKey)
		delete(logicalToolCalls, activeKey)
		toolStateMu.Unlock()
	})

	pruneRememberedState(now)

	toolStateMu.Lock()
	_, expiredExists := logicalToolCalls[expiredKey]
	_, activeExists := logicalToolCalls[activeKey]
	toolStateMu.Unlock()
	if expiredExists {
		t.Fatal("expired remembered state should be removed")
	}
	if !activeExists {
		t.Fatal("active remembered state should remain")
	}
}
