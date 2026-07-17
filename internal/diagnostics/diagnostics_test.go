package diagnostics

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
)

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

func TestWriteJSONLConcurrentLinesRemainValid(t *testing.T) {
	path := t.TempDir() + "/session.jsonl"
	const writers = 32
	payload := strings.Repeat("x", 128*1024)

	var wait sync.WaitGroup
	for index := 0; index < writers; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			WriteJSONL(path, map[string]any{"index": index, "payload": payload})
		}(index)
	}
	wait.Wait()

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	seen := make(map[int]bool, writers)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 256*1024)
	for scanner.Scan() {
		var record struct {
			Index   int    `json:"index"`
			Payload string `json:"payload"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("invalid JSONL line: %v", err)
		}
		if record.Payload != payload {
			t.Fatalf("payload for line %d was truncated", record.Index)
		}
		seen[record.Index] = true
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(seen) != writers {
		t.Fatalf("wrote %d distinct lines, want %d", len(seen), writers)
	}
}
