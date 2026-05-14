//go:build linux

package core

import "testing"

func TestNormalizeDurableAgentLivePolicyAcceptsParentRelayOnlyAlias(t *testing.T) {
	t.Parallel()

	policy := NormalizeDurableAgentLivePolicy(DurableAgentLivePolicy{PublicSurfaceMode: "parent_relay_only"})
	if policy.PublicSurfaceMode != "explicit_parent_relay_only" {
		t.Fatalf("PublicSurfaceMode = %q, want explicit_parent_relay_only", policy.PublicSurfaceMode)
	}
	if policy.PublicSurfaceMode == "none" {
		t.Fatal("PublicSurfaceMode normalized to none; recipe alias must not silently erase visibility")
	}
}
