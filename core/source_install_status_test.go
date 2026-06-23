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
			name:             "mismatched revision with update available remains update available",
			runningRevision:  "abc123",
			expectedRevision: "def456",
			metadataStatus:   "present",
			updateAvailable:  true,
			want:             "release_update_available",
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
