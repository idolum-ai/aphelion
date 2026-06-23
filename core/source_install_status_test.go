//go:build linux

package core

import "testing"

func TestClassifySourceInstallStatusSeparatesVerifiedSourceFromStaleReleaseMetadata(t *testing.T) {
	tests := []struct {
		name             string
		runningRevision  string
		expectedRevision string
		metadataStatus   string
		updateAvailable  bool
		want             string
	}{
		{
			name:             "source revision verified while release metadata is stale",
			runningRevision:  "abc123",
			expectedRevision: "abc123",
			metadataStatus:   "present",
			updateAvailable:  true,
			want:             "source_verified_release_metadata_stale",
		},
		{
			name:             "mismatched revision reports install revision mismatch",
			runningRevision:  "abc123",
			expectedRevision: "def456",
			metadataStatus:   "present",
			updateAvailable:  true,
			want:             "source_install_revision_mismatch",
		},
		{
			name:             "no update available is current",
			runningRevision:  "abc123",
			expectedRevision: "abc123",
			metadataStatus:   "present",
			updateAvailable:  false,
			want:             "release_status_current",
		},
		{
			name:             "unreadable metadata is reported separately",
			runningRevision:  "abc123",
			expectedRevision: "abc123",
			metadataStatus:   "unreadable",
			updateAvailable:  false,
			want:             "release_metadata_unreadable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifySourceInstallStatus(tt.runningRevision, tt.expectedRevision, tt.metadataStatus, tt.updateAvailable)
			if got != tt.want {
				t.Fatalf("ClassifySourceInstallStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClassifySourceInstallReliabilityCarriesOperatorPolicy(t *testing.T) {
	got := ClassifySourceInstallReliability(SourceInstallStatusInput{
		CurrentRevision:  "abc123",
		RunningRevision:  "abc123",
		ExpectedRevision: "abc123",
		MetadataStatus:   "present",
		UpdateAvailable:  true,
	})
	if got.Condition != "source_verified_release_metadata_stale" {
		t.Fatalf("condition = %q, want stale source metadata", got.Condition)
	}
	if got.StatusClass != StatusClassOperationalTension {
		t.Fatalf("status class = %q, want operational tension", got.StatusClass)
	}
	if got.FailureClass != ReliabilityFailureReleaseFreshness {
		t.Fatalf("failure class = %q, want release freshness", got.FailureClass)
	}
	if got.RetryPolicy != ReliabilityRetryRefreshMetadata {
		t.Fatalf("retry policy = %q, want metadata refresh", got.RetryPolicy)
	}
	if got.NextAction == "" || got.NextAction == "none" {
		t.Fatalf("next action = %q, want operator-legible action", got.NextAction)
	}
}
