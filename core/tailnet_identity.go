//go:build linux

package core

import (
	"context"
	"strings"
)

type TailnetPeerIdentity struct {
	StableNodeID string
	NodeName     string
	ComputedName string
	LoginName    string
	Tags         []string
	RemoteAddr   string
}

type tailnetPeerIdentityContextKey struct{}

func WithTailnetPeerIdentity(ctx context.Context, identity TailnetPeerIdentity) context.Context {
	return context.WithValue(ctx, tailnetPeerIdentityContextKey{}, NormalizeTailnetPeerIdentity(identity))
}

func TailnetPeerIdentityFromContext(ctx context.Context) (TailnetPeerIdentity, bool) {
	identity, ok := ctx.Value(tailnetPeerIdentityContextKey{}).(TailnetPeerIdentity)
	if !ok {
		return TailnetPeerIdentity{}, false
	}
	identity = NormalizeTailnetPeerIdentity(identity)
	return identity, identity.StableNodeID != "" || identity.LoginName != "" || identity.NodeName != ""
}

func NormalizeTailnetPeerIdentity(identity TailnetPeerIdentity) TailnetPeerIdentity {
	identity.StableNodeID = strings.TrimSpace(identity.StableNodeID)
	identity.NodeName = strings.Trim(strings.TrimSpace(identity.NodeName), ".")
	identity.ComputedName = strings.Trim(strings.TrimSpace(identity.ComputedName), ".")
	identity.LoginName = strings.ToLower(strings.TrimSpace(identity.LoginName))
	identity.Tags = normalizeDurableAgentStringSet(identity.Tags)
	identity.RemoteAddr = strings.TrimSpace(identity.RemoteAddr)
	return identity
}
