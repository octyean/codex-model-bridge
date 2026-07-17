package config

import (
	"strings"
	"testing"
)

func TestDefaultConfigTextUsesRandomLocalToken(t *testing.T) {
	first, err := DefaultConfigText(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := DefaultConfigText(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	firstToken := configValue(first, "local_token")
	secondToken := configValue(second, "local_token")
	if len(firstToken) != 40 || len(secondToken) != 40 {
		t.Fatalf("unexpected token lengths: %d, %d", len(firstToken), len(secondToken))
	}
	if firstToken == secondToken {
		t.Fatal("default configs should not reuse a fixed local token")
	}
	if firstToken == "codex-bridge-local-token" || secondToken == "codex-bridge-local-token" {
		t.Fatal("default config used the legacy fixed token")
	}
}

func TestDefaultContextWindowForKimiForCoding(t *testing.T) {
	if got := DefaultContextWindowForModel("kimi-for-coding"); got != 256000 {
		t.Fatalf("context window = %d, want 256000", got)
	}
}

func configValue(text string, key string) string {
	prefix := key + " = \""
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, prefix) && strings.HasSuffix(line, "\"") {
			return strings.TrimSuffix(strings.TrimPrefix(line, prefix), "\"")
		}
	}
	return ""
}
