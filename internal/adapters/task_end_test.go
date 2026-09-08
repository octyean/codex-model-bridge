package adapters

import (
	"strings"
	"testing"
)

func TestUseExplicitTaskEndOnlyForThirdPartyNonGPTModels(t *testing.T) {
	tests := []struct {
		name     string
		adapter  Adapter
		model    string
		expected bool
	}{
		{name: "kimi", adapter: Get(KimiName), model: "kimi-for-coding", expected: true},
		{name: "mimo", adapter: Get(MimoName), model: "mimo-v2.5-pro", expected: true},
		{name: "default third party", adapter: Get(DefaultName), model: "claude-sonnet-4", expected: true},
		{name: "gpt default profile", adapter: Get(DefaultName), model: "gpt-5.6-sol", expected: false},
		{name: "chatgpt default profile", adapter: Get(DefaultName), model: "chatgpt-4o-latest", expected: false},
		{name: "reasoning model default profile", adapter: Get(DefaultName), model: "o3-pro", expected: false},
		{name: "openai profile", adapter: Get(OpenAIName), model: "custom-openai-model", expected: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := UseExplicitTaskEnd(test.adapter, test.model); actual != test.expected {
				t.Fatalf("UseExplicitTaskEnd(%q, %q) = %v, want %v", test.adapter.Name(), test.model, actual, test.expected)
			}
		})
	}
}

func TestThirdPartyAdaptersPreserveExactFinalConstraints(t *testing.T) {
	for _, name := range []string{KimiName, MimoName} {
		note := Get(name).ResponseDisciplineNote()
		if !strings.Contains(note, "output exactly that text with no prefix, suffix, or completion summary") {
			t.Fatalf("%s response discipline is missing exact-final rule:\n%s", name, note)
		}
	}
}
