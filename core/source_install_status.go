//go:build linux

package core

import "strings"

func ClassifySourceInstallStatus(runningRevision string, expectedRevision string, metadataStatus string, updateAvailable bool) string {
	runningRevision = strings.TrimSpace(runningRevision)
	expectedRevision = strings.TrimSpace(expectedRevision)
	metadataStatus = strings.TrimSpace(metadataStatus)
	if metadataStatus != "" && metadataStatus != "present" && metadataStatus != "current" && metadataStatus != "ok" {
		return "release_metadata_" + metadataStatus
	}
	if runningRevision != "" && expectedRevision != "" && runningRevision == expectedRevision && updateAvailable {
		return "source_verified_release_metadata_stale"
	}
	if updateAvailable {
		return "release_update_available"
	}
	return "release_status_current"
}
