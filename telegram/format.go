//go:build linux

package telegram

import (
	"html"
	"net/url"
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

	for offset := 0; offset < len(input); {
		line, next, hasNewline := nextTelegramFormatLine(input, offset)
		if body, codeNext, ok := parseFencedCodeBlock(input, offset); ok && markdownFenceLine(line) {
			out.WriteString("<pre><code>")
			out.WriteString(html.EscapeString(body))
			out.WriteString("</code></pre>")
			changed = true
			offset = codeNext
			continue
		}

		if quote, ok := markdownQuoteLine(line); ok {
			quoteLines := []string{quote}
			lastHadNewline := hasNewline
			offset = next
			for offset < len(input) {
				nextLine, nextOffset, nextHasNewline := nextTelegramFormatLine(input, offset)
				nextQuote, nextOK := markdownQuoteLine(nextLine)
				if !nextOK {
					break
				}
				quoteLines = append(quoteLines, nextQuote)
				lastHadNewline = nextHasNewline
				offset = nextOffset
			}
			out.WriteString("<blockquote>")
			for i, quoteLine := range quoteLines {
				if i > 0 {
					out.WriteByte('\n')
				}
				rendered, inlineChanged := renderTelegramInlineHTML(quoteLine)
				if inlineChanged {
					changed = true
				}
				out.WriteString(rendered)
			}
			out.WriteString("</blockquote>")
			if lastHadNewline {
				out.WriteByte('\n')
			}
			changed = true
			continue
		}

		if heading, ok := markdownHeadingLine(line); ok {
			out.WriteString("<b>")
			out.WriteString(html.EscapeString(heading))
			out.WriteString("</b>")
			if hasNewline {
				out.WriteByte('\n')
			}
			changed = true
			offset = next
			continue
		}

		if markdownSeparatorLine(line) {
			if hasNewline {
				out.WriteByte('\n')
			}
			changed = true
			offset = next
			continue
		}

		rendered, inlineChanged := renderTelegramInlineHTML(line)
		if inlineChanged {
			changed = true
		}
		out.WriteString(rendered)
		if hasNewline {
			out.WriteByte('\n')
		}
		offset = next
	}
	return out.String(), changed
}

func nextTelegramFormatLine(input string, offset int) (string, int, bool) {
	if offset >= len(input) {
		return "", len(input), false
	}
	rel := strings.IndexByte(input[offset:], '\n')
	if rel < 0 {
		return input[offset:], len(input), false
	}
	end := offset + rel
	return input[offset:end], end + 1, true
}

func renderTelegramInlineHTML(input string) (string, bool) {
	var out strings.Builder
	changed := false
	for i := 0; i < len(input); {
		switch {
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
		case input[i] == '[':
			body, next, ok := parseMarkdownLink(input, i)
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

func markdownFenceLine(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "```")
}

func markdownHeadingLine(line string) (string, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	count := 0
	for count < len(trimmed) && trimmed[count] == '#' {
		count++
	}
	if count == 0 || count > 6 || count >= len(trimmed) {
		return "", false
	}
	if trimmed[count] != ' ' && trimmed[count] != '\t' {
		return "", false
	}
	title := strings.TrimSpace(trimmed[count:])
	title = trimMarkdownClosingHeadingHashes(title)
	if title == "" {
		return "", false
	}
	return title, true
}

func trimMarkdownClosingHeadingHashes(title string) string {
	title = strings.TrimSpace(title)
	hashStart := len(title)
	for hashStart > 0 && title[hashStart-1] == '#' {
		hashStart--
	}
	if hashStart == len(title) || hashStart == 0 {
		return title
	}
	if title[hashStart-1] != ' ' && title[hashStart-1] != '\t' {
		return title
	}
	return strings.TrimSpace(title[:hashStart])
}

func markdownSeparatorLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 3 {
		return false
	}
	first := trimmed[0]
	if first != '-' && first != '*' && first != '_' {
		return false
	}
	for i := 0; i < len(trimmed); i++ {
		if trimmed[i] != first {
			return false
		}
	}
	return true
}

func markdownQuoteLine(line string) (string, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, ">") {
		return "", false
	}
	body := strings.TrimPrefix(trimmed, ">")
	if strings.HasPrefix(body, " ") {
		body = strings.TrimPrefix(body, " ")
	}
	return body, true
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

func parseMarkdownLink(input string, start int) (string, int, bool) {
	if start >= len(input) || input[start] != '[' {
		return "", start, false
	}
	labelEndRel := strings.IndexByte(input[start+1:], ']')
	if labelEndRel < 0 {
		return "", start, false
	}
	labelEnd := start + 1 + labelEndRel
	if labelEnd+1 >= len(input) || input[labelEnd+1] != '(' {
		return "", start, false
	}
	urlEndRel := strings.IndexByte(input[labelEnd+2:], ')')
	if urlEndRel < 0 {
		return "", start, false
	}
	urlEnd := labelEnd + 2 + urlEndRel
	label := input[start+1 : labelEnd]
	rawURL := input[labelEnd+2 : urlEnd]
	if label == "" || strings.Contains(label, "\n") || strings.Contains(rawURL, "\n") || !safeTelegramLinkURL(rawURL) {
		return "", start, false
	}
	rendered := `<a href="` + html.EscapeString(strings.TrimSpace(rawURL)) + `">` + html.EscapeString(label) + `</a>`
	return rendered, urlEnd + 1, true
}

func safeTelegramLinkURL(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "mailto":
		return true
	default:
		return false
	}
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
