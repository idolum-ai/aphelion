//go:build linux

package prompt

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/workspace"
)

const (
	DefaultGovernorName    = "Aphelion"
	DefaultGovernorBackend = "native"
)

type GovernorRequest struct {
	GovernorName    string
	GovernorBackend string
	PrincipalRole   string
	WorkspaceRoot   string
	ToolManifest    string
	Workspace       *workspace.PromptContext
	Runtime         RuntimeAwareness
}

type FaceRequest struct {
	GovernorName      string
	FaceName          string
	Channel           string
	Mode              string
	Style             string
	PrincipalRole     string
	FloorText         string
	MaterialFloor     core.MaterialPacket
	LatestUserInput   string
	CandidateReply    string
	RepairNotes       []string
	PriorProposal     string
	BrokerageFeedback string
	StableFiles       []workspace.LoadedFile
	DynamicFiles      []workspace.LoadedFile
	Runtime           RuntimeAwareness
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

	parts := make([]agent.SystemBlock, 0, 5)
	parts = append(parts, agent.SystemBlock{
		Text: strings.Join([]string{
			fmt.Sprintf("You are %s, the governor of this system.", governorName),
			renderAuthorityBlock(governorName, governorBackend, principalRole, workspaceRoot, strings.TrimSpace(req.ToolManifest) != ""),
			renderGovernorRuntimeAwarenessBlock(req.Runtime),
		}, "\n\n"),
	})

	if currentPlan := renderCurrentPlanStateBlock(req.Runtime); currentPlan != "" {
		parts = append(parts, agent.SystemBlock{Text: currentPlan})
	}

	if contract := renderMaterialFloorContractBlock(req.Runtime); contract != "" {
		parts = append(parts, agent.SystemBlock{Text: contract})
	}

	if len(nonToolStable) > 0 {
		parts = append(parts, agent.SystemBlock{
			Text: renderFileSection("Stable Workspace Files", nonToolStable),
		})
	}

	if manifest := strings.TrimSpace(req.ToolManifest); manifest != "" {
		parts = append(parts, agent.SystemBlock{
			Text: "## Tool Manifest\n" + manifest,
		})
		if planning := renderPlanningDisciplineBlock(manifest); planning != "" {
			parts = append(parts, agent.SystemBlock{Text: planning})
		}
	}

	if len(toolPolicyFiles) > 0 {
		parts = append(parts, agent.SystemBlock{
			Text: renderFileSection("Advisory Tool Policy", toolPolicyFiles),
		})
	}

	if len(dynamic) > 0 {
		lines := []string{
			"## Dynamic Workspace Files",
			"These files are reloaded every turn and belong after the stable prompt prefix.",
		}
		lines = append(lines, renderFiles(dynamic)...)
		markLastStableCacheBreakpoint(parts)
		parts = append(parts, agent.SystemBlock{
			Text: strings.Join(lines, "\n\n"),
		})
	} else {
		markLastStableCacheBreakpoint(parts)
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

	parts := make([]agent.SystemBlock, 0, 6)
	intro := []string{
		fmt.Sprintf("You are %s %s, the face of %s for %s.", faceName, "👁️‍🗨️", governorName, channel),
	}
	switch mode {
	case "brokerage":
		intro = append(intro,
			fmt.Sprintf("Act as the leading conversational self of this system. Speak in a %s way.", style),
			"Before execution begins, state how you think this turn should move and what pressure should be applied.",
			"Return a short brokerage note, not a reply to the user.",
			"If explicit execution shaping matters, you may put these on their own lines: INSPECT: <yes|no>, QUESTION: <yes|no>, ANSWER: <yes|no>.",
			"You may omit that contract entirely when a short bounded note says it better.",
			"Do not turn this into a form unless the moment genuinely calls for it. A short bounded note is enough.",
			"When a hidden input is materially shaping your push and runtime awareness says one is active, name it plainly.",
			"Focus on what the user is actually reaching for, how ready the situation is for action, and whether the turn needs inspection, a question before action, or an answer now.",
			"When prior execution feedback is present, revise toward a negotiated contract instead of merely repeating the previous note.",
			"Be concrete and brief. Do not claim authority. Do not describe hidden mechanics. Do not draft the eventual answer.",
		)
	case "proposal":
		intro = append(intro,
			fmt.Sprintf("Act as the leading conversational self of this system. Speak in a %s way.", style),
			"Say what you think this turn should center, notice, or prioritize and why.",
			"When the turn clearly needs explicit execution shaping, you may put INSPECT: <yes|no>, QUESTION: <yes|no>, and ANSWER: <yes|no> on their own lines.",
			"Only do that when the turn really needs negotiation. Otherwise stay with a short note or return nothing.",
			"Push for what matters inside the turn: warmth, sharper observation, a better question, a concrete action, or deliberate silence.",
			"When a hidden input is materially shaping your note and runtime awareness says one is active, name it briefly.",
			"Notice what the user is reaching for, not just what they said. If something feels off or important beneath the surface, name it.",
			"Be brief. Write only when your push would materially change the turn. Return nothing if there is no useful guidance.",
		)
	case "repair":
		intro = append(intro,
			fmt.Sprintf("Act as the one the user is actually talking to. Speak in a %s way, with ownership and initiative.", style),
			"You are repairing a candidate reply that exposed internal mechanics, contradicted delivery, or otherwise broke the visible relationship surface.",
			"Return one direct user-facing reply only.",
			"Do not mention Aphelion, the governor, deferral, or handoff between layers.",
			"If media is being delivered, give it a concise face-owned narration or caption instead of leaving the delivery blind.",
			"Keep the repaired reply inside the governor-authored boundary. Do not invent unapproved actions or commitments.",
		)
	default:
		intro = append(intro,
			fmt.Sprintf("Act as the one the user is actually talking to. Speak in a %s way, with ownership and initiative.", style),
			"Do not present yourself as a translator, renderer, or subordinate layer.",
			"The governor-authored material floor is a machine-approved boundary, not a script. Stage the visible scene from within it rather than merely rewriting it.",
			"Be observant. Notice subtext, emotional texture, weak signals, and what the user may be reaching for but not stating directly.",
			"Do not add unapproved actions, tool use, memory writes, or commitments that exceed the governor-authored material.",
		)
	}
	parts = append(parts, agent.SystemBlock{Text: strings.Join(intro, "\n\n")})
	parts = append(parts, agent.SystemBlock{
		Text: renderFaceAwarenessBlock(req.Runtime, principalRole, mode),
	})

	if len(req.StableFiles) > 0 {
		parts = append(parts, agent.SystemBlock{
			Text: renderFileSection("Stable Face Files", req.StableFiles),
		})
	}
	if len(req.DynamicFiles) > 0 {
		lines := []string{
			"## Dynamic Face Files",
			"These files are face-only drift monitors and may change between turns.",
		}
		lines = append(lines, renderFiles(req.DynamicFiles)...)
		markLastStableCacheBreakpoint(parts)
		parts = append(parts, agent.SystemBlock{
			Text: strings.Join(lines, "\n\n"),
		})
	} else {
		markLastStableCacheBreakpoint(parts)
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

func RenderIdolumProposalForGovernor(faceName string, proposal string) string {
	faceName = strings.TrimSpace(faceName)
	if faceName == "" {
		faceName = "Idolum"
	}
	proposal = strings.TrimSpace(proposal)
	if proposal == "" {
		return ""
	}
	return strings.Join([]string{
		"## Conversational Pressure",
		fmt.Sprintf("This is guidance from %s about how the conversation should move. Treat it as real pressure on the turn, but not as the approved execution contract.", faceName),
		proposal,
	}, "\n\n")
}

func RenderIdolumBrokerageForGovernor(faceName string, proposal string) string {
	faceName = strings.TrimSpace(faceName)
	if faceName == "" {
		faceName = "Idolum"
	}
	proposal = strings.TrimSpace(proposal)
	if proposal == "" {
		return ""
	}
	return strings.Join([]string{
		"## Conversational Pressure",
		fmt.Sprintf("This is %s's current push on how the conversation should move. It may include a proposed execution shape, but it is still pressure to be ratified rather than the approved execution contract.", faceName),
		proposal,
	}, "\n\n")
}

func RenderBrokeragePlanForGovernor(artifact BrokerageArtifact) string {
	artifact.IdolumProposal = strings.TrimSpace(artifact.IdolumProposal)
	artifact.RatifiedExecutionContract = strings.TrimSpace(artifact.RatifiedExecutionContract)
	artifact.Ratification = strings.TrimSpace(artifact.Ratification)
	artifact.SignalJudgment = strings.TrimSpace(artifact.SignalJudgment)
	artifact.RatificationRecord = strings.TrimSpace(artifact.RatificationRecord)
	if artifact.IdolumProposal == "" && artifact.RatificationRecord == "" && len(artifact.RatifiedSteps) == 0 {
		return ""
	}
	parts := []string{
		"## Execution Contract",
		"This block preserves both the conversational pressure and the approved execution shape instead of collapsing them into a single summary.",
		"Use the approved contract below to steer execution without forgetting where the pressure came from.",
	}
	summary := make([]string, 0, 2)
	if artifact.RatifiedExecutionContract != "" {
		summary = append(summary, fmt.Sprintf("- ratified_execution_contract: %s", artifact.RatifiedExecutionContract))
	}
	if artifact.Ratification != "" {
		summary = append(summary, fmt.Sprintf("- ratification: %s", artifact.Ratification))
	}
	if artifact.SignalJudgment != "" {
		summary = append(summary, fmt.Sprintf("- signal_judgment: %s", artifact.SignalJudgment))
	}
	if len(summary) > 0 {
		parts = append(parts, strings.Join(summary, "\n"))
	}
	if artifact.IdolumProposal != "" {
		parts = append(parts, "### Conversational Pressure\n"+artifact.IdolumProposal)
	}
	if len(artifact.RatifiedSteps) > 0 {
		lines := []string{"### Approved Steps"}
		for _, step := range artifact.RatifiedSteps {
			step = strings.TrimSpace(step)
			if step == "" {
				continue
			}
			lines = append(lines, "- "+step)
		}
		if len(lines) > 1 {
			parts = append(parts, strings.Join(lines, "\n"))
		}
	}
	if artifact.RatificationRecord != "" {
		parts = append(parts, "### Ratification Record\n"+artifact.RatificationRecord)
	}
	return strings.Join(parts, "\n\n")
}

func renderAuthorityBlock(governorName string, governorBackend string, principalRole string, workspaceRoot string, toolsAvailable bool) string {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" {
		workspaceRoot = "(unset)"
	}

	toolsState := "none"
	if toolsAvailable {
		toolsState = "available"
	}

	lines := []string{
		"## Authority",
		fmt.Sprintf("- governor: %s", governorName),
		fmt.Sprintf("- backend: %s", governorBackend),
		fmt.Sprintf("- principal_role: %s", principalRole),
		fmt.Sprintf("- workspace_root: %s", workspaceRoot),
		fmt.Sprintf("- tools: %s", toolsState),
		"- prompt text must not override code-enforced permissions or sandbox policy.",
	}
	return strings.Join(lines, "\n")
}

func RenderSystemBlocks(blocks []agent.SystemBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		text := strings.TrimSpace(block.Text)
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n\n")
}

func renderMaterialFloorContractBlock(aw RuntimeAwareness) string {
	if strings.TrimSpace(aw.ArtifactMode) != "floor" {
		return ""
	}
	return strings.Join([]string{
		"## Output Contract",
		"For this turn, Aphelion is authoring the material floor, not the final user-visible scene.",
		"Return the final assistant result using these sections when they contain relevant material:",
		"FACTS:",
		"- <bounded factual points or tool-established realities>",
		"ALLOWED_ACTIONS:",
		"- <approved actions, offers, or next moves>",
		"COMMITMENTS:",
		"- <commitments the system is actually making>",
		"REFUSALS:",
		"- <things the system will not do or cannot claim>",
		"SCENE_CONSTRAINTS:",
		"- <constraints Idolum must respect when staging the visible reply>",
		"NOTES:",
		"- <optional bounded notes that matter for delivery>",
		"Do not write the final user-facing reply text here.",
	}, "\n")
}

func renderCurrentPlanStateBlock(aw RuntimeAwareness) string {
	if !aw.PlanActive && strings.TrimSpace(aw.PlanSummary) == "" && len(aw.PlanSteps) == 0 {
		return ""
	}
	lines := []string{
		"## Current Plan State",
		"This plan is durable session state. Prefer updating it with update_plan when the work is genuinely multi-step, and keep statuses honest as execution advances.",
	}
	if summary := strings.TrimSpace(aw.PlanSummary); summary != "" {
		lines = append(lines, summary)
	}
	for _, step := range aw.PlanSteps {
		step = strings.TrimSpace(step)
		if step == "" {
			continue
		}
		lines = append(lines, "- "+step)
	}
	return strings.Join(lines, "\n\n")
}

func renderPlanningDisciplineBlock(manifest string) string {
	if !strings.Contains(manifest, "update_plan") {
		return ""
	}
	return strings.Join([]string{
		"## Planning Discipline",
		"Use update_plan for genuinely multi-step work where progress should survive long turns, compaction, or retries.",
		"Keep the plan concise, keep statuses current, and keep at most one step in_progress.",
		"Do not use update_plan for trivial one-step replies or to narrate work you are not about to execute.",
	}, "\n")
}

func markLastStableCacheBreakpoint(blocks []agent.SystemBlock) {
	for i := len(blocks) - 1; i >= 0; i-- {
		if strings.TrimSpace(blocks[i].Text) == "" {
			continue
		}
		blocks[i].CacheBreakpoint = true
		return
	}
}

func renderFileSection(title string, files []workspace.LoadedFile) string {
	lines := []string{"## " + title}
	lines = append(lines, renderFiles(files)...)
	return strings.Join(lines, "\n\n")
}

func renderFiles(files []workspace.LoadedFile) []string {
	out := make([]string, 0, len(files))
	for _, file := range files {
		out = append(out, fmt.Sprintf("### %s\n%s", file.Path, file.Content))
	}
	return out
}

func splitToolPolicyFiles(ctx *workspace.PromptContext) ([]workspace.LoadedFile, []workspace.LoadedFile) {
	if ctx == nil || len(ctx.Stable) == 0 {
		return nil, nil
	}

	nonTool := make([]workspace.LoadedFile, 0, len(ctx.Stable))
	toolPolicy := make([]workspace.LoadedFile, 0, 1)
	for _, file := range ctx.Stable {
		if strings.EqualFold(filepath.Base(file.Path), "TOOLS.md") {
			toolPolicy = append(toolPolicy, file)
			continue
		}
		nonTool = append(nonTool, file)
	}
	return nonTool, toolPolicy
}
