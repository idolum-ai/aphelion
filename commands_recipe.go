//go:build linux

package main

import (
	"strings"

	"github.com/idolum-ai/aphelion/telegram"
)

const recipeCallbackPrefix = "recipe:"

func recipeSelectorRows(kind string, current string, options []string, labelFor func(string) string) [][]telegram.InlineButton {
	rows := make([][]telegram.InlineButton, 0, 2)
	row := make([]telegram.InlineButton, 0, 2)
	current = strings.ToLower(strings.TrimSpace(current))
	for _, option := range options {
		option = strings.TrimSpace(option)
		if option == "" {
			continue
		}
		label := labelFor(option)
		if strings.EqualFold(option, current) {
			label = "• " + label
		}
		row = append(row, telegram.InlineButton{
			Text:         label,
			CallbackData: encodeRecipeCallbackData(kind, option),
		})
		if len(row) == 2 {
			rows = append(rows, row)
			row = make([]telegram.InlineButton, 0, 2)
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return rows
}

func encodeRecipeCallbackData(kind string, value string) string {
	return recipeCallbackPrefix + strings.TrimSpace(kind) + ":" + strings.TrimSpace(value)
}

func decodeRecipeCallbackData(data string) (kind string, value string, ok bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, recipeCallbackPrefix) {
		return "", "", false
	}
	parts := strings.SplitN(trimmed, ":", 3)
	if len(parts) != 3 {
		return "", "", false
	}
	kind = strings.TrimSpace(parts[1])
	value = strings.TrimSpace(parts[2])
	if kind == "" || value == "" {
		return "", "", false
	}
	return kind, value, true
}

func personaModelButtonLabel(model string) string {
	trimmed := strings.TrimSpace(model)
	switch strings.ToLower(trimmed) {
	case "claude-opus-4-7":
		return "Opus 4.7"
	case "claude-opus-4-6":
		return "Opus 4.6"
	case "claude-sonnet-4-6":
		return "Sonnet 4.6"
	default:
		return trimmed
	}
}

func governorEffortButtonLabel(effort string) string {
	return strings.ToUpper(strings.TrimSpace(effort))
}
