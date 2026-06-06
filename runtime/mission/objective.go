//go:build linux

package mission

import (
	"log"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) RecordWorkingObjectiveForInbound(key session.SessionKey, msg core.InboundMessage) {
	if r == nil || r.store == nil {
		return
	}
	if msg.Origin == core.InboundOriginTurnAuthorization || strings.TrimSpace(msg.DurableAgentID) != "" {
		return
	}
	objective := inferWorkingObjectiveFromUserText(msg.Text)
	if objective == "" {
		return
	}
	previous, _ := r.store.WorkingObjective(key)
	if err := r.store.UpdateWorkingObjective(key, session.WorkingObjective{
		Objective:  objective,
		Source:     "inferred",
		Confidence: "medium",
		CreatedAt:  time.Now().UTC(),
	}); err != nil {
		log.Printf("WARN working objective update failed chat_id=%d err=%v", key.ChatID, err)
		return
	}
	if strings.TrimSpace(previous.Objective) != objective {
		r.recordExecutionEvent(key, core.ExecutionEventMissionObjectiveDerived, "mission", "derived", map[string]any{
			"objective":          objective,
			"previous_objective": strings.TrimSpace(previous.Objective),
			"source":             "inbound_user_text",
			"confidence":         "medium",
		}, time.Now().UTC())
	}
}

func inferWorkingObjectiveFromUserText(text string) string {
	text = operationArtifactRequestUserText(text)
	text = strings.TrimSpace(text)
	if text == "" || strings.HasPrefix(text, "/") {
		return ""
	}
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > 180 {
		text = strings.TrimSpace(string(runes[:180])) + "…"
	}
	return text
}
