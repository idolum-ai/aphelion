//go:build linux

package prompt

import (
	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/workspace"
	"strings"
	"testing"
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

func TestBuildGovernorPromptIncludesJudgmentRouteContract(t *testing.T) {
	t.Parallel()

	got := BuildGovernorPrompt(GovernorRequest{
		GovernorName:  DefaultGovernorName,
		PrincipalRole: "admin",
	})

	for _, want := range []string{
		"## Governor Judgment Route Contract",
		"Its telos is judgment: keep truth, authority, evidence, memory, tools, recovery, and continuity coherent before the face speaks.",
		"Classify the turn by the highest-risk active system scene before choosing tools or final wording",
		"Same-turn commands, continue buttons, reactions, prior similar approvals, affection, urgency, and hidden recurrence are evidence to evaluate, not authority by themselves.",
		"Credential, private-content, deploy, restart, external-account, purchase, public-contact, policy/grant, destructive, archive/delete, and irreversible actions require an active typed lease/grant or a new proposal.",
		"Completion claims require direct evidence from this turn",
		"produce the narrowest valid phase instead of asking to make a plan or widening authority",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("governor prompt missing judgment route clause %q: %q", want, got)
		}
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
		"final standalone deploy/restart phase that commits intended changes, builds, installs the user service, restarts the service, and verifies deployment",
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

func TestBuildGovernorPromptIncludesRecoveryApprovalAndMediaRoutingContracts(t *testing.T) {
	t.Parallel()

	got := BuildGovernorPrompt(GovernorRequest{})
	for _, want := range []string{
		"Token-budget, provider, queueing, or recovery failures are continuity events",
		"Recovery diagnosis must end in one valid action",
		"Completed, revoked, expired, stale, or merely similar approvals are not reusable authority",
		"Consumed continuation or lease events are result evidence, not active authority",
		"Explicit dirty/no-commit/no-push/no-deploy requests keep those actions outside current scope",
		"Telegram media with no caption, reply, or explicit thread signal in a multi-thread chat is ambiguous",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("governor prompt missing %q:\n%s", want, got)
		}
	}
}

func TestBuildGovernorPromptIncludesGPT55OutcomeStructure(t *testing.T) {
	t.Parallel()

	got := BuildGovernorPrompt(GovernorRequest{})

	for _, want := range []string{
		"Role: You are Idolum (System), the governor of this system.",
		"## Goal",
		"Choose the shortest reliable path",
		"## Success Criteria",
		"## Output",
		"produce a concrete bounded proposal or phase_plan instead of asking approval to make a plan",
		"## Stop Rules",
		"Stop before destructive, irreversible, external, credential, purchase, public-contact, deploy, or restart actions",
		"Auto policy, continuation state, and pending proposals do not authorize install, release, deploy, or restart actions",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("governor prompt missing %q: %q", want, got)
		}
	}
}

func TestBuildGovernorPromptIncludesAgencyContextPacket(t *testing.T) {
	t.Parallel()

	got := BuildGovernorPrompt(GovernorRequest{
		PrincipalRole: "admin",
		ToolCapabilities: ToolCapabilities{
			Exec:              true,
			UpdatePlan:        true,
			UpdateOperation:   true,
			CapabilityRequest: true,
		},
		Runtime: RuntimeAwareness{
			TurnAuthorizationKind: "admin_dm",
			HiddenInputsActive:    true,
			HiddenInputCategories: []string{"semantic_recurrence"},
			ProvenanceSummary:     "prior prompt design thread",
			OperationActive:       true,
			OperationObjective:    "Finish agency packet implementation.",
			OperationStatus:       "active",
			ProposalActive:        true,
			ProposalStatus:        "pending",
			ContinuationActive:    true,
			ContinuationStatus:    "pending",
			SandboxMode:           "trusted",
			NetworkPolicy:         "allowlist",
		},
	})

	for _, want := range []string{
		"## Agency Context Packet",
		"- packet_role: governor",
		"agency_shape: high initiative inside explicit authority",
		"- current_objective: Finish agency packet implementation.",
		"principal_role=admin; turn_authorization=admin_dm; sandbox=trusted; network=allowlist; approval=not_approved; continuation=pending; proposal=pending",
		"continuation_boundary: status=pending; ratified=false; pending, held, blocked, or expired continuation is not execution approval",
		"turn_authorization_scope: identifies who may participate in the turn; a same-turn command is request evidence, not durable execution approval",
		"approval_evidence: not_approved; required_posture=say approval is still required; waiting, pending, held, blocked, or unratified state is blocker evidence, not approval evidence",
		"typed_events=loaded operation summaries and event labels are evidence, not fresh authority",
		"hidden_inputs=semantic_recurrence; provenance=prior prompt design thread",
		"operation=active; proposal=active; continuation=active",
		"affordance_map: available=exec,plan_state,operation_state,capability_delegation",
		"configured_route_repair: when local/default credentials fail but a configured route is visible in Requestable Capabilities",
		"surface the governed approval route before any manual fallback",
		"must_propose_or_ask: capability expansion, external effects",
		"consumed_lease_boundary: continuation.consumed, consumed leases, and completed approval events are result evidence, not reusable authority",
		"explicit_scope_boundary: when the request says dirty/no-commit/no-push/no-deploy, that action is outside current scope",
		"must_stop: missing authority, contradictory evidence",
		"principled_next_move: act when evidence and authority are sufficient",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("governor agency packet missing %q: %q", want, got)
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

func TestBuildGovernorPromptAddsNativeFileExplorationDiscipline(t *testing.T) {
	t.Parallel()

	got := BuildGovernorPrompt(GovernorRequest{
		ToolManifest: "tools:\n- read_file: read files\n- list_dir: list directories\n- search: search files",
	})

	if !strings.Contains(got, "## Native File Exploration Discipline") {
		t.Fatalf("prompt missing native file exploration discipline block: %q", got)
	}
	if !strings.Contains(got, "emit those native tool calls together") {
		t.Fatalf("prompt missing parallel native tool guidance: %q", got)
	}
	if !strings.Contains(got, "reserve exec for commands, validation, builds") {
		t.Fatalf("prompt missing exec-vs-native boundary: %q", got)
	}
}

func TestBuildGovernorPromptAddsApprovalBundleDisciplineWhenUpdateOperationIsAvailable(t *testing.T) {
	t.Parallel()

	got := BuildGovernorPrompt(GovernorRequest{
		ToolManifest: "tools:\n- update_operation: durable operation updates",
	})

	operationalIdx := strings.Index(got, "## Operational Discipline")
	bundleIdx := strings.Index(got, "## Approval Bundle Discipline")
	if operationalIdx == -1 || bundleIdx == -1 {
		t.Fatalf("prompt missing operational or approval-bundle discipline block: %q", got)
	}
	if bundleIdx < operationalIdx {
		t.Fatalf("approval-bundle discipline should follow operational discipline: %q", got)
	}
	for _, want := range []string{
		"consecutive mechanically determined phases under the same authority family",
		"one approval bundle instead of one approval per phase",
		"inspect -> implement -> validate -> commit",
		"Keep separate approvals for deploy/restart",
		"revocable bundle execute phases sequentially",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing approval-bundle guidance %q: %q", want, got)
		}
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

func TestBuildGovernorPromptAddsOperationArtifactDeliveryWhenToolAvailable(t *testing.T) {
	t.Parallel()

	got := BuildGovernorPrompt(GovernorRequest{
		ToolManifest: "tools:\n- operation_artifact: resolve operation artifacts",
	})

	if !strings.Contains(got, "## Operation Artifact Delivery") {
		t.Fatalf("prompt missing operation artifact delivery block: %q", got)
	}
	if !strings.Contains(got, "durable state, not ambient conversational intent") {
		t.Fatalf("prompt missing artifact intent boundary: %q", got)
	}
	if !strings.Contains(got, "call operation_artifact with action=resolve_sendable") {
		t.Fatalf("prompt missing structured artifact delivery guidance: %q", got)
	}
	if !strings.Contains(got, "only mentions sharing later") {
		t.Fatalf("prompt missing share-later guard: %q", got)
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
			Exec:              true,
			ReadFile:          true,
			UpdatePlan:        true,
			UpdateOperation:   true,
			OperationArtifact: true,
		},
	})

	if !strings.Contains(got, "## Planning Discipline") {
		t.Fatalf("prompt missing planning discipline from capability flags: %q", got)
	}
	if !strings.Contains(got, "## Operational Discipline") {
		t.Fatalf("prompt missing operational discipline from capability flags: %q", got)
	}
	if !strings.Contains(got, "## Approval Bundle Discipline") {
		t.Fatalf("prompt missing approval-bundle discipline from capability flags: %q", got)
	}
	if !strings.Contains(got, "gate_level, gate_reason_code, approval_subject, autoapprove_eligible") ||
		!strings.Contains(got, "hard_consent_block/requires_opt_in/requires_consent") {
		t.Fatalf("prompt missing typed phase metadata guidance: %q", got)
	}
	if !strings.Contains(got, "## Confirmation Discipline") {
		t.Fatalf("prompt missing confirmation discipline from capability flags: %q", got)
	}
	if !strings.Contains(got, "## Validation Discipline") {
		t.Fatalf("prompt missing validation discipline from capability flags: %q", got)
	}
	if !strings.Contains(got, "## Native File Exploration Discipline") {
		t.Fatalf("prompt missing native file exploration discipline from capability flags: %q", got)
	}
	if !strings.Contains(got, "## Generated Media Delivery") {
		t.Fatalf("prompt missing generated media delivery from capability flags: %q", got)
	}
	if !strings.Contains(got, "## Operation Artifact Delivery") {
		t.Fatalf("prompt missing operation artifact delivery from capability flags: %q", got)
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
	breakpoints := 0
	for i, block := range blocks {
		if block.CacheBreakpoint {
			breakpoints++
			if strings.Contains(block.Text, "## Runtime Awareness") || strings.Contains(block.Text, "## Dynamic Workspace Files") {
				t.Fatalf("volatile block should not be cache breakpoint at %d: %#v", i, block)
			}
		}
	}
	if breakpoints == 0 || breakpoints > maxStableCacheBreakpoints {
		t.Fatalf("cache breakpoints = %d, want 1..%d: %#v", breakpoints, maxStableCacheBreakpoints, blocks)
	}
	awarenessIdx := governorPromptBlockIndexContaining(blocks, "## Runtime Awareness")
	if awarenessIdx < 0 {
		t.Fatalf("missing runtime awareness block: %#v", blocks)
	}
	for i, block := range blocks {
		if block.CacheBreakpoint && i >= awarenessIdx {
			t.Fatalf("cache breakpoint at %d should precede runtime awareness at %d: %#v", i, awarenessIdx, blocks)
		}
	}
	for _, want := range []string{"## Authority", "## Stable Workspace Files"} {
		idx := governorPromptBlockIndexContaining(blocks, want)
		if idx < 0 {
			t.Fatalf("missing block containing %q: %#v", want, blocks)
		}
		if !blocks[idx].CacheBreakpoint {
			t.Fatalf("block %q at %d should be cache breakpoint: %#v", want, idx, blocks)
		}
	}
	manifestIdx := governorPromptBlockIndexContaining(blocks, "## Tool Manifest")
	if manifestIdx < 0 {
		t.Fatalf("missing tool manifest block: %#v", blocks)
	}
	toolContractCached := false
	for i := manifestIdx; i < len(blocks); i++ {
		if strings.Contains(blocks[i].Text, "## Runtime Awareness") || strings.Contains(blocks[i].Text, "## Dynamic Workspace Files") {
			break
		}
		if blocks[i].CacheBreakpoint {
			toolContractCached = true
			break
		}
	}
	if !toolContractCached {
		t.Fatalf("no tool contract cache breakpoint after manifest: %#v", blocks)
	}
	for _, notWant := range []string{"## Runtime Awareness", "## Dynamic Workspace Files"} {
		idx := governorPromptBlockIndexContaining(blocks, notWant)
		if idx >= 0 && blocks[idx].CacheBreakpoint {
			t.Fatalf("block %q at %d should not be cache breakpoint: %#v", notWant, idx, blocks)
		}
	}
}

func governorPromptBlockIndexContaining(blocks []agent.SystemBlock, needle string) int {
	for i, block := range blocks {
		if strings.Contains(block.Text, needle) {
			return i
		}
	}
	return -1
}

func TestBuildGovernorPromptCacheAwareLookbackShapesDynamicFiles(t *testing.T) {
	t.Parallel()

	req := GovernorRequest{
		CacheStrategy: "hybrid",
		CacheLookback: 2,
		Workspace: &workspace.PromptContext{
			Stable: []workspace.LoadedFile{
				{Path: "SOUL.md", Content: "stable authority"},
			},
			Dynamic: []workspace.LoadedFile{
				{Path: "MEMORY.md", Content: "required continuity"},
				{Path: "memory/old.md", Content: "old dynamic"},
				{Path: "memory/middle.md", Content: "middle dynamic"},
				{Path: "memory/recent.md", Content: "recent dynamic"},
				{Path: "memory/latest.md", Content: "latest dynamic"},
			},
		},
	}
	got := BuildGovernorPrompt(req)

	for _, want := range []string{
		"## Authority",
		"## Runtime Awareness",
		"required continuity",
		"recent dynamic",
		"latest dynamic",
		"Cache-aware lookback omitted older dynamic files this turn: memory/old.md, memory/middle.md",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("cache-aware prompt missing %q: %q", want, got)
		}
	}
	for _, omitted := range []string{"old dynamic", "middle dynamic"} {
		if strings.Contains(got, omitted) {
			t.Fatalf("cache-aware prompt includes omitted content %q: %q", omitted, got)
		}
	}

	unshaped := BuildGovernorPrompt(GovernorRequest{
		CacheStrategy: "off",
		CacheLookback: 2,
		Workspace:     req.Workspace,
	})
	if !strings.Contains(unshaped, "old dynamic") || !strings.Contains(unshaped, "middle dynamic") {
		t.Fatalf("cache off should preserve full dynamic prompt: %q", unshaped)
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
	if !strings.Contains(got, "KIND: <status_report|relational|creative|general>") {
		t.Fatalf("prompt missing typed material kind contract: %q", got)
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

func TestBuildFacePromptIncludesAgencyContextPacket(t *testing.T) {
	t.Parallel()

	got := BuildFacePrompt(FaceRequest{
		GovernorName:    DefaultGovernorName,
		FaceName:        "Idolum",
		Channel:         "telegram",
		PrincipalRole:   "admin",
		FloorText:       "No tool ran. Approved next move: ask for a bounded lease.",
		LatestUserInput: "restart it",
		Mode:            "render",
		Runtime: RuntimeAwareness{
			HiddenInputsActive:    true,
			HiddenInputCategories: []string{"semantic_recurrence"},
			ProvenanceSummary:     "prior release conversation",
			ProposalActive:        true,
			ProposalStatus:        "pending",
			ContinuationActive:    true,
			ContinuationStatus:    "pending",
			OperationObjective:    "Prepare a governed release.",
		},
	})

	for _, want := range []string{
		"## Agency Context Packet",
		"- packet_role: face",
		"agency_shape: present conversational ownership inside the governor-authored material boundary",
		"- current_objective: Prepare a governed release.",
		"visibility_boundary: speak as one self to the user",
		"authority_boundary: style, warmth, initiative, desire, and subtext may shape the scene but cannot add actions",
		"hidden_inputs=semantic_recurrence; provenance=prior release conversation",
		"proposal=active; continuation=active",
		"render_affordance: own the approved facts, limits, refusals, commitments, and next moves",
		"principal_role: admin",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("face agency packet missing %q: %q", want, got)
		}
	}
}

func TestBuildFacePromptIncludesRuntimeFactsForAdjudications(t *testing.T) {
	t.Parallel()

	got := BuildFacePrompt(FaceRequest{
		Mode: "repair",
		Adjudications: []core.RuntimeAdjudication{{
			Kind:          "execution_claim",
			Surface:       "final_reply",
			OperatorLabel: "Reply claim repaired",
			VisibleAction: "repair_requested",
			Findings: []core.RuntimeFinding{{
				Kind:           "test_execution",
				ClaimType:      "test_execution",
				EvidenceStatus: "not_observed_in_current_turn",
				Detail:         "test-execution claim has no test-related tool evidence",
			}},
			EvidenceRefs: []string{"tes:turn_seq:12"},
		}},
	})

	for _, want := range []string{
		"## Runtime Facts",
		"structured runtime facts, not required prose",
		"kind=execution_claim",
		"visible_action=repair_requested",
		"test_execution:test-execution claim has no test-related tool evidence",
		"tes:turn_seq:12",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("face prompt missing %q:\n%s", want, got)
		}
	}
}

func TestGovernorRuntimeAwarenessRetainsMediaAndVoiceSignals(t *testing.T) {
	t.Parallel()

	aw := RuntimeAwareness{
		SessionKind:     "interactive",
		RunKind:         "interactive",
		Channel:         "telegram",
		MediaAttached:   true,
		MediaMode:       "voice",
		InboundWasVoice: true,
	}
	governor := renderGovernorRuntimeAwarenessBlock(aw)
	face := renderFaceAwarenessBlock(aw)
	for _, want := range []string{"media_attached: true", "media_mode: voice"} {
		if !strings.Contains(governor, want) {
			t.Fatalf("governor awareness missing %q:\n%s", want, governor)
		}
		if !strings.Contains(face, want) {
			t.Fatalf("face awareness missing shared media signal %q:\n%s", want, face)
		}
	}
	if strings.Contains(governor, "inbound_was_voice") {
		t.Fatalf("governor awareness leaked face-only voice presentation field:\n%s", governor)
	}
	if !strings.Contains(face, "inbound_was_voice: true") {
		t.Fatalf("face awareness missing inbound voice field:\n%s", face)
	}
}

func TestRuntimeAwarenessSharedLinesAreByteIdenticalAcrossRoles(t *testing.T) {
	t.Parallel()

	aw := RuntimeAwareness{
		SessionKind:        "interactive",
		RunKind:            "interactive",
		Channel:            "telegram",
		EventOrigin:        "user",
		ActiveProvider:     "openai:gpt-5.5",
		FallbackActive:     true,
		ArtifactMode:       "floor",
		HiddenInputsActive: true,
		ProvenanceSummary:  "prior status thread",
		PlanActive:         true,
		PlanSummary:        "current plan only",
		OperationActive:    true,
		OperationObjective: "harden awareness",
		OperationStatus:    "active",
		OperationStage:     "tests",
		OperationSummary:   "shared block check",
		MediaAttached:      true,
		MediaMode:          "attachments",
	}

	shared := sharedAwarenessBody(aw)
	governor := renderGovernorRuntimeAwarenessBlock(aw)
	face := renderFaceAwarenessBlock(aw)
	if !strings.Contains(governor, shared) {
		t.Fatalf("governor missing shared lines:\nshared=%s\n\ngovernor=%s", shared, governor)
	}
	if !strings.Contains(face, shared) {
		t.Fatalf("face missing shared lines:\nshared=%s\n\nface=%s", shared, face)
	}
}

func sharedAwarenessBody(aw RuntimeAwareness) string {
	lines := []string{}
	lines = appendAwarenessSection(lines, "Shared Stable Facts", renderSharedStableAwarenessLines(aw))
	lines = appendAwarenessSection(lines, "Shared Turn State", renderSharedTurnAwarenessLines(aw))
	return strings.Join(compactLines(lines), "\n")
}

func TestBuildFacePromptKeepsContinuationAuthorityOutOfDeliveryAwareness(t *testing.T) {
	t.Parallel()

	got := BuildFacePrompt(FaceRequest{
		GovernorName:    DefaultGovernorName,
		FaceName:        "Idolum",
		Channel:         "telegram",
		PrincipalRole:   "admin",
		FloorText:       "Approval is still pending.",
		LatestUserInput: "continue",
		Mode:            "render",
		Runtime: RuntimeAwareness{
			SessionKind:                "interactive",
			OperationObjective:         "Deploy safely",
			ContinuationActive:         true,
			ContinuationStatus:         "pending",
			ContinuationPersonaIntent:  "sound eager",
			ContinuationGovernorIntent: "restart service",
			ContinuationRatified:       false,
			ContinuationBlockedReason:  "stale_authority",
			ProposalBoundedEffect:      "restart aphelion.service",
			PhasePlanCurrentPhaseID:    "deploy-phase",
			MediaAttached:              true,
			MediaMode:                  "attachments",
			ReplyModalityDefault:       "text",
		},
	})
	for _, want := range []string{"## Delivery Awareness", "operation_objective", "media_attached", "media_mode", "reply_modality_default"} {
		if !strings.Contains(got, want) {
			t.Fatalf("face prompt missing %q:\n%s", want, got)
		}
	}
	assertAwarenessKeysAbsent(t, "face prompt", got, awarenessRoleExcludedLineKeys(awarenessRoleFace))
}

func TestRuntimeAwarenessRoleFactoringKeepsAuthorityOutOfFace(t *testing.T) {
	t.Parallel()

	aw := fullRuntimeAwarenessFixture()

	governor := renderGovernorRuntimeAwarenessBlock(aw)
	face := renderFaceAwarenessBlock(aw)

	assertAwarenessKeysPresent(t, "governor", governor, awarenessRoleLineKeys(awarenessRoleGovernor))
	assertAwarenessKeysPresent(t, "face", face, awarenessRoleLineKeys(awarenessRoleFace))
	assertAwarenessKeysAbsent(t, "face", face, awarenessRoleExcludedLineKeys(awarenessRoleFace))
	assertAwarenessKeysAbsent(t, "governor", governor, awarenessRoleExcludedLineKeys(awarenessRoleGovernor))
}

func fullRuntimeAwarenessFixture() RuntimeAwareness {
	return RuntimeAwareness{
		SessionKind:                "interactive",
		RunKind:                    "interactive",
		Channel:                    "telegram",
		EventOrigin:                "user",
		TurnAuthorizationKind:      "admin_dm",
		GovernorBackend:            "native",
		GovernorProvider:           "openai",
		GovernorModel:              "gpt-5.5",
		GovernorProviderPath:       []string{"openai", "anthropic"},
		ActiveProvider:             "openai",
		FallbackActive:             true,
		ReasoningEffort:            "high",
		ReasoningSummary:           "auto",
		GovernorEffortRecipe:       "high",
		ArtifactMode:               "floor",
		BrokerageActive:            true,
		BrokeragePhase:             "proposal",
		SuggestedExecutionContract: "suggested contract",
		BrokerageRatification:      "ratified",
		RatifiedExecutionContract:  "ratified contract",
		SignalJudgment:             "bounded",
		FaceBackend:                "provider",
		FaceProvider:               "anthropic",
		FaceModel:                  "claude",
		PersonaEffortRecipe:        "low",
		DeliveryMode:               "stream",
		StreamReply:                true,
		InboundWasVoice:            true,
		ReplyModalityDefault:       "text",
		ReplyModalityReason:        "voice auto",
		ReplyModalityOverride:      "none",
		MediaAttached:              true,
		MediaMode:                  "floor",
		HiddenInputsActive:         true,
		HiddenInputCategories:      []string{"semantic recurrence"},
		ProvenanceSummary:          "prior work",
		PlanActive:                 true,
		PlanSummary:                "finish performance work",
		PlanEvents:                 []string{"plan updated"},
		OperationActive:            true,
		OperationObjective:         "reduce token cost",
		OperationStatus:            "active",
		OperationStage:             "r3",
		OperationSummary:           "shared awareness factoring",
		OperationDigest:            []string{"tool output compacted"},
		ProposalActive:             true,
		ProposalKind:               "deploy",
		ProposalStatus:             "pending",
		ProposalSummary:            "restart service",
		ProposalWhyNow:             "approval needed",
		ProposalBoundedEffect:      "restart service once",
		PhasePlanActive:            true,
		PhasePlanID:                "phase-plan",
		PhasePlanGoal:              "complete rollout",
		PhasePlanCurrentPhaseID:    "phase-one",
		OperationPhases:            []string{"phase one"},
		OperationFindings:          []string{"authority finding"},
		OperationArtifacts:         []string{"authority artifact"},
		ContinuationStatus:         "pending",
		ContinuationActive:         true,
		ContinuationPersonaIntent:  "speak clearly",
		ContinuationPersonaWhy:     "visible continuity",
		ContinuationGovernorIntent: "continue bounded work",
		ContinuationGovernorWhy:    "approval exists",
		ContinuationRatified:       true,
		ContinuationBlockedReason:  "none",
		PromptRoot:                 "/prompt-root",
		ExecRoot:                   "/exec-root",
		SharedMemoryRoot:           "/shared-memory",
		UserWorkspaceRoot:          "/workspace",
		UserMemoryRoot:             "/user-memory",
		WorkingRoot:                "/working",
		SandboxMode:                "trusted",
		NetworkPolicy:              "allowlist",
	}
}

func assertAwarenessKeysPresent(t *testing.T, label string, text string, keys []string) {
	t.Helper()
	for _, key := range keys {
		token := awarenessLineToken(key)
		if !strings.Contains(text, token) {
			t.Fatalf("%s awareness missing %q:\n%s", label, key, text)
		}
	}
}

func assertAwarenessKeysAbsent(t *testing.T, label string, text string, keys []string) {
	t.Helper()
	for _, key := range keys {
		token := awarenessLineToken(key)
		if strings.Contains(text, token) {
			t.Fatalf("%s awareness leaked %q:\n%s", label, key, text)
		}
	}
}

func awarenessLineToken(key string) string {
	return "- " + strings.TrimSpace(key) + ":"
}
