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

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

const authorityBundleToolName = "authority_bundle"

func (r *Registry) authorityBundle(_ context.Context, input json.RawMessage, p principal.Principal, key session.SessionKey) (string, error) {
	if r.store == nil {
		return "", fmt.Errorf("authority_bundle requires transcript store")
	}
	if key.ChatID == 0 && key.UserID == 0 && key.Scope.IsZero() {
		return "", fmt.Errorf("authority_bundle requires session context")
	}
	var in authorityBundleInput
	if err := decodeToolObjectInput(input, &in, "authority_bundle"); err != nil {
		return "", err
	}
	switch normalizeShellAlternativeToken(in.Action) {
	case "propose":
		return r.proposeAuthorityBundle(in, p, key)
	case "show":
		return r.renderAuthorityBundleByID(strings.TrimSpace(in.BundleID))
	default:
		return "", fmt.Errorf("authority_bundle action must be propose or show")
	}
}

func (r *Registry) proposeAuthorityBundle(in authorityBundleInput, p principal.Principal, key session.SessionKey) (string, error) {
	now := time.Now().UTC()
	principalID := toolAuthorityCanonicalPrincipal(p)
	if principalID == "" {
		return "", fmt.Errorf("authority_bundle requires a principal")
	}
	sourceActions, err := r.authorityBundleSourceActions(key, in)
	if err != nil {
		return "", err
	}
	compiled, err := r.compileAuthorityBundleFromInput(in, key, principalID, sourceActions, now)
	if err != nil {
		return "", err
	}
	stored, err := r.store.UpsertAuthorityBundleContract(compiled)
	if err != nil {
		return "", err
	}
	operationInput, err := compileAuthorityBundleRecoveryHandoff(stored)
	if err != nil {
		return "", err
	}
	if _, err := r.store.RecordNextAction(session.NextActionInput{
		RecordID:           authorityBundleNextActionRecordID(key, stored.BundleID),
		Key:                key,
		Owner:              "tool",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "authority_bundle_request",
		SubjectRef:         stored.BundleID,
		CausalRefs:         authorityBundleCausalRefs(stored),
		NextAction:         "review the bounded authority bundle and approve only if the allowed, forbidden, and stop boundaries match the objective",
		RequiredAuthority:  "authority_bundle",
		ResourceBlocker:    "authority_bundle_approval",
		RetryPolicy:        "retry_after_bundle_approval",
		OperationKind:      operationInput.Kind,
		OperationTool:      operationInput.Tool,
		OperationInputJSON: operationInput.InputJSON,
		OperatorProjection: authorityBundleOperatorProjection(stored),
		CreatedAt:          now,
	}); err != nil {
		return "", err
	}
	return renderAuthorityBundle("[AUTHORITY_BUNDLE_PROPOSED]", stored), nil
}

func (r *Registry) compileAuthorityBundleFromInput(in authorityBundleInput, key session.SessionKey, principalID string, sourceActions []session.NextActionRecord, now time.Time) (session.AuthorityBundleContract, error) {
	primaryContractID := strings.TrimSpace(in.PrimaryContinuationContractID)
	requiredGrants := parseCapabilityGrantSpecInputs(in.RequiredCapabilityGrants)
	components := []session.AuthorityBundleComponent{}
	sourceIDs := append([]string(nil), in.SourceNextActionRecordIDs...)
	for _, action := range sourceActions {
		if !authorityBundleContains(sourceIDs, action.RecordID) {
			sourceIDs = append(sourceIDs, action.RecordID)
		}
		component, contractID, grants, err := r.authorityBundleComponentFromNextAction(action)
		if err != nil {
			return session.AuthorityBundleContract{}, err
		}
		if component.Kind != "" {
			components = append(components, component)
		}
		if primaryContractID == "" {
			primaryContractID = contractID
		}
		requiredGrants = append(requiredGrants, grants...)
	}
	for _, requestID := range authorityBundleStringList(in.CapabilityRequestIDs) {
		spec, err := r.authorityBundleGrantSpecForCapabilityRequest(requestID)
		if err != nil {
			return session.AuthorityBundleContract{}, err
		}
		requiredGrants = append(requiredGrants, spec)
		components = append(components, session.AuthorityBundleComponent{Kind: "capability_request", RefID: requestID})
	}
	if primaryContractID != "" {
		contract, ok, err := r.store.ContinuationRecoveryContract(primaryContractID)
		if err != nil {
			return session.AuthorityBundleContract{}, err
		}
		if !ok {
			return session.AuthorityBundleContract{}, fmt.Errorf("authority_bundle continuation contract %q not found", primaryContractID)
		}
		if contract.SessionID != "" && contract.SessionID != session.SessionIDForKey(key) {
			return session.AuthorityBundleContract{}, fmt.Errorf("authority_bundle continuation contract session mismatch")
		}
		requirement, err := requestApprovalContinuationLeaseRequirementFromContract(contract)
		if err != nil {
			return session.AuthorityBundleContract{}, err
		}
		requiredGrants = append(requiredGrants, requestApprovalContinuationLeaseGrantSpecs(requirement)...)
		components = append(components, session.AuthorityBundleComponent{Kind: "continuation_recovery_contract", RefID: primaryContractID, Subject: contract.SubjectKind, SubjectRef: contract.SubjectRef})
	}
	var err error
	requiredGrants, err = r.authorityBundleRequiredGrantSpecs(requiredGrants)
	if err != nil {
		return session.AuthorityBundleContract{}, err
	}
	expiresAt, err := parseOptionalAuthorityBundleTime(in.ExpiresAt)
	if err != nil {
		return session.AuthorityBundleContract{}, err
	}
	if expiresAt.IsZero() {
		expiresAt = now.Add(30 * time.Minute)
	}
	requestInstanceID := strings.TrimSpace(in.RequestInstanceID)
	if requestInstanceID == "" {
		requestInstanceID = authorityBundleRequestInstanceID(key, in, sourceIDs, now)
	}
	return session.CompileAuthorityBundleContract(session.AuthorityBundleContractInput{
		RequestInstanceID:             requestInstanceID,
		SessionID:                     session.SessionIDForKey(key),
		Principal:                     principalID,
		Objective:                     strings.TrimSpace(in.Objective),
		Summary:                       strings.TrimSpace(in.Summary),
		SourceNextActionRecordIDs:     sourceIDs,
		AllowedActions:                append([]string(nil), in.AllowedActions...),
		ForbiddenActions:              append([]string(nil), in.ForbiddenActions...),
		StopConditions:                append([]string(nil), in.StopConditions...),
		PrimaryContinuationContractID: primaryContractID,
		RequiredCapabilityGrants:      session.NormalizeCapabilityGrantSpecs(requiredGrants),
		Components:                    components,
		ExpiresAt:                     expiresAt,
		CreatedAt:                     now,
	})
}

func (r *Registry) authorityBundleRequiredGrantSpecs(specs []session.CapabilityGrantSpec) ([]session.CapabilityGrantSpec, error) {
	specs = session.NormalizeCapabilityGrantSpecs(specs)
	out := make([]session.CapabilityGrantSpec, 0, len(specs))
	for _, spec := range specs {
		resolved, covered, err := r.resolveAuthorityBundleRequiredGrantSpec(spec)
		if err != nil {
			return nil, err
		}
		if covered {
			continue
		}
		out = append(out, resolved)
	}
	return session.NormalizeCapabilityGrantSpecs(out), nil
}

func (r *Registry) resolveAuthorityBundleRequiredGrantSpec(spec session.CapabilityGrantSpec) (session.CapabilityGrantSpec, bool, error) {
	spec = session.NormalizeCapabilityGrantSpec(spec)
	request := session.CapabilityRequest{}
	if spec.RequestID != "" {
		stored, ok, err := r.store.CapabilityRequest(spec.RequestID)
		if err != nil {
			return session.CapabilityGrantSpec{}, false, err
		}
		if !ok {
			return session.CapabilityGrantSpec{}, false, fmt.Errorf("authority_bundle capability request %q not found", spec.RequestID)
		}
		request = session.NormalizeCapabilityRequest(stored)
		if request.ReviewStatus != session.CapabilityReviewStatusApproved &&
			request.ReviewStatus != session.CapabilityReviewStatusParentApproved &&
			request.ReviewStatus != session.CapabilityReviewStatusProposed {
			return session.CapabilityGrantSpec{}, false, fmt.Errorf("authority_bundle capability request %q has unsupported status %s", spec.RequestID, request.ReviewStatus)
		}
	}
	if spec.Kind == "" {
		spec.Kind = request.Kind
	}
	if spec.TargetResource == "" {
		spec.TargetResource = request.TargetResource
	}
	if spec.GrantedTo == "" {
		spec.GrantedTo = firstNonEmpty(request.RequestedFor, request.RequestedBy)
	}
	if spec.GrantID == "" {
		spec.GrantID = strings.TrimSpace(request.GrantID)
	}
	if spec.Contract == "" {
		spec.Contract = strings.TrimSpace(request.Contract)
	}
	if spec.Constraints == "" {
		spec.Constraints = strings.TrimSpace(request.Constraints)
	}
	if len(spec.AllowedActions) == 0 {
		spec.AllowedActions = []string{"invoke"}
	}
	spec = session.NormalizeCapabilityGrantSpec(spec)
	if spec.Kind == "" || spec.TargetResource == "" || spec.GrantedTo == "" {
		return session.CapabilityGrantSpec{}, false, fmt.Errorf("authority_bundle required capability grant spec for request %q is incomplete", spec.RequestID)
	}
	covered := true
	for _, action := range spec.AllowedActions {
		grant, ok, err := r.store.ActiveCapabilityGrant(spec.Kind, spec.TargetResource, spec.GrantedTo, action)
		if err != nil {
			return session.CapabilityGrantSpec{}, false, err
		}
		if !ok || grant.GrantID == "" {
			covered = false
			break
		}
	}
	return spec, covered, nil
}

func (r *Registry) authorityBundleSourceActions(key session.SessionKey, in authorityBundleInput) ([]session.NextActionRecord, error) {
	if !in.IncludeOpenAuthorityBlockers && len(in.SourceNextActionRecordIDs) == 0 {
		return nil, nil
	}
	open, err := r.store.OpenNextActionsBySession(key, 200)
	if err != nil {
		return nil, err
	}
	wanted := map[string]struct{}{}
	for _, id := range in.SourceNextActionRecordIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			wanted[id] = struct{}{}
		}
	}
	out := []session.NextActionRecord{}
	for _, action := range open {
		if _, ok := wanted[strings.TrimSpace(action.RecordID)]; ok {
			out = append(out, action)
			continue
		}
		if in.IncludeOpenAuthorityBlockers && authorityBundleAutoIncludesOpenAuthorityBlocker(action) {
			out = append(out, action)
		}
	}
	return out, nil
}

func authorityBundleAutoIncludesOpenAuthorityBlocker(action session.NextActionRecord) bool {
	if action.State != session.NextActionBlockedNeedsAuthority {
		return false
	}
	return strings.TrimSpace(action.OperationTool) == requestApprovalToolName &&
		strings.TrimSpace(action.OperationKind) == "continuation_lease_request"
}

func (r *Registry) authorityBundleComponentFromNextAction(action session.NextActionRecord) (session.AuthorityBundleComponent, string, []session.CapabilityGrantSpec, error) {
	if action.State != session.NextActionBlockedNeedsAuthority {
		return session.AuthorityBundleComponent{}, "", nil, nil
	}
	switch strings.TrimSpace(action.OperationTool) {
	case requestApprovalToolName:
		var in requestApprovalInput
		if err := decodeToolObjectInput(json.RawMessage(action.OperationInputJSON), &in, "request_approval"); err != nil {
			return session.AuthorityBundleComponent{}, "", nil, fmt.Errorf("decode authority bundle request_approval action %s: %w", action.RecordID, err)
		}
		if requestApprovalActionToken(in.Action) != "request_continuation_lease" || strings.TrimSpace(in.ContractID) == "" {
			return session.AuthorityBundleComponent{}, "", nil, nil
		}
		return session.AuthorityBundleComponent{Kind: "next_action", RefID: action.RecordID, Subject: action.SubjectKind, SubjectRef: action.SubjectRef}, strings.TrimSpace(in.ContractID), nil, nil
	case "capability_authority":
		var in capabilityInput
		if err := decodeToolObjectInput(json.RawMessage(action.OperationInputJSON), &in, "capability_authority"); err != nil {
			return session.AuthorityBundleComponent{}, "", nil, fmt.Errorf("decode authority bundle capability action %s: %w", action.RecordID, err)
		}
		if strings.TrimSpace(in.Action) != "grant_set" || strings.TrimSpace(in.RequestID) == "" {
			return session.AuthorityBundleComponent{}, "", nil, nil
		}
		spec, err := r.authorityBundleGrantSpecForCapabilityRequest(in.RequestID)
		if err != nil {
			return session.AuthorityBundleComponent{}, "", nil, err
		}
		if in.GrantID != "" {
			spec.GrantID = strings.TrimSpace(in.GrantID)
		}
		if in.Kind != "" {
			spec.Kind = session.CapabilityKind(in.Kind)
		}
		if in.TargetResource != "" {
			spec.TargetResource = strings.TrimSpace(in.TargetResource)
		}
		if in.Principal != "" {
			spec.GrantedTo = strings.TrimSpace(in.Principal)
		}
		if len(in.AllowedActions) > 0 {
			spec.AllowedActions = append([]string(nil), in.AllowedActions...)
		}
		spec = session.NormalizeCapabilityGrantSpec(spec)
		return session.AuthorityBundleComponent{Kind: "next_action", RefID: action.RecordID, Subject: action.SubjectKind, SubjectRef: action.SubjectRef}, "", []session.CapabilityGrantSpec{spec}, nil
	default:
		return session.AuthorityBundleComponent{}, "", nil, nil
	}
}

func (r *Registry) authorityBundleGrantSpecForCapabilityRequest(requestID string) (session.CapabilityGrantSpec, error) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return session.CapabilityGrantSpec{}, fmt.Errorf("authority_bundle capability request id is required")
	}
	request, ok, err := r.store.CapabilityRequest(requestID)
	if err != nil {
		return session.CapabilityGrantSpec{}, err
	}
	if !ok {
		return session.CapabilityGrantSpec{}, fmt.Errorf("authority_bundle capability request %q not found", requestID)
	}
	request = session.NormalizeCapabilityRequest(request)
	if request.ReviewStatus != session.CapabilityReviewStatusApproved && request.ReviewStatus != session.CapabilityReviewStatusParentApproved && request.ReviewStatus != session.CapabilityReviewStatusProposed {
		return session.CapabilityGrantSpec{}, fmt.Errorf("authority_bundle capability request %q has unsupported status %s", requestID, request.ReviewStatus)
	}
	actions := []string{"invoke"}
	return session.NormalizeCapabilityGrantSpec(session.CapabilityGrantSpec{
		RequestID:      request.RequestID,
		GrantID:        strings.TrimSpace(request.GrantID),
		Kind:           request.Kind,
		TargetResource: request.TargetResource,
		GrantedTo:      firstNonEmpty(request.RequestedFor, request.RequestedBy),
		AllowedActions: actions,
		Contract:       request.Contract,
		Constraints:    request.Constraints,
	}), nil
}

func authorityBundleNextActionRecordID(key session.SessionKey, bundleID string) string {
	return session.NextActionRecordID(session.SessionIDForKey(key), "authority_bundle_request", strings.TrimSpace(bundleID), session.NextActionBlockedNeedsAuthority, time.Unix(0, 0).UTC())
}

func authorityBundleRequestInstanceID(key session.SessionKey, in authorityBundleInput, sourceIDs []string, now time.Time) string {
	payload := map[string]any{
		"session_id": session.SessionIDForKey(key),
		"objective":  strings.TrimSpace(in.Objective),
		"summary":    strings.TrimSpace(in.Summary),
		"sources":    authorityBundleStringList(sourceIDs),
		"created_at": now.UTC().UnixNano(),
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	token := hex.EncodeToString(sum[:])
	if len(token) > 24 {
		token = token[:24]
	}
	return "bundle-request-" + token
}

func authorityBundleCausalRefs(bundle session.AuthorityBundleContract) []string {
	refs := []string{"authority_bundle:" + strings.TrimSpace(bundle.BundleID)}
	for _, id := range bundle.SourceNextActionRecordIDs {
		refs = append(refs, "next_action:"+strings.TrimSpace(id))
	}
	if id := strings.TrimSpace(bundle.PrimaryContinuationContractID); id != "" {
		refs = append(refs, "continuation_recovery_contract:"+id)
	}
	for _, grant := range bundle.RequiredCapabilityGrants {
		if id := strings.TrimSpace(grant.RequestID); id != "" {
			refs = append(refs, "capability_request:"+id)
		}
	}
	return authorityBundleStringList(refs)
}

func authorityBundleStringList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func authorityBundleContains(values []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}

func authorityBundleOperatorProjection(bundle session.AuthorityBundleContract) string {
	return fmt.Sprintf("Review bounded authority bundle %s. Allowed: %s. Forbidden: %s. Stop: %s.",
		strings.TrimSpace(bundle.BundleID),
		strings.Join(bundle.AllowedActions, ", "),
		strings.Join(bundle.ForbiddenActions, ", "),
		strings.Join(bundle.StopConditions, "; "),
	)
}

func renderAuthorityBundle(prefix string, bundle session.AuthorityBundleContract) string {
	lines := []string{
		prefix,
		"bundle_id: " + strings.TrimSpace(bundle.BundleID),
		"summary: " + strings.TrimSpace(bundle.Summary),
		"allowed: " + strings.Join(bundle.AllowedActions, ", "),
		"forbidden: " + strings.Join(bundle.ForbiddenActions, ", "),
		"stops: " + strings.Join(bundle.StopConditions, "; "),
	}
	if id := strings.TrimSpace(bundle.PrimaryContinuationContractID); id != "" {
		lines = append(lines, "primary_continuation_contract_id: "+id)
	}
	if len(bundle.RequiredCapabilityGrants) > 0 {
		lines = append(lines, fmt.Sprintf("required_capability_grants: %d", len(bundle.RequiredCapabilityGrants)))
	}
	return strings.Join(lines, "\n")
}

func (r *Registry) renderAuthorityBundleByID(bundleID string) (string, error) {
	bundle, ok, err := r.store.AuthorityBundleContract(bundleID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("authority bundle %q not found", bundleID)
	}
	return renderAuthorityBundle("[AUTHORITY_BUNDLE]", bundle), nil
}

func parseOptionalAuthorityBundleTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse authority bundle expires_at: %w", err)
	}
	return t.UTC(), nil
}

func compileAuthorityBundleRecoveryHandoff(contract session.AuthorityBundleContract) (recoveryHandoffOperation, error) {
	contract = session.NormalizeAuthorityBundleContract(contract)
	if strings.TrimSpace(contract.BundleID) == "" {
		return recoveryHandoffOperation{}, fmt.Errorf("incomplete authority bundle recovery handoff")
	}
	payload := map[string]any{
		"action":      "request_authority_bundle",
		"contract_id": contract.BundleID,
	}
	op, err := compileRecoveryHandoffOperation("authority_bundle_request", requestApprovalToolName, payload)
	if err != nil {
		return recoveryHandoffOperation{}, err
	}
	if err := ValidateRecoveryHandoffToolInput(session.NextActionBlockedNeedsAuthority, op.Tool, op.InputJSON); err != nil {
		return recoveryHandoffOperation{}, err
	}
	return op, nil
}

func authorityBundleToolDefinition() agent.ToolDef {
	return agent.ToolDef{
		Name:        authorityBundleToolName,
		Description: "Propose or inspect a bounded authority bundle from typed blockers. The model may draft boundaries, but only the compiled contract can be approved or used.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"action": {"type": "string", "enum": ["propose", "show"]},
				"bundle_id": {"type": "string", "description": "Compiled authority bundle id to inspect with action=show"},
				"request_instance_id": {"type": "string", "description": "Optional stable request instance id. Omit for a fresh one-time bundle."},
				"objective": {"type": "string", "description": "Objective this authority bundle serves"},
				"summary": {"type": "string", "description": "Short operator-readable bundle summary"},
				"source_next_action_record_ids": {"type": "array", "items": {"type": "string"}, "description": "Open typed blocker rows this bundle addresses"},
				"include_open_authority_blockers": {"type": "boolean", "description": "When true, include current open continuation-lease authority blockers in this session. Capability grant blockers must be named explicitly."},
				"allowed_actions": {"type": "array", "items": {"type": "string"}},
				"forbidden_actions": {"type": "array", "items": {"type": "string"}},
				"stop_conditions": {"type": "array", "items": {"type": "string"}},
				"primary_continuation_contract_id": {"type": "string", "description": "Existing continuation recovery contract id for the primary retry path"},
				"capability_request_ids": {"type": "array", "items": {"type": "string"}, "description": "Existing capability requests to include as required grants"},
				"required_capability_grants": {"type": "array", "items": {"type": "object"}, "description": "Optional exact grant specs to include"},
				"expires_at": {"type": "string", "description": "Optional RFC3339 expiration"}
			},
			"required": ["action"]
		}`),
	}
}
