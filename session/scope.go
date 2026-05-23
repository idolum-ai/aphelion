//go:build linux

package session

import (
	"fmt"
	"strconv"
	"strings"
)

func NormalizeScopeRef(ref ScopeRef) ScopeRef {
	ref.Kind = ScopeKind(strings.TrimSpace(strings.ToLower(string(ref.Kind))))
	ref.ID = strings.TrimSpace(ref.ID)
	ref.DurableAgentID = strings.TrimSpace(ref.DurableAgentID)
	ref.ParentScopeKind = ScopeKind(strings.TrimSpace(strings.ToLower(string(ref.ParentScopeKind))))
	ref.ParentScopeID = strings.TrimSpace(ref.ParentScopeID)
	return ref
}

func (ref ScopeRef) IsZero() bool {
	ref = NormalizeScopeRef(ref)
	return ref.Kind == "" && ref.ID == "" && ref.DurableAgentID == "" && ref.ParentScopeKind == "" && ref.ParentScopeID == ""
}

func (ref ScopeRef) String() string {
	ref = NormalizeScopeRef(ref)
	if ref.Kind == "" && ref.ID == "" {
		return ""
	}
	if ref.ID == "" {
		return string(ref.Kind)
	}
	return fmt.Sprintf("%s:%s", ref.Kind, ref.ID)
}

func defaultScopeForKey(key SessionKey) ScopeRef {
	if !key.Scope.IsZero() {
		return NormalizeScopeRef(key.Scope)
	}
	if key.ChatID == 0 {
		return ScopeRef{}
	}
	return ScopeRef{
		Kind: ScopeKindTelegramDM,
		ID:   strconv.FormatInt(key.ChatID, 10),
	}
}

func SessionIDForKey(key SessionKey) string {
	scope := defaultScopeForKey(key)
	base := scope.String()
	if base == "" {
		base = fmt.Sprintf("transport:%d", key.ChatID)
	}
	if key.UserID != 0 {
		base += fmt.Sprintf("/user:%d", key.UserID)
	}
	return base
}

func SessionIDFromParts(chatID int64, userID int64, scope ScopeRef) string {
	return SessionIDForKey(SessionKey{
		ChatID: chatID,
		UserID: userID,
		Scope:  scope,
	})
}

func TelegramThreadScopeID(chatID int64, threadID int64) string {
	if chatID == 0 || threadID <= 0 {
		return ""
	}
	return fmt.Sprintf("%d:%d", chatID, threadID)
}

func TelegramThreadScopeRef(chatID int64, threadID int64) ScopeRef {
	id := TelegramThreadScopeID(chatID, threadID)
	if id == "" {
		return ScopeRef{}
	}
	return ScopeRef{Kind: ScopeKindTelegramThread, ID: id}
}

func TelegramThreadIDFromScope(chatID int64, scope ScopeRef) (int64, bool) {
	scope = NormalizeScopeRef(scope)
	if scope.Kind != ScopeKindTelegramThread || chatID == 0 {
		return 0, false
	}
	prefix := strconv.FormatInt(chatID, 10) + ":"
	raw := strings.TrimSpace(scope.ID)
	if !strings.HasPrefix(raw, prefix) {
		return 0, false
	}
	threadID, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(raw, prefix)), 10, 64)
	if err != nil || threadID <= 0 {
		return 0, false
	}
	return threadID, true
}
