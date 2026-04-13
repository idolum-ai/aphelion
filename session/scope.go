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
