//go:build linux

package core

func ClassifySourceInstallStatus(runningRevision string, expectedRevision string, metadataStatus string, updateAvailable bool) string {
	latestVersion := ""
	if updateAvailable {
		latestVersion = "precomputed_update_available"
	}
	return ClassifySourceInstallReliability(SourceInstallStatusInput{
		RunningRevision:  runningRevision,
		ExpectedRevision: expectedRevision,
		LatestVersion:    latestVersion,
		MetadataStatus:   metadataStatus,
		UpdateAvailable:  updateAvailable,
	}).Condition
}
