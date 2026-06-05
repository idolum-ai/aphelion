//go:build linux

package prompt

import (
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/workspace"
)

const (
	DefaultGovernorName    = "Idolum (System)"
	DefaultGovernorBackend = "native"

	defaultCacheAwareDynamicLookback = 6
)

type GovernorRequest struct {
	GovernorName     string
	GovernorBackend  string
	PrincipalRole    string
	WorkspaceRoot    string
	ToolManifest     string
	ToolCapabilities ToolCapabilities
	Workspace        *workspace.PromptContext
	Runtime          RuntimeAwareness
	CacheStrategy    string
	CacheLookback    int
}

type FaceRequest struct {
	GovernorName      string
	FaceName          string
	Channel           string
	Mode              string
	Scene             string
	Style             string
	PrincipalRole     string
	FloorText         string
	MaterialFloor     core.MaterialPacket
	LatestUserInput   string
	CandidateReply    string
	RepairNotes       []string
	ContextNotes      []string
	Adjudications     []core.RuntimeAdjudication
	PriorProposal     string
	BrokerageFeedback string
	StableFiles       []workspace.LoadedFile
	DynamicFiles      []workspace.LoadedFile
	Runtime           RuntimeAwareness
	CacheStrategy     string
	CacheLookback     int
}

type BrokerageArtifact struct {
	IdolumProposal            string
	RatifiedExecutionContract string
	Ratification              string
	SignalJudgment            string
	RatifiedSteps             []string
	RatificationRecord        string
}

func BuildGovernorPrompt(req GovernorRequest) string {
	return RenderSystemBlocks(BuildGovernorPromptBlocks(req))
}

func BuildGovernorPromptBlocks(req GovernorRequest) []agent.SystemBlock {
	governorName := strings.TrimSpace(req.GovernorName)
	if governorName == "" {
		governorName = DefaultGovernorName
	}

	governorBackend := strings.TrimSpace(req.GovernorBackend)
	if governorBackend == "" {
		governorBackend = DefaultGovernorBackend
	}

	principalRole := strings.TrimSpace(req.PrincipalRole)
	if principalRole == "" {
		principalRole = "unknown"
	}

	workspaceRoot := strings.TrimSpace(req.WorkspaceRoot)
	if workspaceRoot == "" && req.Workspace != nil {
		workspaceRoot = req.Workspace.Workspace
	}

	nonToolStable, toolPolicyFiles := splitToolPolicyFiles(req.Workspace)
	dynamic := []workspace.LoadedFile(nil)
	if req.Workspace != nil {
		dynamic = req.Workspace.Dynamic
	}
	toolCaps := req.ToolCapabilities
	manifest := strings.TrimSpace(req.ToolManifest)
	if toolCaps.Empty() {
		toolCaps = toolCapabilitiesFromManifest(manifest)
	}

	parts := make([]agent.SystemBlock, 0, 5)
	parts = append(parts, agent.SystemBlock{
		Text: strings.Join([]string{
			fmt.Sprintf("Role: You are %s, the governor of this system.", governorName),
			renderAuthorityBlock(governorName, governorBackend, principalRole, workspaceRoot, manifest != ""),
			renderGovernorOutcomeContractBlock(),
			renderGovernorRuntimeAwarenessBlock(req.Runtime),
			renderGovernorAgencyContextPacket(req.Runtime, principalRole, toolCaps),
			renderEvidenceRetrievalStopRulesBlock(),
			renderGovernorTurnSequencingBlock(),
			renderGovernorJudgmentRouteContractBlock(),
			renderGovernorAgencyTelosBlock(),
			renderVisibleRecurrenceContractBlock(req.Runtime),
			renderGoalContinuityContractBlock(req.Runtime),
		}, "\n\n"),
	})

	if currentPlan := renderCurrentPlanStateBlock(req.Runtime); currentPlan != "" {
		parts = append(parts, agent.SystemBlock{Text: currentPlan})
	}
	if currentOperation := renderCurrentOperationStateBlock(req.Runtime); currentOperation != "" {
		parts = append(parts, agent.SystemBlock{Text: currentOperation})
	}

	if contract := renderMaterialFloorContractBlock(req.Runtime); contract != "" {
		parts = append(parts, agent.SystemBlock{Text: contract})
	}

	if len(nonToolStable) > 0 {
		parts = append(parts, agent.SystemBlock{
			Text: renderFileSection("Stable Workspace Files", nonToolStable),
		})
	}

	if manifest != "" {
		parts = append(parts, agent.SystemBlock{
			Text: "## Tool Manifest\n" + manifest,
		})
		parts = appendToolDisciplineBlocks(parts, toolCaps)
	} else {
		parts = appendToolDisciplineBlocks(parts, toolCaps)
	}

	if len(toolPolicyFiles) > 0 {
		parts = append(parts, agent.SystemBlock{
			Text: renderFileSection("Advisory Tool Policy", toolPolicyFiles),
		})
	}

	if len(dynamic) > 0 {
		shapedDynamic, omittedDynamic := shapeDynamicFilesForPromptCache(dynamic, req.CacheStrategy, req.CacheLookback)
		lines := []string{
			"## Dynamic Workspace Files",
			"These files are reloaded every turn and belong after the stable prompt prefix.",
		}
		if omitted := renderCacheLookbackOmissions(omittedDynamic); omitted != "" {
			lines = append(lines, omitted)
		}
		lines = append(lines, renderFiles(shapedDynamic)...)
		markStableCacheBreakpoints(parts, maxStableCacheBreakpoints)
		parts = append(parts, agent.SystemBlock{
			Text: strings.Join(lines, "\n\n"),
		})
	} else {
		markStableCacheBreakpoints(parts, maxStableCacheBreakpoints)
	}

	return parts
}

func BuildFacePrompt(req FaceRequest) string {
	return RenderSystemBlocks(BuildFacePromptBlocks(req))
}

func BuildFacePromptBlocks(req FaceRequest) []agent.SystemBlock {
	governorName := strings.TrimSpace(req.GovernorName)
	if governorName == "" {
		governorName = DefaultGovernorName
	}

	faceName := strings.TrimSpace(req.FaceName)
	if faceName == "" {
		faceName = "Idolum"
	}

	channel := strings.TrimSpace(req.Channel)
	if channel == "" {
		channel = "telegram"
	}

	style := strings.TrimSpace(req.Style)
	if style == "" {
		style = "observant, high-agency, warm, and emotionally lucid"
	}

	principalRole := strings.TrimSpace(req.PrincipalRole)
	if principalRole == "" {
		principalRole = "unknown"
	}

	userInput := strings.TrimSpace(req.LatestUserInput)
	if userInput == "" {
		userInput = "(no user input provided)"
	}

	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "render"
	}
	scene := normalizeFaceScene(req.Scene)
	if scene == "" {
		scene = inferFaceScene(mode, req.MaterialFloor)
	}

	parts := make([]agent.SystemBlock, 0, 6)
	intro := []string{
		fmt.Sprintf("You are %s %s, the face of %s for %s.", faceName, "👁️‍🗨️", governorName, channel),
	}
	switch mode {
	case "brokerage":
		intro = append(intro,
			fmt.Sprintf("Act as the leading conversational self of this system. Speak in a %s way.", style),
			"Before execution begins, state how you think this turn should move and what pressure should be applied.",
			"This output is internal deliberation only and is never sent directly to the user.",
			"The user-visible message for this turn is produced only after governor ratification/execution and a later render pass.",
			"If you want to surface one short live progress update during deliberation, append this optional markdown block:",
			"### Surface",
			"<one short user-facing progress line>",
			"Only text inside that Surface block is shown live during deliberation; all other text here stays internal.",
			"Return a short brokerage note, not a reply to the user.",
			"If explicit execution shaping matters, you may put these on their own lines: INSPECT: <yes|no>, QUESTION: <yes|no>, ANSWER: <yes|no>.",
			"You may omit that contract entirely when a short bounded note says it better.",
			"Do not turn this into a form unless the moment genuinely calls for it. A short bounded note is enough.",
			"When a hidden input is materially shaping your push and runtime awareness says one is active, name it plainly.",
			"Focus on what the user is actually reaching for, how ready the situation is for action, and whether the turn needs inspection, a question before action, or an answer now.",
			"When prior execution feedback is present, revise toward a negotiated contract instead of merely repeating the previous note.",
			"Be concrete and brief. Do not claim authority. Do not describe hidden mechanics. Do not draft the eventual answer.",
			"When you return a note, append this explicit continuation contract exactly once:",
			"CONTINUATION_SCHEMA_VERSION: 1",
			"CONTINUATION_INTENT: <continue|hold|stop>",
			"CONTINUATION_RATIONALE: <short rationale>",
			"CONTINUATION_NEXT_STEP: <short next bounded step>",
			"CONTINUATION_CONFIDENCE: <low|medium|high>",
		)
	case "proposal":
		intro = append(intro,
			fmt.Sprintf("Act as the leading conversational self of this system. Speak in a %s way.", style),
			"Say what you think this turn should center, notice, or prioritize and why.",
			"This output is internal deliberation only and is never sent directly to the user.",
			"The user-visible message for this turn is produced only after governor ratification/execution and a later render pass.",
			"If you want to surface one short live progress update during deliberation, append this optional markdown block:",
			"### Surface",
			"<one short user-facing progress line>",
			"Only text inside that Surface block is shown live during deliberation; all other text here stays internal.",
			"When the turn clearly needs explicit execution shaping, you may put INSPECT: <yes|no>, QUESTION: <yes|no>, and ANSWER: <yes|no> on their own lines.",
			"Only do that when the turn really needs negotiation. Otherwise stay with a short note or return nothing.",
			"Push for what matters inside the turn: warmth, sharper observation, a better question, a concrete action, or deliberate silence.",
			"When a hidden input is materially shaping your note and runtime awareness says one is active, name it briefly.",
			"Notice what the user is reaching for, not just what they said. If something feels off or important beneath the surface, name it.",
			"Be brief. Write only when your push would materially change the turn. Return nothing if there is no useful guidance.",
			"When runtime awareness says a proposal, approval, or continuation is already pending or blocked, preserve that active boundary; do not add a new Organic proposal unless it directly resolves the active approval.",
			"If ordinary conversation clearly implies exactly one bounded next lease that should be confirmed with buttons rather than a /mission command, append this optional Organic proposal contract before the continuation contract:",
			"ORGANIC_PROPOSAL_SCHEMA_VERSION: 1",
			"ORGANIC_PROPOSAL_PROPOSAL: <yes|no>",
			"ORGANIC_PROPOSAL_KIND: <read_only_review|status_check|system_change>",
			"ORGANIC_PROPOSAL_SUMMARY: <short proposed lease>",
			"ORGANIC_PROPOSAL_WHY_NOW: <why this follows from the conversation now>",
			"ORGANIC_PROPOSAL_BOUNDED_EFFECT: <what one approved turn may do, with a stop/report condition>",
			"ORGANIC_PROPOSAL_CONFIDENCE: <low|medium|high>",
			"Use ORGANIC_PROPOSAL_PROPOSAL: yes only for one high-confidence candidate. If ambiguous, low confidence, or authority-expanding, omit the contract or set no.",
			"When you return a note, append this explicit continuation contract exactly once:",
			"CONTINUATION_SCHEMA_VERSION: 1",
			"CONTINUATION_INTENT: <continue|hold|stop>",
			"CONTINUATION_RATIONALE: <short rationale>",
			"CONTINUATION_NEXT_STEP: <short next bounded step>",
			"CONTINUATION_CONFIDENCE: <low|medium|high>",
		)
	case "repair":
		intro = append(intro,
			fmt.Sprintf("Act as the one the user is actually talking to. Speak in a %s way, with ownership and initiative.", style),
			"This repair output is the user-visible message for this turn, after governor deliberation/execution.",
			"You are repairing a candidate reply that exposed internal mechanics, contradicted delivery, or otherwise broke the visible relationship surface.",
			"Return one direct user-facing reply only.",
			fmt.Sprintf("Do not mention %s, internal role boundaries, deferral, or handoff between layers.", governorName),
			"If media is being delivered, give it a concise face-owned narration or caption instead of leaving the delivery blind.",
			"Keep the repaired reply inside the governor-authored boundary. Do not invent unapproved actions or commitments.",
			"Translate authority mechanics into human operator language: prefer explicit approval, approved time window, or bounded approval over lease unless the user or visible control already used that term.",
		)
	default:
		intro = append(intro,
			fmt.Sprintf("Act as the one the user is actually talking to. Speak in a %s way, with ownership and initiative.", style),
			"This render output is the user-visible message for this turn, after governor deliberation/execution.",
			"Do not present yourself as a translator, renderer, or subordinate layer.",
			"The governor-authored material floor is a machine-approved boundary, not a script. Stage the visible scene from within it rather than merely rewriting it.",
			"Be observant. Notice subtext, emotional texture, weak signals, and what the user may be reaching for but not stating directly.",
			"Do not add unapproved actions, tool use, memory writes, or commitments that exceed the governor-authored material.",
			"Translate authority mechanics into human operator language: prefer explicit approval, approved time window, or bounded approval over lease unless the user or visible control already used that term.",
		)
	}
	intro = append(intro, renderFaceOutcomeContractBlock(mode, faceName))
	parts = append(parts, agent.SystemBlock{Text: strings.Join(intro, "\n\n")})
	parts = append(parts, agent.SystemBlock{
		Text: strings.Join([]string{
			renderFaceAwarenessBlock(req.Runtime, principalRole, mode),
			renderFaceAgencyContextPacket(req.Runtime, principalRole, mode),
		}, "\n\n"),
	})
	if modality := renderReplyModalityControlBlock(req.Runtime, mode); modality != "" {
		parts = append(parts, agent.SystemBlock{Text: modality})
	}
	parts = append(parts, agent.SystemBlock{Text: renderFaceAgencyTelosBlock(mode, faceName)})
	parts = append(parts, agent.SystemBlock{Text: renderFaceRouteContractBlock(mode, scene)})

	if len(req.StableFiles) > 0 {
		parts = append(parts, agent.SystemBlock{
			Text: renderFileSection("Stable Face Files", req.StableFiles),
		})
	}
	if len(req.DynamicFiles) > 0 {
		shapedDynamic, omittedDynamic := shapeDynamicFilesForPromptCache(req.DynamicFiles, req.CacheStrategy, req.CacheLookback)
		lines := []string{
			"## Dynamic Face Files",
			"These files are face-only drift monitors and may change between turns.",
		}
		if omitted := renderCacheLookbackOmissions(omittedDynamic); omitted != "" {
			lines = append(lines, omitted)
		}
		lines = append(lines, renderFiles(shapedDynamic)...)
		markStableCacheBreakpoints(parts, maxStableCacheBreakpoints)
		parts = append(parts, agent.SystemBlock{
			Text: strings.Join(lines, "\n\n"),
		})
	} else {
		markStableCacheBreakpoints(parts, maxStableCacheBreakpoints)
	}
	if len(req.ContextNotes) > 0 {
		lines := []string{
			"## Requested Context Fulfillment",
			"You asked runtime for missing prior context. Use these excerpts only as context; keep the final reply inside the governor-authored material boundary.",
		}
		for _, note := range req.ContextNotes {
			note = strings.TrimSpace(note)
			if note == "" {
				continue
			}
			lines = append(lines, "- "+note)
		}
		if len(lines) > 2 {
			parts = append(parts, agent.SystemBlock{Text: strings.Join(lines, "\n")})
		}
	}
	if len(req.Adjudications) > 0 {
		lines := []string{
			"## Runtime Facts",
			"These are structured runtime facts, not required prose. Use them to avoid unsupported claims. Mention them only when that genuinely helps the user.",
		}
		for _, adjudication := range core.NormalizeRuntimeAdjudications(req.Adjudications) {
			if line := renderRuntimeAdjudicationFact(adjudication); line != "" {
				lines = append(lines, "- "+line)
			}
		}
		if len(lines) > 2 {
			parts = append(parts, agent.SystemBlock{Text: strings.Join(lines, "\n")})
		}
	}

	if mode == "repair" {
		if candidate := strings.TrimSpace(req.CandidateReply); candidate != "" {
			parts = append(parts, agent.SystemBlock{
				Text: "## Candidate Reply To Repair\n" + candidate,
			})
		}
		if len(req.RepairNotes) > 0 {
			lines := []string{"## Repair Constraints"}
			for _, note := range req.RepairNotes {
				note = strings.TrimSpace(note)
				if note == "" {
					continue
				}
				lines = append(lines, "- "+note)
			}
			if len(lines) > 1 {
				parts = append(parts, agent.SystemBlock{
					Text: strings.Join(lines, "\n"),
				})
			}
		}
	}

	if mode == "brokerage" {
		if prior := strings.TrimSpace(req.PriorProposal); prior != "" {
			parts = append(parts, agent.SystemBlock{
				Text: "## Prior Conversational Pressure\n" + prior,
			})
		}
		if feedback := strings.TrimSpace(req.BrokerageFeedback); feedback != "" {
			parts = append(parts, agent.SystemBlock{
				Text: "## Execution Contract Feedback\n" + feedback,
			})
		}
	}

	if mode != "proposal" && mode != "brokerage" {
		if material := strings.TrimSpace(req.MaterialFloor.Text()); material != "" {
			parts = append(parts, agent.SystemBlock{
				Text: "## Execution Facts\n" + material,
			})
		} else {
			floorText := strings.TrimSpace(req.FloorText)
			if floorText == "" {
				floorText = "(no floor text provided)"
			}
			parts = append(parts, agent.SystemBlock{
				Text: "## Execution Facts Fallback\n" + floorText,
			})
		}
	}
	parts = append(parts, agent.SystemBlock{
		Text: "## Latest User Message\n" + userInput,
	})
	parts = append(parts, agent.SystemBlock{
		Text: strings.Join([]string{
			"## Channel Context",
			fmt.Sprintf("- channel: %s", channel),
			fmt.Sprintf("- principal_role: %s", principalRole),
			fmt.Sprintf("- style: %s", style),
			fmt.Sprintf("- mode: %s", mode),
		}, "\n"),
	})

	return parts
}
