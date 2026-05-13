//go:build linux

package session

const (
	MissionStatusDormant   MissionStatus = "dormant"
	MissionStatusCandidate MissionStatus = "candidate"
	MissionStatusActive    MissionStatus = "active"
	MissionStatusBlocked   MissionStatus = "blocked"
	MissionStatusCompleted MissionStatus = "completed"
	MissionStatusExpired   MissionStatus = "expired"
	MissionStatusArchived  MissionStatus = "archived"
)
