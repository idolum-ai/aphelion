//go:build linux

package durableagent

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

func (h *HTTPHandler) enrollmentPeerIdentity(r *http.Request) (core.TailnetPeerIdentity, error) {
	identity, ok := core.TailnetPeerIdentityFromContext(r.Context())
	if !h.RequirePeerIdentity {
		return identity, nil
	}
	if !ok || strings.TrimSpace(identity.StableNodeID) == "" {
		return core.TailnetPeerIdentity{}, errors.New("durable agent tailnet peer identity is required")
	}
	return identity, nil
}

func (h *HTTPHandler) controlPeerIdentity(r *http.Request, agentID string) (core.TailnetPeerIdentity, error) {
	if !h.RequirePeerIdentity {
		return core.TailnetPeerIdentity{}, nil
	}
	identity, ok := core.TailnetPeerIdentityFromContext(r.Context())
	if !ok || strings.TrimSpace(identity.StableNodeID) == "" {
		return core.TailnetPeerIdentity{}, errors.New("durable agent tailnet peer identity is required")
	}
	enrollment, err := h.store.DurableAgentRemoteEnrollment(agentID)
	if err != nil {
		return core.TailnetPeerIdentity{}, err
	}
	agent, err := h.store.DurableAgent(agentID)
	if err != nil {
		return core.TailnetPeerIdentity{}, err
	}
	if tailnetIdentityIsBound(enrollment.TailnetIdentity) {
		if !tailnetStableNodeMatches(enrollment.TailnetIdentity, identity) {
			return core.TailnetPeerIdentity{}, errors.New("durable agent control request came from a different tailnet node")
		}
	}
	if err := validateTailnetPeerIdentityForAgent(*agent, identity); err != nil {
		return core.TailnetPeerIdentity{}, err
	}
	return identity, nil
}

func validateTailnetPeerIdentityForAgent(agent core.DurableAgent, identity core.TailnetPeerIdentity) error {
	identity = core.NormalizeTailnetPeerIdentity(identity)
	if strings.TrimSpace(identity.StableNodeID) == "" {
		return errors.New("durable agent tailnet stable node id is required")
	}
	policy := core.NormalizeDurableAgentLivePolicy(agent.LivePolicy)
	if strings.TrimSpace(policy.TailnetMode) == "" {
		return nil
	}
	if strings.TrimSpace(policy.TailnetHostname) != "" && !tailnetHostnameMatches(identity, policy.TailnetHostname) {
		return fmt.Errorf("durable agent tailnet peer hostname does not match %s", policy.TailnetHostname)
	}
	if len(policy.TailnetTags) == 0 {
		return nil
	}
	tagSet := make(map[string]struct{}, len(identity.Tags))
	for _, tag := range identity.Tags {
		tagSet[strings.TrimSpace(tag)] = struct{}{}
	}
	for _, tag := range policy.TailnetTags {
		if _, ok := tagSet[strings.TrimSpace(tag)]; !ok {
			return fmt.Errorf("durable agent tailnet peer is missing required tag %s", tag)
		}
	}
	return nil
}

func tailnetStableNodeMatches(a core.TailnetPeerIdentity, b core.TailnetPeerIdentity) bool {
	a = core.NormalizeTailnetPeerIdentity(a)
	b = core.NormalizeTailnetPeerIdentity(b)
	return a.StableNodeID != "" && a.StableNodeID == b.StableNodeID
}

func tailnetIdentityIsBound(identity core.TailnetPeerIdentity) bool {
	identity = core.NormalizeTailnetPeerIdentity(identity)
	return identity.StableNodeID != ""
}

func tailnetHostnameMatches(identity core.TailnetPeerIdentity, want string) bool {
	want = normalizeTailnetHostname(want)
	if want == "" {
		return true
	}
	for _, candidate := range []string{identity.ComputedName, identity.NodeName} {
		candidate = normalizeTailnetHostname(candidate)
		if candidate == "" {
			continue
		}
		if candidate == want || strings.HasPrefix(candidate, want+".") {
			return true
		}
		if first, _, ok := strings.Cut(candidate, "."); ok && first == want {
			return true
		}
	}
	return false
}

func normalizeTailnetHostname(value string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(value), "."))
}
