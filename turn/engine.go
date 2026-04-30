//go:build linux

package turn

import (
	"context"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/prompt"
)

// Machine is the production Engine implementation.
//
// It keeps turn lifecycle stages explicit while delegating concrete behavior to
// governor/face/persistence/delivery ports.
type Machine struct {
	Governor         GovernorPort
	Face             FacePort
	Persistence      PersistencePort
	Delivery         DeliveryPort
	Options          Options
	PolicyFunc       func(Request) Policy
	RuntimeAwareness prompt.RuntimeAwareness
}

var _ Engine = (*Machine)(nil)

func (m *Machine) Handle(ctx context.Context, req Request) (*Result, error) {
	if err := m.validateRequest(req); err != nil {
		return nil, err
	}
	prepared := m.prepareContext(req)
	policy := m.policyFor(req)
	result := &Result{Policy: policy, ReplyWithVoice: req.ReplyWithVoice}

	proposal, err := m.maybePropose(ctx, prepared, policy)
	if err != nil {
		return nil, err
	}
	if proposal != nil {
		result.ProposalNote = strings.TrimSpace(proposal.Note)
	}

	gov, err := m.executeGovernor(ctx, prepared, policy, proposal)
	if err != nil {
		return nil, err
	}
	result.Turn = gov.Turn
	result.Prepared = gov.Prepared
	result.OutHistory = append([]agent.Message(nil), gov.OutHistory...)
	result.HistoryInputLen = gov.HistoryInputLen
	result.FloorText = strings.TrimSpace(gov.FloorText)
	result.FloorMetadata = strings.TrimSpace(gov.FloorMetadata)
	result.MaterialFloor = gov.MaterialFloor
	result.PlanState = gov.PlanState
	result.OperationState = gov.OperationState
	result.PersonaIntent = gov.PersonaIntent
	result.GovernorIntent = gov.GovernorIntent
	if result.Turn != nil {
		result.Turn.TokenUsage = addTokenUsage(result.Turn.TokenUsage, gov.Usage)
	}

	rendered, err := m.renderScene(ctx, prepared, policy, gov)
	if err != nil {
		return nil, err
	}
	if rendered != nil && strings.TrimSpace(rendered.Text) != "" {
		result.VisibleReply = strings.TrimSpace(rendered.Text)
		result.RenderedStream = rendered.Streamed
		result.RenderedID = rendered.RenderedID
		result.RenderedType = rendered.RenderedType
		result.ReplyModality = strings.TrimSpace(rendered.ReplyModality)
	} else {
		result.VisibleReply = fallbackVisibleReply(gov)
	}
	if rendered != nil {
		result.Turn.TokenUsage = addTokenUsage(result.Turn.TokenUsage, rendered.Usage)
	}
	switch strings.ToLower(strings.TrimSpace(result.ReplyModality)) {
	case "text":
		result.ReplyWithVoice = false
	case "voice":
		result.ReplyWithVoice = true
	}

	plan := DefaultCommitPlan(policy)
	if m.Persistence != nil {
		commit, err := m.Persistence.Persist(ctx, CommitRequest{Request: req, Result: result, Plan: plan})
		if err != nil {
			return result, err
		}
		if commit != nil {
			result.Commit = *commit
		}
	}

	if m.Delivery != nil {
		delivered, err := m.Delivery.Deliver(ctx, DeliveryRequest{
			Message:         buildOutboundMessage(req, result),
			InboundWasVoice: req.InboundWasVoice,
			ReplyWithVoice:  result.ReplyWithVoice,
			Result:          result,
		})
		if err != nil {
			return result, err
		}
		if delivered != nil {
			result.Delivery = *delivered
		}
	}

	return result, nil
}
