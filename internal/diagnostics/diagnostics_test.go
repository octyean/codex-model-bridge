package diagnostics

import "testing"

func TestCompactLargeFieldsReplacesLargeBodyWithSummary(t *testing.T) {
	record := map[string]any{
		"event": "prompt_request",
		"body":  map[string]any{"text": string(make([]byte, SessionInlineMaxBytes+1))},
	}

	compact := CompactLargeFields(record, SessionInlineMaxBytes, "body")

	if _, ok := compact["body"]; ok {
		t.Fatal("body should be replaced")
	}
	summary, ok := compact["body_summary"].(map[string]any)
	if !ok {
		t.Fatal("body_summary missing")
	}
	if summary["hash"] == "" || summary["bytes"] == 0 {
		t.Fatalf("summary is incomplete: %#v", summary)
	}
	if record["body"] == nil {
		t.Fatal("original record should not be mutated")
	}
}

func TestCompactLargeFieldsKeepsSmallBody(t *testing.T) {
	body := map[string]any{"ok": false, "error": "upstream failed"}
	record := map[string]any{"body": body}

	compact := CompactLargeFields(record, SessionInlineMaxBytes, "body")

	if compact["body"] == nil {
		t.Fatal("small body should stay inline")
	}
	if _, ok := compact["body_summary"]; ok {
		t.Fatal("small body should not be summarized")
	}
}
