package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex-bridge/internal/adapters"
	"codex-bridge/internal/codex"
)

func TestDefaultApplyPatchToolBecomesTextEditor(t *testing.T) {
	chatTools, ctx := FromCodex([]codex.ResponseTool{{Type: "custom", Name: "apply_patch"}}, adapters.Get(adapters.DefaultName))
	if len(chatTools) != 1 {
		t.Fatalf("tools len = %d", len(chatTools))
	}
	if chatTools[0].Function.Name != "codex_text_editor" {
		t.Fatalf("tool name = %q", chatTools[0].Function.Name)
	}
	if !ctx.IsCustom("codex_text_editor") {
		t.Fatalf("text editor should be custom")
	}
}

func TestExtractCustomInput(t *testing.T) {
	got := ExtractCustomInput(`{"input":"*** Begin Patch\n*** End Patch\n"}`)
	want := "*** Begin Patch\n*** End Patch\n"
	if got != want {
		t.Fatalf("input = %q, want %q", got, want)
	}
}

func TestNonGPTApplyPatchDescription(t *testing.T) {
	chatTools, _ := FromCodex([]codex.ResponseTool{{Type: "custom", Name: "apply_patch"}}, adapters.Get(adapters.MimoName))
	if chatTools[0].Function.Name != "codex_text_editor" {
		t.Fatalf("non-GPT should see text editor tool, got %q", chatTools[0].Function.Name)
	}
	description := chatTools[0].Function.Description
	for _, forbidden := range []string{"apply_patch", "*** Begin Patch", "*** End Patch", "bridge", "Bridge", "Codex"} {
		if strings.Contains(description, forbidden) {
			t.Fatalf("description should hide %q: %q", forbidden, description)
		}
	}
	for _, want := range []string{"str_replace", "old_str", "insert_after", "move_file", "delete_file"} {
		if !strings.Contains(description, want) {
			t.Fatalf("description missing %q: %q", want, description)
		}
	}
}

func TestNormalizeTextEditorCommand(t *testing.T) {
	cases := map[string]string{
		"create":       "create",
		"create_file":  "create",
		"replace":      "str_replace",
		"str_replace":  "str_replace",
		"insert":       "insert_after",
		"insert_after": "insert_after",
		"rename":       "move_file",
		"rename_file":  "move_file",
		"move":         "move_file",
		"move_file":    "move_file",
		"delete":       "delete_file",
		"delete_file":  "delete_file",
	}
	for input, want := range cases {
		if got := NormalizeTextEditorCommand(input); got != want {
			t.Fatalf("NormalizeTextEditorCommand(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeCustomInputRemovesMarkdownFence(t *testing.T) {
	got := adapters.Get(adapters.DefaultName).NormalizeCustomInput("apply_patch", "```patch\r\n*** Begin Patch\r\n*** End Patch\r\n```")
	want := "*** Begin Patch\n*** End Patch"
	if got != want {
		t.Fatalf("normalized input = %q, want %q", got, want)
	}
}

func TestExtractPatchCustomToolInputFromJSONEnvelope(t *testing.T) {
	_, ctx := FromCodex([]codex.ResponseTool{{Type: "custom", Name: "apply_patch"}}, adapters.Get(adapters.OpenAIName))
	entry := ctx.Entry("apply_patch")
	got := ExtractCustomToolInput(entry, `{"patch":"*** Begin Patch\n*** Add File: hello.txt\n+hello\n*** End Patch\n"}`, adapters.Get(adapters.OpenAIName))
	want := "*** Begin Patch\n*** Add File: hello.txt\n+hello\n*** End Patch"
	if got != want {
		t.Fatalf("input = %q, want %q", got, want)
	}
}

func TestExtractPatchCustomToolInputFromNestedArguments(t *testing.T) {
	_, ctx := FromCodex([]codex.ResponseTool{{Type: "custom", Name: "apply_patch"}}, adapters.Get(adapters.OpenAIName))
	entry := ctx.Entry("apply_patch")
	got := ExtractCustomToolInput(entry, "{\"arguments\":{\"content\":\"```patch\\n*** Begin Patch\\n*** Add File: hello.txt\\n+hello\\n*** End Patch\\n```\"}}", adapters.Get(adapters.OpenAIName))
	want := "*** Begin Patch\n*** Add File: hello.txt\n+hello\n*** End Patch"
	if got != want {
		t.Fatalf("input = %q, want %q", got, want)
	}
}

func TestPlainCustomToolDoesNotUsePatchEnvelopeKeys(t *testing.T) {
	_, ctx := FromCodex([]codex.ResponseTool{{Type: "custom", Name: "custom_tool"}}, adapters.Get(adapters.DeepSeekName))
	entry := ctx.Entry("custom_tool")
	got := ExtractCustomToolInput(entry, `{"patch":"not input","input":"real input"}`, adapters.Get(adapters.DeepSeekName))
	if got != "real input" {
		t.Fatalf("input = %q", got)
	}
}

func TestOpenAIApplyPatchToolCarriesPatchSemantics(t *testing.T) {
	_, ctx := FromCodex([]codex.ResponseTool{{Type: "custom", Name: "apply_patch"}}, adapters.Get(adapters.OpenAIName))
	entry := ctx.Entry("apply_patch")
	if entry.Kind() != KindPatch {
		t.Fatalf("kind = %q", entry.Kind())
	}
	if entry.Descriptor.InputMode != InputModeFreeform || entry.Descriptor.SideEffect != SideEffectWriteFiles {
		t.Fatalf("descriptor = %#v", entry.Descriptor)
	}
	if !ctx.HasFileWriteTool() {
		t.Fatalf("patch tool should be classified as file write")
	}
}

func TestNonGPTApplyPatchToolBecomesTextEditorButKeepsPatchSemantics(t *testing.T) {
	chatTools, ctx := FromCodex([]codex.ResponseTool{{Type: "custom", Name: "apply_patch"}}, adapters.Get(adapters.MimoName))
	if len(chatTools) != 1 || chatTools[0].Function.Name != "codex_text_editor" {
		t.Fatalf("chat tools = %#v", chatTools)
	}
	entry := ctx.Entry("codex_text_editor")
	if entry.Kind() != KindTextEditor || entry.OriginalName() != "apply_patch" {
		t.Fatalf("entry = %#v", entry)
	}
	if entry.Descriptor.InputMode != InputModeJSON || entry.Descriptor.SideEffect != SideEffectWriteFiles {
		t.Fatalf("descriptor = %#v", entry.Descriptor)
	}
	if !ctx.HasFileWriteTool() {
		t.Fatalf("text editor should be classified as file write")
	}
}

func TestKimiApplyPatchToolBecomesTextEditor(t *testing.T) {
	chatTools, ctx := FromCodex([]codex.ResponseTool{{Type: "custom", Name: "apply_patch"}}, adapters.Get(adapters.KimiName))
	if len(chatTools) != 1 || chatTools[0].Function.Name != "codex_text_editor" {
		t.Fatalf("chat tools = %#v", chatTools)
	}
	entry := ctx.Entry("codex_text_editor")
	if entry.Kind() != KindTextEditor || entry.OriginalName() != "apply_patch" {
		t.Fatalf("entry = %#v", entry)
	}
}

func TestKimiShellToolDescriptionCarriesFileWriteBoundary(t *testing.T) {
	chatTools, _ := FromCodex([]codex.ResponseTool{{Type: "function", Name: "exec_command", Description: "Run a command."}}, adapters.Get(adapters.KimiName))
	if len(chatTools) != 1 {
		t.Fatalf("chat tools = %#v", chatTools)
	}
	description := chatTools[0].Function.Description
	for _, want := range []string{"This shell is not a file editor", "sed -i", "rm", "mv", "Use the text editor tool"} {
		if !strings.Contains(description, want) {
			t.Fatalf("description missing %q: %s", want, description)
		}
	}
}

func TestTextEditorStrReplaceBuildsApplyPatchInput(t *testing.T) {
	_, ctx := FromCodex([]codex.ResponseTool{{Type: "custom", Name: "apply_patch"}}, adapters.Get(adapters.DeepSeekName))
	entry := ctx.Entry("codex_text_editor")
	got := ExtractCustomToolInput(entry, `{"command":"str_replace","path":"./src/App.vue","old_str":"  <span>待结</span>","new_str":"  <span>有单未结</span>"}`, adapters.Get(adapters.DeepSeekName))
	want := "*** Begin Patch\n*** Update File: src/App.vue\n@@\n-  <span>待结</span>\n+  <span>有单未结</span>\n*** End Patch"
	if got != want {
		t.Fatalf("input = %q, want %q", got, want)
	}
}

func TestTextEditorStrReplaceInheritsIndentWhenNewLineOmitsIt(t *testing.T) {
	got, err := TextEditorPatchInput(`{"command":"str_replace","path":"js/request.js","old_str":"\t\t\ttitle: data.msg || '请求失败',","new_str":"title: data.msg || '请求失败，请稍后再试',"}`)
	if err != nil {
		t.Fatalf("text editor patch: %v", err)
	}
	want := "*** Begin Patch\n*** Update File: js/request.js\n@@\n-\t\t\ttitle: data.msg || '请求失败',\n+\t\t\ttitle: data.msg || '请求失败，请稍后再试',\n*** End Patch"
	if got != want {
		t.Fatalf("input = %q, want %q", got, want)
	}
}

func TestTextEditorStrReplaceCompletesShorterSameFamilyIndent(t *testing.T) {
	got, err := TextEditorPatchInput(`{"command":"str_replace","path":"js/request.js","old_str":"\t\t\ttitle: data.msg || '请求失败',","new_str":"\t\ttitle: data.msg || '请求失败，请稍后再试',"}`)
	if err != nil {
		t.Fatalf("text editor patch: %v", err)
	}
	want := "*** Begin Patch\n*** Update File: js/request.js\n@@\n-\t\t\ttitle: data.msg || '请求失败',\n+\t\t\ttitle: data.msg || '请求失败，请稍后再试',\n*** End Patch"
	if got != want {
		t.Fatalf("input = %q, want %q", got, want)
	}
}

func TestTextEditorStrReplaceExpandsUniqueSubstringToLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "request.js")
	if err := os.WriteFile(path, []byte("\t\tuni.showToast({\n\t\t\ttitle: data.msg || '请求失败',\n\t\t})\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	arguments, _ := json.Marshal(map[string]string{
		"command": "str_replace",
		"path":    path,
		"old_str": "data.msg || '请求失败'",
		"new_str": "data.msg || '请求失败，请稍后再试'",
	})
	got, err := TextEditorPatchInput(string(arguments))
	if err != nil {
		t.Fatalf("text editor patch: %v", err)
	}
	want := "*** Begin Patch\n*** Update File: " + path + "\n@@\n-\t\t\ttitle: data.msg || '请求失败',\n+\t\t\ttitle: data.msg || '请求失败，请稍后再试',\n*** End Patch"
	if got != want {
		t.Fatalf("input = %q, want %q", got, want)
	}
}

func TestTextEditorStrReplaceAlreadyAppliedSubstringBuildsLocalResult(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(path, []byte("alpha done\nbeta done\ngamma\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	arguments, _ := json.Marshal(map[string]string{
		"command": "str_replace",
		"path":    path,
		"old_str": "alpha",
		"new_str": "alpha done",
	})
	got, err := TextEditorPatchInput(string(arguments))
	if err != nil {
		t.Fatalf("text editor patch: %v", err)
	}
	for _, want := range []string{
		"TEXT_EDITOR_ALREADY_APPLIED",
		"path: " + path,
		"file_edit_state: already_applied",
		"forbidden_next_action: repeat_same_text_editor_edit",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("input missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "*** Add File") {
		t.Fatalf("local result must not be executable patch: %q", got)
	}
}

func TestTextEditorStrReplaceAlreadyAppliedBuildsLocalResult(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(path, []byte("alpha done\nbeta\ngamma\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	arguments, _ := json.Marshal(map[string]string{
		"command": "str_replace",
		"path":    path,
		"old_str": "alpha",
		"new_str": "alpha done",
	})
	got, err := TextEditorPatchInput(string(arguments))
	if err != nil {
		t.Fatalf("text editor patch: %v", err)
	}
	for _, want := range []string{
		"TEXT_EDITOR_ALREADY_APPLIED",
		"path: " + path,
		"file_edit_state: already_applied",
		"forbidden_next_action: repeat_same_text_editor_edit",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("input missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "*** Add File") {
		t.Fatalf("local result must not be executable patch: %q", got)
	}
}

func TestTextEditorCreateBuildsApplyPatchInput(t *testing.T) {
	got, err := TextEditorPatchInput(`{"command":"create","path":"deep/path/notes.md","file_text":"# Notes\n\nHello\n"}`)
	if err != nil {
		t.Fatalf("text editor patch: %v", err)
	}
	want := "*** Begin Patch\n*** Add File: deep/path/notes.md\n+# Notes\n+\n+Hello\n*** End Patch"
	if got != want {
		t.Fatalf("input = %q, want %q", got, want)
	}
}

func TestTextEditorCreateFileAliasBuildsApplyPatchInput(t *testing.T) {
	got, err := TextEditorPatchInput(`{"command":"create_file","path":"deep/path/notes.md","file_text":"# Notes\n\nHello\n"}`)
	if err != nil {
		t.Fatalf("text editor patch: %v", err)
	}
	want := "*** Begin Patch\n*** Add File: deep/path/notes.md\n+# Notes\n+\n+Hello\n*** End Patch"
	if got != want {
		t.Fatalf("input = %q, want %q", got, want)
	}
}

func TestTextEditorArgumentsFromPatchReplaysSimpleReplace(t *testing.T) {
	got, ok := TextEditorArgumentsFromPatch("*** Begin Patch\n*** Update File: a.java\n@@\n-old\n+new\n*** End Patch")
	if !ok {
		t.Fatalf("expected patch to be reversible")
	}
	for _, want := range []string{`"command":"str_replace"`, `"path":"a.java"`, `"old_str":"old"`, `"new_str":"new"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("arguments missing %q: %s", want, got)
		}
	}
}

func TestTextEditorArgumentsFromPatchRejectsComplexPatch(t *testing.T) {
	got, ok := TextEditorArgumentsFromPatch("*** Begin Patch\n*** Update File: a.java\n*** Move to: b.java\n@@\n existing\n+new\n*** End Patch")
	if ok || got != "" {
		t.Fatalf("complex patch should not be replayed as text editor arguments: %q", got)
	}
}

func TestTextEditorCreateExistingFileBuildsLocalResult(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	arguments, _ := json.Marshal(map[string]string{
		"command":   "create",
		"path":      path,
		"file_text": "new\n",
	})
	got, err := TextEditorPatchInput(string(arguments))
	if err != nil {
		t.Fatalf("text editor patch: %v", err)
	}
	for _, want := range []string{
		"TEXT_EDITOR_CREATE_TARGET_ALREADY_EXISTS",
		"path: " + path,
		"file_edit_state: not_modified",
		"forbidden_next_action: retry_create_same_path",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("input missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "*** Add File") {
		t.Fatalf("local result must not be executable patch: %q", got)
	}
}

func TestTextEditorInsertAfterBuildsApplyPatchInput(t *testing.T) {
	got, err := TextEditorPatchInput(`{"command":"insert_after","path":"README.md","insert_after":"## Install","text":"\n## Usage\nnpm run dev"}`)
	if err != nil {
		t.Fatalf("text editor patch: %v", err)
	}
	want := "*** Begin Patch\n*** Update File: README.md\n@@\n ## Install\n+\n+## Usage\n+npm run dev\n*** End Patch"
	if got != want {
		t.Fatalf("input = %q, want %q", got, want)
	}
}

func TestTextEditorMoveFileBuildsApplyPatchInput(t *testing.T) {
	got, err := TextEditorPatchInput(`{"command":"move_file","path":"src/old.js","destination_path":"src/new.js"}`)
	if err != nil {
		t.Fatalf("text editor patch: %v", err)
	}
	want := "*** Begin Patch\n*** Update File: src/old.js\n*** Move to: src/new.js\n*** End Patch"
	if got != want {
		t.Fatalf("input = %q, want %q", got, want)
	}
}

func TestTextEditorMoveFileCanReplaceContent(t *testing.T) {
	got, err := TextEditorPatchInput(`{"command":"move_file","path":"docs/old.md","destination_path":"docs/new.md","old_str":"# Old\n\nEndpoint: /v1","new_str":"# New\n\nEndpoint: /v2"}`)
	if err != nil {
		t.Fatalf("text editor patch: %v", err)
	}
	want := "*** Begin Patch\n*** Update File: docs/old.md\n*** Move to: docs/new.md\n@@\n-# Old\n-\n-Endpoint: /v1\n+# New\n+\n+Endpoint: /v2\n*** End Patch"
	if got != want {
		t.Fatalf("input = %q, want %q", got, want)
	}
}

func TestTextEditorMoveFileAcceptsRenameAlias(t *testing.T) {
	got, err := TextEditorPatchInput(`{"command":"rename_file","path":"./docs/old.md","new_path":"./docs/new.md"}`)
	if err != nil {
		t.Fatalf("text editor patch: %v", err)
	}
	want := "*** Begin Patch\n*** Update File: docs/old.md\n*** Move to: docs/new.md\n*** End Patch"
	if got != want {
		t.Fatalf("input = %q, want %q", got, want)
	}
}

func TestTextEditorMoveFileRejectsSamePathAsLocalResult(t *testing.T) {
	got, err := TextEditorPatchInput(`{"command":"move_file","path":"scripts/readme.js","destination_path":"scripts/readme.js","old_str":"old","new_str":"new"}`)
	if err != nil {
		t.Fatalf("text editor patch: %v", err)
	}
	for _, want := range []string{
		"TEXT_EDITOR_MOVE_TARGET_SAME_AS_SOURCE",
		"path: scripts/readme.js",
		"required_next_action: use_str_replace_for_same_file_content_edits",
		"forbidden_next_action: retry_move_file_same_path",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("result missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "*** Move to:") {
		t.Fatalf("same-path move must not become patch: %s", got)
	}
}

func TestTextEditorArgumentsFromPatchReplaysMoveFile(t *testing.T) {
	got, ok := TextEditorArgumentsFromPatch("*** Begin Patch\n*** Update File: old.md\n*** Move to: new.md\n*** End Patch")
	if !ok {
		t.Fatalf("expected move patch to be reversible")
	}
	for _, want := range []string{`"command":"move_file"`, `"path":"old.md"`, `"destination_path":"new.md"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("arguments missing %q: %s", want, got)
		}
	}
}

func TestTextEditorArgumentsFromPatchReplaysMoveFileWithReplace(t *testing.T) {
	got, ok := TextEditorArgumentsFromPatch("*** Begin Patch\n*** Update File: old.md\n*** Move to: new.md\n@@\n-# Old\n+# New\n*** End Patch")
	if !ok {
		t.Fatalf("expected move patch to be reversible")
	}
	for _, want := range []string{`"command":"move_file"`, `"path":"old.md"`, `"destination_path":"new.md"`, `"old_str":"# Old"`, `"new_str":"# New"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("arguments missing %q: %s", want, got)
		}
	}
}

func TestToolSearchAndLocalShellBecomeChatFunctions(t *testing.T) {
	chatTools, ctx := FromCodex([]codex.ResponseTool{
		{Type: "tool_search"},
		{Type: "local_shell"},
	}, adapters.Get(adapters.DefaultName))
	if len(chatTools) != 2 {
		t.Fatalf("tools len = %d", len(chatTools))
	}
	if ctx.Entry("tool_search").Kind() != KindToolSearch {
		t.Fatalf("tool_search entry = %#v", ctx.Entry("tool_search"))
	}
	if ctx.Entry("shell").Kind() != KindShell {
		t.Fatalf("shell entry = %#v", ctx.Entry("shell"))
	}
}

func TestExecCommandFunctionStaysFunctionTool(t *testing.T) {
	params := json.RawMessage(`{"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"]}`)
	chatTools, ctx := FromCodex([]codex.ResponseTool{{Type: "function", Name: "exec_command", Parameters: params}}, adapters.Get(adapters.DefaultName))
	if len(chatTools) != 1 || chatTools[0].Function.Name != "exec_command" {
		t.Fatalf("chat tools = %#v", chatTools)
	}
	if ctx.Entry("exec_command").Kind() != KindFunction {
		t.Fatalf("exec_command entry = %#v", ctx.Entry("exec_command"))
	}
	if !strings.Contains(string(chatTools[0].Function.Parameters), `"cmd"`) {
		t.Fatalf("parameters = %s", chatTools[0].Function.Parameters)
	}
}

func TestNamespaceToolsKeepNamespaceInRegistry(t *testing.T) {
	var tool codex.ResponseTool
	raw := `{"type":"namespace","name":"browser","tools":[{"type":"function","name":"open","description":"open url","parameters":{"type":"object"}}]}`
	if err := json.Unmarshal([]byte(raw), &tool); err != nil {
		t.Fatalf("decode tool: %v", err)
	}
	chatTools, ctx := FromCodex([]codex.ResponseTool{tool}, adapters.Get(adapters.DefaultName))
	if len(chatTools) != 1 || chatTools[0].Function.Name != "browser__open" {
		t.Fatalf("chat tools = %#v", chatTools)
	}
	if entry := ctx.Entry("browser__open"); entry.Namespace != "browser" || entry.OriginalName() != "open" {
		t.Fatalf("entry = %#v", entry)
	}
}

func TestNamespaceToolsDoNotCollideWhenChildNamesMatch(t *testing.T) {
	var memoryTool codex.ResponseTool
	var localTool codex.ResponseTool
	if err := json.Unmarshal([]byte(`{"type":"namespace","name":"mcp__openviking_memory","tools":[{"type":"function","name":"read","description":"Read viking:// resources","parameters":{"type":"object"}}]}`), &memoryTool); err != nil {
		t.Fatalf("decode memory tool: %v", err)
	}
	if err := json.Unmarshal([]byte(`{"type":"namespace","name":"local_file","tools":[{"type":"function","name":"read","description":"Read local files","parameters":{"type":"object"}}]}`), &localTool); err != nil {
		t.Fatalf("decode local tool: %v", err)
	}
	chatTools, ctx := FromCodex([]codex.ResponseTool{memoryTool, localTool}, adapters.Get(adapters.DeepSeekName))
	if len(chatTools) != 2 {
		t.Fatalf("chat tools = %#v", chatTools)
	}
	names := []string{chatTools[0].Function.Name, chatTools[1].Function.Name}
	if strings.Join(names, ",") != "mcp_openviking_memory__read,local_file__read" {
		t.Fatalf("tool names = %#v", names)
	}
	if entry := ctx.Entry("mcp_openviking_memory__read"); entry.Namespace != "mcp__openviking_memory" || entry.OriginalName() != "read" {
		t.Fatalf("memory entry = %#v", entry)
	}
	if entry := ctx.Entry("local_file__read"); entry.Namespace != "local_file" || entry.OriginalName() != "read" {
		t.Fatalf("local entry = %#v", entry)
	}
}

func TestUnsupportedHostedToolsAreFiltered(t *testing.T) {
	chatTools, _ := FromCodex([]codex.ResponseTool{
		{Type: "web_search_preview"},
		{Type: "mcp", Name: "github"},
		{Type: "function", Name: "ok"},
	}, adapters.Get(adapters.DefaultName))
	if len(chatTools) != 1 || chatTools[0].Function.Name != "ok" {
		t.Fatalf("chat tools = %#v", chatTools)
	}
}

func TestToolChoiceConvertsForcedFunction(t *testing.T) {
	_, ctx := FromCodex([]codex.ResponseTool{{Type: "function", Name: "shell"}}, adapters.Get(adapters.DefaultName))
	got := ToolChoice(map[string]any{"type": "function", "name": "shell"}, ctx)
	obj, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("tool choice = %#v", got)
	}
	if obj["type"] != "function" {
		t.Fatalf("tool choice = %#v", obj)
	}
}

func TestToolChoiceFiltersAllowedTools(t *testing.T) {
	_, ctx := FromCodex([]codex.ResponseTool{{Type: "function", Name: "keep"}}, adapters.Get(adapters.DefaultName))
	got := ToolChoice(map[string]any{
		"type": "allowed_tools",
		"mode": "auto",
		"tools": []any{
			map[string]any{"type": "function", "name": "keep"},
			map[string]any{"type": "function", "name": "drop"},
		},
	}, ctx)
	obj, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("tool choice = %#v", got)
	}
	tools := obj["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("allowed tools = %#v", tools)
	}
}

func TestShellArgumentsAcceptArrayCommand(t *testing.T) {
	got := ShellArguments(`["bash","-lc","pwd"]`)
	commands := got["commands"].([]string)
	if len(commands) != 3 {
		t.Fatalf("commands = %#v", commands)
	}
}
