//go:build linux

package prompt

import (
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/workspace"
)

func TestBuildGovernorPromptPlacesAuthorityFirst(t *testing.T) {
	t.Parallel()

	got := BuildGovernorPrompt(GovernorRequest{
		GovernorName:    "Aphelion",
		GovernorBackend: "native",
		PrincipalRole:   "admin",
		WorkspaceRoot:   "/tmp/ws",
		Workspace: &workspace.PromptContext{
			Stable: []workspace.LoadedFile{
				{Path: "SOUL.md", Content: "core soul"},
			},
		},
	})

	authorityIdx := strings.Index(got, "## Authority")
	soulIdx := strings.Index(got, "### SOUL.md")
	if authorityIdx == -1 || soulIdx == -1 {
		t.Fatalf("prompt missing sections: %q", got)
	}
	if authorityIdx > soulIdx {
		t.Fatalf("authority block should precede workspace files: %q", got)
	}
}

func TestBuildGovernorPromptPlacesManifestBeforeToolsPolicy(t *testing.T) {
	t.Parallel()

	got := BuildGovernorPrompt(GovernorRequest{
		ToolManifest: "tools:\n- exec: shell execution",
		Workspace: &workspace.PromptContext{
			Stable: []workspace.LoadedFile{
				{Path: "AGENTS.md", Content: "agent rules"},
				{Path: "TOOLS.md", Content: "be careful with tools"},
			},
		},
	})

	manifestIdx := strings.Index(got, "## Tool Manifest")
	toolsIdx := strings.Index(got, "### TOOLS.md")
	if manifestIdx == -1 || toolsIdx == -1 {
		t.Fatalf("prompt missing tool sections: %q", got)
	}
	if manifestIdx > toolsIdx {
		t.Fatalf("tool manifest should precede TOOLS.md: %q", got)
	}
}

func TestBuildGovernorPromptPlacesDynamicFilesAfterStableSections(t *testing.T) {
	t.Parallel()

	got := BuildGovernorPrompt(GovernorRequest{
		Workspace: &workspace.PromptContext{
			Stable: []workspace.LoadedFile{
				{Path: "SOUL.md", Content: "stable"},
			},
			Dynamic: []workspace.LoadedFile{
				{Path: "MEMORY.md", Content: "dynamic"},
			},
		},
	})

	stableIdx := strings.Index(got, "## Stable Workspace Files")
	dynamicIdx := strings.Index(got, "## Dynamic Workspace Files")
	if stableIdx == -1 || dynamicIdx == -1 {
		t.Fatalf("prompt missing stable/dynamic sections: %q", got)
	}
	if stableIdx > dynamicIdx {
		t.Fatalf("dynamic files should follow stable sections: %q", got)
	}
}

func TestBuildFacePromptOmitsToolDefinitions(t *testing.T) {
	t.Parallel()

	got := BuildFacePrompt(FaceRequest{
		GovernorName:    "Aphelion",
		FaceName:        "Host",
		Channel:         "telegram",
		CanonicalReply:  "I changed the file.",
		LatestUserInput: "please update it",
	})

	if strings.Contains(got, "## Tool Manifest") || strings.Contains(got, "exec constraints") {
		t.Fatalf("face prompt should not include tool definitions: %q", got)
	}
	if !strings.Contains(got, "## Canonical Governor Reply") {
		t.Fatalf("face prompt missing canonical reply section: %q", got)
	}
}

func TestBuildFacePromptIncludesHostFilesAndOrder(t *testing.T) {
	t.Parallel()

	got := BuildFacePrompt(FaceRequest{
		GovernorName:    "Aphelion",
		FaceName:        "Host",
		Channel:         "telegram",
		PrincipalRole:   "admin",
		CanonicalReply:  "Canonical answer",
		LatestUserInput: "What changed?",
		StableFiles: []workspace.LoadedFile{
			{Path: "HOST.md", Content: "host defaults"},
		},
		DynamicFiles: []workspace.LoadedFile{
			{Path: "QUESTIONS-TO-HOST.md", Content: "avoid flattery"},
		},
	})

	if !strings.Contains(got, "## Stable Face Files") || !strings.Contains(got, "### HOST.md") {
		t.Fatalf("face prompt missing stable host files: %q", got)
	}
	if !strings.Contains(got, "## Dynamic Face Files") || !strings.Contains(got, "### QUESTIONS-TO-HOST.md") {
		t.Fatalf("face prompt missing dynamic host files: %q", got)
	}
	if !strings.Contains(got, "Host is presentational, not sovereign.") {
		t.Fatalf("face prompt missing sovereignty boundary: %q", got)
	}

	stableIdx := strings.Index(got, "## Stable Face Files")
	dynamicIdx := strings.Index(got, "## Dynamic Face Files")
	canonicalIdx := strings.Index(got, "## Canonical Governor Reply")
	userIdx := strings.Index(got, "## Latest User Message")
	if stableIdx == -1 || dynamicIdx == -1 || canonicalIdx == -1 || userIdx == -1 {
		t.Fatalf("face prompt missing expected layered sections: %q", got)
	}
	if !(stableIdx < dynamicIdx && dynamicIdx < canonicalIdx && canonicalIdx < userIdx) {
		t.Fatalf("face prompt sections are out of order: %q", got)
	}
}
