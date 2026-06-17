//go:build linux

package runtime

import "testing"

func TestFaceModelForProviderSupportsOpus48(t *testing.T) {
	t.Parallel()
	if got := faceModelForProvider("anthropic", personaModelOpus48); got != personaModelOpus48 {
		t.Fatalf("anthropic faceModelForProvider() = %q, want %q", got, personaModelOpus48)
	}
	if got := faceModelForProvider("openrouter", personaModelOpus48); got != "anthropic/"+personaModelOpus48 {
		t.Fatalf("openrouter faceModelForProvider() = %q, want anthropic/%s", got, personaModelOpus48)
	}
}

func TestFaceModelForProviderSupportsGPT55(t *testing.T) {
	t.Parallel()
	if got := faceModelForProvider("openai", personaModelGPT55); got != personaModelGPT55 {
		t.Fatalf("openai faceModelForProvider() = %q, want %q", got, personaModelGPT55)
	}
	if got := faceModelForProvider("openrouter", personaModelGPT55); got != "openai/"+personaModelGPT55 {
		t.Fatalf("openrouter faceModelForProvider() = %q, want openai/%s", got, personaModelGPT55)
	}
	if got := faceModelForProvider("anthropic", personaModelGPT55); got != personaModelSonnet {
		t.Fatalf("anthropic faceModelForProvider() = %q, want fallback %q", got, personaModelSonnet)
	}
}
