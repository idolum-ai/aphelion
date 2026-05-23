//go:build linux

package telegrampresentation

import (
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestDisplaySlotOriginDetailRoundTrip(t *testing.T) {
	t.Parallel()

	detail := OriginDetailForDisplaySlot(7)
	if detail != "thread_display:7" {
		t.Fatalf("OriginDetailForDisplaySlot() = %q, want thread_display:7", detail)
	}
	slot, ok := DisplaySlotFromOriginDetail("  " + detail + "  ")
	if !ok || slot != 7 {
		t.Fatalf("DisplaySlotFromOriginDetail() = %d/%v, want 7/true", slot, ok)
	}
	if slot, ok := DisplaySlotFromOriginDetail("thread_display:nope"); ok || slot != 0 {
		t.Fatalf("DisplaySlotFromOriginDetail(invalid) = %d/%v, want 0/false", slot, ok)
	}
}

func TestLabelForThreadUsesDisplaySlotOnlyForOpenThreads(t *testing.T) {
	t.Parallel()

	open := session.TelegramThread{ThreadID: 42, DisplaySlot: 1, Status: session.TelegramThreadStatusOpen}
	if got := LabelForThread(open, 42); got != "1" {
		t.Fatalf("LabelForThread(open) = %q, want display slot", got)
	}
	closed := session.TelegramThread{ThreadID: 42, DisplaySlot: 1, Status: session.TelegramThreadStatusClosed, ArchivedDisplayName: "archive-a"}
	if got := LabelForThread(closed, 42); got != "archive-a" {
		t.Fatalf("LabelForThread(closed) = %q, want archived name", got)
	}
}

func TestFallbackPresentationIsDisplayOnlyUnresolved(t *testing.T) {
	t.Parallel()

	p := FallbackPresentation(99, 42)
	if p.ThreadID != 42 || p.Label != "unresolved" || p.Prefix != "(thread unresolved)" {
		t.Fatalf("FallbackPresentation() = %#v, want unresolved label retaining durable id only structurally", p)
	}
}

func TestPrefixForMessageUsesCallbackDisplayMetadata(t *testing.T) {
	t.Parallel()

	msg := core.InboundMessage{TelegramThreadID: 42, OriginDetail: OriginDetailForDisplaySlot(1)}
	if got := PrefixForMessage(msg); got != "(thread 1)\n\n" {
		t.Fatalf("PrefixForMessage() = %q, want display slot prefix", got)
	}
}
