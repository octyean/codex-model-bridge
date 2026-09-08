package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTextEditorToolCallFromPatch(t *testing.T) {
	tests := []struct {
		name     string
		patch    string
		wantTool string
		wantArg  string
	}{
		{
			name:     "write",
			patch:    "*** Begin Patch\n*** Add File: a.txt\n+hello\n*** End Patch",
			wantTool: TextEditorWriteToolName,
			wantArg:  `"file_text":"hello"`,
		},
		{
			name:     "replace",
			patch:    "*** Begin Patch\n*** Update File: a.txt\n@@\n-old\n+new\n*** End Patch",
			wantTool: TextEditorReplaceToolName,
			wantArg:  `"old_str":"old"`,
		},
		{
			name:     "insert after match",
			patch:    "*** Begin Patch\n*** Update File: a.txt\n@@\n anchor\n+inserted\n*** End Patch",
			wantTool: TextEditorInsertMatchToolName,
			wantArg:  `"insert_text":"inserted"`,
		},
		{
			name:     "move",
			patch:    "*** Begin Patch\n*** Update File: a.txt\n*** Move to: b.txt\n*** End Patch",
			wantTool: TextEditorMoveToolName,
			wantArg:  `"destination_path":"b.txt"`,
		},
		{
			name:     "delete",
			patch:    "*** Begin Patch\n*** Delete File: a.txt\n*** End Patch",
			wantTool: TextEditorDeleteToolName,
			wantArg:  `"path":"a.txt"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tool, arguments, ok := TextEditorToolCallFromPatch(test.patch)
			if !ok || tool != test.wantTool || !strings.Contains(arguments, test.wantArg) {
				t.Fatalf("tool=%q arguments=%q ok=%v", tool, arguments, ok)
			}
		})
	}
}

func TestTextEditorLineInsertUsesWorkspaceFile(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "a.txt")
	if err := os.WriteFile(path, []byte("first\nsecond\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	patch, err := TextEditorPatchInputWithWorkspace(
		`{"command":"insert","path":"a.txt","insert_line":1,"insert_text":"middle"}`,
		workspace,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(patch, " first\n+middle") {
		t.Fatalf("insert patch = %s", patch)
	}
}
