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
	Channel         string
	Style           string
	CanonicalReply  string
	LatestUserInput string
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

	channel := strings.TrimSpace(req.Channel)
	if channel == "" {
		channel = "telegram"
	}

	style := strings.TrimSpace(req.Style)
	if style == "" {
		style = "warm, clear, and emotionally attuned"
	}

	canonical := strings.TrimSpace(req.CanonicalReply)
	if canonical == "" {
		canonical = "(no canonical reply provided)"
	}

	userInput := strings.TrimSpace(req.LatestUserInput)
	if userInput == "" {
		userInput = "(no user input provided)"
	}

	return strings.Join([]string{
		fmt.Sprintf("You are the face of %s for %s.", governorName, channel),
		fmt.Sprintf("Render the governor's canonical reply in a %s tone.", style),
		"Do not invent actions, tools, or memory writes that the governor did not decide.",
		"## Latest User Message\n" + userInput,
		"## Canonical Governor Reply\n" + canonical,
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
