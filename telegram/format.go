//go:build linux

package telegram

import (
	"html"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	ParseModeHTML       = "HTML"
	ParseModeMarkdownV2 = "MarkdownV2"
)

type formattedText struct {
	Text      string
	ParseMode string
	PlainText string
}

func prepareFormattedText(input string, requestedParseMode string) formattedText {
	plain := strings.ReplaceAll(input, "\r\n", "\n")
	switch strings.TrimSpace(requestedParseMode) {
	case ParseModeHTML, ParseModeMarkdownV2:
		return formattedText{
			Text:      plain,
			ParseMode: requestedParseMode,
			PlainText: plain,
		}
	default:
		rendered, changed := renderTelegramHTMLSubset(plain)
		if !changed {
			return formattedText{Text: plain, PlainText: plain}
		}
		return formattedText{
			Text:      rendered,
			ParseMode: ParseModeHTML,
			PlainText: plain,
		}
	}
}

func renderTelegramHTMLSubset(input string) (string, bool) {
	input = strings.ReplaceAll(input, "\r\n", "\n")
	if input == "" {
		return "", false
	}
	var out strings.Builder
	changed := false
	for i := 0; i < len(input); {
		switch {
		case strings.HasPrefix(input[i:], "```"):
			body, next, ok := parseFencedCodeBlock(input, i)
			if !ok {
				r, size := utf8.DecodeRuneInString(input[i:])
				out.WriteString(html.EscapeString(string(r)))
				i += size
				continue
			}
			out.WriteString("<pre><code>")
			out.WriteString(html.EscapeString(body))
			out.WriteString("</code></pre>")
			changed = true
			i = next
		case strings.HasPrefix(input[i:], "`"):
			body, next, ok := parseInlineSpan(input, i, "`", func(content string) string {
				return "<code>" + html.EscapeString(content) + "</code>"
			})
			if !ok {
				r, size := utf8.DecodeRuneInString(input[i:])
				out.WriteString(html.EscapeString(string(r)))
				i += size
				continue
			}
			out.WriteString(body)
			changed = true
			i = next
		case strings.HasPrefix(input[i:], "**"):
			body, next, ok := parseInlineSpan(input, i, "**", func(content string) string {
				return "<b>" + html.EscapeString(content) + "</b>"
			})
			if !ok {
				r, size := utf8.DecodeRuneInString(input[i:])
				out.WriteString(html.EscapeString(string(r)))
				i += size
				continue
			}
			out.WriteString(body)
			changed = true
			i = next
		case input[i] == '*' && canStartItalic(input, i):
			body, next, ok := parseInlineSpan(input, i, "*", func(content string) string {
				return "<i>" + html.EscapeString(content) + "</i>"
			})
			if !ok {
				r, size := utf8.DecodeRuneInString(input[i:])
				out.WriteString(html.EscapeString(string(r)))
				i += size
				continue
			}
			out.WriteString(body)
			changed = true
			i = next
		default:
			r, size := utf8.DecodeRuneInString(input[i:])
			out.WriteString(html.EscapeString(string(r)))
			i += size
		}
	}
	return out.String(), changed
}

func parseFencedCodeBlock(input string, start int) (string, int, bool) {
	if !strings.HasPrefix(input[start:], "```") {
		return "", start, false
	}
	openEnd := start + 3
	closeRel := strings.Index(input[openEnd:], "```")
	if closeRel < 0 {
		return "", start, false
	}
	closeStart := openEnd + closeRel
	body := input[openEnd:closeStart]
	body = strings.TrimPrefix(body, "\n")
	if idx := strings.IndexByte(body, '\n'); idx >= 0 {
		first := strings.TrimSpace(body[:idx])
		if isLikelyFenceLanguage(first) {
			body = body[idx+1:]
		}
	}
	return body, closeStart + 3, true
}

func isLikelyFenceLanguage(line string) bool {
	if line == "" {
		return false
	}
	for _, r := range line {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '+' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func parseInlineSpan(input string, start int, delim string, render func(string) string) (string, int, bool) {
	if !strings.HasPrefix(input[start:], delim) {
		return "", start, false
	}
	searchFrom := start + len(delim)
	closeRel := strings.Index(input[searchFrom:], delim)
	if closeRel < 0 {
		return "", start, false
	}
	closeStart := searchFrom + closeRel
	content := input[searchFrom:closeStart]
	if content == "" || strings.Contains(content, "\n") {
		return "", start, false
	}
	return render(content), closeStart + len(delim), true
}

func canStartItalic(input string, idx int) bool {
	if idx+1 < len(input) && input[idx+1] == '*' {
		return false
	}
	if idx > 0 {
		prev, _ := utf8.DecodeLastRuneInString(input[:idx])
		if unicode.IsLetter(prev) || unicode.IsDigit(prev) {
			return false
		}
	}
	return true
}

func isTelegramParseError(description string) bool {
	value := strings.ToLower(strings.TrimSpace(description))
	return strings.Contains(value, "can't parse entities") ||
		strings.Contains(value, "parse entities") ||
		strings.Contains(value, "parse mode") ||
		strings.Contains(value, "entity end")
}

func isTelegramMessageNotModified(description string) bool {
	value := strings.ToLower(strings.TrimSpace(description))
	return strings.Contains(value, "message is not modified") || strings.Contains(value, "not modified")
}

func IsStaleCallbackQueryError(err error) bool {
	if err == nil {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(err.Error()))
	if !strings.Contains(value, "answercallbackquery") {
		return false
	}
	return strings.Contains(value, "query is too old") ||
		strings.Contains(value, "response timeout expired") ||
		strings.Contains(value, "query id is invalid")
}
