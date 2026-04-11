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
	if !strings.Contains(got, "## Runtime Awareness") {
		t.Fatalf("prompt missing runtime awareness block: %q", got)
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

func TestBuildGovernorPromptBlocksMarksStableBoundaryForCaching(t *testing.T) {
	t.Parallel()

	blocks := BuildGovernorPromptBlocks(GovernorRequest{
		ToolManifest: "tools:\n- exec",
		Workspace: &workspace.PromptContext{
			Stable: []workspace.LoadedFile{
				{Path: "SOUL.md", Content: "stable"},
			},
			Dynamic: []workspace.LoadedFile{
				{Path: "MEMORY.md", Content: "dynamic"},
			},
		},
	})

	if len(blocks) < 3 {
		t.Fatalf("block count = %d, want at least 3", len(blocks))
	}
	if !blocks[len(blocks)-2].CacheBreakpoint {
		t.Fatalf("last stable block should be cache breakpoint: %#v", blocks)
	}
	if blocks[len(blocks)-1].CacheBreakpoint {
		t.Fatalf("dynamic block should not be cache breakpoint: %#v", blocks[len(blocks)-1])
	}
}

func TestBuildFacePromptOmitsToolDefinitions(t *testing.T) {
	t.Parallel()

	got := BuildFacePrompt(FaceRequest{
		GovernorName:    "Aphelion",
		FaceName:        "Idolum",
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
	if !strings.Contains(got, "## Delivery Awareness") {
		t.Fatalf("face prompt missing delivery awareness block: %q", got)
	}
	if !strings.Contains(got, "Do not present yourself as a translator") {
		t.Fatalf("face prompt missing ownership boundary: %q", got)
	}
}

func TestBuildFacePromptIncludesIdolumFilesAndOrder(t *testing.T) {
	t.Parallel()

	got := BuildFacePrompt(FaceRequest{
		GovernorName:    "Aphelion",
		FaceName:        "Idolum",
		Channel:         "telegram",
		PrincipalRole:   "admin",
		CanonicalReply:  "Canonical answer",
		LatestUserInput: "What changed?",
		StableFiles: []workspace.LoadedFile{
			{Path: "IDOLUM.md", Content: "idolum defaults"},
		},
		DynamicFiles: []workspace.LoadedFile{
			{Path: "QUESTIONS-TO-IDOLUM.md", Content: "avoid flattery"},
		},
	})

	if !strings.Contains(got, "## Stable Face Files") || !strings.Contains(got, "### IDOLUM.md") {
		t.Fatalf("face prompt missing stable idolum files: %q", got)
	}
	if !strings.Contains(got, "## Dynamic Face Files") || !strings.Contains(got, "### QUESTIONS-TO-IDOLUM.md") {
		t.Fatalf("face prompt missing dynamic idolum files: %q", got)
	}
	if !strings.Contains(got, "Act as the one the user is actually talking to.") {
		t.Fatalf("face prompt missing phenomenological primary guidance: %q", got)
	}

	stableIdx := strings.Index(got, "## Stable Face Files")
	awarenessIdx := strings.Index(got, "## Delivery Awareness")
	dynamicIdx := strings.Index(got, "## Dynamic Face Files")
	canonicalIdx := strings.Index(got, "## Canonical Governor Reply")
	userIdx := strings.Index(got, "## Latest User Message")
	if awarenessIdx == -1 || stableIdx == -1 || dynamicIdx == -1 || canonicalIdx == -1 || userIdx == -1 {
		t.Fatalf("face prompt missing expected layered sections: %q", got)
	}
	if !(awarenessIdx < stableIdx && stableIdx < dynamicIdx && dynamicIdx < canonicalIdx && canonicalIdx < userIdx) {
		t.Fatalf("face prompt sections are out of order: %q", got)
	}
}

func TestBuildFaceProposalPromptEncouragesIdolumPush(t *testing.T) {
	t.Parallel()

	got := BuildFacePrompt(FaceRequest{
		GovernorName:    "Aphelion",
		FaceName:        "Idolum",
		Channel:         "telegram",
		PrincipalRole:   "admin",
		LatestUserInput: "help me",
		Mode:            "proposal",
	})

	if strings.Contains(got, "## Canonical Governor Reply") {
		t.Fatalf("proposal prompt should not include canonical reply section: %q", got)
	}
	if !strings.Contains(got, "This is not turn-mode selection") {
		t.Fatalf("proposal prompt missing boundary from brokerage: %q", got)
	}
	if !strings.Contains(got, "reaching for") {
		t.Fatalf("proposal prompt missing subtext observation guidance: %q", got)
	}
}

func TestBuildFaceBrokeragePromptEncouragesTurnModeSelection(t *testing.T) {
	t.Parallel()

	got := BuildFacePrompt(FaceRequest{
		GovernorName:    "Aphelion",
		FaceName:        "Idolum",
		Channel:         "telegram",
		PrincipalRole:   "admin",
		LatestUserInput: "come up with some features for my codebase",
		Mode:            "brokerage",
	})

	if strings.Contains(got, "## Canonical Governor Reply") {
		t.Fatalf("brokerage prompt should not include canonical reply section: %q", got)
	}
	if !strings.Contains(got, "If you name a turn mode, put it on its own line as MODE:") {
		t.Fatalf("brokerage prompt missing turn mode guidance: %q", got)
	}
	if !strings.Contains(got, "You may omit a mode entirely") {
		t.Fatalf("brokerage prompt missing optional-mode guidance: %q", got)
	}
	if !strings.Contains(got, "Do not turn this into a form") {
		t.Fatalf("brokerage prompt missing anti-bureaucracy guidance: %q", got)
	}
	if !strings.Contains(got, "inspect_then_answer") {
		t.Fatalf("brokerage prompt missing brokerage mode vocabulary: %q", got)
	}
}

func TestBuildFacePromptBlocksMarksStableBoundaryForCaching(t *testing.T) {
	t.Parallel()

	blocks := BuildFacePromptBlocks(FaceRequest{
		GovernorName:    "Aphelion",
		FaceName:        "Idolum",
		Channel:         "telegram",
		LatestUserInput: "hello",
		CanonicalReply:  "hi",
		StableFiles: []workspace.LoadedFile{
			{Path: "IDOLUM.md", Content: "stable"},
		},
		DynamicFiles: []workspace.LoadedFile{
			{Path: "QUESTIONS-TO-IDOLUM.md", Content: "dynamic"},
		},
	})

	if len(blocks) < 4 {
		t.Fatalf("block count = %d, want at least 4", len(blocks))
	}
	if !blocks[2].CacheBreakpoint {
		t.Fatalf("stable face files block should be cache breakpoint: %#v", blocks[2])
	}
	if blocks[3].CacheBreakpoint {
		t.Fatalf("dynamic face block should not be cache breakpoint: %#v", blocks[3])
	}
}

func TestBuildGovernorPromptIncludesResolvedRuntimeFacts(t *testing.T) {
	t.Parallel()

	got := BuildGovernorPrompt(GovernorRequest{
		GovernorName:    "Aphelion",
		GovernorBackend: "codex",
		PrincipalRole:   "approved_user",
		WorkspaceRoot:   "/tmp/user-work",
		Runtime: RuntimeAwareness{
			SessionKind:          "interactive",
			RunKind:              "interactive",
			Channel:              "telegram",
			GovernorProvider:     "codex",
			GovernorModel:        "codex",
			GovernorProviderPath: []string{"codex", "anthropic", "openrouter"},
			ActiveProvider:       "codex",
			FallbackActive:       false,
			ReasoningEffort:      "medium",
			ReasoningSummary:     "auto",
			PromptRoot:           "/tmp/prompt",
			ExecRoot:             "/tmp/exec",
			SharedMemoryRoot:     "/tmp/shared",
			UserWorkspaceRoot:    "/tmp/users/42/work",
			UserMemoryRoot:       "/tmp/users/42/memory",
			WorkingRoot:          "/tmp/users/42/work",
			SandboxMode:          "isolated",
			NetworkPolicy:        "deny",
		},
	})

	for _, want := range []string{
		"- run_kind: interactive",
		"- channel: telegram",
		"- governor_provider: codex",
		"- configured_provider_path: codex -> anthropic -> openrouter",
		"- prompt_root: /tmp/prompt",
		"- sandbox_mode: isolated",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q: %q", want, got)
		}
	}
}

func TestBuildFacePromptKeepsAwarenessNarrow(t *testing.T) {
	t.Parallel()

	got := BuildFacePrompt(FaceRequest{
		GovernorName:    "Aphelion",
		FaceName:        "Idolum",
		Channel:         "telegram",
		PrincipalRole:   "admin",
		CanonicalReply:  "done",
		LatestUserInput: "hello",
		Runtime: RuntimeAwareness{
			SessionKind:      "interactive",
			RunKind:          "interactive",
			Channel:          "telegram",
			GovernorBackend:  "codex",
			GovernorProvider: "codex",
			GovernorModel:    "codex",
			ActiveProvider:   "anthropic",
			FallbackActive:   true,
			ReasoningEffort:  "medium",
			ReasoningSummary: "auto",
			FaceBackend:      "provider",
			FaceProvider:     "anthropic",
			DeliveryMode:     "stream",
			StreamReply:      true,
			ExecRoot:         "/tmp/exec",
		},
	})

	for _, want := range []string{
		"- active_provider: anthropic",
		"- fallback_active: true",
		"- delivery_mode: stream",
		"- stream_reply: true",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("face prompt missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "exec_root") {
		t.Fatalf("face prompt should not expose exec roots: %q", got)
	}
}

func TestRenderIdolumProposalForGovernorWrapsAdvisory(t *testing.T) {
	t.Parallel()

	got := RenderIdolumProposalForGovernor("Idolum", "Push for more initiative.")
	if !strings.Contains(got, "## Idolum Proposal") {
		t.Fatalf("wrapped proposal missing heading: %q", got)
	}
	if !strings.Contains(got, "Push for more initiative.") {
		t.Fatalf("wrapped proposal missing content: %q", got)
	}
}

func TestRenderBrokeragePlanForGovernorWrapsNegotiation(t *testing.T) {
	t.Parallel()

	got := RenderBrokeragePlanForGovernor(BrokerageArtifact{
		IdolumProposal:     "MODE: inspect_then_answer\nPUSH:\n- Inspect first.",
		RatifiedTurnMode:   "inspect_then_answer",
		Ratification:       "adapt",
		RatifiedSteps:      []string{"Inspect prompt, runtime, and memory surfaces first."},
		RatificationRecord: "MODE: inspect_then_answer\nRATIFICATION: adapt\nPLAN:\n- Inspect prompt, runtime, and memory surfaces first.",
	})
	if !strings.Contains(got, "## Negotiated Turn Brokerage") {
		t.Fatalf("wrapped plan missing heading: %q", got)
	}
	if !strings.Contains(got, "- ratification: adapt") {
		t.Fatalf("wrapped plan missing ratification summary: %q", got)
	}
	if !strings.Contains(got, "### Idolum Position") {
		t.Fatalf("wrapped plan missing idolum position: %q", got)
	}
	if !strings.Contains(got, "### Aphelion Execution Contract") {
		t.Fatalf("wrapped plan missing execution contract: %q", got)
	}
	if !strings.Contains(got, "### Aphelion Ratification Record") {
		t.Fatalf("wrapped plan missing ratification record: %q", got)
	}
}
