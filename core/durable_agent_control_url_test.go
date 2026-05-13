//go:build linux

package core

import "testing"

func TestValidateDurableAgentParentControlURLTransportPolicy(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"https://house.example/control",
		"http://localhost:8765/control",
		"http://127.0.0.1:8765/control",
		"http://[::1]:8765/control",
		"http://aphelion.example.ts.net:8765/control",
	} {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			if err := ValidateDurableAgentParentControlURL(raw); err != nil {
				t.Fatalf("ValidateDurableAgentParentControlURL(%q) err = %v", raw, err)
			}
		})
	}

	for _, raw := range []string{
		"http://house.example/control",
		"http://ts.net/control",
		"ftp://house.example/control",
		"house.example/control",
	} {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			if err := ValidateDurableAgentParentControlURL(raw); err == nil {
				t.Fatalf("ValidateDurableAgentParentControlURL(%q) err = nil, want rejection", raw)
			}
		})
	}
}
