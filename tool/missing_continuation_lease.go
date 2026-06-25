//go:build linux

package tool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

type missingContinuationLeaseRequirement struct {
	AgentID             string
	Resource            string
	GrantID             string
	GrantTargetResource string
	RequestInstanceID   string
	Principal           string
	LeaseClass          session.ContinuationLeaseClass
	AllowedActions      []string
	Constraints         map[string]string
	Tool                string
	ToolAction          string
	NextAction          string
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
	if requirement.Tool != "" && requirement.LeaseClass != "" {
		return fmt.Sprintf("missing %s continuation lease for %s", requirement.LeaseClass, requirement.Tool)
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
		Tool:                "durable_agent",
		ToolAction:          "wake_once",
		NextAction:          "approve a bounded child_wake continuation lease before retrying the blocked durable_agent wake_once invocation",
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
	if missingContinuationLeaseSubjectToken(requirement) == "" || requirement.Principal == "" || requirement.LeaseClass == "" {
		return fmt.Errorf("%w; additionally failed to materialize lease request: incomplete lease requirement", err)
	}
	now := time.Now().UTC()
	subjectRef := missingContinuationLeaseSubjectRef(requirement)
	if existing, ok, lookupErr := r.openMissingContinuationLeaseAction(key, subjectRef); lookupErr != nil {
		return fmt.Errorf("%w; additionally failed to inspect existing lease request: %v", err, lookupErr)
	} else if ok {
		return safeToolFailureError{
			class:       "authority_rejected",
			summary:     fmt.Sprintf("tool execution failed: missing %s continuation lease; lease request already recorded", requirement.LeaseClass),
			retryPolicy: "ask_for_grant",
			cause:       fmt.Errorf("%w; existing next action %s", err, strings.TrimSpace(existing.RecordID)),
		}
	}
	requirement.RequestInstanceID = missingContinuationLeaseRequestInstanceID(key, requirement, now)
	operation, opErr := compileContinuationLeaseRecoveryHandoff(requirement)
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
		SubjectRef:         subjectRef,
		CausalRefs:         missingContinuationLeaseCausalRefs(requirement),
		NextAction:         firstNonEmpty(requirement.NextAction, "approve a bounded continuation lease before retrying the blocked tool invocation"),
		RequiredAuthority:  string(requirement.LeaseClass),
		ResourceBlocker:    "missing_continuation_lease",
		RetryPolicy:        "retry_after_lease",
		OperationKind:      operation.Kind,
		OperationTool:      operation.Tool,
		OperationInputJSON: operation.InputJSON,
		OperatorProjection: requirement.OperatorProjection,
		CreatedAt:          now,
	})
	if recordErr != nil {
		return fmt.Errorf("%w; additionally failed to materialize lease request: %v", err, recordErr)
	}
	return safeToolFailureError{
		class:       "authority_rejected",
		summary:     fmt.Sprintf("tool execution failed: missing %s continuation lease; lease request recorded", requirement.LeaseClass),
		retryPolicy: "ask_for_grant",
		cause:       err,
	}
}

func (r *Registry) openMissingContinuationLeaseAction(key session.SessionKey, subjectRef string) (session.NextActionRecord, bool, error) {
	if r == nil || r.store == nil {
		return session.NextActionRecord{}, false, nil
	}
	open, err := r.store.OpenNextActionsBySession(key, 100)
	if err != nil {
		return session.NextActionRecord{}, false, err
	}
	for _, action := range open {
		if action.SubjectKind == "continuation_lease_request" && strings.TrimSpace(action.SubjectRef) == strings.TrimSpace(subjectRef) {
			return action, true, nil
		}
	}
	return session.NextActionRecord{}, false, nil
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
	requirement.Resource = strings.TrimSpace(requirement.Resource)
	requirement.GrantID = strings.TrimSpace(requirement.GrantID)
	requirement.GrantTargetResource = strings.TrimSpace(requirement.GrantTargetResource)
	requirement.RequestInstanceID = strings.TrimSpace(requirement.RequestInstanceID)
	requirement.Principal = strings.TrimSpace(requirement.Principal)
	requirement.LeaseClass = session.NormalizeContinuationLeaseClass(requirement.LeaseClass)
	requirement.Tool = strings.TrimSpace(requirement.Tool)
	requirement.ToolAction = strings.ToLower(strings.TrimSpace(requirement.ToolAction))
	requirement.ToolAction = strings.ReplaceAll(requirement.ToolAction, "-", "_")
	requirement.ToolAction = strings.ReplaceAll(requirement.ToolAction, " ", "_")
	requirement.NextAction = strings.TrimSpace(requirement.NextAction)
	requirement.AllowedActions = normalizeUniqueStrings(requirement.AllowedActions)
	if len(requirement.AllowedActions) == 0 {
		switch requirement.LeaseClass {
		case session.ContinuationLeaseClassDataAccess:
			requirement.AllowedActions = []string{"read_approved_resource"}
		case session.ContinuationLeaseClassLocalWorkspace:
			requirement.AllowedActions = []string{"edit_files"}
		case session.ContinuationLeaseClassChildWake:
			requirement.AllowedActions = []string{durableAgentWakeOnceAction}
		}
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
	parts := []string{
		string(requirement.LeaseClass),
		missingContinuationLeaseSubjectToken(requirement),
		requirement.GrantID,
	}
	if requirement.Tool != "" {
		parts = append(parts, "tool="+requirement.Tool)
	}
	if requirement.ToolAction != "" {
		parts = append(parts, "action="+requirement.ToolAction)
	}
	if requirement.Resource != "" {
		parts = append(parts, "resource="+missingContinuationLeaseHashToken(requirement.Resource))
	}
	return strings.Join(parts, ":")
}

func missingContinuationLeaseSubjectToken(requirement missingContinuationLeaseRequirement) string {
	requirement = normalizeMissingContinuationLeaseRequirement(requirement)
	if requirement.AgentID != "" {
		return requirement.AgentID
	}
	if requirement.GrantID != "" {
		return requirement.GrantID
	}
	if requirement.Resource != "" {
		sum := sha256.Sum256([]byte(requirement.Resource))
		return "resource-" + hex.EncodeToString(sum[:8])
	}
	if requirement.Tool != "" {
		return requirement.Tool
	}
	return ""
}

func missingContinuationLeaseCausalRefs(requirement missingContinuationLeaseRequirement) []string {
	requirement = normalizeMissingContinuationLeaseRequirement(requirement)
	refs := []string{
		"continuation_lease_class:" + string(requirement.LeaseClass),
	}
	if requirement.AgentID != "" {
		refs = append(refs, "durable_agent:"+requirement.AgentID)
	}
	if requirement.GrantID != "" {
		refs = append(refs, "capability_grant:"+requirement.GrantID)
	}
	if requirement.GrantTargetResource != "" {
		refs = append(refs, "capability:"+requirement.GrantTargetResource)
	}
	if requirement.Tool != "" {
		refs = append(refs, "tool:"+requirement.Tool)
	}
	if requirement.ToolAction != "" {
		refs = append(refs, "tool_action:"+requirement.ToolAction)
	}
	if requirement.Resource != "" {
		refs = append(refs, "resource:"+missingContinuationLeaseHashToken(requirement.Resource))
	}
	return refs
}

func missingContinuationLeaseHashToken(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:8])
}

func missingContinuationLeaseNextActionRecordID(key session.SessionKey, requirement missingContinuationLeaseRequirement) string {
	requirement = normalizeMissingContinuationLeaseRequirement(requirement)
	seed := strings.Join([]string{
		session.SessionIDForKey(key),
		missingContinuationLeaseSubjectRef(requirement),
		requirement.RequestInstanceID,
		string(session.NextActionBlockedNeedsAuthority),
	}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "next_missing_lease_" + hex.EncodeToString(sum[:12])
}

func missingContinuationLeaseRequestInstanceID(key session.SessionKey, requirement missingContinuationLeaseRequirement, now time.Time) string {
	requirement = normalizeMissingContinuationLeaseRequirement(requirement)
	if requirement.RequestInstanceID != "" {
		return requirement.RequestInstanceID
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	seed := strings.Join([]string{
		session.SessionIDForKey(key),
		missingContinuationLeaseSubjectRef(requirement),
		now.UTC().Format(time.RFC3339Nano),
	}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "lease-request-instance-" + hex.EncodeToString(sum[:12])
}
