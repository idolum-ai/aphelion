//go:build linux

package pipeline

import (
	"context"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/prompt"
)

// FacePolicy names the minimal turn policy that governs proposal and rendering.
// It is intentionally small so the governance and rendering mechanics can be
// reasoned about independently from runtime transport concerns.
type FacePolicy struct {
	Proposal bool
	Render   bool
}

// TurnPrepareContract is the output boundary from the prepare phase.
type TurnPrepareContract struct {
	UserText               string
	LedgerText             string
	AgentMedia             []core.Media
	ArtifactRefs           []core.ArtifactReference
	InboundWasVoice        bool
	MediaAttached          bool
	MediaMode              string
	PreferredReplyModality string
	ArtifactDecisionInputs []core.HiddenInput
}

// TurnExecutionContract is the governor-facing execution snapshot used when
// building governor prompts and runtime awareness.
type TurnExecutionContract struct {
	Provider      agent.Provider
	Backend       string
	ProviderName  string
	ModelName     string
	ProviderPath  []string
	MediaAttached bool
	MediaMode     string
}

// FloorMaterial represents the floor output after governor execution.
type FloorMaterial struct {
	Packet     core.MaterialPacket
	Text       string
	Structured bool
}

// RenderContract captures render-mode intent after floor extraction.
type RenderContract struct {
	Channel         string
	Mode            string
	GovernorName    string
	FaceName        string
	PrincipalRole   string
	WorkspaceRoot   string
	LatestUserInput string
	Floor           FloorArtifact
	Material        FloorMaterial
	Runtime         prompt.RuntimeAwareness
	FallbackMode    string
}

// RepairContract captures the repair phase intent when draft output violates
// constitution gates.
type RepairContract struct {
	Channel       string
	PrincipalRole string
	UserText      string
	Candidate     string
	FloorText     string
	Material      FloorMaterial
	Runtime       prompt.RuntimeAwareness
	Violations    []string
	Adjudications []core.RuntimeAdjudication
	MediaCount    int
}

// DeliveryContract captures delivery intent prior to side-effectful send.
type DeliveryContract struct {
	Mode          string
	ReplyText     string
	MediaOnly     bool
	ShouldStream  bool
	ReplyWithData bool
}

// BrokerageProposal is Idolum's bounded pre-turn push about how a turn should
// move.
type BrokerageProposal struct {
	RawText           string
	SuggestedContract ExecutionContract
	Usage             core.TokenUsage
}

// BrokerageRatification is Aphelion's bounded answer to a proposal.
type BrokerageRatification struct {
	RawText          string
	RatifiedContract ExecutionContract
	Disposition      RatificationDisposition
	SignalJudgment   SignalJudgment
	RatifiedSteps    []string
	Usage            core.TokenUsage
}

// BrokerageArtifact preserves both sides of the negotiated brokerage surface.
type BrokerageArtifact struct {
	Proposal     BrokerageProposal
	Ratification BrokerageRatification
}

// ProposalRequest names the face-side input for a pre-turn proposal or
// brokerage pass.
type ProposalRequest struct {
	GovernorName    string
	FaceName        string
	Channel         string
	Style           string
	Mode            string
	PrincipalRole   string
	WorkspaceRoot   string
	LatestUserInput string
	Runtime         prompt.RuntimeAwareness
}

// RenderRequest names the face-side input for ordinary scene authorship.
type RenderRequest struct {
	GovernorName    string
	FaceName        string
	Channel         string
	PrincipalRole   string
	WorkspaceRoot   string
	LatestUserInput string
	Runtime         prompt.RuntimeAwareness
	Floor           FloorArtifact
}

// FallbackRequest names the degraded delivery input when a floor must surface
// without ordinary scene authorship.
type FallbackRequest struct {
	Channel string
	Voice   bool
	Floor   FloorArtifact
}

// ProposalPort is the face-side proposal surface.
type ProposalPort interface {
	Propose(ctx context.Context, req ProposalRequest) (*BrokerageProposal, error)
}

// ScenePort is the ordinary face-scene authorship surface.
type ScenePort interface {
	RenderScene(ctx context.Context, req RenderRequest) (*SceneArtifact, error)
}

// FallbackPort is the degraded floor-to-user delivery surface.
type FallbackPort interface {
	RenderFallback(ctx context.Context, req FallbackRequest) (*FallbackArtifact, error)
}

// DecideInteractiveFacePolicy chooses the bicameral face policy for a turn.
func DecideInteractiveFacePolicy(userText string) FacePolicy {
	trimmed := strings.TrimSpace(userText)
	if trimmed == "" || strings.HasPrefix(trimmed, "/") {
		return FacePolicy{}
	}
	return FacePolicy{Proposal: true, Render: true}
}

// RenderDecisionInput carries the minimally necessary context for a render decision.
type RenderDecisionInput struct {
	UserText          string
	FloorText         string
	ToolLog           []string
	GeneratedMessages []agent.Message
}

// ShouldRenderInteractiveIdolumReply determines whether Idolum scene rendering is
// requested for an interactive turn.
func ShouldRenderInteractiveIdolumReply(policy FacePolicy, input RenderDecisionInput) bool {
	if !policy.Render {
		return false
	}
	trimmedUserText := strings.TrimSpace(input.UserText)
	if trimmedUserText == "" {
		return false
	}
	if strings.HasPrefix(trimmedUserText, "/") {
		return false
	}

	floorTrimmed := strings.TrimSpace(input.FloorText)
	isNoResponse := floorTrimmed == "" || strings.EqualFold(floorTrimmed, "(no response)")
	hasToolContext := len(input.ToolLog) > 0 || generatedHasToolMessages(input.GeneratedMessages)
	if isNoResponse && !hasToolContext {
		return false
	}
	return true
}

// ShouldUseMaterialFloorContract keeps material-floor contract usage explicit.
func ShouldUseMaterialFloorContract(policy FacePolicy) bool {
	return policy.Proposal
}

// FormatFloorTextForRender is currently an explicit helper for render heuristics.
func FormatFloorTextForRender(packet core.MaterialPacket, fallback string) string {
	if packet.Empty() {
		return fallback
	}
	parts := make([]string, 0, len(packet.Facts)+len(packet.AllowedActions)+len(packet.Commitments)+len(packet.Refusals)+len(packet.SceneConstraints)+len(packet.Notes))
	parts = append(parts, packet.Facts...)
	parts = append(parts, packet.AllowedActions...)
	parts = append(parts, packet.Commitments...)
	parts = append(parts, packet.Refusals...)
	parts = append(parts, packet.SceneConstraints...)
	parts = append(parts, packet.Notes...)
	joined := strings.TrimSpace(strings.Join(parts, " "))
	if joined == "" {
		return fallback
	}
	return joined
}

func generatedHasToolMessages(messages []agent.Message) bool {
	for _, msg := range messages {
		if msg.Role == "tool" {
			return true
		}
	}
	return false
}
