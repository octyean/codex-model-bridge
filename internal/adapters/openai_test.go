package adapters

import "testing"

func TestOpenAIAdapterSupportsImageInput(t *testing.T) {
	adapter := Get(OpenAIName)
	caps := adapter.Capabilities()
	if adapter.Name() != OpenAIName {
		t.Fatalf("adapter name = %q", adapter.Name())
	}
	if !HasImageInput(caps) {
		t.Fatalf("openai adapter should support image input")
	}
	if !caps.SupportsImageDetailOriginal {
		t.Fatalf("openai adapter should keep original image detail")
	}
}
