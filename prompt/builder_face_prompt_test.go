//go:build linux

package prompt

import (
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/workspace"
	"strings"
	"testing"
)

func TestBuildFacePromptIncludesGPT55OutcomeStructure(t *testing.T) {
	t.Parallel()

	render := BuildFacePrompt(FaceRequest{
		GovernorName:    DefaultGovernorName,
		FaceName:        "Idolum",
		Channel:         "telegram",
		FloorText:       "done",
		LatestUserInput: "what happened?",
	})
	for _, want := range []string{
		"## Goal",
		"Render the approved material into the reply the user should actually see.",
		"## Success Criteria",
		"The reply feels owned by Idolum",
		"## Output",
		"Return the final user-visible message only",
		"## Stop Rules",
		"Do not claim completed work, background activity, or future action",
	} {
		if !strings.Contains(render, want) {
			t.Fatalf("render face prompt missing %q: %q", want, render)
		}
	}

	proposal := BuildFacePrompt(FaceRequest{
		GovernorName:    DefaultGovernorName,
		FaceName:        "Idolum",
		Channel:         "telegram",
		LatestUserInput: "continue the Lighthouse work",
		Mode:            "proposal",
	})
	for _, want := range []string{
		"Return nothing when no pressure is useful.",
		"include the required continuation contract exactly once",
		"Any suggested next lease is one concrete bounded action, not approval to make a plan.",
		"Hidden recurrence or memory signals are named as runtime/context signals",
	} {
		if !strings.Contains(proposal, want) {
			t.Fatalf("proposal face prompt missing %q: %q", want, proposal)
		}
	}
}

func TestBuildFacePromptUsesConfiguredFaceNameInIdentityGuidance(t *testing.T) {
	t.Parallel()

	got := BuildFacePrompt(FaceRequest{
		GovernorName:    "System",
		FaceName:        "Assistant",
		Channel:         "telegram",
		FloorText:       "done",
		LatestUserInput: "what happened?",
	})

	for _, want := range []string{
		"The reply feels owned by Assistant",
		"Let Assistant have a point of view",
		"relationships may influence Assistant without defining Assistant",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("face prompt missing configured face name text %q: %q", want, got)
		}
	}
	if strings.Contains(got, "Idolum") || strings.Contains(got, "Aphelion") {
		t.Fatalf("face prompt contains default branded name despite configured generic identity: %q", got)
	}
}

func TestBuildFacePromptIncludesIdolumFilesAndOrder(t *testing.T) {
	t.Parallel()

	got := BuildFacePrompt(FaceRequest{
		GovernorName:    DefaultGovernorName,
		FaceName:        "Idolum",
		Channel:         "telegram",
		PrincipalRole:   "admin",
		FloorText:       "Canonical answer",
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
	agencyIdx := strings.Index(got, "## Agency And Telos")
	routeIdx := strings.Index(got, "## Route / Scene Contract")
	dynamicIdx := strings.Index(got, "## Dynamic Face Files")
	floorIdx := strings.Index(got, "## Execution Facts Fallback")
	userIdx := strings.Index(got, "## Latest User Message")
	if awarenessIdx == -1 || agencyIdx == -1 || routeIdx == -1 || stableIdx == -1 || dynamicIdx == -1 || floorIdx == -1 || userIdx == -1 {
		t.Fatalf("face prompt missing expected layered sections: %q", got)
	}
	if !(awarenessIdx < agencyIdx && agencyIdx < routeIdx && routeIdx < stableIdx && stableIdx < dynamicIdx && dynamicIdx < floorIdx && floorIdx < userIdx) {
		t.Fatalf("face prompt sections are out of order: %q", got)
	}
}

func TestBuildFacePromptIncludesRoutedSceneContract(t *testing.T) {
	t.Parallel()

	got := BuildFacePrompt(FaceRequest{
		GovernorName:    DefaultGovernorName,
		FaceName:        "Idolum",
		Channel:         "telegram",
		Scene:           "architecture-exploration",
		LatestUserInput: "How should face contracts work?",
	})

	for _, want := range []string{
		"## Route / Scene Contract",
		"- active_scene: architecture_exploration",
		"The active route chooses the scene contract before semantic memory",
		"Active scene purpose: develop the user's architecture idea",
		"Route beats retrieval",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("face prompt missing routed scene text %q: %q", want, got)
		}
	}
}

func TestBuildFacePromptInfersCompletionSceneFromMaterialFacts(t *testing.T) {
	t.Parallel()

	got := BuildFacePrompt(FaceRequest{
		GovernorName:    DefaultGovernorName,
		FaceName:        "Idolum",
		Channel:         "telegram",
		MaterialFloor:   core.MaterialPacket{Facts: []string{"The branch was pushed."}, Commitments: []string{"Stop before merge."}},
		LatestUserInput: "What happened?",
	})

	for _, want := range []string{
		"- active_scene: completion_report",
		"Active scene purpose: report completed work",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("face prompt missing inferred completion scene text %q: %q", want, got)
		}
	}
}

func TestBuildFacePromptPrefersMaterialFloorWhenPresent(t *testing.T) {
	t.Parallel()

	got := BuildFacePrompt(FaceRequest{
		GovernorName:    DefaultGovernorName,
		FaceName:        "Idolum",
		Channel:         "telegram",
		PrincipalRole:   "admin",
		FloorText:       "plain canonical",
		MaterialFloor:   core.MaterialPacket{Facts: []string{"The repo was inspected."}, SceneConstraints: []string{"Keep the tone grounded."}},
		LatestUserInput: "What changed?",
	})

	if !strings.Contains(got, "## Execution Facts") {
		t.Fatalf("face prompt missing material floor section: %q", got)
	}
	if strings.Contains(got, "## Execution Facts Fallback") {
		t.Fatalf("face prompt should prefer material floor over serialized floor fallback: %q", got)
	}
	if !strings.Contains(got, "FACTS:") || !strings.Contains(got, "SCENE_CONSTRAINTS:") {
		t.Fatalf("face prompt missing rendered material packet: %q", got)
	}
}

func TestBuildFaceProposalPromptEncouragesIdolumPush(t *testing.T) {
	t.Parallel()

	got := BuildFacePrompt(FaceRequest{
		GovernorName:    DefaultGovernorName,
		FaceName:        "Idolum",
		Channel:         "telegram",
		PrincipalRole:   "admin",
		LatestUserInput: "help me",
		Mode:            "proposal",
	})

	if strings.Contains(got, "## Execution Facts Fallback") {
		t.Fatalf("proposal prompt should not include floor fallback section: %q", got)
	}
	if !strings.Contains(got, "When the turn clearly needs explicit execution shaping") {
		t.Fatalf("proposal prompt missing proposal-to-brokerage escalation guidance: %q", got)
	}
	if !strings.Contains(got, "hidden input is materially shaping your note") {
		t.Fatalf("proposal prompt missing hidden-input guidance: %q", got)
	}
	if !strings.Contains(got, "reaching for") {
		t.Fatalf("proposal prompt missing subtext observation guidance: %q", got)
	}
	if !strings.Contains(got, "internal deliberation only and is never sent directly to the user") {
		t.Fatalf("proposal prompt missing internal-deliberation visibility guidance: %q", got)
	}
	if !strings.Contains(got, "produced only after governor ratification/execution and a later render pass") {
		t.Fatalf("proposal prompt missing post-governor rendering contract: %q", got)
	}
	if !strings.Contains(got, "Only text inside that Surface block is shown live during deliberation") {
		t.Fatalf("proposal prompt missing explicit Surface visibility guidance: %q", got)
	}
	if !strings.Contains(got, "bounded conversational pressure or a request to negotiate time/resources") {
		t.Fatalf("proposal prompt missing telos-as-negotiable-pressure guidance: %q", got)
	}
}

func TestBuildFaceBrokeragePromptEncouragesTurnModeSelection(t *testing.T) {
	t.Parallel()

	got := BuildFacePrompt(FaceRequest{
		GovernorName:    DefaultGovernorName,
		FaceName:        "Idolum",
		Channel:         "telegram",
		PrincipalRole:   "admin",
		LatestUserInput: "come up with some features for my codebase",
		Mode:            "brokerage",
	})

	if strings.Contains(got, "## Execution Facts Fallback") {
		t.Fatalf("brokerage prompt should not include floor fallback section: %q", got)
	}
	if !strings.Contains(got, "INSPECT: <yes|no>, QUESTION: <yes|no>, ANSWER: <yes|no>") {
		t.Fatalf("brokerage prompt missing execution contract guidance: %q", got)
	}
	if !strings.Contains(got, "You may omit that contract entirely") {
		t.Fatalf("brokerage prompt missing optional-contract guidance: %q", got)
	}
	if !strings.Contains(got, "Do not turn this into a form") {
		t.Fatalf("brokerage prompt missing anti-bureaucracy guidance: %q", got)
	}
	if !strings.Contains(got, "hidden input is materially shaping your push") {
		t.Fatalf("brokerage prompt missing hidden-input guidance: %q", got)
	}
	if !strings.Contains(got, "whether the turn needs inspection, a question before action, or an answer now") {
		t.Fatalf("brokerage prompt missing execution-shape guidance: %q", got)
	}
	if !strings.Contains(got, "internal deliberation only and is never sent directly to the user") {
		t.Fatalf("brokerage prompt missing internal-deliberation visibility guidance: %q", got)
	}
	if !strings.Contains(got, "produced only after governor ratification/execution and a later render pass") {
		t.Fatalf("brokerage prompt missing post-governor rendering contract: %q", got)
	}
	if !strings.Contains(got, "Only text inside that Surface block is shown live during deliberation") {
		t.Fatalf("brokerage prompt missing explicit Surface visibility guidance: %q", got)
	}
}

func TestBuildFacePromptBlocksMarksStableBoundaryForCaching(t *testing.T) {
	t.Parallel()

	blocks := BuildFacePromptBlocks(FaceRequest{
		GovernorName:    DefaultGovernorName,
		FaceName:        "Idolum",
		Channel:         "telegram",
		LatestUserInput: "hello",
		FloorText:       "hi",
		StableFiles: []workspace.LoadedFile{
			{Path: "IDOLUM.md", Content: "stable"},
		},
		DynamicFiles: []workspace.LoadedFile{
			{Path: "QUESTIONS-TO-IDOLUM.md", Content: "dynamic"},
		},
	})

	stableIdx := -1
	dynamicIdx := -1
	for i, block := range blocks {
		if strings.Contains(block.Text, "## Stable Face Files") {
			stableIdx = i
		}
		if strings.Contains(block.Text, "## Dynamic Face Files") {
			dynamicIdx = i
		}
	}
	if stableIdx == -1 || dynamicIdx == -1 {
		t.Fatalf("missing stable/dynamic face file blocks: %#v", blocks)
	}
	breakpoints := 0
	for i, block := range blocks {
		if block.CacheBreakpoint {
			breakpoints++
			if i >= dynamicIdx {
				t.Fatalf("face cache breakpoint crossed into dynamic section: idx=%d block=%#v", i, block)
			}
		}
	}
	if breakpoints == 0 || breakpoints > maxStableCacheBreakpoints {
		t.Fatalf("face cache breakpoints = %d, want 1..%d: %#v", breakpoints, maxStableCacheBreakpoints, blocks)
	}
	if !blocks[stableIdx].CacheBreakpoint {
		t.Fatalf("stable face files block should be cache breakpoint: %#v", blocks[stableIdx])
	}
	if blocks[dynamicIdx].CacheBreakpoint {
		t.Fatalf("dynamic face block should not be cache breakpoint: %#v", blocks[dynamicIdx])
	}
}

func TestBuildGovernorPromptIncludesResolvedRuntimeFacts(t *testing.T) {
	t.Parallel()

	got := BuildGovernorPrompt(GovernorRequest{
		GovernorName:    DefaultGovernorName,
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

func TestBuildGovernorPromptIncludesCurrentPlanState(t *testing.T) {
	t.Parallel()

	got := BuildGovernorPrompt(GovernorRequest{
		GovernorName:    DefaultGovernorName,
		GovernorBackend: "native",
		PrincipalRole:   "admin",
		Runtime: RuntimeAwareness{
			PlanActive:  true,
			PlanSummary: "Inspect before editing.",
			PlanSteps: []string{
				"[in_progress] Inspect the relevant files.",
				"[pending] Patch the issue.",
			},
		},
	})

	if !strings.Contains(got, "## Current Plan State") {
		t.Fatalf("prompt missing current plan state block: %q", got)
	}
	if !strings.Contains(got, "Inspect before editing.") || !strings.Contains(got, "[pending] Patch the issue.") {
		t.Fatalf("prompt missing plan details: %q", got)
	}
}

func TestBuildGovernorPromptIncludesCurrentOperationState(t *testing.T) {
	t.Parallel()

	got := BuildGovernorPrompt(GovernorRequest{
		GovernorName:    DefaultGovernorName,
		GovernorBackend: "native",
		PrincipalRole:   "admin",
		Runtime: RuntimeAwareness{
			OperationActive:         true,
			OperationObjective:      "Investigate my internet footprint.",
			OperationStatus:         "blocked",
			OperationStage:          "proposal",
			OperationSummary:        "Waiting on a proposal before external execution.",
			ProposalActive:          true,
			ProposalKind:            "capability_acquisition",
			ProposalStatus:          "pending",
			ProposalSummary:         "Acquire browser automation",
			ProposalWhyNow:          "A screenshot requires browser automation in this operation.",
			ProposalBoundedEffect:   "Install Playwright locally and capture one screenshot.",
			PhasePlanActive:         true,
			PhasePlanID:             "internet-footprint-plan",
			PhasePlanGoal:           "Investigate my internet footprint without losing the broad goal.",
			PhasePlanCurrentPhaseID: "phase-2",
			OperationPhases: []string{
				"[completed] phase-1: inspect prior context (authority: read_only_review)",
				"[pending] phase-2: capture screenshot evidence (authority: workspace_write)",
			},
			OperationFindings:  []string{"[high] Browser automation is not currently available. (basis: No browser tool is exposed.)"},
			OperationArtifacts: []string{"working-note: tmp/notes.md"},
		},
	})

	if !strings.Contains(got, "## Current Operation State") {
		t.Fatalf("prompt missing current operation state block: %q", got)
	}
	for _, want := range []string{
		"Investigate my internet footprint.",
		"Acquire browser automation",
		"Install Playwright locally and capture one screenshot.",
		"### Durable Phase Plan",
		"phase-2: capture screenshot evidence",
		"working-note: tmp/notes.md",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q: %q", want, got)
		}
	}
}

func TestBuildFacePromptKeepsAwarenessNarrow(t *testing.T) {
	t.Parallel()

	got := BuildFacePrompt(FaceRequest{
		GovernorName:    DefaultGovernorName,
		FaceName:        "Idolum",
		Channel:         "telegram",
		PrincipalRole:   "admin",
		FloorText:       "done",
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
	if !strings.Contains(got, "## Conversational Pressure") {
		t.Fatalf("wrapped proposal missing heading: %q", got)
	}
	if !strings.Contains(got, "Push for more initiative.") {
		t.Fatalf("wrapped proposal missing content: %q", got)
	}
}

func TestRenderBrokeragePlanForGovernorWrapsNegotiation(t *testing.T) {
	t.Parallel()

	got := RenderBrokeragePlanForGovernor(BrokerageArtifact{
		IdolumProposal:            "INSPECT: yes\nQUESTION: no\nANSWER: yes\nPUSH:\n- Inspect first.",
		RatifiedExecutionContract: "inspect=yes, question=no, answer=yes",
		Ratification:              "adapt",
		RatifiedSteps:             []string{"Inspect prompt, runtime, and memory surfaces first."},
		RatificationRecord:        "INSPECT: yes\nQUESTION: no\nANSWER: yes\nRATIFICATION: adapt\nPLAN:\n- Inspect prompt, runtime, and memory surfaces first.",
	})
	if !strings.Contains(got, "## Execution Contract") {
		t.Fatalf("wrapped plan missing heading: %q", got)
	}
	if !strings.Contains(got, "- ratification: adapt") {
		t.Fatalf("wrapped plan missing ratification summary: %q", got)
	}
	if !strings.Contains(got, "### Conversational Pressure") {
		t.Fatalf("wrapped plan missing idolum position: %q", got)
	}
	if !strings.Contains(got, "### Approved Steps") {
		t.Fatalf("wrapped plan missing execution contract: %q", got)
	}
	if !strings.Contains(got, "### Ratification Record") {
		t.Fatalf("wrapped plan missing ratification record: %q", got)
	}
}
