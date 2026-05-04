//go:build linux

package turn

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

type preparedContext struct {
	request      Request
	now          time.Time
	governorName string
	faceName     string
	channel      string
	style        string
}

func (m *Machine) validateRequest(req Request) error {
	if req.Session == nil {
		return fmt.Errorf("turn: session is required")
	}
	if req.Inbound.ChatID == 0 {
		return fmt.Errorf("turn: inbound chat id is required")
	}
	if req.SessionKey.ChatID != 0 && req.SessionKey.ChatID != req.Inbound.ChatID {
		return fmt.Errorf("turn: session key chat id %d does not match inbound chat id %d", req.SessionKey.ChatID, req.Inbound.ChatID)
	}
	return nil
}

func (m *Machine) prepareContext(req Request) preparedContext {
	now := req.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	governorName := strings.TrimSpace(m.Options.GovernorName)
	if governorName == "" {
		governorName = "System"
	}
	faceName := strings.TrimSpace(m.Options.FaceName)
	if faceName == "" {
		faceName = "Assistant"
	}
	channel := strings.TrimSpace(m.Options.Channel)
	if channel == "" {
		channel = "telegram"
	}
	style := strings.TrimSpace(m.Options.Style)
	if style == "" {
		style = "observant, high-agency, warm, and emotionally lucid"
	}
	return preparedContext{
		request:      req,
		now:          now,
		governorName: governorName,
		faceName:     faceName,
		channel:      channel,
		style:        style,
	}
}

func (m *Machine) policyFor(req Request) Policy {
	if m.PolicyFunc != nil {
		return m.PolicyFunc(req)
	}
	return DefaultPolicy(req)
}

func (m *Machine) maybePropose(ctx context.Context, prepared preparedContext, policy Policy) (*FaceProposalResult, error) {
	if m.Face == nil || !policy.Proposal {
		return nil, nil
	}
	mode := "proposal"
	if policy.Brokerage {
		mode = "brokerage"
	}
	return m.Face.Propose(ctx, FaceProposalRequest{
		GovernorName:    prepared.governorName,
		FaceName:        prepared.faceName,
		Channel:         prepared.channel,
		Style:           prepared.style,
		Mode:            mode,
		LatestUserInput: preparedLatestUserInput(prepared.request),
		Runtime:         m.RuntimeAwareness,
	})
}

func (m *Machine) executeGovernor(ctx context.Context, prepared preparedContext, policy Policy, proposal *FaceProposalResult) (*GovernorResult, error) {
	if m.Governor == nil {
		return nil, fmt.Errorf("turn: governor port is required")
	}
	faceNote := ""
	if proposal != nil {
		faceNote = strings.TrimSpace(proposal.Note)
	}
	return m.Governor.Execute(ctx, GovernorRequest{
		RunKind:    prepared.request.RunKind,
		SessionKey: prepared.request.SessionKey,
		Inbound:    prepared.request.Inbound,
		Session:    prepared.request.Session,
		FaceNote:   faceNote,
		Policy:     policy,
	})
}

func (m *Machine) renderScene(ctx context.Context, prepared preparedContext, policy Policy, gov *GovernorResult) (*FaceRenderResult, error) {
	if m.Face == nil || !policy.Render {
		return nil, nil
	}
	return m.Face.Render(ctx, FaceRenderRequest{
		GovernorName:    prepared.governorName,
		FaceName:        prepared.faceName,
		Channel:         prepared.channel,
		Style:           prepared.style,
		FloorText:       strings.TrimSpace(gov.FloorText),
		MaterialFloor:   gov.MaterialFloor,
		LatestUserInput: preparedLatestUserInput(prepared.request),
		Runtime:         m.RuntimeAwareness,
	})
}

func fallbackVisibleReply(gov *GovernorResult) string {
	if gov == nil {
		return ""
	}
	if text := strings.TrimSpace(gov.FloorText); text != "" {
		return text
	}
	if gov.Turn != nil {
		if text := strings.TrimSpace(gov.Turn.Text); text != "" {
			return text
		}
	}
	if material := strings.TrimSpace(gov.MaterialFloor.Text()); material != "" {
		return material
	}
	return ""
}

func addTokenUsage(dst core.TokenUsage, src core.TokenUsage) core.TokenUsage {
	dst.InputTokens += src.InputTokens
	dst.OutputTokens += src.OutputTokens
	dst.TotalTokens += src.TotalTokens
	dst.CacheReadTokens += src.CacheReadTokens
	dst.CacheWriteTokens += src.CacheWriteTokens
	return dst
}

func buildOutboundMessage(req Request, result *Result) core.OutboundMessage {
	msg := core.OutboundMessage{
		ChatID:  req.Inbound.ChatID,
		Text:    strings.TrimSpace(result.VisibleReply),
		ReplyTo: req.Inbound.ReplyTo,
	}
	if result != nil && result.Turn != nil {
		msg.Media = append(msg.Media, result.Turn.Media...)
	}
	return msg
}

func preparedLatestUserInput(req Request) string {
	if prepared := strings.TrimSpace(req.PreparedUserText); prepared != "" {
		return prepared
	}
	return strings.TrimSpace(req.Inbound.Text)
}
