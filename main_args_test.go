//go:build linux

package main

import "testing"

func TestFirstPositionalArgFindsFirstNonEmpty(t *testing.T) {
	t.Parallel()

	got, ok := firstPositionalArg([]string{"", "   ", "m", "other"})
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if got != "m" {
		t.Fatalf("firstPositionalArg() = %q, want m", got)
	}
}

func TestFirstPositionalArgReturnsFalseWhenEmpty(t *testing.T) {
	t.Parallel()

	got, ok := firstPositionalArg([]string{"", " \t "})
	if ok {
		t.Fatalf("ok = true with value %q, want false", got)
	}
	if got != "" {
		t.Fatalf("value = %q, want empty", got)
	}
}
