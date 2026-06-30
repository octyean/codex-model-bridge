package upstreamprobe

import "testing"

func TestProbeURLs(t *testing.T) {
	base := "https://example.test/v1/responses"
	if got := responsesURL(base); got != "https://example.test/v1/responses" {
		t.Fatalf("responsesURL = %q", got)
	}
	if got := chatCompletionsURL(base); got != "https://example.test/v1/chat/completions" {
		t.Fatalf("chatCompletionsURL = %q", got)
	}
	if got := modelsURL(base); got != "https://example.test/v1/models" {
		t.Fatalf("modelsURL = %q", got)
	}
}
