//go:build linux

package tool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

type missingGrantRequirement struct {
	RequestID          string
	Kind               session.CapabilityKind
	TargetResource     string
	GrantedTo          string
	AllowedActions     []string
	Contract           string
	Constraints        string
	Purpose            string
	RiskClass          string
	ReviewSummary      string
	OperatorProjection string
	OperationKind      string
	OperationTool      string
}

type missingGrantContract struct {
	Requirement         missingGrantRequirement
	AcceptedPrincipals  []string
	AcceptedGrantShapes []missingGrantAcceptedShape
}

type missingGrantAcceptedShape struct {
	Kind                session.CapabilityKind
	TargetResource      string
	Action              string
	ToolInvocationScope missingGrantToolInvocationScopeMode
	RequiredConstraints map[string]string
}

type missingGrantToolInvocationScopeMode string

const (
	missingGrantToolInvocationScopeIgnored  missingGrantToolInvocationScopeMode = ""
	missingGrantToolInvocationScopeOptional missingGrantToolInvocationScopeMode = "optional"
	missingGrantToolInvocationScopeRequired missingGrantToolInvocationScopeMode = "required"
)

type missingGrantError struct {
	contract missingGrantContract
	cause    error
}

func (e missingGrantError) Error() string {
	if strings.TrimSpace(e.contract.Requirement.RequestID) != "" {
		return fmt.Sprintf("missing capability grant; review request %s is required", strings.TrimSpace(e.contract.Requirement.RequestID))
	}
	if e.cause != nil {
		return e.cause.Error()
	}
	return "missing capability grant"
}

func (e missingGrantError) Unwrap() error {
	return e.cause
}

func (r *Registry) activeGrantForMissingGrantContract(contract missingGrantContract, input json.RawMessage) (session.CapabilityGrant, bool, error) {
	if r == nil || r.store == nil {
		return session.CapabilityGrant{}, false, nil
	}
	contract = normalizeMissingGrantContract(contract)
	if contract.Requirement.Kind == "" || contract.Requirement.TargetResource == "" {
		return session.CapabilityGrant{}, false, nil
	}
	queryCache := map[string][]session.CapabilityGrant{}
	for _, shape := range contract.AcceptedGrantShapes {
		for _, principalID := range contract.AcceptedPrincipals {
			key := strings.Join([]string{string(shape.Kind), shape.TargetResource, principalID, shape.Action}, "\x00")
			grants, ok := queryCache[key]
			if !ok {
				var err error
				grants, err = r.store.ActiveCapabilityGrants(shape.Kind, shape.TargetResource, principalID, shape.Action)
				if err != nil {
					return session.CapabilityGrant{}, false, err
				}
				queryCache[key] = grants
			}
			for _, grant := range grants {
				if !missingGrantShapeAllowsGrant(shape, grant, input) {
					continue
				}
				return grant, true, nil
			}
		}
	}
	return session.CapabilityGrant{}, false, nil
}

func (r *Registry) materializeMissingGrantRequirement(_ context.Context, key session.SessionKey, actor principal.Principal, requirement missingGrantRequirement, now time.Time) (session.CapabilityRequest, int64, session.NextActionRecord, error) {
	if r == nil || r.store == nil {
		return session.CapabilityRequest{}, 0, session.NextActionRecord{}, fmt.Errorf("missing capability grant materialization requires transcript store")
	}
	if !toolSessionKeyHasIdentity(key) {
		return session.CapabilityRequest{}, 0, session.NextActionRecord{}, fmt.Errorf("missing capability grant materialization requires session identity")
	}
	requirement = normalizeMissingGrantRequirement(requirement)
	if requirement.RequestID == "" || requirement.Kind == "" || requirement.TargetResource == "" || requirement.GrantedTo == "" {
		return session.CapabilityRequest{}, 0, session.NextActionRecord{}, fmt.Errorf("missing capability grant requirement is incomplete")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	request, ok, err := r.store.CapabilityRequest(requirement.RequestID)
	if err != nil {
		return session.CapabilityRequest{}, 0, session.NextActionRecord{}, err
	}
	if !ok {
		request, err = r.store.UpsertCapabilityRequest(session.CapabilityRequest{
			RequestID:      requirement.RequestID,
			RequestedBy:    toolAuthorityPrincipalDisplay(actor),
			RequestedFor:   requirement.GrantedTo,
			Kind:           requirement.Kind,
			TargetResource: requirement.TargetResource,
			Purpose:        requirement.Purpose,
			RiskClass:      requirement.RiskClass,
			Contract:       requirement.Contract,
			Constraints:    requirement.Constraints,
			ReviewStatus:   session.CapabilityReviewStatusProposed,
			CreatedAt:      now,
			UpdatedAt:      now,
		})
		if err != nil {
			return session.CapabilityRequest{}, 0, session.NextActionRecord{}, err
		}
	}

	reviewTarget := capabilityRequestReviewTarget(capabilityInput{}, key)
	reviewEventID := int64(0)
	if reviewTarget.ChatID != 0 && (request.ReviewStatus == session.CapabilityReviewStatusProposed || request.ReviewStatus == session.CapabilityReviewStatusParentApproved) {
		reviewEventID, err = r.pendingCapabilityRequestReviewEventID(reviewTarget.ChatID, request.RequestID)
		if err != nil {
			return session.CapabilityRequest{}, 0, session.NextActionRecord{}, err
		}
		if reviewEventID == 0 {
			in := capabilityInput{
				ReviewTargetChatID: reviewTarget.ChatID,
				ReviewSummary:      requirement.ReviewSummary,
			}
			reviewEventID, err = r.queueCapabilityRequestReviewEvent(request, in, actor, key, reviewTarget)
			if err != nil {
				return session.CapabilityRequest{}, 0, session.NextActionRecord{}, err
			}
		}
	}

	operationInput, err := missingGrantOperationInputJSON(request, requirement)
	if err != nil {
		return session.CapabilityRequest{}, 0, session.NextActionRecord{}, err
	}
	projection := requirement.OperatorProjection
	if reviewEventID > 0 {
		projection = strings.TrimSpace(projection + fmt.Sprintf("\nreview_event_id: %d", reviewEventID))
	}
	action, err := r.store.RecordNextAction(session.NextActionInput{
		RecordID:           missingGrantNextActionRecordID(key, request.RequestID),
		Key:                key,
		Owner:              "tool",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "capability_request",
		SubjectRef:         request.RequestID,
		CausalRefs:         []string{"capability_request:" + request.RequestID, "capability:" + string(request.Kind) + ":" + request.TargetResource},
		NextAction:         "review and grant the exact missing capability before retrying the blocked tool invocation",
		RequiredAuthority:  "capability_grant",
		ResourceBlocker:    "missing_capability_grant",
		RetryPolicy:        "retry_after_grant",
		OperationKind:      firstNonEmpty(requirement.OperationKind, "capability_grant_review"),
		OperationTool:      firstNonEmpty(requirement.OperationTool, "capability_authority"),
		OperationInputJSON: operationInput,
		OperatorProjection: projection,
		CreatedAt:          now,
	})
	if err != nil {
		return session.CapabilityRequest{}, 0, session.NextActionRecord{}, err
	}
	return request, reviewEventID, action, nil
}

func missingGrantNextActionRecordID(key session.SessionKey, requestID string) string {
	seed := strings.Join([]string{
		session.SessionIDForKey(key),
		"capability_request",
		strings.TrimSpace(requestID),
		string(session.NextActionBlockedNeedsAuthority),
	}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "next_missing_grant_" + hex.EncodeToString(sum[:12])
}

func (r *Registry) pendingCapabilityRequestReviewEventID(targetChatID int64, requestID string) (int64, error) {
	if r == nil || r.store == nil || targetChatID == 0 || strings.TrimSpace(requestID) == "" {
		return 0, nil
	}
	events, err := r.store.PendingReviewEvents(targetChatID, 100)
	if err != nil {
		return 0, err
	}
	for _, event := range events {
		var metadata map[string]any
		if err := json.Unmarshal([]byte(event.MetadataJSON), &metadata); err != nil {
			continue
		}
		if strings.TrimSpace(fmt.Sprint(metadata["request_id"])) == strings.TrimSpace(requestID) {
			return event.ID, nil
		}
	}
	return 0, nil
}

func normalizeMissingGrantRequirement(requirement missingGrantRequirement) missingGrantRequirement {
	requirement.RequestID = strings.TrimSpace(requirement.RequestID)
	requirement.Kind = session.NormalizeCapabilityKind(requirement.Kind)
	requirement.TargetResource = strings.TrimSpace(requirement.TargetResource)
	requirement.GrantedTo = strings.TrimSpace(requirement.GrantedTo)
	requirement.AllowedActions = session.NormalizeCapabilityActions(requirement.AllowedActions)
	if len(requirement.AllowedActions) == 0 {
		requirement.AllowedActions = []string{"invoke"}
	}
	requirement.Contract = strings.TrimSpace(requirement.Contract)
	if requirement.Contract == "" {
		requirement.Contract = "{}"
	}
	requirement.Constraints = strings.TrimSpace(requirement.Constraints)
	if requirement.Constraints == "" {
		requirement.Constraints = "{}"
	}
	requirement.Purpose = strings.TrimSpace(requirement.Purpose)
	requirement.RiskClass = strings.TrimSpace(requirement.RiskClass)
	requirement.ReviewSummary = strings.TrimSpace(requirement.ReviewSummary)
	requirement.OperatorProjection = strings.TrimSpace(requirement.OperatorProjection)
	requirement.OperationKind = strings.TrimSpace(requirement.OperationKind)
	requirement.OperationTool = strings.TrimSpace(requirement.OperationTool)
	if requirement.RequestID == "" {
		requirement.RequestID = stableMissingGrantRequestID(requirement)
	}
	if requirement.Purpose == "" {
		requirement.Purpose = fmt.Sprintf("Grant %s on %s to %s.", requirement.Kind, requirement.TargetResource, requirement.GrantedTo)
	}
	if requirement.RiskClass == "" {
		requirement.RiskClass = "authority"
	}
	if requirement.ReviewSummary == "" {
		requirement.ReviewSummary = fmt.Sprintf("Missing capability grant: kind=%s target=%s requested_for=%s actions=%s", requirement.Kind, requirement.TargetResource, requirement.GrantedTo, strings.Join(requirement.AllowedActions, ","))
	}
	if requirement.OperatorProjection == "" {
		requirement.OperatorProjection = requirement.ReviewSummary
	}
	return requirement
}

func stableMissingGrantRequestID(requirement missingGrantRequirement) string {
	payload := map[string]any{
		"kind":            string(requirement.Kind),
		"target_resource": strings.TrimSpace(requirement.TargetResource),
		"granted_to":      strings.TrimSpace(requirement.GrantedTo),
		"allowed_actions": session.NormalizeCapabilityActions(requirement.AllowedActions),
		"contract":        strings.TrimSpace(requirement.Contract),
		"constraints":     strings.TrimSpace(requirement.Constraints),
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return "req-missing-grant-" + hex.EncodeToString(sum[:10])
}

func missingGrantOperationInputJSON(request session.CapabilityRequest, requirement missingGrantRequirement) (string, error) {
	payload := map[string]any{
		"action":            "grant_set",
		"request_id":        request.RequestID,
		"kind":              string(requirement.Kind),
		"target_resource":   requirement.TargetResource,
		"principal":         requirement.GrantedTo,
		"allowed_actions":   requirement.AllowedActions,
		"contract":          json.RawMessage(requirement.Contract),
		"constraints":       json.RawMessage(requirement.Constraints),
		"grant_status":      string(session.CapabilityGrantStatusActive),
		"retry_after_grant": true,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (r *Registry) materializeMissingGrantError(ctx context.Context, key session.SessionKey, actor principal.Principal, err error) error {
	var missing missingGrantError
	if !errors.As(err, &missing) {
		return err
	}
	contract := normalizeMissingGrantContract(missing.contract)
	request, reviewEventID, _, materializeErr := r.materializeMissingGrantRequirement(ctx, key, actor, contract.Requirement, time.Now().UTC())
	if materializeErr != nil {
		return fmt.Errorf("%w; additionally failed to materialize review request: %v", err, materializeErr)
	}
	if reviewEventID > 0 {
		return fmt.Errorf("missing capability grant; queued review request %s as review_event_id=%d", request.RequestID, reviewEventID)
	}
	return fmt.Errorf("missing capability grant; recorded review request %s", request.RequestID)
}

func normalizeMissingGrantContract(contract missingGrantContract) missingGrantContract {
	contract.Requirement = normalizeMissingGrantRequirement(contract.Requirement)
	if len(contract.AcceptedPrincipals) == 0 {
		contract.AcceptedPrincipals = []string{contract.Requirement.GrantedTo}
	} else {
		contract.AcceptedPrincipals = normalizeUniqueStrings(append(contract.AcceptedPrincipals, contract.Requirement.GrantedTo))
	}
	if len(contract.AcceptedGrantShapes) == 0 {
		contract.AcceptedGrantShapes = []missingGrantAcceptedShape{missingGrantAcceptedShapeFromRequirement(contract.Requirement)}
	} else {
		contract.AcceptedGrantShapes = normalizeMissingGrantAcceptedShapes(contract.AcceptedGrantShapes)
	}
	return contract
}

func missingGrantAcceptedShapeFromRequirement(requirement missingGrantRequirement) missingGrantAcceptedShape {
	requirement = normalizeMissingGrantRequirement(requirement)
	action := "invoke"
	if len(requirement.AllowedActions) > 0 {
		action = requirement.AllowedActions[0]
	}
	return missingGrantAcceptedShape{
		Kind:                requirement.Kind,
		TargetResource:      requirement.TargetResource,
		Action:              action,
		ToolInvocationScope: missingGrantToolInvocationScopeOptional,
	}
}

func normalizeMissingGrantAcceptedShapes(shapes []missingGrantAcceptedShape) []missingGrantAcceptedShape {
	out := make([]missingGrantAcceptedShape, 0, len(shapes))
	seen := map[string]struct{}{}
	for _, shape := range shapes {
		shape.Kind = session.NormalizeCapabilityKind(shape.Kind)
		shape.TargetResource = strings.TrimSpace(shape.TargetResource)
		shape.Action = strings.TrimSpace(shape.Action)
		if shape.Action == "" {
			shape.Action = "invoke"
		}
		actions := session.NormalizeCapabilityActions([]string{shape.Action})
		if len(actions) == 0 {
			continue
		}
		shape.Action = actions[0]
		if shape.Kind == "" || shape.TargetResource == "" || shape.Action == "" {
			continue
		}
		if shape.RequiredConstraints != nil {
			required := map[string]string{}
			for key, value := range shape.RequiredConstraints {
				key = strings.TrimSpace(key)
				value = strings.TrimSpace(value)
				if key == "" || value == "" {
					continue
				}
				required[key] = value
			}
			shape.RequiredConstraints = required
		}
		key := strings.Join([]string{string(shape.Kind), shape.TargetResource, shape.Action, string(shape.ToolInvocationScope), compactJSON(shape.RequiredConstraints)}, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, shape)
	}
	return out
}

func missingGrantShapeAllowsGrant(shape missingGrantAcceptedShape, grant session.CapabilityGrant, input json.RawMessage) bool {
	if session.NormalizeCapabilityKind(grant.Kind) != shape.Kind {
		return false
	}
	if strings.TrimSpace(grant.TargetResource) != strings.TrimSpace(shape.TargetResource) {
		return false
	}
	_, hasScope, err := capabilityGrantToolInvocationScope(grant)
	if err != nil {
		return false
	}
	if hasScope && len(shape.RequiredConstraints) > 0 && capabilityGrantHasAnyConstraint(grant, shape.RequiredConstraints) && !missingGrantRequiredConstraintsAllowGrant(shape.RequiredConstraints, grant) {
		return false
	}
	switch shape.ToolInvocationScope {
	case missingGrantToolInvocationScopeRequired:
		if !hasScope {
			return false
		}
		if len(input) > 0 {
			if err := validateCapabilityToolInvocationInput(grant, input); err != nil {
				return false
			}
		}
		return missingGrantRequiredConstraintsAllowGrantWhenPresent(shape.RequiredConstraints, grant)
	case missingGrantToolInvocationScopeOptional:
		if hasScope {
			if len(input) > 0 {
				if err := validateCapabilityToolInvocationInput(grant, input); err != nil {
					return false
				}
			}
			return missingGrantRequiredConstraintsAllowGrantWhenPresent(shape.RequiredConstraints, grant)
		}
	}
	return missingGrantRequiredConstraintsAllowGrant(shape.RequiredConstraints, grant)
}

func missingGrantRequiredConstraintsAllowGrantWhenPresent(required map[string]string, grant session.CapabilityGrant) bool {
	if len(required) == 0 || !capabilityGrantHasAnyConstraint(grant, required) {
		return true
	}
	return missingGrantRequiredConstraintsAllowGrant(required, grant)
}

func missingGrantRequiredConstraintsAllowGrant(required map[string]string, grant session.CapabilityGrant) bool {
	if len(required) == 0 {
		return true
	}
	values, ok := capabilityGrantConstraintStrings(grant)
	if !ok {
		return false
	}
	for key, want := range required {
		if strings.TrimSpace(values[key]) != strings.TrimSpace(want) {
			return false
		}
	}
	return true
}

func capabilityGrantHasAnyConstraint(grant session.CapabilityGrant, required map[string]string) bool {
	if len(required) == 0 {
		return false
	}
	values, ok := capabilityGrantConstraintStrings(grant)
	if !ok {
		return false
	}
	for key := range required {
		if _, ok := values[key]; ok {
			return true
		}
	}
	return false
}

func capabilityGrantConstraintStrings(grant session.CapabilityGrant) (map[string]string, bool) {
	raw := strings.TrimSpace(grant.Constraints)
	if raw == "" || raw == "{}" {
		return nil, false
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, false
	}
	out := map[string]string{}
	for key, value := range decoded {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		var scalar string
		if err := json.Unmarshal(value, &scalar); err == nil {
			out[key] = strings.TrimSpace(scalar)
			continue
		}
		out[key] = strings.TrimSpace(string(value))
	}
	return out, true
}

func genericMissingGrantRequirementForTool(toolName string, p principal.Principal) missingGrantRequirement {
	return genericMissingGrantContractForTool(toolName, p).Requirement
}

func genericMissingGrantContractForTool(toolName string, p principal.Principal) missingGrantContract {
	toolName = strings.TrimSpace(toolName)
	grantedTo := toolAuthorityCanonicalPrincipal(p)
	contract := compactJSON(map[string]any{
		"bounded_effect": "Allow invoking the named registered tool only through the capability-managed tool boundary.",
		"tool_name":      toolName,
	})
	requirement := missingGrantRequirement{
		Kind:               session.CapabilityKindTool,
		TargetResource:     toolName,
		GrantedTo:          grantedTo,
		AllowedActions:     []string{"invoke"},
		Contract:           contract,
		Constraints:        "{}",
		Purpose:            fmt.Sprintf("Allow %s to invoke registered tool %s through the governed tool boundary.", grantedTo, toolName),
		RiskClass:          "authority",
		ReviewSummary:      fmt.Sprintf("Missing tool invoke grant: tool=%s requested_for=%s", toolName, grantedTo),
		OperatorProjection: fmt.Sprintf("Tool %s is blocked because %s has no active invoke grant. Review the exact capability request, then retry the tool.", toolName, grantedTo),
		OperationKind:      "capability_grant_review",
		OperationTool:      "capability_authority",
	}
	return missingGrantContract{
		Requirement:        requirement,
		AcceptedPrincipals: toolAuthorityPrincipalIDs(p),
	}
}

func compactJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(raw)
}
