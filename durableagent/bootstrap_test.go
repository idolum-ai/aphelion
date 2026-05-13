//go:build linux

package durableagent

import (
	"path/filepath"
	"testing"

	"github.com/idolum-ai/aphelion/core"
)

func TestRemoteBootstrapRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "child-bootstrap.json")
	want := core.DurableAgentRemoteBootstrap{
		AgentID:          "family-group",
		ParentAgentID:    "house",
		ChannelKind:      "telegram_group",
		ParentControlURL: "https://house.example/control",
		EnrollmentToken:  "enroll-token-1",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
		BootstrapLLM:     testDurableAgentBootstrapLLM(),
		BootstrapCeiling: core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DurableAgentLivePolicy{
			Charter:            "Observe and surface bounded family coordination.",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		LocalStorageRoots: []string{"/srv/aphelion/family-group/work", "/srv/aphelion/family-group/memory"},
		SecretScopes:      []string{"telegram_bot"},
		NetworkPolicy:     "restricted",
	}

	if err := WriteRemoteBootstrap(path, want); err != nil {
		t.Fatalf("WriteRemoteBootstrap() err = %v", err)
	}
	got, err := ReadRemoteBootstrap(path)
	if err != nil {
		t.Fatalf("ReadRemoteBootstrap() err = %v", err)
	}
	if got.AgentID != want.AgentID {
		t.Fatalf("AgentID = %q, want %q", got.AgentID, want.AgentID)
	}
	if got.ParentControlURL != want.ParentControlURL {
		t.Fatalf("ParentControlURL = %q, want %q", got.ParentControlURL, want.ParentControlURL)
	}
	if got.BootstrapLLM.Backend != "native" {
		t.Fatalf("BootstrapLLM.Backend = %q, want native", got.BootstrapLLM.Backend)
	}
	if got.BootstrapLLM.NativeProvider != "openrouter" {
		t.Fatalf("BootstrapLLM.NativeProvider = %q, want openrouter", got.BootstrapLLM.NativeProvider)
	}
	if len(got.LocalStorageRoots) != 2 {
		t.Fatalf("LocalStorageRoots len = %d, want 2", len(got.LocalStorageRoots))
	}
}

func TestRemoteBootstrapEnrollmentPayloadUsesNormalizedIdentity(t *testing.T) {
	t.Parallel()

	bootstrap := core.DurableAgentRemoteBootstrap{
		AgentID:          " family-group ",
		ParentAgentID:    " house ",
		ChannelKind:      " telegram_group ",
		ParentControlURL: " https://house.example/control ",
		EnrollmentToken:  " enroll-token-1 ",
		BootstrapLLM:     testDurableAgentBootstrapLLM(),
	}

	payload := bootstrap.EnrollmentPayload()
	if payload.AgentID != "family-group" {
		t.Fatalf("EnrollmentPayload().AgentID = %q, want family-group", payload.AgentID)
	}
	if payload.ParentAgentID != "house" {
		t.Fatalf("EnrollmentPayload().ParentAgentID = %q, want house", payload.ParentAgentID)
	}
	if payload.EnrollmentToken != "enroll-token-1" {
		t.Fatalf("EnrollmentPayload().EnrollmentToken = %q, want trimmed token", payload.EnrollmentToken)
	}
	if payload.ProtocolVersion != core.DefaultDurableAgentControlProtocolVersion {
		t.Fatalf("EnrollmentPayload().ProtocolVersion = %q, want %q", payload.ProtocolVersion, core.DefaultDurableAgentControlProtocolVersion)
	}
	if err := core.ValidateDurableAgentEnrollmentPayload(payload); err != nil {
		t.Fatalf("ValidateDurableAgentEnrollmentPayload() err = %v", err)
	}
}
