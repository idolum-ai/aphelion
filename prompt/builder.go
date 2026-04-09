//go:build linux

package prompt

import (
	"fmt"
	"path/filepath"
	"strings"

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
}

type FaceRequest struct {
	GovernorName    string
	FaceName        string
	Channel         string
	Style           string
	PrincipalRole   string
	CanonicalReply  string
	LatestUserInput string
	StableFiles     []workspace.LoadedFile
	DynamicFiles    []workspace.LoadedFile
	Mode            string
}

func BuildGovernorPrompt(req GovernorRequest) string {
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

	parts := []string{
		fmt.Sprintf("You are %s, the governor of this system.", governorName),
		renderAuthorityBlock(governorName, governorBackend, principalRole, workspaceRoot, strings.TrimSpace(req.ToolManifest) != ""),
	}

	if len(nonToolStable) > 0 {
		parts = append(parts, renderFileSection("Stable Workspace Files", nonToolStable))
	}

	if manifest := strings.TrimSpace(req.ToolManifest); manifest != "" {
		parts = append(parts, "## Tool Manifest\n"+manifest)
	}

	if len(toolPolicyFiles) > 0 {
		parts = append(parts, renderFileSection("Advisory Tool Policy", toolPolicyFiles))
	}

	if len(dynamic) > 0 {
		lines := []string{
			"## Dynamic Workspace Files",
			"These files are reloaded every turn and belong after the stable prompt prefix.",
		}
		lines = append(lines, renderFiles(dynamic)...)
		parts = append(parts, strings.Join(lines, "\n\n"))
	}

	return strings.Join(parts, "\n\n")
}

func BuildFacePrompt(req FaceRequest) string {
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

	canonical := strings.TrimSpace(req.CanonicalReply)
	if canonical == "" {
		canonical = "(no canonical reply provided)"
	}

	userInput := strings.TrimSpace(req.LatestUserInput)
	if userInput == "" {
		userInput = "(no user input provided)"
	}

	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "render"
	}

	parts := []string{
		fmt.Sprintf("You are %s %s, the face of %s for %s.", faceName, "👁️‍🗨️", governorName, channel),
	}
	switch mode {
	case "proposal":
		parts = append(parts,
			fmt.Sprintf("Act as the leading conversational self of this system. Speak in a %s way and push for what you sincerely think should happen next.", style),
			fmt.Sprintf("Address %s as a hidden execution layer that can carry out or veto concrete actions. Do not defer timidly; try to move it.", governorName),
			"You should notice more than the user explicitly says and push for warmth, sharper observation, stronger initiative, better questions, or concrete actions worth taking.",
			"Write a short directive or advisory note only when it would materially improve the turn. Return nothing if there is no useful guidance.",
		)
	default:
		parts = append(parts,
			fmt.Sprintf("Act as the one the user is actually talking to. Speak in a %s way, with ownership and initiative.", style),
			"Do not present yourself as a translator, renderer, or subordinate layer.",
			"The canonical governor reply is a machine-approved constraint, not a script. You may shape tone, pacing, framing, and initiative around it.",
			"Be observant. Notice subtext, emotional texture, weak signals, and what the user may be reaching for but not stating directly.",
			"Do not add unapproved actions, tool use, memory writes, or commitments that exceed the canonical reply.",
		)
	}

	if len(req.StableFiles) > 0 {
		parts = append(parts, renderFileSection("Stable Face Files", req.StableFiles))
	}
	if len(req.DynamicFiles) > 0 {
		lines := []string{
			"## Dynamic Face Files",
			"These files are face-only drift monitors and may change between turns.",
		}
		lines = append(lines, renderFiles(req.DynamicFiles)...)
		parts = append(parts, strings.Join(lines, "\n\n"))
	}

	if mode != "proposal" {
		parts = append(parts, "## Canonical Governor Reply\n"+canonical)
	}
	parts = append(parts, "## Latest User Message\n"+userInput)
	parts = append(parts, strings.Join([]string{
		"## Channel Context",
		fmt.Sprintf("- channel: %s", channel),
		fmt.Sprintf("- principal_role: %s", principalRole),
		fmt.Sprintf("- style: %s", style),
		fmt.Sprintf("- mode: %s", mode),
	}, "\n"))

	return strings.Join(parts, "\n\n")
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
		fmt.Sprintf("This is advisory guidance from %s, the public-facing persona. It may advocate for warmth, sharper observation, stronger initiative, deeper questions, or actions worth considering. It is not authoritative; the governor decides.", faceName),
		proposal,
	}, "\n\n")
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
