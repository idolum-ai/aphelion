//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	toolpkg "github.com/idolum-ai/aphelion/tool"
)

type capabilityAccessDenial struct {
	Kind           session.CapabilityKind
	TargetResource string
	Principal      string
	Action         string
}

type missingCapabilityGrantMaterializer interface {
	MaterializeMissingGrantRequirement(context.Context, session.SessionKey, principal.Principal, toolpkg.MissingGrantRequirement, time.Time) (session.CapabilityRequest, int64, session.NextActionRecord, error)
}

func deniedCapabilityAccessFromAudit(audit *turnAuditRecorder) (capabilityAccessDenial, bool) {
	if audit == nil {
		return capabilityAccessDenial{}, false
	}
	snapshot := audit.Snapshot()
	for i := len(snapshot.ToolCalls) - 1; i >= 0; i-- {
		call := snapshot.ToolCalls[i]
		if strings.TrimSpace(call.Name) != "capability_authority" {
			continue
		}
		denial, ok := parseDeniedCapabilityAccess(call.OutputPreview)
		if ok {
			return denial, true
		}
	}
	return capabilityAccessDenial{}, false
}

func parseDeniedCapabilityAccess(output string) (capabilityAccessDenial, bool) {
	values := map[string]string{}
	seenHeader := false
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "[CAPABILITY_ACCESS]" {
			seenHeader = true
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		values[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	if !seenHeader || !strings.EqualFold(values["allowed"], "false") {
		return capabilityAccessDenial{}, false
	}
	kind := session.NormalizeCapabilityKind(session.CapabilityKind(values["kind"]))
	target := strings.TrimSpace(values["target_resource"])
	principalID := strings.TrimSpace(values["principal"])
	action := strings.TrimSpace(values["action"])
	if action == "" {
		action = "invoke"
	}
	actions := session.NormalizeCapabilityActions([]string{action})
	if len(actions) == 0 {
		return capabilityAccessDenial{}, false
	}
	action = actions[0]
	if kind == "" || target == "" || principalID == "" {
		return capabilityAccessDenial{}, false
	}
	return capabilityAccessDenial{
		Kind:           kind,
		TargetResource: target,
		Principal:      principalID,
		Action:         action,
	}, true
}

func (r *Runtime) materializeDeniedCapabilityAccessApproval(ctx context.Context, key session.SessionKey, msg core.InboundMessage, audit *turnAuditRecorder, now time.Time) (bool, error) {
	if r == nil || r.tools == nil {
		return false, nil
	}
	denial, ok := deniedCapabilityAccessFromAudit(audit)
	if !ok {
		return false, nil
	}
	actor, ok := r.recoveryApprovalMaterializationActor(msg)
	if !ok {
		return false, nil
	}
	materializer, ok := r.tools.(missingCapabilityGrantMaterializer)
	if !ok {
		return false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	_, _, _, err := materializer.MaterializeMissingGrantRequirement(ctx, key, actor, missingCapabilityGrantRequirementFromDenial(denial), now)
	if err != nil {
		return false, err
	}
	return true, nil
}

func missingCapabilityGrantRequirementFromDenial(denial capabilityAccessDenial) toolpkg.MissingGrantRequirement {
	contract := fmt.Sprintf(
		`{"bounded_effect":"Grant the denied capability only for the blocked operation that produced this access_check.","source":"capability_authority.access_check","kind":%q,"target_resource":%q,"requested_for":%q,"action":%q}`,
		string(denial.Kind),
		denial.TargetResource,
		denial.Principal,
		denial.Action,
	)
	return toolpkg.MissingGrantRequirement{
		Kind:               denial.Kind,
		TargetResource:     denial.TargetResource,
		GrantedTo:          denial.Principal,
		AllowedActions:     []string{denial.Action},
		Contract:           contract,
		Constraints:        "{}",
		Purpose:            fmt.Sprintf("Grant %s access to %s for %s after a denied capability access check.", denial.Action, denial.TargetResource, denial.Principal),
		RiskClass:          "authority",
		ReviewSummary:      fmt.Sprintf("Missing capability grant: kind=%s target=%s principal=%s action=%s", denial.Kind, denial.TargetResource, denial.Principal, denial.Action),
		OperatorProjection: fmt.Sprintf("%s is blocked because %s lacks %s access to %s. Review the exact capability request, then retry the operation.", denial.Principal, denial.Principal, denial.Action, denial.TargetResource),
		OperationKind:      "capability_grant_review",
		OperationTool:      "capability_authority",
	}
}
