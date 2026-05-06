//go:build linux

package telegram

import (
	"strings"
	"testing"
)

func TestRenderTelegramHTMLSubsetPreservesUTF8Runes(t *testing.T) {
	rendered, changed := renderTelegramHTMLSubset("plain — dash and *this*")
	if !changed {
		t.Fatal("changed = false, want true")
	}
	want := "plain — dash and <i>this</i>"
	if rendered != want {
		t.Fatalf("rendered = %q, want %q", rendered, want)
	}
}

func TestPrepareFormattedTextPreservesUTF8Runes(t *testing.T) {
	formatted := prepareFormattedText("`ok` — still fine", "")
	if formatted.ParseMode != ParseModeHTML {
		t.Fatalf("parse mode = %q, want %q", formatted.ParseMode, ParseModeHTML)
	}
	want := "<code>ok</code> — still fine"
	if formatted.Text != want {
		t.Fatalf("text = %q, want %q", formatted.Text, want)
	}
	if formatted.PlainText != "`ok` — still fine" {
		t.Fatalf("plain text = %q, want original input", formatted.PlainText)
	}
}

func TestRenderTelegramHTMLSubsetFormatsOperatorMarkdown(t *testing.T) {
	input := strings.Join([]string{
		"Yes.",
		"",
		"### 1. Peirce / Bateson / Spencer-Brown: the cut before the symbol",
		"",
		"That matters for LLM testing: not “did the model notice the formatting?”",
		"",
		"---",
		"",
		"> What does this symbol denote?",
		"> And what does it permit?",
		"",
		"- God behind temple/law",
		"- system prompt behind chat",
		"",
		"Read [Telegram docs](https://core.telegram.org/bots/api#formatting-options).",
	}, "\n")

	rendered, changed := renderTelegramHTMLSubset(input)
	if !changed {
		t.Fatal("changed = false, want heading/quote/link rendering")
	}
	for _, want := range []string{
		"<b>1. Peirce / Bateson / Spencer-Brown: the cut before the symbol</b>",
		"<blockquote>What does this symbol denote?\nAnd what does it permit?</blockquote>",
		"- God behind temple/law",
		`Read <a href="https://core.telegram.org/bots/api#formatting-options">Telegram docs</a>.`,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered = %q, want substring %q", rendered, want)
		}
	}
	if strings.Contains(rendered, "###") || strings.Contains(rendered, "---") || strings.Contains(rendered, "&gt; What") {
		t.Fatalf("rendered = %q, still contains raw Markdown structure", rendered)
	}
}

func TestRenderTelegramHTMLSubsetLeavesNonMarkdownTextPlain(t *testing.T) {
	rendered, changed := renderTelegramHTMLSubset("Issue #39 is not a heading\n2 - 1 is not a rule\nUse > only in prose")
	if changed {
		t.Fatalf("changed = true, want non-Markdown prose left plain: %q", rendered)
	}
}

func TestRenderTelegramHTMLSubsetEscapesUnclosedMarkdownInsideRenderedBlock(t *testing.T) {
	rendered, changed := renderTelegramHTMLSubset("### Title with *unclosed <tag>")
	if !changed {
		t.Fatal("changed = false, want heading rendering")
	}
	want := "<b>Title with *unclosed &lt;tag&gt;</b>"
	if rendered != want {
		t.Fatalf("rendered = %q, want %q", rendered, want)
	}
}

func TestRenderTelegramHTMLSubsetRejectsUnsafeMarkdownLinks(t *testing.T) {
	rendered, changed := renderTelegramHTMLSubset("Open [bad](javascript:alert(1))")
	if changed {
		t.Fatalf("changed = true, want unsafe link left as plain Markdown: %q", rendered)
	}

	rendered, changed = renderTelegramHTMLSubset("### Open [bad](javascript:alert(1))")
	if !changed {
		t.Fatal("changed = false, want heading rendering")
	}
	want := "<b>Open [bad](javascript:alert(1))</b>"
	if rendered != want {
		t.Fatalf("rendered = %q, want %q", rendered, want)
	}
}

func TestRenderTelegramHTMLSubsetPreservesCodeFenceBody(t *testing.T) {
	input := "```go\nfmt.Println(\"<ok>\")\n```\nDone."
	rendered, changed := renderTelegramHTMLSubset(input)
	if !changed {
		t.Fatal("changed = false, want code fence rendering")
	}
	want := "<pre><code>fmt.Println(&#34;&lt;ok&gt;&#34;)\n</code></pre>\nDone."
	if rendered != want {
		t.Fatalf("rendered = %q, want %q", rendered, want)
	}
}
