package adapters

import "testing"

func TestMimoAdapterSupportsImageInput(t *testing.T) {
	adapter := Get(MimoName)
	caps := adapter.Capabilities()
	if adapter.Name() != MimoName {
		t.Fatalf("adapter name = %q", adapter.Name())
	}
	if !HasImageInput(caps) {
		t.Fatalf("mimo adapter should support image input")
	}
	if !caps.SupportsImageDetailOriginal {
		t.Fatalf("mimo adapter should keep original image detail")
	}
}
