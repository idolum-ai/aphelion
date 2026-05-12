//go:build linux

package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/workspace"
)

func TestAgencyPromptGoldens(t *testing.T) {
	cases := []struct {
		name  string
		build func() string
		order []string
	}{
		{
			name:  "governor_execution",
			build: goldenGovernorExecutionPrompt,
			order: []string{
				"## Authority",
				"## Runtime Awareness",
				"## Agency Context Packet",
				"## Evidence Retrieval And Stop Rules",
				"## Stable Workspace Files",
				"## Tool Manifest",
				"## Dynamic Workspace Files",
			},
		},
		{
			name:  "face_render",
			build: goldenFaceRenderPrompt,
			order: []string{
				"## Delivery Awareness",
				"## Agency Context Packet",
				"## Agency And Telos",
				"## Stable Face Files",
				"## Dynamic Face Files",
				"## Execution Facts",
				"## Latest User Message",
				"## Channel Context",
			},
		},
		{
			name:  "face_proposal",
			build: goldenFaceProposalPrompt,
			order: []string{
				"## Delivery Awareness",
				"## Agency Context Packet",
				"## Agency And Telos",
				"## Stable Face Files",
				"## Dynamic Face Files",
				"## Latest User Message",
				"## Channel Context",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeGoldenPrompt(tc.build())
			assertGoldenOrder(t, got, tc.order)

			path := filepath.Join("testdata", "golden", tc.name+".golden")
			if os.Getenv("APHELION_UPDATE_GOLDENS") == "1" {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatalf("create golden dir: %v", err)
				}
				if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
					t.Fatalf("write golden %s: %v", path, err)
				}
			}

			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s: %v", path, err)
			}
			if got != string(want) {
				t.Fatalf("golden mismatch for %s; run APHELION_UPDATE_GOLDENS=1 go test ./prompt -run TestAgencyPromptGoldens", path)
			}
		})
	}
}

func goldenGovernorExecutionPrompt() string {
	return BuildGovernorPrompt(GovernorRequest{
		GovernorName:    DefaultGovernorName,
		GovernorBackend: "native",
		PrincipalRole:   "admin",
		WorkspaceRoot:   "$WORKSPACE",
		ToolManifest: strings.Join([]string{
			"tools:",
			"- exec: shell execution",
			"- update_plan: visible plan updates",
			"- update_operation: durable operation updates",
			"- capability_request: request bounded authority",
		}, "\n"),
		Workspace: &workspace.PromptContext{
			Stable: []workspace.LoadedFile{
				{Path: "SOUL.md", Content: "Hold the machine-owned floor."},
				{Path: "AGENTS.md", Content: "Governor owns authority; face owns user-visible presentation."},
			},
			Dynamic: []workspace.LoadedFile{
				{Path: "MEMORY.md", Content: "Recent thread: agency must remain bounded by typed authority."},
			},
		},
		Runtime: RuntimeAwareness{
			SessionKind:           "interactive",
			RunKind:               "interactive",
			Channel:               "telegram",
			EventOrigin:           "message",
			TurnAuthorizationKind: "admin_dm",
			GovernorProvider:      "openai",
			GovernorModel:         "gpt-5.5",
			GovernorProviderPath:  []string{"openai", "anthropic"},
			ActiveProvider:        "openai",
			ReasoningEffort:       "high",
			ReasoningSummary:      "auto",
			HiddenInputsActive:    true,
			HiddenInputCategories: []string{"semantic_recurrence"},
			ProvenanceSummary:     "similar release-hardening thread",
			PlanActive:            true,
			PlanSummary:           "Harden agency prompts invisibly.",
			OperationActive:       true,
			OperationObjective:    "Complete agency quality hardening.",
			OperationStatus:       "active",
			ProposalActive:        true,
			ProposalStatus:        "pending",
			SandboxMode:           "trusted",
			NetworkPolicy:         "allowlist",
		},
	})
}

func goldenFaceRenderPrompt() string {
	return BuildFacePrompt(FaceRequest{
		GovernorName:    DefaultGovernorName,
		FaceName:        "Idolum",
		Channel:         "telegram",
		PrincipalRole:   "admin",
		LatestUserInput: "What changed?",
		MaterialFloor: core.MaterialPacket{
			Facts:          []string{"The agency packet was added to the prompt boundary."},
			Commitments:    []string{"Keep visible replies inside approved facts."},
			AllowedActions: []string{"Offer the next bounded validation step."},
		},
		StableFiles: []workspace.LoadedFile{
			{Path: "IDOLUM.md", Content: "Speak as one present self without exposing machinery."},
		},
		DynamicFiles: []workspace.LoadedFile{
			{Path: "QUESTIONS-TO-IDOLUM.md", Content: "Catch invisible authority drift."},
		},
		Runtime: RuntimeAwareness{
			SessionKind:           "interactive",
			RunKind:               "interactive",
			Channel:               "telegram",
			TurnAuthorizationKind: "admin_dm",
			FaceBackend:           "provider",
			FaceProvider:          "openai",
			FaceModel:             "gpt-5.5",
			DeliveryMode:          "stream",
			StreamReply:           true,
			OperationObjective:    "Complete agency quality hardening.",
			ProposalActive:        true,
			ProposalStatus:        "pending",
		},
	})
}

func goldenFaceProposalPrompt() string {
	return BuildFacePrompt(FaceRequest{
		GovernorName:    DefaultGovernorName,
		FaceName:        "Idolum",
		Channel:         "telegram",
		Mode:            "proposal",
		PrincipalRole:   "admin",
		LatestUserInput: "Can we make the system more alive without making it fragile?",
		StableFiles: []workspace.LoadedFile{
			{Path: "IDOLUM.md", Content: "High agency must still respect the approved floor."},
		},
		DynamicFiles: []workspace.LoadedFile{
			{Path: "memory/dreams.md", Content: "Long-horizon desires may become bounded proposals."},
		},
		Runtime: RuntimeAwareness{
			SessionKind:           "interactive",
			RunKind:               "interactive",
			Channel:               "telegram",
			TurnAuthorizationKind: "admin_dm",
			HiddenInputsActive:    true,
			HiddenInputCategories: []string{"semantic_recurrence"},
			ProvenanceSummary:     "prior agency analysis",
			OperationObjective:    "Negotiate the next safe agency improvement.",
			ContinuationActive:    true,
			ContinuationStatus:    "pending",
		},
	})
}

func normalizeGoldenPrompt(raw string) string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		raw = strings.ReplaceAll(raw, home, "$HOME")
	}
	raw = strings.ReplaceAll(raw, "/tmp/aphelion-golden", "$TMP")
	return strings.TrimSpace(raw) + "\n"
}

func assertGoldenOrder(t *testing.T, text string, sections []string) {
	t.Helper()
	last := -1
	for _, section := range sections {
		idx := strings.Index(text, section)
		if idx < 0 {
			t.Fatalf("prompt missing section %q", section)
		}
		if idx <= last {
			t.Fatalf("section %q is out of order", section)
		}
		last = idx
	}
}
