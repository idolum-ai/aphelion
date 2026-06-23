//go:build linux

package core

func ClassifySourceInstallStatus(runningRevision string, expectedRevision string, metadataStatus string, updateAvailable bool) string {
	return ClassifySourceInstallReliability(SourceInstallStatusInput{
		RunningRevision:  runningRevision,
		ExpectedRevision: expectedRevision,
		MetadataStatus:   metadataStatus,
		UpdateAvailable:  updateAvailable,
	}).Condition
}
