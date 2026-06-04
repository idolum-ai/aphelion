//go:build linux

package standalonecli

import (
	"context"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const (
	VerifyDeployDefaultTimeout          = verifyDeployDefaultTimeout
	VerifyDeployBlessingPrefix          = verifyDeployBlessingPrefix
	VerifyDeployProbePrompt             = verifyDeployProbePrompt
	VerifyDeployDurableChildrenStatus   = verifyDeployDurableChildrenStatus
	VerifyDeployDurableChildrenRequired = verifyDeployDurableChildrenRequired
	VerifyDeployDurableChildrenWarn     = verifyDeployDurableChildrenWarn
	VerifyDeployDurableChildrenOff      = verifyDeployDurableChildrenOff
	DeployProbeStatusPass               = deployProbeStatusPass
	DeployProbeStatusFail               = deployProbeStatusFail
)

type DeployProbeStatus = deployProbeStatus
type DeployProbeResult = deployProbeResult
type DeployVerificationReport = deployVerificationReport
type DeployVerificationOptions = deployVerificationOptions
type DeployTurnRunner = deployTurnRunner
type DeployVerificationSender = deployVerificationSender
type BuiltDeployVerificationRuntime = builtDeployVerificationRuntime
type ServiceGuardCheck = serviceGuardCheck
type ServiceGuardReport = serviceGuardReport

func VerifyAphelionServiceGuard(ctx context.Context, check ServiceGuardCheck) (ServiceGuardReport, error) {
	return verifyAphelionServiceGuard(ctx, check)
}

func NormalizeVerifyDeployDurableChildrenMode(mode string) (string, error) {
	return normalizeVerifyDeployDurableChildrenMode(mode)
}
func VerifyDeployDurableChildrenStatusSummary(store *session.SQLiteStore) (string, error) {
	return verifyDeployDurableChildrenStatusSummary(store)
}
func VerifyDeployDurableChildren(ctx context.Context, store *session.SQLiteStore, wake func(context.Context, string, time.Time) error) (string, error) {
	return verifyDeployDurableChildren(ctx, store, wake)
}
func VerifyDeployActiveDurableChildren(store *session.SQLiteStore) ([]core.DurableAgent, error) {
	return verifyDeployActiveDurableChildren(store)
}
