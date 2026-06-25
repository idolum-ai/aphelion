//go:build linux

package tool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

type missingContinuationLeaseRequirement struct {
	AgentID             string
	GrantID             string
	GrantTargetResource string
	Principal           string
	LeaseClass          session.ContinuationLeaseClass
	AllowedActions      []string
	Constraints         map[string]string
	OperatorProjection  string
}

type missingContinuationLeaseError struct {
	requirement missingContinuationLeaseRequirement
	cause       error
}

func (e missingContinuationLeaseError) Error() string {
	requirement := normalizeMissingContinuationLeaseRequirement(e.requirement)
	if requirement.AgentID != "" && requirement.LeaseClass != "" {
		return fmt.Sprintf("missing %s continuation lease for child %s", requirement.LeaseClass, requirement.AgentID)
	}
	if e.cause != nil {
		return e.cause.Error()
	}
	return "missing continuation lease"
}

func (e missingContinuationLeaseError) Unwrap() error {
	return e.cause
}

func durableAgentWakeOnceLeaseRequirement(agentID string, grant session.CapabilityGrant, p principal.Principal) missingContinuationLeaseRequirement {
	agentID = strings.TrimSpace(agentID)
	principalID := toolAuthorityCanonicalPrincipal(p)
	return normalizeMissingContinuationLeaseRequirement(missingContinuationLeaseRequirement{
		AgentID:             agentID,
		GrantID:             grant.GrantID,
		GrantTargetResource: grant.TargetResource,
		Principal:           principalID,
		LeaseClass:          session.ContinuationLeaseClassChildWake,
		AllowedActions:      []string{durableAgentWakeOnceAction},
		Constraints:         map[string]string{"agent_id": agentID},
		OperatorProjection: fmt.Sprintf(
			"durable_agent wake_once for %s has an active grant (%s) but no current child_wake continuation lease. Ask the admin to approve one bounded child_wake turn for this exact child, then retry wake_once once.",
			agentID,
			strings.TrimSpace(grant.GrantID),
		),
	})
}

func (r *Registry) materializeMissingContinuationLeaseError(_ context.Context, key session.SessionKey, _ principal.Principal, err error) error {
	var missing missingContinuationLeaseError
	if !asMissingContinuationLeaseError(err, &missing) {
		return err
	}
	if r == nil || r.store == nil {
		return fmt.Errorf("%w; additionally failed to materialize lease request: transcript store unavailable", err)
	}
	if !toolSessionKeyHasIdentity(key) {
		return fmt.Errorf("%w; additionally failed to materialize lease request: session identity unavailable", err)
	}
	requirement := normalizeMissingContinuationLeaseRequirement(missing.requirement)
	if requirement.AgentID == "" || requirement.Principal == "" || requirement.LeaseClass == "" {
		return fmt.Errorf("%w; additionally failed to materialize lease request: incomplete lease requirement", err)
	}
	now := time.Now().UTC()
	operationInput, opErr := missingContinuationLeaseOperationInputJSON(requirement)
	if opErr != nil {
		return fmt.Errorf("%w; additionally failed to materialize lease request: %v", err, opErr)
	}
	recordID := missingContinuationLeaseNextActionRecordID(key, requirement)
	_, recordErr := r.store.RecordNextAction(session.NextActionInput{
		RecordID:           recordID,
		Key:                key,
		Owner:              "tool",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         missingContinuationLeaseSubjectRef(requirement),
		CausalRefs:         missingContinuationLeaseCausalRefs(requirement),
		NextAction:         "approve a bounded child_wake continuation lease before retrying the blocked durable_agent wake_once invocation",
		RequiredAuthority:  string(requirement.LeaseClass),
		ResourceBlocker:    "missing_continuation_lease",
		RetryPolicy:        "retry_after_lease",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: operationInput,
		OperatorProjection: requirement.OperatorProjection,
		CreatedAt:          now,
	})
	if recordErr != nil {
		return fmt.Errorf("%w; additionally failed to materialize lease request: %v", err, recordErr)
	}
	return fmt.Errorf("missing continuation lease; recorded child_wake lease request %s", recordID)
}

func asMissingContinuationLeaseError(err error, target *missingContinuationLeaseError) bool {
	if err == nil || target == nil {
		return false
	}
	if typed, ok := err.(missingContinuationLeaseError); ok {
		*target = typed
		return true
	}
	type unwrapper interface {
		Unwrap() error
	}
	if wrapped, ok := err.(unwrapper); ok {
		return asMissingContinuationLeaseError(wrapped.Unwrap(), target)
	}
	return false
}

func normalizeMissingContinuationLeaseRequirement(requirement missingContinuationLeaseRequirement) missingContinuationLeaseRequirement {
	requirement.AgentID = strings.TrimSpace(requirement.AgentID)
	requirement.GrantID = strings.TrimSpace(requirement.GrantID)
	requirement.GrantTargetResource = strings.TrimSpace(requirement.GrantTargetResource)
	requirement.Principal = strings.TrimSpace(requirement.Principal)
	requirement.LeaseClass = session.NormalizeContinuationLeaseClass(requirement.LeaseClass)
	requirement.AllowedActions = normalizeUniqueStrings(requirement.AllowedActions)
	if len(requirement.AllowedActions) == 0 {
		requirement.AllowedActions = []string{durableAgentWakeOnceAction}
	}
	constraints := map[string]string{}
	for key, value := range requirement.Constraints {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		constraints[key] = value
	}
	if len(constraints) == 0 && requirement.AgentID != "" {
		constraints["agent_id"] = requirement.AgentID
	}
	requirement.Constraints = constraints
	if requirement.OperatorProjection == "" {
		requirement.OperatorProjection = fmt.Sprintf("Approve a bounded %s continuation lease before retrying durable_agent wake_once.", requirement.LeaseClass)
	}
	return requirement
}

func missingContinuationLeaseSubjectRef(requirement missingContinuationLeaseRequirement) string {
	requirement = normalizeMissingContinuationLeaseRequirement(requirement)
	return strings.Join([]string{
		string(requirement.LeaseClass),
		requirement.AgentID,
		requirement.GrantID,
	}, ":")
}

func missingContinuationLeaseCausalRefs(requirement missingContinuationLeaseRequirement) []string {
	requirement = normalizeMissingContinuationLeaseRequirement(requirement)
	refs := []string{
		"continuation_lease_class:" + string(requirement.LeaseClass),
		"durable_agent:" + requirement.AgentID,
	}
	if requirement.GrantID != "" {
		refs = append(refs, "capability_grant:"+requirement.GrantID)
	}
	if requirement.GrantTargetResource != "" {
		refs = append(refs, "capability:"+requirement.GrantTargetResource)
	}
	return refs
}

func missingContinuationLeaseNextActionRecordID(key session.SessionKey, requirement missingContinuationLeaseRequirement) string {
	requirement = normalizeMissingContinuationLeaseRequirement(requirement)
	seed := strings.Join([]string{
		session.SessionIDForKey(key),
		missingContinuationLeaseSubjectRef(requirement),
		string(session.NextActionBlockedNeedsAuthority),
	}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "next_missing_lease_" + hex.EncodeToString(sum[:12])
}

func missingContinuationLeaseOperationInputJSON(requirement missingContinuationLeaseRequirement) (string, error) {
	requirement = normalizeMissingContinuationLeaseRequirement(requirement)
	payload := map[string]any{
		"action":                "request_continuation_lease",
		"lease_class":           string(requirement.LeaseClass),
		"principal":             requirement.Principal,
		"allowed_actions":       requirement.AllowedActions,
		"constraints":           requirement.Constraints,
		"tool":                  "durable_agent",
		"tool_action":           "wake_once",
		"agent_id":              requirement.AgentID,
		"grant_id":              requirement.GrantID,
		"grant_target_resource": requirement.GrantTargetResource,
		"retry_after_lease":     true,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
