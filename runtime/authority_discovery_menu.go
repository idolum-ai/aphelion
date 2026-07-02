//go:build linux

package runtime

import (
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

type AuthorityDiscoveryTokenState string

const (
	AuthorityDiscoveryTokenUnknown         AuthorityDiscoveryTokenState = "unknown"
	AuthorityDiscoveryTokenPartial         AuthorityDiscoveryTokenState = "partial"
	AuthorityDiscoveryTokenOneApprovalAway AuthorityDiscoveryTokenState = "one_approval_away"
	AuthorityDiscoveryTokenExecutable      AuthorityDiscoveryTokenState = "executable"
	AuthorityDiscoveryTokenSpent           AuthorityDiscoveryTokenState = "spent"
	AuthorityDiscoveryTokenExpired         AuthorityDiscoveryTokenState = "expired"
	AuthorityDiscoveryTokenInvalid         AuthorityDiscoveryTokenState = "invalid"
)

type AuthorityDiscoveryLoadoutSlot struct {
	TokenID       string
	LabelRef      string
	StepRef       string
	ShapeHash     string
	LiveAuthority bool
	ExpiresAt     time.Time
}

type AuthorityDiscoveryResolutionCandidate struct {
	Kind string
	Ref  string
}

type AuthorityDiscoveryMenuToken struct {
	TokenID              string
	StepRef              string
	ShapeHash            string
	LabelRef             string
	State                AuthorityDiscoveryTokenState
	Properties           map[session.IdentificationObservationProperty][]string
	ResolutionCandidates []AuthorityDiscoveryResolutionCandidate
	ExpiresAt            time.Time
}

type AuthorityDiscoveryMenuInput struct {
	PlanID      string
	PlanVersion string
	SessionID   string
	Entries     []session.IdentificationLedgerProjection
	Loadout     []AuthorityDiscoveryLoadoutSlot
	Now         time.Time
}

type AuthorityDiscoveryMenu struct {
	PlanID      string
	PlanVersion string
	SessionID   string
	Tokens      []AuthorityDiscoveryMenuToken
}

type AuthorityDiscoveryTraceMetrics struct {
	Interruptions       int
	OverGrantMass       int
	IdentifiedBreadth   int
	MalformedShapeCount int
	WastedCollisions    int
	LiveTailCount       int
}

func CompileAuthorityDiscoveryMenu(input AuthorityDiscoveryMenuInput) AuthorityDiscoveryMenu {
	now := input.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	planVersion := strings.TrimSpace(input.PlanVersion)
	if planVersion == "" {
		planVersion = session.IdentificationDefaultPlanVersion
	}
	menu := AuthorityDiscoveryMenu{
		PlanID:      strings.TrimSpace(input.PlanID),
		PlanVersion: planVersion,
		SessionID:   strings.TrimSpace(input.SessionID),
	}
	for _, projection := range input.Entries {
		entry := session.NormalizeIdentificationLedgerEntry(projection.Entry)
		if menu.PlanID != "" && entry.PlanID != menu.PlanID {
			continue
		}
		if menu.PlanVersion != "" && entry.PlanVersion != menu.PlanVersion {
			continue
		}
		if menu.SessionID != "" && entry.SessionID != menu.SessionID {
			continue
		}
		token := AuthorityDiscoveryMenuToken{
			TokenID:    entry.EntryID,
			StepRef:    entry.StepRef,
			ShapeHash:  entry.ShapeHash,
			LabelRef:   entry.LabelRef,
			State:      authorityDiscoveryStateForEntry(entry, now),
			Properties: map[session.IdentificationObservationProperty][]string{},
			ExpiresAt:  entry.ExpiresAt,
		}
		for _, observation := range projection.Observations {
			observation = session.NormalizeIdentificationLedgerObservation(observation)
			if !observation.ExpiresAt.IsZero() && !observation.ExpiresAt.After(now) {
				continue
			}
			token.Properties[observation.Property] = append(token.Properties[observation.Property], observation.Value)
		}
		if entry.LabelRef != "" {
			token.ResolutionCandidates = append(token.ResolutionCandidates, AuthorityDiscoveryResolutionCandidate{
				Kind: authorityDiscoveryCandidateKind(entry.LabelRef),
				Ref:  entry.LabelRef,
			})
		}
		menu.Tokens = append(menu.Tokens, token)
	}
	for _, slot := range input.Loadout {
		tokenID := strings.TrimSpace(slot.TokenID)
		if tokenID == "" {
			tokenID = strings.TrimSpace(slot.LabelRef)
		}
		if tokenID == "" {
			continue
		}
		state := AuthorityDiscoveryTokenOneApprovalAway
		if !slot.ExpiresAt.IsZero() && !slot.ExpiresAt.After(now) {
			state = AuthorityDiscoveryTokenExpired
		} else if slot.LiveAuthority {
			state = AuthorityDiscoveryTokenExecutable
		}
		token := AuthorityDiscoveryMenuToken{
			TokenID:   tokenID,
			StepRef:   strings.TrimSpace(slot.StepRef),
			ShapeHash: strings.TrimSpace(slot.ShapeHash),
			LabelRef:  strings.TrimSpace(slot.LabelRef),
			State:     state,
			Properties: map[session.IdentificationObservationProperty][]string{
				session.IdentificationPropertyBundleFit: []string{"standing_loadout"},
			},
			ExpiresAt: slot.ExpiresAt,
		}
		if token.LabelRef != "" {
			token.ResolutionCandidates = append(token.ResolutionCandidates, AuthorityDiscoveryResolutionCandidate{
				Kind: authorityDiscoveryCandidateKind(token.LabelRef),
				Ref:  token.LabelRef,
			})
		}
		menu.Tokens = append(menu.Tokens, token)
	}
	sort.SliceStable(menu.Tokens, func(i, j int) bool {
		if menu.Tokens[i].StepRef == menu.Tokens[j].StepRef {
			return menu.Tokens[i].TokenID < menu.Tokens[j].TokenID
		}
		return menu.Tokens[i].StepRef < menu.Tokens[j].StepRef
	})
	return menu
}

func ScoreAuthorityDiscoveryMenu(menu AuthorityDiscoveryMenu) AuthorityDiscoveryTraceMetrics {
	coverage := map[session.IdentificationObservationProperty]struct{}{}
	metrics := AuthorityDiscoveryTraceMetrics{}
	for _, token := range menu.Tokens {
		switch token.State {
		case AuthorityDiscoveryTokenOneApprovalAway:
			metrics.Interruptions++
		case AuthorityDiscoveryTokenExecutable:
			metrics.LiveTailCount++
			if token.StepRef == "" {
				metrics.OverGrantMass++
			}
		case AuthorityDiscoveryTokenPartial:
			metrics.WastedCollisions++
		case AuthorityDiscoveryTokenInvalid:
			metrics.MalformedShapeCount++
		}
		for property, values := range token.Properties {
			if len(values) > 0 {
				coverage[property] = struct{}{}
			}
		}
	}
	metrics.IdentifiedBreadth = len(coverage)
	return metrics
}

func authorityDiscoveryStateForEntry(entry session.IdentificationLedgerEntry, now time.Time) AuthorityDiscoveryTokenState {
	if !entry.ExpiresAt.IsZero() && !entry.ExpiresAt.After(now) {
		return AuthorityDiscoveryTokenExpired
	}
	switch entry.Status {
	case session.IdentificationLedgerStatusApproved:
		return AuthorityDiscoveryTokenExecutable
	case session.IdentificationLedgerStatusConsumed:
		return AuthorityDiscoveryTokenSpent
	case session.IdentificationLedgerStatusExpired:
		return AuthorityDiscoveryTokenExpired
	case session.IdentificationLedgerStatusInvalidated:
		return AuthorityDiscoveryTokenInvalid
	case session.IdentificationLedgerStatusProposed:
		return AuthorityDiscoveryTokenOneApprovalAway
	case session.IdentificationLedgerStatusPartial:
		return AuthorityDiscoveryTokenPartial
	default:
		return AuthorityDiscoveryTokenUnknown
	}
}

func authorityDiscoveryCandidateKind(ref string) string {
	ref = strings.TrimSpace(ref)
	switch {
	case strings.HasPrefix(ref, "crc-"):
		return "continuation_recovery_contract"
	case strings.HasPrefix(ref, "authbundle-"):
		return "authority_bundle"
	case strings.HasPrefix(ref, "grant"):
		return "capability_grant"
	default:
		return "authority_ref"
	}
}
