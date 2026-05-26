//go:build linux

package telegramcommands

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/telegram"
)

const durableAgentsCallbackPrefix = "agents:"

type durableAgentsCallbackAction string

const (
	durableAgentsCallbackRefresh       durableAgentsCallbackAction = "refresh"
	durableAgentsCallbackAnalyze       durableAgentsCallbackAction = "analyze"
	durableAgentsCallbackDetail        durableAgentsCallbackAction = "detail"
	durableAgentsCallbackBack          durableAgentsCallbackAction = "back"
	durableAgentsCallbackBrief         durableAgentsCallbackAction = "brief"
	durableAgentsCallbackPark          durableAgentsCallbackAction = "park"
	durableAgentsCallbackResume        durableAgentsCallbackAction = "resume"
	durableAgentsCallbackRetireAsk     durableAgentsCallbackAction = "retire_ask"
	durableAgentsCallbackRetireConfirm durableAgentsCallbackAction = "retire_confirm"
	durableAgentsCallbackRetireBrief   durableAgentsCallbackAction = "retire_brief"
	durableAgentsCallbackRetireCancel  durableAgentsCallbackAction = "retire_cancel"
)

type durableAgentsCallbackRequest struct {
	Action durableAgentsCallbackAction
	View   string
	Page   int
	Token  string
}

func encodeDurableAgentsRefreshCallbackData(view string, page int) string {
	return encodeDurableAgentsCallback(durableAgentsCallbackRefresh, view, page, "")
}

func encodeDurableAgentsAnalyzeCallbackData() string {
	return durableAgentsCallbackPrefix + string(durableAgentsCallbackAnalyze)
}

func encodeDurableAgentsDetailCallbackData(agentID string, view string, page int) string {
	return encodeDurableAgentsCallback(durableAgentsCallbackDetail, view, page, durableAgentsCallbackToken(agentID))
}

func encodeDurableAgentsBackCallbackData(view string, page int) string {
	return encodeDurableAgentsCallback(durableAgentsCallbackBack, view, page, "")
}

func encodeDurableAgentsActionCallbackData(action durableAgentsCallbackAction, agentID string, view string, page int) string {
	return encodeDurableAgentsCallback(action, view, page, durableAgentsCallbackToken(agentID))
}

func encodeDurableAgentsCallback(action durableAgentsCallbackAction, view string, page int, token string) string {
	view = normalizeDurableAgentsView(view)
	if page < 1 {
		page = 1
	}
	parts := []string{string(action), view, strconv.Itoa(page)}
	if strings.TrimSpace(token) != "" {
		parts = append(parts, strings.TrimSpace(token))
	}
	data := durableAgentsCallbackPrefix + strings.Join(parts, ":")
	if len(data) > core.TelegramCallbackDataMaxBytes {
		return ""
	}
	return data
}

func decodeDurableAgentsCallbackData(data string) (durableAgentsCallbackRequest, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, durableAgentsCallbackPrefix) {
		return durableAgentsCallbackRequest{}, false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, durableAgentsCallbackPrefix))
	if payload == string(durableAgentsCallbackAnalyze) {
		return durableAgentsCallbackRequest{Action: durableAgentsCallbackAnalyze, View: telegramPageViewList, Page: 1}, true
	}
	parts := strings.Split(payload, ":")
	if len(parts) < 3 || len(parts) > 4 {
		return durableAgentsCallbackRequest{}, false
	}
	action := durableAgentsCallbackAction(strings.TrimSpace(parts[0]))
	if !validDurableAgentsCallbackAction(action) {
		return durableAgentsCallbackRequest{}, false
	}
	view := normalizeDurableAgentsView(parts[1])
	page, err := strconv.Atoi(strings.TrimSpace(parts[2]))
	if err != nil || page < 1 {
		return durableAgentsCallbackRequest{}, false
	}
	token := ""
	if len(parts) == 4 {
		token = strings.TrimSpace(parts[3])
		if token == "" {
			return durableAgentsCallbackRequest{}, false
		}
	}
	switch action {
	case durableAgentsCallbackRefresh, durableAgentsCallbackBack:
		if token != "" {
			return durableAgentsCallbackRequest{}, false
		}
	default:
		if token == "" {
			return durableAgentsCallbackRequest{}, false
		}
	}
	return durableAgentsCallbackRequest{Action: action, View: view, Page: page, Token: token}, true
}

func validDurableAgentsCallbackAction(action durableAgentsCallbackAction) bool {
	switch action {
	case durableAgentsCallbackRefresh, durableAgentsCallbackDetail, durableAgentsCallbackBack, durableAgentsCallbackBrief, durableAgentsCallbackPark, durableAgentsCallbackResume, durableAgentsCallbackRetireAsk, durableAgentsCallbackRetireConfirm, durableAgentsCallbackRetireBrief, durableAgentsCallbackRetireCancel:
		return true
	default:
		return false
	}
}

func normalizeDurableAgentsView(view string) string {
	switch strings.TrimSpace(view) {
	case telegramPageViewRetired:
		return telegramPageViewRetired
	default:
		return telegramPageViewList
	}
}

func durableAgentsCallbackToken(value string) string {
	sum := sha1.Sum([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])[:12]
}

func resolveDurableAgentCallbackToken(agents []core.DurableAgentStatusSnapshot, token string) (core.DurableAgentStatusSnapshot, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return core.DurableAgentStatusSnapshot{}, false
	}
	var found core.DurableAgentStatusSnapshot
	foundOK := false
	for _, agent := range agents {
		agentID := strings.TrimSpace(agent.AgentID)
		if agentID == "" || durableAgentsCallbackToken(agentID) != token {
			continue
		}
		if foundOK {
			return core.DurableAgentStatusSnapshot{}, false
		}
		found = agent
		foundOK = true
	}
	return found, foundOK
}

func durableAgentDetailRows(agents []core.DurableAgentStatusSnapshot, start int, view string, page int) [][]telegram.InlineButton {
	rows := make([][]telegram.InlineButton, 0, (len(agents)+1)/2)
	row := make([]telegram.InlineButton, 0, 2)
	for i, agent := range agents {
		agentID := strings.TrimSpace(agent.AgentID)
		if agentID == "" {
			continue
		}
		row = append(row, telegram.InlineButton{
			Text:         fmt.Sprintf("Agent %d", start+i+1),
			CallbackData: encodeDurableAgentsDetailCallbackData(agentID, view, page),
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

func durableAgentDetailButtonRows(agent core.DurableAgentStatusSnapshot, view string, page int) [][]telegram.InlineButton {
	agentID := strings.TrimSpace(agent.AgentID)
	status := strings.ToLower(strings.TrimSpace(agent.Status))
	rows := make([][]telegram.InlineButton, 0, 3)
	if status != "retired" {
		actionRow := make([]telegram.InlineButton, 0, 3)
		if status == "active" {
			actionRow = append(actionRow,
				telegram.InlineButton{Text: "Brief", CallbackData: encodeDurableAgentsActionCallbackData(durableAgentsCallbackBrief, agentID, view, page)},
				telegram.InlineButton{Text: "Park", CallbackData: encodeDurableAgentsActionCallbackData(durableAgentsCallbackPark, agentID, view, page)},
			)
		} else {
			actionRow = append(actionRow, telegram.InlineButton{Text: "Resume", CallbackData: encodeDurableAgentsActionCallbackData(durableAgentsCallbackResume, agentID, view, page)})
		}
		actionRow = append(actionRow, telegram.InlineButton{Text: "Retire", CallbackData: encodeDurableAgentsActionCallbackData(durableAgentsCallbackRetireAsk, agentID, view, page)})
		rows = append(rows, actionRow)
	}
	rows = append(rows, []telegram.InlineButton{
		{Text: "Refresh", CallbackData: encodeDurableAgentsDetailCallbackData(agentID, view, page)},
		{Text: "Back", CallbackData: encodeDurableAgentsBackCallbackData(view, page)},
	})
	return rows
}

func durableAgentRetireConfirmRows(agentID string, view string, page int) [][]telegram.InlineButton {
	return [][]telegram.InlineButton{
		{
			{Text: "Cancel", CallbackData: encodeDurableAgentsActionCallbackData(durableAgentsCallbackRetireCancel, agentID, view, page)},
			{Text: "Retire", CallbackData: encodeDurableAgentsActionCallbackData(durableAgentsCallbackRetireConfirm, agentID, view, page)},
		},
		{
			{Text: "Brief", CallbackData: encodeDurableAgentsActionCallbackData(durableAgentsCallbackRetireBrief, agentID, view, page)},
		},
	}
}
