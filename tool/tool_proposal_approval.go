//go:build linux

package tool

import (
	"context"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

type ToolProposalRatificationApprover interface {
	ConfirmToolProposalRatification(ctx context.Context, req ToolProposalRatificationApprovalRequest) (ToolProposalRatificationApprovalDecision, error)
}

type ToolProposalRatificationApprovalRequest struct {
	Principal  principal.Principal
	SessionKey session.SessionKey
	Proposal   session.ToolProposal
}

type ToolProposalRatificationApprovalDecision struct {
	Approved bool
	TimedOut bool
}
