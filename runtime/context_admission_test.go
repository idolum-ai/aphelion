//go:build linux

package runtime

import (
	"testing"

	"github.com/idolum-ai/aphelion/agent"
)

func TestModelContextAdmissionPayloadSurfacesToolEvidenceProjection(t *testing.T) {
	t.Parallel()

	empty := modelContextAdmissionPayload(agent.ModelRequestEvent{})
	if len(empty) != 0 {
		t.Fatalf("empty admission payload = %#v, want omitted", empty)
	}

	payload := modelContextAdmissionPayload(agent.ModelRequestEvent{
		ContextAdmissionToolEvidenceLayers:  3,
		ContextAdmissionToolEvidencePacked:  2,
		ContextAdmissionToolEvidenceDigests: 1,
		ContextAdmissionSuppressedLayers:    1,
	})
	if payload["tool_evidence_layers"] != 3 ||
		payload["tool_evidence_packed"] != 2 ||
		payload["tool_evidence_digests"] != 1 ||
		payload["suppressed_layers"] != 1 {
		t.Fatalf("admission payload = %#v, want tool evidence projection counts", payload)
	}
}
