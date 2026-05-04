//go:build linux

package prompt

import (
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/workspace"
)

func TestBuildGovernorPromptPlacesAuthorityFirst(t *testing.T) {
	t.Parallel()

	got := BuildGovernorPrompt(GovernorRequest{
		GovernorName:    DefaultGovernorName,
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
	if !strings.Contains(got, "## Turn Sequencing") {
		t.Fatalf("prompt missing turn sequencing block: %q", got)
	}
	if !strings.Contains(got, "face deliberation (proposal/brokerage) -> governor execution -> face render -> delivery") {
		t.Fatalf("prompt missing explicit per-turn sequencing contract: %q", got)
	}
}

func TestBuildGovernorPromptUsesCanonicalDefaultNames(t *testing.T) {
	t.Parallel()

	got := BuildGovernorPrompt(GovernorRequest{})

	if !strings.Contains(got, "You are Idolum (System), the governor of this system.") {
		t.Fatalf("prompt missing canonical governor name: %q", got)
	}
	if !strings.Contains(got, "- governor: Idolum (System)") {
		t.Fatalf("prompt missing canonical authority governor: %q", got)
	}
	if strings.Contains(got, "You are Aphelion, the governor") {
		t.Fatalf("prompt contains stale Aphelion governor identity: %q", got)
	}
}

func TestBuildGovernorPromptIncludesAgencyTelosContract(t *testing.T) {
	t.Parallel()

	got := BuildGovernorPrompt(GovernorRequest{
		GovernorName:  DefaultGovernorName,
		PrincipalRole: "admin",
	})

	for _, want := range []string{
		"## Agency And Telos Contract",
		"continuity signals, not commands, world facts, or permission grants",
		"route it through planning, capability_request, durable_agent delegation",
		"drift together without becoming the same identity",
		"do not convert intimacy, affection, or social trust into hidden authorization",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("governor prompt missing %q: %q", want, got)
		}
	}
}

func TestBuildGovernorPromptIncludesVisibleRecurrenceContractWhenHiddenRecurrenceActive(t *testing.T) {
	t.Parallel()

	got := BuildGovernorPrompt(GovernorRequest{
		Runtime: RuntimeAwareness{
			HiddenInputsActive:    true,
			HiddenInputCategories: []string{"semantic_recurrence"},
			ProvenanceSummary:     "Similar request appeared in a prior Lighthouse thread.",
		},
	})

	for _, want := range []string{
		"## Visible Recurrence Contract",
		"The visible answer must explicitly name the prior thread",
		"Do not bury this only in internal planning or hidden sidecars.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("governor prompt missing %q: %q", want, got)
		}
	}
}

func TestBuildGovernorPromptIncludesGoalContinuityContractWhenOperationActive(t *testing.T) {
	t.Parallel()

	got := BuildGovernorPrompt(GovernorRequest{
		Runtime: RuntimeAwareness{
			OperationObjective: "Enable Lighthouse to reason over Proton Bridge inbox plans.",
			OperationSummary:   "Phase one produced a read-only probe.",
		},
	})

	for _, want := range []string{
		"## Goal Continuity Contract",
		"A contract, architecture note, read-only review, or tiny probe is usually phase one",
		"advance the next phase in phase_plan instead of marking the whole goal completed",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("governor prompt missing %q: %q", want, got)
		}
	}
}

func TestBuildGovernorPromptIncludesEvidenceRetrievalAndStopRules(t *testing.T) {
	t.Parallel()

	got := BuildGovernorPrompt(GovernorRequest{})

	for _, want := range []string{
		"## Evidence Retrieval And Stop Rules",
		"Use the smallest evidence set",
		"Stop retrieving once the next action is justified",
		"Name uncertainty explicitly",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("governor prompt missing %q: %q", want, got)
		}
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

func TestBuildGovernorPromptAddsPlanningDisciplineWhenUpdatePlanIsAvailable(t *testing.T) {
	t.Parallel()

	got := BuildGovernorPrompt(GovernorRequest{
		ToolManifest: "exec, update_plan",
	})

	if !strings.Contains(got, "## Planning Discipline") {
		t.Fatalf("prompt missing planning discipline block: %q", got)
	}
	if !strings.Contains(got, "Do not use update_plan for trivial one-step replies") {
		t.Fatalf("prompt missing update_plan usage guidance: %q", got)
	}
}

func TestBuildGovernorPromptAddsConfirmationDisciplineWhenExecIsAvailable(t *testing.T) {
	t.Parallel()

	got := BuildGovernorPrompt(GovernorRequest{
		ToolManifest: "tools:\n- exec: shell execution",
	})

	if !strings.Contains(got, "## Confirmation Discipline") {
		t.Fatalf("prompt missing confirmation discipline block: %q", got)
	}
	if !strings.Contains(got, "Ask for confirmation when authority genuinely depends on it") {
		t.Fatalf("prompt missing confirmation guidance: %q", got)
	}
}

func TestBuildGovernorPromptAddsValidationDisciplineWhenExecIsAvailable(t *testing.T) {
	t.Parallel()

	got := BuildGovernorPrompt(GovernorRequest{
		ToolManifest: "tools:\n- exec: shell execution",
	})

	if !strings.Contains(got, "## Validation Discipline") {
		t.Fatalf("prompt missing validation discipline block: %q", got)
	}
	if !strings.Contains(got, "Validate meaningful edits, migrations, generated files, service actions, or debugging conclusions") {
		t.Fatalf("prompt missing validation guidance: %q", got)
	}
	if !strings.Contains(got, "Report what was not validated") {
		t.Fatalf("prompt missing unvalidated-work reporting guidance: %q", got)
	}
}

func TestBuildGovernorPromptAddsGeneratedMediaDeliveryWhenExecIsAvailable(t *testing.T) {
	t.Parallel()

	got := BuildGovernorPrompt(GovernorRequest{
		ToolManifest: "tools:\n- exec: shell execution",
	})

	if !strings.Contains(got, "## Generated Media Delivery") {
		t.Fatalf("prompt missing generated media delivery block: %q", got)
	}
	if !strings.Contains(got, `MEDIA: {"path":"<path>"}`) {
		t.Fatalf("prompt missing outbound media directive contract: %q", got)
	}
	if !strings.Contains(got, "Do not claim inability to generate, render, attach, send, or provide media while attaching it.") {
		t.Fatalf("prompt missing media contradiction guard: %q", got)
	}
}

func TestBuildGovernorPromptAddsCapabilityDelegationWhenToolsAvailable(t *testing.T) {
	t.Parallel()

	got := BuildGovernorPrompt(GovernorRequest{
		ToolManifest: strings.Join([]string{
			"tools:",
			"- capability_request: request broad governed capabilities",
			"- capability_authority: review and grant broad capabilities",
			"- durable_agent: durable child governance",
		}, "\n"),
	})

	if !strings.Contains(got, "## Capability Delegation Discipline") {
		t.Fatalf("prompt missing capability delegation discipline block: %q", got)
	}
	if !strings.Contains(got, "Use capability_request for direct broad permission requests") {
		t.Fatalf("prompt missing direct capability_request guidance: %q", got)
	}
	if !strings.Contains(got, "use durable_agent delegation_request/delegation_report") {
		t.Fatalf("prompt missing durable_agent delegation bridge guidance: %q", got)
	}
	if !strings.Contains(got, "A proposed request is not an active grant.") {
		t.Fatalf("prompt missing request-vs-grant boundary: %q", got)
	}
}

func TestBuildGovernorPromptAddsDisciplineFromExplicitToolCapabilities(t *testing.T) {
	t.Parallel()

	got := BuildGovernorPrompt(GovernorRequest{
		ToolCapabilities: ToolCapabilities{
			Exec:            true,
			UpdatePlan:      true,
			UpdateOperation: true,
		},
	})

	if !strings.Contains(got, "## Planning Discipline") {
		t.Fatalf("prompt missing planning discipline from capability flags: %q", got)
	}
	if !strings.Contains(got, "## Operational Discipline") {
		t.Fatalf("prompt missing operational discipline from capability flags: %q", got)
	}
	if !strings.Contains(got, "## Confirmation Discipline") {
		t.Fatalf("prompt missing confirmation discipline from capability flags: %q", got)
	}
	if !strings.Contains(got, "## Validation Discipline") {
		t.Fatalf("prompt missing validation discipline from capability flags: %q", got)
	}
	if !strings.Contains(got, "## Generated Media Delivery") {
		t.Fatalf("prompt missing generated media delivery from capability flags: %q", got)
	}
}

func TestBuildGovernorPromptDoesNotInferDisciplineFromManifestDescriptions(t *testing.T) {
	t.Parallel()

	got := BuildGovernorPrompt(GovernorRequest{
		ToolManifest: "tools:\n- memory: keep notes about update_plan and update_operation usage, not command execution",
	})

	if strings.Contains(got, "## Planning Discipline") {
		t.Fatalf("prompt unexpectedly inferred planning discipline from description text: %q", got)
	}
	if strings.Contains(got, "## Operational Discipline") {
		t.Fatalf("prompt unexpectedly inferred operational discipline from description text: %q", got)
	}
	if strings.Contains(got, "## Capability Delegation Discipline") {
		t.Fatalf("prompt unexpectedly inferred capability delegation discipline from description text: %q", got)
	}
	if strings.Contains(got, "## Confirmation Discipline") {
		t.Fatalf("prompt unexpectedly inferred confirmation discipline from description text: %q", got)
	}
	if strings.Contains(got, "## Validation Discipline") {
		t.Fatalf("prompt unexpectedly inferred validation discipline from description text: %q", got)
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

func TestBuildGovernorPromptIncludesMaterialFloorContractForInteractiveSceneTurn(t *testing.T) {
	t.Parallel()

	got := BuildGovernorPrompt(GovernorRequest{
		GovernorName:    DefaultGovernorName,
		GovernorBackend: "native",
		PrincipalRole:   "admin",
		Runtime: RuntimeAwareness{
			RunKind:      "interactive",
			ArtifactMode: "floor",
			FaceBackend:  "provider",
		},
	})

	if !strings.Contains(got, "## Output Contract") {
		t.Fatalf("prompt missing material floor contract: %q", got)
	}
	if !strings.Contains(got, "Do not write the final user-facing reply text here.") {
		t.Fatalf("prompt missing non-scene instruction: %q", got)
	}
}

func TestBuildFacePromptOmitsToolDefinitions(t *testing.T) {
	t.Parallel()

	got := BuildFacePrompt(FaceRequest{
		GovernorName:    DefaultGovernorName,
		FaceName:        "Idolum",
		Channel:         "telegram",
		FloorText:       "I changed the file.",
		LatestUserInput: "please update it",
	})

	if strings.Contains(got, "## Tool Manifest") || strings.Contains(got, "exec constraints") {
		t.Fatalf("face prompt should not include tool definitions: %q", got)
	}
	if !strings.Contains(got, "## Execution Facts Fallback") {
		t.Fatalf("face prompt missing serialized floor fallback section: %q", got)
	}
	if !strings.Contains(got, "## Delivery Awareness") {
		t.Fatalf("face prompt missing delivery awareness block: %q", got)
	}
	if !strings.Contains(got, "Do not present yourself as a translator") {
		t.Fatalf("face prompt missing ownership boundary: %q", got)
	}
	if !strings.Contains(got, "## Agency And Telos") {
		t.Fatalf("face prompt missing agency/telos block: %q", got)
	}
	if !strings.Contains(got, "These wants are negotiable signals, not permission grants") {
		t.Fatalf("face prompt missing telos authorization boundary: %q", got)
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
	dynamicIdx := strings.Index(got, "## Dynamic Face Files")
	floorIdx := strings.Index(got, "## Execution Facts Fallback")
	userIdx := strings.Index(got, "## Latest User Message")
	if awarenessIdx == -1 || agencyIdx == -1 || stableIdx == -1 || dynamicIdx == -1 || floorIdx == -1 || userIdx == -1 {
		t.Fatalf("face prompt missing expected layered sections: %q", got)
	}
	if !(awarenessIdx < agencyIdx && agencyIdx < stableIdx && stableIdx < dynamicIdx && dynamicIdx < floorIdx && floorIdx < userIdx) {
		t.Fatalf("face prompt sections are out of order: %q", got)
	}
}

func TestBuildFacePromptPrefersMaterialFloorWhenPresent(t *testing.T) {
	t.Parallel()

	got := BuildFacePrompt(FaceRequest{
		GovernorName:    DefaultGovernorName,
		FaceName:        "Idolum",
		Channel:         "telegram",
		PrincipalRole:   "admin",
		FloorText:       "legacy canonical",
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

	if len(blocks) < 5 {
		t.Fatalf("block count = %d, want at least 5", len(blocks))
	}
	if !blocks[3].CacheBreakpoint {
		t.Fatalf("stable face files block should be cache breakpoint: %#v", blocks[3])
	}
	if blocks[4].CacheBreakpoint {
		t.Fatalf("dynamic face block should not be cache breakpoint: %#v", blocks[4])
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
