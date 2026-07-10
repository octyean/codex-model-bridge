package toolruntime

import "testing"

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
