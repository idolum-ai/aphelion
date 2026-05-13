//go:build linux

package core

import (
	"strings"
	"testing"
)

func TestNormalizeInterpretationClaimDefaultsSchemaAndDedupesLists(t *testing.T) {
	claim := NormalizeInterpretationClaim(InterpretationClaim{
		Intent:         " media_reply_modality ",
		AuthorityClass: " read_only_review ",
		Risk:           []string{" audio ", "audio", ""},
		EvidenceRefs:   []string{" floor_metadata ", "floor_metadata"},
	})
	if claim.SchemaVersion != InterpretationSchemaV1 {
		t.Fatalf("SchemaVersion = %q, want %q", claim.SchemaVersion, InterpretationSchemaV1)
	}
	if claim.Intent != "media_reply_modality" || claim.AuthorityClass != "read_only_review" {
		t.Fatalf("claim = %#v, want trimmed tokens", claim)
	}
	if len(claim.Risk) != 1 || claim.Risk[0] != "audio" {
		t.Fatalf("Risk = %#v, want deduped audio", claim.Risk)
	}
	if len(claim.EvidenceRefs) != 1 || claim.EvidenceRefs[0] != "floor_metadata" {
		t.Fatalf("EvidenceRefs = %#v, want deduped floor_metadata", claim.EvidenceRefs)
	}
	if !claim.Active() {
		t.Fatal("claim.Active() = false, want true")
	}
}

func TestDebugBreadcrumbLinesAreStableAndCompact(t *testing.T) {
	crumb := ContinuationDebugBreadcrumb(6313146, "aprop-example", "runtime.renderApproval", "runtime/continuation.go", "open /health trace for canonical state")
	lines := DebugBreadcrumbLines(crumb)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"trace_id: continuation:6313146:aprop-example",
		"canonical_record: continuation_state chat_id=6313146 decision_id=aprop-example",
		"projection: runtime.renderApproval",
		"inspect_command: /health trace 6313146",
		"code_owner: runtime/continuation.go",
		"next_repair_action: open /health trace for canonical state",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("breadcrumb lines = %#v, want %q", lines, want)
		}
	}
}
