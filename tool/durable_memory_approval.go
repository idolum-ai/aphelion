//go:build linux

package tool

import (
	"context"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

type DurableMemoryDelegationApprover interface {
	ConfirmDurableMemoryDelegation(ctx context.Context, req DurableMemoryDelegationApprovalRequest) (DurableMemoryDelegationApprovalDecision, error)
}

type DurableMemoryDelegationApprovalRequest struct {
	Principal  principal.Principal
	SessionKey session.SessionKey
	Agent      core.DurableAgent
	Reason     string
	Entries    []DurableMemoryDelegationEntry
}

type DurableMemoryDelegationEntry struct {
	CandidateID string
	SourceStore string
	TargetStore string
	Content     string
}

type DurableMemoryDelegationApprovalDecision struct {
	Approved bool
	TimedOut bool
}
