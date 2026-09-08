package toolruntime

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDecideAllowsNoProgressHistoryWhenLocalTextUnavailable(t *testing.T) {
	sessionID := "session-" + t.Name()
	tool := ToolInfo{Name: "replace_text", Kind: "text_editor_patch", Arguments: `{"path":"/tmp/a","old_str":"a","new_str":"b"}`}

	RememberRequest(RequestContext{RequestID: "req-first-" + t.Name(), SessionID: sessionID})
	t.Cleanup(func() { ForgetRequest("req-first-" + t.Name()) })
	first := Decide(CallContext{RequestID: "req-first-" + t.Name(), CallID: "call-first-" + t.Name(), Tool: tool, CanReturnLocalText: true})
	if first.Action != DecisionAllow {
		t.Fatalf("first action = %q, want allow", first.Action)
	}
	ObserveOutput(OutputContext{
		RequestID:   "req-first-" + t.Name(),
		CallID:      "call-first-" + t.Name(),
		Tool:        tool,
		RawOutput:   "text_editor_no_progress",
		ToolFailed:  true,
		FailureKind: "no_progress",
	})

	RememberRequest(RequestContext{RequestID: "req-second-" + t.Name(), SessionID: sessionID})
	t.Cleanup(func() { ForgetRequest("req-second-" + t.Name()) })
	second := Decide(CallContext{RequestID: "req-second-" + t.Name(), CallID: "call-second-" + t.Name(), Tool: tool, CanReturnLocalText: false})
	if second.Action != DecisionAllow {
		t.Fatalf("second action = %q, want allow", second.Action)
	}
}

func TestObserveOutputKeepsCallProfilesScopedByRequest(t *testing.T) {
	requestA := "req-a-" + t.Name()
	requestB := "req-b-" + t.Name()
	callID := "shared-call-" + t.Name()
	toolA := ToolInfo{Name: "read_file", Arguments: `{"path":"/tmp/a"}`}
	toolB := ToolInfo{Name: "list_files", Arguments: `{"path":"/tmp/b"}`}

	RememberRequest(RequestContext{RequestID: requestA})
	RememberRequest(RequestContext{RequestID: requestB})
	t.Cleanup(func() {
		ForgetRequest(requestA)
		ForgetRequest(requestB)
	})
	Decide(CallContext{RequestID: requestA, CallID: callID, Tool: toolA})
	Decide(CallContext{RequestID: requestB, CallID: callID, Tool: toolB})

	outcomeB := ObserveOutput(OutputContext{RequestID: requestB, CallID: callID, Tool: toolB, RawOutput: "ok"})
	outcomeA := ObserveOutput(OutputContext{RequestID: requestA, CallID: callID, Tool: toolA, RawOutput: "ok"})

	if outcomeA.ProgressKey != ProfileTool(toolA).Signature {
		t.Fatalf("request A progress key = %q", outcomeA.ProgressKey)
	}
	if outcomeB.ProgressKey != ProfileTool(toolB).Signature {
		t.Fatalf("request B progress key = %q", outcomeB.ProgressKey)
	}
}

func TestProgressResetsSessionFailureEpoch(t *testing.T) {
	sessionID := "session-" + t.Name()
	failedTool := ToolInfo{Name: "replace_text", Kind: "text_editor_patch", Arguments: `{"path":"/tmp/a","old_str":"a","new_str":"b"}`}
	progressTool := ToolInfo{Name: "read_file", Arguments: `{"path":"/tmp/a"}`}
	firstRequest := "req-first-" + t.Name()
	progressRequest := "req-progress-" + t.Name()
	finalRequest := "req-final-" + t.Name()

	RememberRequest(RequestContext{RequestID: firstRequest, SessionID: sessionID})
	first := Decide(CallContext{RequestID: firstRequest, CallID: "call-first-" + t.Name(), Tool: failedTool, CanReturnLocalText: true})
	if first.Action != DecisionAllow {
		t.Fatalf("first action = %q, want allow", first.Action)
	}
	ObserveOutput(OutputContext{
		RequestID:   firstRequest,
		CallID:      "call-first-" + t.Name(),
		Tool:        failedTool,
		RawOutput:   "text_editor_no_progress",
		ToolFailed:  true,
		FailureKind: "no_progress",
	})
	ForgetRequest(firstRequest)

	RememberRequest(RequestContext{RequestID: progressRequest, SessionID: sessionID})
	Decide(CallContext{RequestID: progressRequest, CallID: "call-progress-" + t.Name(), Tool: progressTool, CanReturnLocalText: true})
	ObserveOutput(OutputContext{
		RequestID: progressRequest,
		CallID:    "call-progress-" + t.Name(),
		Tool:      progressTool,
		RawOutput: "file contents",
	})
	ForgetRequest(progressRequest)

	RememberRequest(RequestContext{RequestID: finalRequest, SessionID: sessionID})
	t.Cleanup(func() { ForgetRequest(finalRequest) })
	final := Decide(CallContext{RequestID: finalRequest, CallID: "call-final-" + t.Name(), Tool: failedTool, CanReturnLocalText: true})
	if final.Action != DecisionAllow {
		t.Fatalf("final action = %q, want allow after progress", final.Action)
	}
}

func TestForgetRequestDropsPendingCallProfiles(t *testing.T) {
	requestID := "req-" + t.Name()
	callID := "call-" + t.Name()
	tool := ToolInfo{Name: "read_file", Arguments: `{"path":"/tmp/a"}`}

	RememberRequest(RequestContext{RequestID: requestID})
	Decide(CallContext{RequestID: requestID, CallID: callID, Tool: tool})
	ForgetRequest(requestID)

	if _, ok := callProfile(requestID, callID); ok {
		t.Fatal("pending call profile should be removed with its request")
	}
}

func TestRememberRequestPrunesExpiredSessions(t *testing.T) {
	expiredSession := "expired-" + t.Name()
	requestID := "req-" + t.Name()

	mu.Lock()
	sessions[expiredSession] = &sessionState{
		NoProgressFailures: map[string]int{"signature": 1},
		LastSeen:           time.Now().Add(-sessionStateTTL - time.Minute),
	}
	mu.Unlock()

	RememberRequest(RequestContext{RequestID: requestID, SessionID: "active-" + t.Name()})
	t.Cleanup(func() { ForgetRequest(requestID) })

	mu.Lock()
	_, exists := sessions[expiredSession]
	mu.Unlock()
	if exists {
		t.Fatal("expired session state should be pruned")
	}
}

func TestRuntimeLogTypesUseSnakeCaseJSON(t *testing.T) {
	data, err := json.Marshal(struct {
		Profile Profile `json:"profiled_tool"`
		Outcome Outcome `json:"runtime_outcome"`
	}{
		Profile: Profile{Tool: "replace_text", ToolKey: "write", ArgumentsHash: "hash"},
		Outcome: Outcome{OK: true, Category: "success", Progress: true, OutputHash: "output"},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, expected := range []string{`"tool_key"`, `"arguments_hash"`, `"ok"`, `"category"`, `"progress"`, `"output_hash"`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("JSON missing %s: %s", expected, text)
		}
	}
	for _, unexpected := range []string{`"ToolKey"`, `"ArgumentsHash"`, `"OK"`, `"Category"`, `"Progress"`, `"OutputHash"`} {
		if strings.Contains(text, unexpected) {
			t.Fatalf("JSON contains %s: %s", unexpected, text)
		}
	}
}
