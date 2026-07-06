//go:build linux

package runtime

import "testing"

func TestRecoveryRequestExplicitlySelectsCandidateRequiresSpecificToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		request   string
		candidate string
		want      bool
	}{
		{
			name:      "matches explicit PR number",
			request:   "Resume PR 220 review now.",
			candidate: "Review PR 220 and report findings.",
			want:      true,
		},
		{
			name:      "does not match a different PR number",
			request:   "Resume PR 220 review now.",
			candidate: "Review PR 221 and report findings.",
			want:      false,
		},
		{
			name:      "does not match vacuous resume request",
			request:   "Resume review now.",
			candidate: "Review the parked operation and report findings.",
			want:      false,
		},
		{
			name:      "matches short phase identifier",
			request:   "Continue phase C7 now.",
			candidate: "Verify prior outcome for book-phase-c7-merge-render-specimen-pdf.",
			want:      true,
		},
		{
			name:      "negated resume does not match",
			request:   "Do not continue PR 220; stay on the PDF.",
			candidate: "Review PR 220 and report findings.",
			want:      false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := recoveryRequestExplicitlySelectsCandidate(tt.request, tt.candidate); got != tt.want {
				t.Fatalf("recoveryRequestExplicitlySelectsCandidate(%q, %q) = %v, want %v", tt.request, tt.candidate, got, tt.want)
			}
		})
	}
}
