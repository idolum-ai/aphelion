//go:build linux

package runtime

import (
	"fmt"
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

const MaxAuthorityDiscoveryLoadoutSlots = 2

type AuthorityDiscoveryLoadoutSlot struct {
	TokenID       string
	LabelRef      string
	StepRef       string
	ShapeHash     string
	LiveAuthority bool
	ExpiresAt     time.Time
}

type AuthorityDiscoveryResolutionCandidate struct {
	CandidateID string
	Kind        string
	Ref         string
	Action      string
	State       AuthorityDiscoveryTokenState
	Reason      string
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
	PlanID            string
	PlanVersion       string
	SessionID         string
	Entries           []session.IdentificationLedgerProjection
	Loadout           []AuthorityDiscoveryLoadoutSlot
	LiveAuthorityRefs map[string]bool
	Now               time.Time
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

type AuthorityDiscoveryTraceLink struct {
	Kind string
	Ref  string
}

type AuthorityDiscoveryTrace struct {
	Links []AuthorityDiscoveryTraceLink
}

func CompileAuthorityDiscoveryMenu(input AuthorityDiscoveryMenuInput) AuthorityDiscoveryMenu {
	now := input.Now.UTC()
	if now.IsZero() {
		now = time.Unix(0, 0).UTC()
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
			State:      authorityDiscoveryStateForEntry(entry, now, authorityDiscoveryInputLabelLive(input.LiveAuthorityRefs, entry.LabelRef)),
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
			token.ResolutionCandidates = append(token.ResolutionCandidates, authorityDiscoveryResolutionCandidate(
				authorityDiscoveryCandidateKind(entry.LabelRef),
				entry.LabelRef,
				"materialize_or_use_exact_authority",
				token.State,
				"ledger label resolves to an exact stored authority contract or grant",
			))
		}
		menu.Tokens = append(menu.Tokens, token)
	}
	loadoutSlots := 0
	for _, slot := range input.Loadout {
		if loadoutSlots >= MaxAuthorityDiscoveryLoadoutSlots {
			break
		}
		tokenID := strings.TrimSpace(slot.TokenID)
		if tokenID == "" {
			tokenID = strings.TrimSpace(slot.LabelRef)
		}
		if tokenID == "" {
			continue
		}
		state := AuthorityDiscoveryTokenOneApprovalAway
		live := slot.LiveAuthority || authorityDiscoveryInputLabelLive(input.LiveAuthorityRefs, slot.LabelRef)
		if !slot.ExpiresAt.IsZero() && !slot.ExpiresAt.After(now) {
			state = AuthorityDiscoveryTokenExpired
		} else if live {
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
			token.ResolutionCandidates = append(token.ResolutionCandidates, authorityDiscoveryResolutionCandidate(
				authorityDiscoveryCandidateKind(token.LabelRef),
				token.LabelRef,
				"use_loadout_authority",
				token.State,
				"bounded preselected loadout authority",
			))
		}
		menu.Tokens = append(menu.Tokens, token)
		loadoutSlots++
	}
	sort.SliceStable(menu.Tokens, func(i, j int) bool {
		if menu.Tokens[i].StepRef == menu.Tokens[j].StepRef {
			return menu.Tokens[i].TokenID < menu.Tokens[j].TokenID
		}
		return menu.Tokens[i].StepRef < menu.Tokens[j].StepRef
	})
	return menu
}

func authorityDiscoveryInputLabelLive(liveRefs map[string]bool, labelRef string) bool {
	labelRef = strings.TrimSpace(labelRef)
	if labelRef == "" || liveRefs == nil {
		return false
	}
	return liveRefs[labelRef]
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

func AuthorityDiscoveryTraceForReviewEvent(event session.ReviewEvent, action session.NextActionRecord, projection session.IdentificationLedgerProjection) AuthorityDiscoveryTrace {
	trace := AuthorityDiscoveryTrace{}
	if event.ID > 0 {
		trace.Links = append(trace.Links, AuthorityDiscoveryTraceLink{Kind: "review_event", Ref: fmt.Sprint(event.ID)})
	}
	if id := strings.TrimSpace(action.RecordID); id != "" {
		trace.Links = append(trace.Links, AuthorityDiscoveryTraceLink{Kind: "next_action", Ref: id})
	}
	if id := actionRecoveryApprovalContractID(action); id != "" {
		trace.Links = append(trace.Links, AuthorityDiscoveryTraceLink{Kind: "contract", Ref: id})
	}
	entry := session.NormalizeIdentificationLedgerEntry(projection.Entry)
	if id := strings.TrimSpace(entry.EntryID); id != "" {
		trace.Links = append(trace.Links, AuthorityDiscoveryTraceLink{Kind: "identification_ledger_entry", Ref: id})
	}
	for _, observation := range projection.Observations {
		observation = session.NormalizeIdentificationLedgerObservation(observation)
		if id := strings.TrimSpace(observation.ObservationID); id != "" {
			trace.Links = append(trace.Links, AuthorityDiscoveryTraceLink{Kind: "identification_ledger_observation", Ref: id})
		}
	}
	return trace
}

type AuthorityDiscoveryMenuBuildInput struct {
	Key         session.SessionKey
	PlanID      string
	PlanVersion string
	SessionID   string
	Loadout     []AuthorityDiscoveryLoadoutSlot
	Now         time.Time
	Limit       int
}

func (r *Runtime) BuildAuthorityDiscoveryMenu(input AuthorityDiscoveryMenuBuildInput) (AuthorityDiscoveryMenu, error) {
	if r == nil || r.store == nil {
		return AuthorityDiscoveryMenu{}, fmt.Errorf("authority discovery menu requires runtime store")
	}
	sessionID := strings.TrimSpace(input.SessionID)
	if sessionID == "" {
		sessionID = session.SessionIDForKey(input.Key)
	}
	planID := strings.TrimSpace(input.PlanID)
	if planID == "" {
		planID = session.IdentificationPlanIDForSession(sessionID)
	}
	planVersion := strings.TrimSpace(input.PlanVersion)
	if planVersion == "" {
		planVersion = session.IdentificationDefaultPlanVersion
	}
	limit := input.Limit
	if limit <= 0 {
		limit = 100
	}
	entries, err := r.store.IdentificationLedgerEntries(session.IdentificationLedgerQuery{
		PlanID:      planID,
		PlanVersion: planVersion,
		SessionID:   sessionID,
		Limit:       limit,
	})
	if err != nil {
		return AuthorityDiscoveryMenu{}, err
	}
	now := input.Now
	if now.IsZero() {
		now = r.authorityDiscoveryNow()
	}
	liveRefs := map[string]bool{}
	for _, projection := range entries {
		labelRef := strings.TrimSpace(projection.Entry.LabelRef)
		if labelRef == "" {
			continue
		}
		live, err := r.authorityDiscoveryLabelLive(input.Key, sessionID, labelRef, projection.Entry.ShapeHash, now)
		if err != nil {
			return AuthorityDiscoveryMenu{}, err
		}
		liveRefs[labelRef] = live
	}
	for _, slot := range input.Loadout {
		labelRef := strings.TrimSpace(slot.LabelRef)
		if labelRef == "" {
			continue
		}
		live, err := r.authorityDiscoveryLabelLive(input.Key, sessionID, labelRef, slot.ShapeHash, now)
		if err != nil {
			return AuthorityDiscoveryMenu{}, err
		}
		liveRefs[labelRef] = live
	}
	return CompileAuthorityDiscoveryMenu(AuthorityDiscoveryMenuInput{
		PlanID:            planID,
		PlanVersion:       planVersion,
		SessionID:         sessionID,
		Entries:           entries,
		Loadout:           input.Loadout,
		LiveAuthorityRefs: liveRefs,
		Now:               now,
	}), nil
}

func (r *Runtime) authorityDiscoveryLabelLive(key session.SessionKey, sessionID string, labelRef string, shapeHash string, now time.Time) (bool, error) {
	labelRef = strings.TrimSpace(labelRef)
	if labelRef == "" || r == nil || r.store == nil {
		return false, nil
	}
	if now.IsZero() {
		now = r.authorityDiscoveryNow()
	}
	switch authorityDiscoveryCandidateKind(labelRef) {
	case "capability_grant":
		grant, ok, err := r.store.CapabilityGrant(labelRef)
		if err != nil || !ok {
			return false, err
		}
		grant = session.NormalizeCapabilityGrant(grant)
		return authorityDiscoveryGrantMatchesShape(grant, shapeHash) &&
			grant.Status == session.CapabilityGrantStatusActive &&
			grant.RevokedAt.IsZero() &&
			(grant.ExpiresAt.IsZero() || grant.ExpiresAt.After(now.UTC())), nil
	case "continuation_recovery_contract":
		return r.authorityDiscoveryContinuationContractLive(key, sessionID, labelRef, now)
	case "authority_bundle":
		return r.authorityDiscoveryAuthorityBundleLive(key, sessionID, labelRef, now)
	default:
		return false, nil
	}
}

func (r *Runtime) authorityDiscoveryContinuationContractLive(key session.SessionKey, sessionID string, contractID string, now time.Time) (bool, error) {
	contract, ok, err := r.store.ContinuationRecoveryContract(contractID)
	if err != nil || !ok {
		return false, err
	}
	contract = session.NormalizeContinuationRecoveryContract(contract)
	if contract.Status != session.ContinuationRecoveryContractStatusRecorded {
		return false, nil
	}
	if strings.TrimSpace(contract.SessionID) != "" && strings.TrimSpace(contract.SessionID) != strings.TrimSpace(sessionID) {
		return false, nil
	}
	state, exists, err := r.store.ContinuationStateIfExists(key)
	if err != nil || !exists {
		return false, err
	}
	state = session.NormalizeContinuationState(state)
	return state.Status == session.ContinuationStatusApproved &&
		state.RemainingTurns > 0 &&
		strings.TrimSpace(state.ContinuationLease.RecoveryContractID) == strings.TrimSpace(contractID) &&
		state.ContinuationLease.ActiveAt(now), nil
}

func (r *Runtime) authorityDiscoveryAuthorityBundleLive(key session.SessionKey, sessionID string, bundleID string, now time.Time) (bool, error) {
	bundle, ok, err := r.store.AuthorityBundleContract(bundleID)
	if err != nil || !ok {
		return false, err
	}
	bundle = session.NormalizeAuthorityBundleContract(bundle)
	if bundle.Status != session.AuthorityBundleStatusRecorded {
		return false, nil
	}
	if strings.TrimSpace(bundle.SessionID) != "" && strings.TrimSpace(bundle.SessionID) != strings.TrimSpace(sessionID) {
		return false, nil
	}
	if !bundle.ExpiresAt.IsZero() && !bundle.ExpiresAt.After(now.UTC()) {
		return false, nil
	}
	if strings.TrimSpace(bundle.PrimaryContinuationContractID) != "" {
		live, err := r.authorityDiscoveryContinuationContractLive(key, sessionID, bundle.PrimaryContinuationContractID, now)
		if err != nil || !live {
			return live, err
		}
	}
	for _, spec := range bundle.RequiredCapabilityGrants {
		spec = session.NormalizeCapabilityGrantSpec(spec)
		if strings.TrimSpace(spec.GrantID) == "" {
			return false, nil
		}
		specLive := false
		for _, shapeHash := range authorityDiscoveryShapeHashesForGrantSpec(spec) {
			live, err := r.authorityDiscoveryLabelLive(key, sessionID, spec.GrantID, shapeHash, now)
			if err != nil {
				return false, err
			}
			if live {
				specLive = true
				break
			}
		}
		if !specLive {
			return false, nil
		}
	}
	return strings.TrimSpace(bundle.PrimaryContinuationContractID) != "" || len(bundle.RequiredCapabilityGrants) > 0, nil
}

func authorityDiscoveryGrantMatchesShape(grant session.CapabilityGrant, shapeHash string) bool {
	shapeHash = strings.TrimSpace(shapeHash)
	if shapeHash == "" {
		return false
	}
	resourceClass := session.AuthorityResourceClass(grant.TargetResource)
	for _, action := range grant.AllowedActions {
		if session.AuthorityShapeHash(session.AuthorityShapeInput{
			Action:        strings.TrimSpace(action),
			ResourceClass: resourceClass,
		}) == shapeHash {
			return true
		}
	}
	return false
}

func authorityDiscoveryShapeHashForGrantSpec(spec session.CapabilityGrantSpec) string {
	hashes := authorityDiscoveryShapeHashesForGrantSpec(spec)
	if len(hashes) == 0 {
		return ""
	}
	return hashes[0]
}

func authorityDiscoveryShapeHashesForGrantSpec(spec session.CapabilityGrantSpec) []string {
	spec = session.NormalizeCapabilityGrantSpec(spec)
	resourceClass := session.AuthorityResourceClass(spec.TargetResource)
	var hashes []string
	for _, action := range spec.AllowedActions {
		if strings.TrimSpace(action) == "" {
			continue
		}
		hash := session.AuthorityShapeHash(session.AuthorityShapeInput{
			Action:        strings.TrimSpace(action),
			ResourceClass: resourceClass,
		})
		if hash != "" {
			hashes = append(hashes, hash)
		}
	}
	return hashes
}

func authorityDiscoveryStateForEntry(entry session.IdentificationLedgerEntry, now time.Time, liveAuthority bool) AuthorityDiscoveryTokenState {
	if !entry.ExpiresAt.IsZero() && !entry.ExpiresAt.After(now) {
		return AuthorityDiscoveryTokenExpired
	}
	switch entry.Status {
	case session.IdentificationLedgerStatusApproved:
		if liveAuthority {
			return AuthorityDiscoveryTokenExecutable
		}
		return AuthorityDiscoveryTokenOneApprovalAway
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

func authorityDiscoveryResolutionCandidate(kind, ref, action string, state AuthorityDiscoveryTokenState, reason string) AuthorityDiscoveryResolutionCandidate {
	kind = strings.TrimSpace(kind)
	ref = strings.TrimSpace(ref)
	action = strings.TrimSpace(action)
	reason = strings.TrimSpace(reason)
	seed := strings.Join([]string{kind, ref, action, string(state), reason}, "\x00")
	id := ""
	if strings.TrimSpace(seed) != "" {
		id = "resolution:" + session.EffectAttemptCommandHash(seed)[7:23]
	}
	return AuthorityDiscoveryResolutionCandidate{
		CandidateID: id,
		Kind:        kind,
		Ref:         ref,
		Action:      action,
		State:       state,
		Reason:      reason,
	}
}
