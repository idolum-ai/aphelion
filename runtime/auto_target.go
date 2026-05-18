//go:build linux

package runtime

import (
	"strconv"
	"strings"

	"github.com/idolum-ai/aphelion/session"
)

func operatorAutoDefaultScope(chatID int64) (string, string) {
	return session.OperatorAutoScopeForRef(telegramDMScopeRef(chatID))
}

func operatorAutoThreadScope(chatID int64, threadID int64) (string, string) {
	return session.OperatorAutoScopeForRef(telegramThreadScopeRef(chatID, threadID))
}

func operatorAutoTargetScopeForKey(key session.SessionKey) (string, string) {
	return session.OperatorAutoScopeForKey(key)
}

func operatorAutoScopeMatches(recordKind string, recordID string, targetKind string, targetID string) bool {
	return strings.TrimSpace(recordKind) == strings.TrimSpace(targetKind) && strings.TrimSpace(recordID) == strings.TrimSpace(targetID)
}

func autoThreadReasonPrefix(threadID int64) string {
	if threadID <= 0 {
		return ""
	}
	return "thread " + strconv.FormatInt(threadID, 10) + " "
}
