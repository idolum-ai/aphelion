//go:build linux

package core

import "testing"

func TestNormalizeDurableAgentLivePolicyTailnetDeclaration(t *testing.T) {
	t.Parallel()

	policy := NormalizeDurableAgentLivePolicy(DurableAgentLivePolicy{
		TailnetMode:          " TSNet ",
		TailnetHostname:      " Child-Mail ",
		TailnetTags:          []string{" tag:aphelion-child ", "tag:mail", "tag:aphelion-child"},
		TailnetSurfacePolicy: " status ",
	})

	if policy.TailnetMode != "tsnet" {
		t.Fatalf("TailnetMode = %q, want tsnet", policy.TailnetMode)
	}
	if policy.TailnetHostname != "child-mail" {
		t.Fatalf("TailnetHostname = %q, want lowercase trimmed hostname", policy.TailnetHostname)
	}
	if len(policy.TailnetTags) != 2 || policy.TailnetTags[0] != "tag:aphelion-child" || policy.TailnetTags[1] != "tag:mail" {
		t.Fatalf("TailnetTags = %#v, want deduped tags", policy.TailnetTags)
	}
	if policy.TailnetSurfacePolicy != "private_status" {
		t.Fatalf("TailnetSurfacePolicy = %q, want private_status", policy.TailnetSurfacePolicy)
	}
}

func TestNormalizeDurableAgentLivePolicyClearsInvalidTailnetDeclaration(t *testing.T) {
	t.Parallel()

	policy := NormalizeDurableAgentLivePolicy(DurableAgentLivePolicy{
		TailnetMode:          "public",
		TailnetHostname:      "child-mail",
		TailnetTags:          []string{"tag:mail"},
		TailnetSurfacePolicy: "public_http",
	})

	if policy.TailnetMode != "" || policy.TailnetHostname != "" || policy.TailnetTags != nil || policy.TailnetSurfacePolicy != "" {
		t.Fatalf("tailnet declaration = %#v, want invalid declaration cleared", policy)
	}
}
