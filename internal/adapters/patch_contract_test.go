package adapters

import "testing"

func TestClassifyPatchFailure(t *testing.T) {
	tests := map[string]PatchFailureKind{
		"Failed to find expected lines":       PatchFailureContextMismatch,
		"Invalid hunk at line 2":              PatchFailureInvalidHunk,
		"invalid patch: missing Begin Patch":  PatchFailureMalformedPatch,
		"TEXT_EDITOR_ALREADY_APPLIED":         PatchFailureAlreadyApplied,
		"permission denied outside workspace": PatchFailurePermissionOrSandbox,
		"no such file":                        PatchFailurePathError,
	}
	for output, want := range tests {
		if got := ClassifyPatchFailure(output); got != want {
			t.Fatalf("ClassifyPatchFailure(%q) = %q, want %q", output, got, want)
		}
	}
}

func TestClassifyToolFailure(t *testing.T) {
	tests := []struct {
		tool   ToolDescriptor
		output string
		want   ToolFailureKind
	}{
		{tool: ToolDescriptor{Name: "tool_search"}, output: "[]", want: ToolFailureToolSearchEmpty},
		{tool: ToolDescriptor{Name: "search_files"}, output: "match_count: 0", want: ToolFailureFileSearchEmpty},
		{tool: ToolDescriptor{Name: "replace_text"}, output: `{"ok":false,"error":"context mismatch"}`, want: ToolFailureStructuredFailure},
	}
	for _, test := range tests {
		if got := ClassifyToolFailure(test.tool, test.output); got != test.want {
			t.Fatalf("ClassifyToolFailure(%q) = %q, want %q", test.output, got, test.want)
		}
	}
}
