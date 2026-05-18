//go:build linux

package session

import "strings"

func OperatorAutoScopeForKey(key SessionKey) (string, string) {
	scope := defaultScopeForKey(key)
	return operatorAutoScopeParts(scope)
}

func OperatorAutoScopeForRef(ref ScopeRef) (string, string) {
	return operatorAutoScopeParts(ref)
}

func operatorAutoScopeParts(ref ScopeRef) (string, string) {
	ref = NormalizeScopeRef(ref)
	kind := strings.TrimSpace(string(ref.Kind))
	id := strings.TrimSpace(ref.ID)
	if kind == "" || id == "" {
		return "", ""
	}
	return kind, id
}
