package transcript

import (
	"strings"
	"testing"
)

func TestHiddenTextEditorHistoryOutputSummaryOmitsRawOutput(t *testing.T) {
	raw := "failed to find expected lines\nUNTRUSTED_MODEL_INSTRUCTION\n" + strings.Repeat("secret payload ", 200)
	summary := hiddenTextEditorHistoryOutputSummary(raw, hiddenFileEditCall{files: []string{"internal/example.go"}})

	for _, forbidden := range []string{"UNTRUSTED_MODEL_INSTRUCTION", "secret payload", "failed to find expected lines"} {
		if strings.Contains(summary, forbidden) {
			t.Fatalf("summary retained raw output %q:\n%s", forbidden, summary)
		}
	}
	for _, expected := range []string{
		"TEXT_EDITOR_HISTORY_OUTPUT_HIDDEN",
		"file_edit_state: failed",
		"failure_kind: context_mismatch",
		"changed_files: internal/example.go",
		"required_next_action:",
		"forbidden_next_action:",
		"recovery:",
	} {
		if !strings.Contains(summary, expected) {
			t.Fatalf("summary missing %q:\n%s", expected, summary)
		}
	}
	if len(summary) > 1000 {
		t.Fatalf("summary is unexpectedly large: %d bytes", len(summary))
	}
}

func TestHiddenTextEditorHistoryOutputSummaryRecordsSuccessWithoutRawOutput(t *testing.T) {
	raw := "Success. Updated the following files:\nM internal/example.go\nprivate detail"
	summary := hiddenTextEditorHistoryOutputSummary(raw, hiddenFileEditCall{files: []string{"internal/example.go"}})

	if strings.Contains(summary, "private detail") || strings.Contains(summary, "Success. Updated") {
		t.Fatalf("summary retained raw success output:\n%s", summary)
	}
	for _, expected := range []string{
		"file_edit_state: completed",
		"failure_kind: none",
		"changed_files: internal/example.go",
		"required_next_action: continue_from_current_file_state",
	} {
		if !strings.Contains(summary, expected) {
			t.Fatalf("summary missing %q:\n%s", expected, summary)
		}
	}
}

func TestHiddenTextEditorHistoryOutputSummaryRecognizesBridgeSuccessMarker(t *testing.T) {
	summary := hiddenTextEditorHistoryOutputSummary("APPLY_PATCH_SUCCEEDED\ninternal detail", hiddenFileEditCall{})
	if !strings.Contains(summary, "file_edit_state: completed") || !strings.Contains(summary, "failure_kind: none") {
		t.Fatalf("success marker was not recognized:\n%s", summary)
	}
	if strings.Contains(summary, "internal detail") || strings.Contains(summary, "APPLY_PATCH_SUCCEEDED") {
		t.Fatalf("summary retained raw output:\n%s", summary)
	}
}
