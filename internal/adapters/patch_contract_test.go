package adapters

import (
	"strings"
	"testing"
)

func TestNormalizePatchInputExtractsJSONEnvelope(t *testing.T) {
	got := NormalizePatchInput(`{"input":"*** Begin Patch\n*** Add File: hello.txt\n+hello\n*** End Patch\n"}`)
	want := "*** Begin Patch\n*** Add File: hello.txt\n+hello\n*** End Patch"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestNormalizePatchInputExtractsNestedJSONEnvelope(t *testing.T) {
	got := NormalizePatchInput("{\"arguments\":{\"patch\":\"```patch\\n*** Begin Patch\\n*** Add File: hello.txt\\n+hello\\n*** End Patch\\n```\"}}")
	want := "*** Begin Patch\n*** Add File: hello.txt\n+hello\n*** End Patch"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestNormalizePatchInputPreservesHunkWhitespace(t *testing.T) {
	input := "  *** Begin Patch\n*** Update File: a.vue\n@@\n- \t<view>\n+ \t<view class=\"x\">\n  \t  <text>hi</text>\n*** End Patch\n  "
	got := NormalizePatchInput(input)
	if strings.Contains(got, "\r") {
		t.Fatalf("normalized should use LF: %q", got)
	}
	for _, want := range []string{
		"- \t<view>",
		"+ \t<view class=\"x\">",
		"  \t  <text>hi</text>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("normalized lost whitespace %q in %q", want, got)
		}
	}
}

func TestNormalizePatchInputCompletesMissingEnvelope(t *testing.T) {
	got := NormalizePatchInput("*** Update File: a.txt\n@@\n-old\n+new")
	want := "*** Begin Patch\n*** Update File: a.txt\n@@\n-old\n+new\n*** End Patch"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestNormalizePatchInputCompletesMissingEnd(t *testing.T) {
	got := NormalizePatchInput("*** Begin Patch\n*** Add File: a.txt\n+hello")
	want := "*** Begin Patch\n*** Add File: a.txt\n+hello\n*** End Patch"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestNormalizePatchInputKeepsAppendInsertion(t *testing.T) {
	got := NormalizePatchInput("*** Begin Patch\n*** Update File: .env.example\n@@\n API_URL=http://localhost\n+ENABLE_LOG=true\n*** End Patch")
	want := "*** Begin Patch\n*** Update File: .env.example\n@@\n API_URL=http://localhost\n+ENABLE_LOG=true\n*** End Patch"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestPatchSucceededFilesExtractsChangedFiles(t *testing.T) {
	got := PatchSucceededFiles("Success. Updated the following files:\nM internal/adapters/deepseek.go\nA ./web/src/App.vue\nD old/request.js\n\nAPPLY_PATCH_SUCCEEDED")
	want := []string{"internal/adapters/deepseek.go", "web/src/App.vue", "old/request.js"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("files = %#v, want %#v", got, want)
	}
}

func TestPatchSucceededFilesExtractsFormattedChangedFiles(t *testing.T) {
	got := PatchSucceededFiles("TEXT_EDITOR_EDIT_SUCCEEDED\nfile_edit_state: completed\nchanged_files: ./a.java, src/App.vue")
	want := []string{"a.java", "src/App.vue"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("files = %#v, want %#v", got, want)
	}
}

func TestPatchTouchedFilesExtractsMultiFilePatch(t *testing.T) {
	got := PatchTouchedFiles("*** Begin Patch\n*** Update File: ./a.java\n@@\n-old\n+new\n*** Add File: web/src/App.vue\n+<template />\n*** Delete File: old/request.js\n*** End Patch")
	want := []string{"a.java", "web/src/App.vue", "old/request.js"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("files = %#v, want %#v", got, want)
	}
}

func TestPatchIsNoopUpdate(t *testing.T) {
	if !PatchIsNoopUpdate("*** Begin Patch\n*** Update File: a.java\n@@\n alpha done\n*** End Patch") {
		t.Fatalf("expected noop update")
	}
	if PatchIsNoopUpdate("*** Begin Patch\n*** Update File: a.java\n@@\n-alpha\n+alpha done\n*** End Patch") {
		t.Fatalf("replacement patch should not be noop")
	}
	if PatchIsNoopUpdate("*** Begin Patch\n*** Add File: a.java\n+alpha\n*** End Patch") {
		t.Fatalf("add file patch should not be noop")
	}
}

func TestPatchIsAlreadyApplied(t *testing.T) {
	if !PatchIsAlreadyApplied("*** Begin Patch\n*** Add File: a.java\n+TEXT_EDITOR_ALREADY_APPLIED\n*** End Patch") {
		t.Fatalf("expected already applied sentinel")
	}
	if !PatchIsAlreadyApplied("*** Begin Patch\n*** Update File: a.java\n@@\n alpha done\n*** End Patch") {
		t.Fatalf("expected noop update to be already applied")
	}
	if PatchIsAlreadyApplied("*** Begin Patch\n*** Update File: a.java\n@@\n-alpha\n+alpha done\n*** End Patch") {
		t.Fatalf("replacement patch should not be already applied")
	}
}

func TestPatchFilesOverlap(t *testing.T) {
	if !PatchFilesOverlap([]string{"./a.java"}, []string{"a.java"}) {
		t.Fatalf("expected overlap")
	}
	if PatchFilesOverlap([]string{"a.java"}, []string{"b.java"}) {
		t.Fatalf("unexpected overlap")
	}
}

func TestClassifyPatchFailure(t *testing.T) {
	cases := map[string]PatchFailureKind{
		"apply_patch verification failed: Failed to find expected lines": PatchFailureContextMismatch,
		"Failed to find context": PatchFailureContextMismatch,
		"permission denied":      PatchFailurePermissionOrSandbox,
		"sandbox denied":         PatchFailurePermissionOrSandbox,
		"open foo: no such file": PatchFailurePathError,
		"invalid hunk at line 3": PatchFailureInvalidHunk,
		"invalid hunk at line 2, '*** Read File: README.md' is not a valid hunk header": PatchFailureReadFileOperation,
		"TEXT_EDITOR_ALREADY_APPLIED":            PatchFailureAlreadyApplied,
		"invalid patch: missing *** Begin Patch": PatchFailureMalformedPatch,
		"apply_patch failed unexpectedly":        PatchFailureUnknown,
		"ok":                                     PatchFailureNone,
	}
	for output, want := range cases {
		if got := ClassifyPatchFailure(output); got != want {
			t.Fatalf("ClassifyPatchFailure(%q) = %q, want %q", output, got, want)
		}
	}
}
