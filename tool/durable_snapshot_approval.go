//go:build linux

package tool

import (
	"context"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

type DurableSnapshotRestoreApprover interface {
	ConfirmDurableSnapshotRestore(ctx context.Context, req DurableSnapshotRestoreApprovalRequest) (DurableSnapshotRestoreApprovalDecision, error)
}

type DurableSnapshotRestoreApprovalRequest struct {
	Principal         principal.Principal
	SessionKey        session.SessionKey
	Agent             core.DurableAgent
	SnapshotID        string
	SnapshotReason    string
	SnapshotCreatedAt time.Time
}

type DurableSnapshotRestoreApprovalDecision struct {
	Approved bool
	TimedOut bool
}
