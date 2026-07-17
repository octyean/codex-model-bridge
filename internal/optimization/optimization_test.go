package optimization

import "testing"

func TestCaptureResponseShapeTracksStablePrefix(t *testing.T) {
	request := func(developer string, user string) map[string]any {
		return map[string]any{
			"input": []any{
				map[string]any{"role": "developer", "content": developer},
				map[string]any{"role": "user", "content": user},
			},
			"tools": []any{
				map[string]any{"type": "function", "name": "read_file", "parameters": map[string]any{"type": "object"}},
			},
		}
	}

	first := CaptureResponseShape(request("stable instructions", "first question"))
	samePrefix := CaptureResponseShape(request("stable instructions", "second question"))
	changedPrefix := CaptureResponseShape(request("changed instructions", "second question"))
	typedToolsRequest := request("stable instructions", "first question")
	typedToolsRequest["tools"] = []map[string]any{
		{"type": "function", "name": "read_file", "parameters": map[string]any{"type": "object"}},
	}
	typedTools := CaptureResponseShape(typedToolsRequest)

	if first.MessageCount != 2 || first.ToolCount != 1 || first.ToolSchemaTokens == 0 {
		t.Fatalf("shape = %#v", first)
	}
	if first.PrefixHash != samePrefix.PrefixHash {
		t.Fatalf("user content changed prefix hash: %q != %q", first.PrefixHash, samePrefix.PrefixHash)
	}
	if first.SystemHash == changedPrefix.SystemHash || first.PrefixHash == changedPrefix.PrefixHash {
		t.Fatalf("developer change was not captured: first=%#v changed=%#v", first, changedPrefix)
	}
	if typedTools.ToolCount != 1 || typedTools.ToolsHash != first.ToolsHash {
		t.Fatalf("typed tools shape = %#v, want tools hash %q", typedTools, first.ToolsHash)
	}
}
