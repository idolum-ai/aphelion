//go:build linux

package runtime

import "testing"

func TestFaceModelForProviderSupportsOpus47(t *testing.T) {
	t.Parallel()
	if got := faceModelForProvider("anthropic", personaModelOpus47); got != personaModelOpus47 {
		t.Fatalf("anthropic faceModelForProvider() = %q, want %q", got, personaModelOpus47)
	}
	if got := faceModelForProvider("openrouter", personaModelOpus47); got != "anthropic/"+personaModelOpus47 {
		t.Fatalf("openrouter faceModelForProvider() = %q, want anthropic/%s", got, personaModelOpus47)
	}
}
