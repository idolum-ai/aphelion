//go:build linux

package core

import (
	"testing"
	"time"
)

func TestClassifySourceInstallStatusSeparatesServiceConsistencyFromReleaseFreshness(t *testing.T) {
	tests := []struct {
		name             string
		runningRevision  string
		expectedRevision string
		latestVersion    string
		metadataStatus   string
		updateAvailable  bool
		want             string
	}{
		{
			name:             "matching service with newer release reports update available",
			runningRevision:  "abc123",
			expectedRevision: "abc123",
			latestVersion:    "v0.3.0",
			metadataStatus:   "present",
			updateAvailable:  true,
			want:             "release_update_available",
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
		{
			name:             "missing metadata does not hide mismatched revision",
			runningRevision:  "abc123",
			expectedRevision: "def456",
			metadataStatus:   "missing",
			updateAvailable:  false,
			want:             "source_install_revision_mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifySourceInstallReliability(SourceInstallStatusInput{
				RunningRevision:  tt.runningRevision,
				ExpectedRevision: tt.expectedRevision,
				LatestVersion:    tt.latestVersion,
				MetadataStatus:   tt.metadataStatus,
				UpdateAvailable:  tt.updateAvailable,
			}).Condition
			if got != tt.want {
				t.Fatalf("ClassifySourceInstallReliability() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClassifySourceInstallReliabilityCarriesOperatorPolicy(t *testing.T) {
	got := ClassifySourceInstallReliabilityAxes(SourceInstallStatusInput{
		CurrentRevision:  "abc123",
		RunningRevision:  "abc123",
		ExpectedRevision: "abc123",
		LatestVersion:    "v0.3.0",
		MetadataStatus:   "present",
		UpdateAvailable:  true,
	})
	if got.ServiceConsistency.Condition != "source_service_consistent" {
		t.Fatalf("service condition = %q, want source_service_consistent", got.ServiceConsistency.Condition)
	}
	if got.ServiceConsistency.StatusClass != StatusClassCurrent ||
		got.ServiceConsistency.FailureClass != ReliabilityFailureNone ||
		got.ServiceConsistency.RetryPolicy != ReliabilityRetryNone ||
		got.ServiceConsistency.NextAction != "none" {
		t.Fatalf("service classification = %#v, want quiet current service axis", got.ServiceConsistency)
	}
	if got.ReleaseFreshness.Condition != "release_update_available" {
		t.Fatalf("freshness condition = %q, want release_update_available", got.ReleaseFreshness.Condition)
	}
	if got.ReleaseFreshness.StatusClass != StatusClassOperationalTension {
		t.Fatalf("freshness status class = %q, want operational tension", got.ReleaseFreshness.StatusClass)
	}
	if got.ReleaseFreshness.FailureClass != ReliabilityFailureReleaseFreshness {
		t.Fatalf("freshness failure class = %q, want release freshness", got.ReleaseFreshness.FailureClass)
	}
	if got.ReleaseFreshness.RetryPolicy != ReliabilityRetryReinstallService {
		t.Fatalf("freshness retry policy = %q, want install/restart guidance", got.ReleaseFreshness.RetryPolicy)
	}
	if got.ReleaseFreshness.NextAction == "" || got.ReleaseFreshness.NextAction == "none" {
		t.Fatalf("freshness next action = %q, want operator-legible action", got.ReleaseFreshness.NextAction)
	}
	if got.Overall.Condition != "release_update_available" {
		t.Fatalf("overall condition = %q, want release_update_available", got.Overall.Condition)
	}
}

func TestClassifySourceInstallReliabilityRequiresExplicitUpdateTarget(t *testing.T) {
	got := ClassifySourceInstallReliabilityAxes(SourceInstallStatusInput{
		CurrentRevision:  "abc123",
		RunningRevision:  "abc123",
		ExpectedRevision: "abc123",
		MetadataStatus:   "present",
		UpdateAvailable:  true,
	})
	if got.ServiceConsistency.Condition != "source_service_consistent" {
		t.Fatalf("service condition = %q, want source_service_consistent", got.ServiceConsistency.Condition)
	}
	if got.ReleaseFreshness.Condition != "release_metadata_update_target_missing" {
		t.Fatalf("freshness condition = %q, want release_metadata_update_target_missing", got.ReleaseFreshness.Condition)
	}
	if got.ReleaseFreshness.FailureClass != ReliabilityFailureReleaseMetadata ||
		got.ReleaseFreshness.RetryPolicy != ReliabilityRetryRefreshMetadata {
		t.Fatalf("freshness classification = %#v, want metadata refresh guidance", got.ReleaseFreshness)
	}
}

func TestClassifySourceInstallReliabilityDoesNotMaskConcreteMismatch(t *testing.T) {
	for _, metadataStatus := range []string{"missing", "unreadable"} {
		t.Run(metadataStatus, func(t *testing.T) {
			got := ClassifySourceInstallReliabilityAxes(SourceInstallStatusInput{
				CurrentRevision:  "abc123",
				RunningRevision:  "abc123",
				ExpectedRevision: "def456",
				MetadataStatus:   metadataStatus,
			})
			if got.ServiceConsistency.Condition != "source_install_revision_mismatch" {
				t.Fatalf("service condition = %q, want source_install_revision_mismatch", got.ServiceConsistency.Condition)
			}
			if got.ReleaseFreshness.Condition != "release_metadata_"+metadataStatus {
				t.Fatalf("freshness condition = %q, want release_metadata_%s", got.ReleaseFreshness.Condition, metadataStatus)
			}
			if got.Overall.Condition != "source_install_revision_mismatch" {
				t.Fatalf("overall condition = %q, want concrete service mismatch to win", got.Overall.Condition)
			}
		})
	}
}

func TestHealthyReliabilityClassificationsAreCurrentAndQuiet(t *testing.T) {
	tests := []struct {
		name string
		got  StatusReliabilityClassification
	}{
		{
			name: "source install current",
			got: ClassifySourceInstallReliability(SourceInstallStatusInput{
				CurrentRevision:  "abc123",
				RunningRevision:  "abc123",
				ExpectedRevision: "abc123",
				MetadataStatus:   "present",
			}),
		},
		{
			name: "provider current",
			got:  ClassifyProviderReliability("", ""),
		},
		{
			name: "transport current",
			got:  ClassifyTransportReliability("telegram:primary", ""),
		},
		{
			name: "persistence current",
			got:  ClassifyPersistenceLatency("execution_events:turn_started", PersistenceLatencySlowThreshold-time.Millisecond),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got.StatusClass != StatusClassCurrent {
				t.Fatalf("status class = %q, want current: %#v", tt.got.StatusClass, tt.got)
			}
			if tt.got.FailureClass != ReliabilityFailureNone || tt.got.RetryPolicy != ReliabilityRetryNone || tt.got.NextAction != "none" {
				t.Fatalf("classification = %#v, want no failure, no retry, no next action", tt.got)
			}
		})
	}
}
