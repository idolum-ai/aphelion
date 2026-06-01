//go:build linux

package pipeline

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/prompt"
)

func TestProtocolBoundaryRejectsRuntimeOrchestrationImports(t *testing.T) {
	t.Parallel()

	forbidden := []string{
		"github.com/idolum-ai/aphelion/runtime",
		"github.com/idolum-ai/aphelion/turn",
		"github.com/idolum-ai/aphelion/session",
		"github.com/idolum-ai/aphelion/tool/sandbox",
		"github.com/idolum-ai/aphelion/face",
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir pipeline package: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(".", name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse imports for %s: %v", name, err)
		}
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, "\"")
			for _, forbiddenPath := range forbidden {
				if path == forbiddenPath || strings.HasPrefix(path, forbiddenPath+"/") {
					t.Fatalf("pipeline import boundary violation: %s imports %s", name, path)
				}
			}
		}
	}
}

func TestMaterialFloorProtocolInvariants(t *testing.T) {
	t.Parallel()

	packet, floorText, structured := BuildFloorFromGovernor(strings.Join([]string{
		"FACTS:",
		"- The file was inspected.",
		"ALLOWED_ACTIONS:",
		"- Report the grounded finding.",
		"SCENE_CONSTRAINTS:",
		"- Do not leak this to degraded delivery.",
	}, "\n"), true)
	if !structured {
		t.Fatal("structured = false, want material contract packet")
	}
	if !strings.Contains(floorText, "SCENE_CONSTRAINTS") {
		t.Fatalf("floorText = %q, want full internal material floor including scene constraints", floorText)
	}

	fallback := SerializeFloorFallback(packet, floorText, FallbackOptions{Channel: "telegram"})
	if strings.Contains(fallback, "Do not leak this to degraded delivery") || strings.Contains(fallback, "SCENE_CONSTRAINTS") {
		t.Fatalf("fallback leaked scene constraints: %q", fallback)
	}
	if !strings.Contains(fallback, "What matters:") || !strings.Contains(fallback, "Next:") {
		t.Fatalf("fallback = %q, want public facts/actions sections", fallback)
	}

	plainPacket, plainFloor, plainStructured := BuildFloorFromGovernor("plain governor floor", true)
	if plainStructured {
		t.Fatal("plainStructured = true, want fail-closed plain-text packet")
	}
	if got := SerializeFloorFallback(plainPacket, plainFloor, FallbackOptions{Channel: "telegram"}); got != "plain governor floor" {
		t.Fatalf("plain fallback = %q, want canonical plain floor", got)
	}
}

func TestBrokerageProtocolRequiresCompleteRatification(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		text string
	}{
		{
			name: "missing answer directive",
			text: "INSPECT: yes\nQUESTION: no\nRATIFICATION: accept\nPLAN:\n- inspect first",
		},
		{
			name: "missing disposition",
			text: "INSPECT: yes\nQUESTION: no\nANSWER: yes\nPLAN:\n- inspect first",
		},
		{
			name: "missing steps",
			text: "INSPECT: yes\nQUESTION: no\nANSWER: yes\nRATIFICATION: accept",
		},
		{
			name: "json missing plan",
			text: `{"contract":{"inspect":true,"question":false,"answer":true},"ratification":"accept"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseBrokerageRatification(tc.text); err == nil {
				t.Fatal("ParseBrokerageRatification() err = nil, want fail-closed incomplete contract")
			}
		})
	}

	if got := ParseExecutionContract("INSPECT: yes\nQUESTION: no"); got != nil {
		t.Fatalf("ParseExecutionContract() = %#v, want nil for partial contract", got)
	}
}

func TestRenderPolicyProtocolInvariants(t *testing.T) {
	t.Parallel()

	policy := FacePolicy{Proposal: true, Render: true}
	cases := []struct {
		name  string
		input RenderDecisionInput
		want  bool
	}{
		{name: "empty user", input: RenderDecisionInput{UserText: "", FloorText: "floor"}, want: false},
		{name: "slash command", input: RenderDecisionInput{UserText: "/doctor", FloorText: "floor"}, want: false},
		{name: "no response without tool context", input: RenderDecisionInput{UserText: "hello", FloorText: "(no response)"}, want: false},
		{name: "no response with tool log", input: RenderDecisionInput{UserText: "hello", FloorText: "(no response)", ToolLog: []string{"go test ./pipeline"}}, want: true},
		{name: "ordinary floor", input: RenderDecisionInput{UserText: "hello", FloorText: "grounded floor"}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ShouldRenderInteractiveIdolumReply(policy, tc.input); got != tc.want {
				t.Fatalf("ShouldRenderInteractiveIdolumReply() = %v, want %v", got, tc.want)
			}
		})
	}
	if got := DecideInteractiveFacePolicy("/status"); got != (FacePolicy{}) {
		t.Fatalf("DecideInteractiveFacePolicy(/status) = %#v, want empty policy", got)
	}
}

func TestRepairProtocolNormalizesDeliveryAndMediaState(t *testing.T) {
	t.Parallel()

	contract, ok := BuildRepairContract(RepairContract{
		Channel:       " telegram ",
		PrincipalRole: " admin ",
		UserText:      " user ",
		Candidate:     " draft ",
		FloorText:     " floor ",
		Runtime: prompt.RuntimeAwareness{
			DeliveryMode:  "idolum_render",
			StreamReply:   true,
			MediaAttached: false,
			MediaMode:     "stale",
		},
		Adjudications: []core.RuntimeAdjudication{{Kind: "  execution_claim  ", Findings: []core.RuntimeFinding{{Kind: "  execution_claim_ungrounded  ", Detail: " unsupported "}}}},
		MediaCount:    2,
	}, []ConstitutionViolation{{Rule: RuleMediaReplyContradiction, Detail: "reply contradicts delivered media"}})
	if !ok {
		t.Fatal("BuildRepairContract() ok = false, want repair contract")
	}
	if contract.Channel != "telegram" || contract.PrincipalRole != "admin" || contract.UserText != "user" || contract.Candidate != "draft" || contract.FloorText != "floor" {
		t.Fatalf("contract string fields not normalized: %#v", contract)
	}
	if contract.Runtime.DeliveryMode != "constitution_repair" {
		t.Fatalf("Runtime.DeliveryMode = %q, want constitution_repair", contract.Runtime.DeliveryMode)
	}
	if contract.Runtime.StreamReply {
		t.Fatal("Runtime.StreamReply = true, want false during repair")
	}
	if !contract.Runtime.MediaAttached || contract.Runtime.MediaMode != "attachments" {
		t.Fatalf("media runtime = attached:%v mode:%q, want attachments", contract.Runtime.MediaAttached, contract.Runtime.MediaMode)
	}
	if len(contract.Violations) != 1 || contract.Violations[0] != "reply contradicts delivered media" {
		t.Fatalf("Violations = %#v, want normalized repair note", contract.Violations)
	}
	if len(contract.Adjudications) != 1 || contract.Adjudications[0].Kind != "execution_claim" {
		t.Fatalf("Adjudications = %#v, want normalized adjudication kind", contract.Adjudications)
	}
	if len(contract.Adjudications[0].Findings) != 1 || contract.Adjudications[0].Findings[0].Kind != "execution_claim_ungrounded" || contract.Adjudications[0].Findings[0].Detail != "unsupported" {
		t.Fatalf("Adjudication findings = %#v, want normalized finding", contract.Adjudications[0].Findings)
	}
}
