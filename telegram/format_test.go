//go:build linux

package telegram

import "testing"

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
