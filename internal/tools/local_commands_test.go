package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalResourceCommandsRunWithoutBridgeSpecificExecutables(t *testing.T) {
	root := t.TempDir()
	quotedFile := filepath.Join(root, "alpha's.txt")
	if err := os.WriteFile(quotedFile, []byte("one\ntwo needle\nthree\nfour\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "needle.go"), []byte("package needle\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	read := readFileCommandForOS("linux", localResourceRead{Path: quotedFile, StartLine: 2, LineLimit: 2}, root)
	assertPortableLocalCommand(t, read.Cmd)
	if output := runLocalCommand(t, read); output != "two needle\nthree\n" {
		t.Fatalf("read output = %q", output)
	}

	list := listFilesCommandForOS("linux", listFilesSpec{Path: root, MaxResults: 20}, root)
	assertPortableLocalCommand(t, list.Cmd)
	listOutput := runLocalCommand(t, list)
	if !strings.Contains(listOutput, quotedFile) || !strings.Contains(listOutput, nested) || strings.Contains(listOutput, "needle.go") {
		t.Fatalf("non-recursive list output = %q", listOutput)
	}

	recursive := listFilesCommandForOS("linux", listFilesSpec{Path: root, Recursive: true, MaxResults: 20}, root)
	if output := runLocalCommand(t, recursive); !strings.Contains(output, "needle.go") {
		t.Fatalf("recursive list output = %q", output)
	}

	search := fileSearchCommandForOS("linux", fileSearchSpec{Query: "needle", Path: root, Glob: "*.go", MaxResults: 20}, root)
	assertPortableLocalCommand(t, search.Cmd)
	searchOutput := runLocalCommand(t, search)
	if !strings.Contains(searchOutput, "needle.go:1:package needle") || !strings.Contains(searchOutput, "needle.go") {
		t.Fatalf("search output = %q", searchOutput)
	}

	recursiveGlob := fileSearchCommandForOS("linux", fileSearchSpec{Query: "needle", Path: root, Glob: "nested/**/*.go", MaxResults: 20}, root)
	if output := runLocalCommand(t, recursiveGlob); !strings.Contains(output, "needle.go:1:package needle") {
		t.Fatalf("recursive glob output = %q", output)
	}
}

func TestWindowsLocalResourceCommandsUsePowerShell(t *testing.T) {
	workdir := `C:\Work`
	path := `C:\Work\O'Brien`
	commands := []ExecCommand{
		readFileCommandForOS("windows", localResourceRead{Path: path, StartLine: 2, LineLimit: 3}, workdir),
		listFilesCommandForOS("windows", listFilesSpec{Path: path, Recursive: true, MaxResults: 20}, workdir),
		fileSearchCommandForOS("windows", fileSearchSpec{Query: "needle", Path: path, Glob: "*.go", MaxResults: 20}, workdir),
	}
	for _, command := range commands {
		assertPortableLocalCommand(t, command.Cmd)
		if !strings.Contains(command.Cmd, "O''Brien") || !strings.Contains(command.Cmd, "30000") {
			t.Fatalf("windows command = %s", command.Cmd)
		}
	}
}

func assertPortableLocalCommand(t *testing.T, command string) {
	t.Helper()
	for _, forbidden := range []string{"rtk ", "rg ", "-maxdepth"} {
		if strings.Contains(command, forbidden) {
			t.Fatalf("command contains %q: %s", forbidden, command)
		}
	}
}

func runLocalCommand(t *testing.T, command ExecCommand) string {
	t.Helper()
	process := exec.Command("sh", "-c", command.Cmd)
	process.Dir = command.Workdir
	output, err := process.CombinedOutput()
	if err != nil {
		t.Fatalf("run command: %v\n%s", err, output)
	}
	return string(output)
}
