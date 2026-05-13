//go:build linux

package prompt

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/idolum-ai/aphelion/workspace"
)

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

func firstNonEmptyPrompt(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
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
