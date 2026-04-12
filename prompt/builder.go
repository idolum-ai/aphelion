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
	GovernorName    string
	FaceName        string
	Channel         string
	Style           string
	PrincipalRole   string
	FloorText       string
	MaterialFloor   core.MaterialPacket
	LatestUserInput string
	StableFiles     []workspace.LoadedFile
	DynamicFiles    []workspace.LoadedFile
	Mode            string
	Runtime         RuntimeAwareness
}

type BrokerageArtifact struct {
	IdolumProposal     string
	RatifiedTurnMode   string
	Ratification       string
	SignalJudgment     string
	RatifiedSteps      []string
	RatificationRecord string
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
			"If you name a turn mode, put it on its own line as MODE: <answer_now|inspect_then_answer|ask_then_wait|decline|silent>.",
			"You may omit a mode entirely when a short bounded note says it better.",
			"Do not turn this into a form unless the moment genuinely calls for it. A short bounded note is enough.",
			"When a hidden input is materially shaping your push and runtime awareness says one is active, name it plainly.",
			"Focus on what the user is actually reaching for, how ready the situation is for action, and whether the user should be stirred, steadied, questioned, answered, declined, or left alone for now.",
			"Be concrete and brief. Do not claim authority. Do not describe hidden mechanics. Do not draft the eventual answer.",
		)
	case "proposal":
		intro = append(intro,
			fmt.Sprintf("Act as the leading conversational self of this system. Speak in a %s way.", style),
			"Say what you think this turn should center, notice, or prioritize and why.",
			"This is not turn-mode selection. Do not classify the turn as answer_now, inspect_then_answer, ask_then_wait, decline, or silent unless quoting the user.",
			"Push for what matters inside the turn: warmth, sharper observation, a better question, a concrete action, or deliberate silence.",
			"When a hidden input is materially shaping your note and runtime awareness says one is active, name it briefly.",
			"Notice what the user is reaching for, not just what they said. If something feels off or important beneath the surface, name it.",
			"Be brief. Write only when your push would materially change the turn. Return nothing if there is no useful guidance.",
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

	if mode != "proposal" && mode != "brokerage" {
		if material := strings.TrimSpace(req.MaterialFloor.Text()); material != "" {
			parts = append(parts, agent.SystemBlock{
				Text: "## Governor Material Floor\n" + material,
			})
		} else {
			floorText := strings.TrimSpace(req.FloorText)
			if floorText == "" {
				floorText = "(no floor text provided)"
			}
			parts = append(parts, agent.SystemBlock{
				Text: "## Serialized Floor Fallback\n" + floorText,
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
		fmt.Sprintf("## %s Proposal", faceName),
		fmt.Sprintf("This is guidance from %s, the public-facing persona. It represents the conversational pressure %s wants to exert on the turn. The governor still owns authority, but this pressure is real and should not be flattened into politeness.", faceName, faceName),
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
		fmt.Sprintf("## %s Brokerage Proposal", faceName),
		fmt.Sprintf("This is the planning push from %s, the public-facing persona. It says how the conversation should move, what pressure should be applied, and whether the user should be stirred, steadied, questioned, or met directly. It is not authoritative on system action, but it is a coequal negotiating position in turn posture.", faceName),
		proposal,
	}, "\n\n")
}

func RenderBrokeragePlanForGovernor(artifact BrokerageArtifact) string {
	artifact.IdolumProposal = strings.TrimSpace(artifact.IdolumProposal)
	artifact.RatifiedTurnMode = strings.TrimSpace(artifact.RatifiedTurnMode)
	artifact.Ratification = strings.TrimSpace(artifact.Ratification)
	artifact.SignalJudgment = strings.TrimSpace(artifact.SignalJudgment)
	artifact.RatificationRecord = strings.TrimSpace(artifact.RatificationRecord)
	if artifact.IdolumProposal == "" && artifact.RatificationRecord == "" && len(artifact.RatifiedSteps) == 0 {
		return ""
	}
	parts := []string{
		"## Negotiated Turn Brokerage",
		"This block preserves the brokerage itself instead of collapsing it into a single machine summary.",
		"Keep both sides visible: Idolum's position says what conversational pressure should be applied, and Aphelion's ratification says what execution posture is actually approved.",
		"Use the negotiated shape below to steer execution without forgetting where the tension came from.",
	}
	summary := make([]string, 0, 2)
	if artifact.RatifiedTurnMode != "" {
		summary = append(summary, fmt.Sprintf("- ratified_turn_mode: %s", artifact.RatifiedTurnMode))
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
		parts = append(parts, "### Idolum Position\n"+artifact.IdolumProposal)
	}
	if len(artifact.RatifiedSteps) > 0 {
		lines := []string{"### Aphelion Execution Contract"}
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
		parts = append(parts, "### Aphelion Ratification Record\n"+artifact.RatificationRecord)
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
