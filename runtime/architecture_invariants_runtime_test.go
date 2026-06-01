//go:build linux

package runtime

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestInvariantFloorSceneSplitPersistsSceneAndFloorSeparately(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = strings.TrimSpace(`FACTS:
- governor fact

ALLOWED_ACTIONS:
- summarize the findings`)
	provider.faceReplyText = "Here is the summary you asked for."

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     4201,
		SenderID:   1001,
		SenderName: "daniel",
		Text:       "summarize what matters",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 4201, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if len(sess.Messages) != 2 {
		t.Fatalf("session messages = %d, want 2", len(sess.Messages))
	}
	assistant := sess.Messages[1]
	if assistant.Role != "assistant" {
		t.Fatalf("assistant role = %q, want assistant", assistant.Role)
	}
	if assistant.Content != "Here is the summary you asked for." {
		t.Fatalf("assistant content = %q, want rendered scene text", assistant.Content)
	}
	if !strings.Contains(assistant.FloorContent, "FACTS:") || !strings.Contains(assistant.FloorContent, "governor fact") {
		t.Fatalf("assistant floor content = %q, want structured floor material", assistant.FloorContent)
	}
	if assistant.Content == assistant.FloorContent {
		t.Fatalf("assistant content and floor should differ, both are %q", assistant.Content)
	}
}

func TestInvariantPersistBeforeDeliverWhenSendFails(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = strings.TrimSpace(`FACTS:
- governor fact on failed send`)
	provider.faceReplyText = "Visible scene should still persist."
	sender.sendErr = errors.New("send failed")

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     4202,
		SenderID:   1001,
		SenderName: "daniel",
		Text:       "trigger send failure",
		MessageID:  2,
	})
	if err == nil {
		t.Fatal("HandleInbound() err = nil, want send failure")
	}
	if !strings.Contains(err.Error(), "send outbound reply") {
		t.Fatalf("HandleInbound() err = %v, want send outbound reply error", err)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 4202, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if sess.TurnCount != 1 {
		t.Fatalf("turn count = %d, want 1 persisted turn", sess.TurnCount)
	}
	if len(sess.Messages) != 2 {
		t.Fatalf("session messages = %d, want 2", len(sess.Messages))
	}
	assistant := sess.Messages[1]
	if assistant.Content != "Visible scene should still persist." {
		t.Fatalf("assistant content = %q, want persisted rendered scene", assistant.Content)
	}
	if !strings.Contains(assistant.FloorContent, "governor fact on failed send") {
		t.Fatalf("assistant floor content = %q, want persisted floor sidecar", assistant.FloorContent)
	}
	outboundIDs, err := store.OutboundAfterTurn(session.SessionKey{ChatID: 4202, UserID: 0}, 0)
	if err != nil {
		t.Fatalf("OutboundAfterTurn() err = %v", err)
	}
	if len(outboundIDs) != 0 {
		t.Fatalf("outbound ids = %#v, want empty on failed delivery", outboundIDs)
	}
}

func TestInvariantRuntimeReadmeDefinesShellBoundaryContract(t *testing.T) {
	t.Parallel()

	readme := readRuntimeInvariantFile(t, "README.md")
	for _, want := range []string{
		"## Shell Boundary Contract",
		"## Top-Level Growth Rules",
		"## Extraction Criteria",
		"## Current Subsystem Map",
		"Top-level `runtime` owns behavior only when that behavior needs direct access to",
		"Runtime must also not take ownership of one-turn stage order.",
		"A top-level file is acceptable when it is an adapter from live runtime facts into",
		"A subsystem is ready for extraction when most of these are true:",
	} {
		assertRuntimeInvariantContains(t, readme, want)
	}
}

func TestInvariantRuntimeReadmeMapsKnownLeafPackages(t *testing.T) {
	t.Parallel()

	readme := readRuntimeInvariantFile(t, "README.md")
	for _, want := range []string{
		"## Leaf Packages",
		"`runtime/codex`: bounded Codex app-server helper package",
		"Top-level `runtime` still owns durable-agent wake wiring, executor",
		"`runtime/doctor`: bounded `/doctor` diagnostics package",
		"Top-level `runtime` still owns command admission, principal resolution",
		"`runtime/mission`: bounded Mission Ledger helper package",
		"Top-level `runtime` still owns hidden-input assembly, transport callback integration",
	} {
		assertRuntimeInvariantContains(t, readme, want)
	}
}

func TestInvariantRuntimeLeafPackageDocsDeclareOwnershipAndStopBoundary(t *testing.T) {
	t.Parallel()

	cases := []struct {
		path string
		owns string
		stop string
	}{
		{path: "codex/doc.go", owns: "Package codex owns", stop: "must stay a leaf under the"},
		{path: "doctor/doc.go", owns: "Package doctor owns", stop: "must not import runtime orchestration"},
		{path: "mission/doc.go", owns: "Package mission owns", stop: "must not own leases"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			doc := readRuntimeInvariantFile(t, tc.path)
			assertRuntimeInvariantContains(t, doc, tc.owns)
			assertRuntimeInvariantContains(t, doc, tc.stop)
		})
	}
}

func readRuntimeInvariantFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) err = %v", path, err)
	}
	return string(content)
}

func assertRuntimeInvariantContains(t *testing.T, content string, want string) {
	t.Helper()
	if strings.Contains(content, want) {
		return
	}
	if strings.Contains(strings.Join(strings.Fields(content), " "), strings.Join(strings.Fields(want), " ")) {
		return
	}
	t.Fatalf("content missing %q", want)
}
