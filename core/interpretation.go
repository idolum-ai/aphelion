//go:build linux

package core

import (
	"fmt"
	"strings"
)

const InterpretationSchemaV1 = "aphelion.interpretation.v1"

// InterpretationClaim is the typed handoff between open language and runtime
// checks. Text may explain a claim, but permission and routing code should
// consume these fields.
type InterpretationClaim struct {
	SchemaVersion      string   `json:"schema_version,omitempty"`
	Intent             string   `json:"intent,omitempty"`
	AuthorityClass     string   `json:"authority_class,omitempty"`
	Scope              string   `json:"scope,omitempty"`
	ConsentSubject     string   `json:"consent_subject,omitempty"`
	Risk               []string `json:"risk,omitempty"`
	MissingContext     []string `json:"missing_context,omitempty"`
	Confidence         string   `json:"confidence,omitempty"`
	EvidenceRefs       []string `json:"evidence_refs,omitempty"`
	ProposedNextAction string   `json:"proposed_next_action,omitempty"`
	Source             string   `json:"source,omitempty"`
}

func NormalizeInterpretationClaim(claim InterpretationClaim) InterpretationClaim {
	claim.SchemaVersion = strings.TrimSpace(claim.SchemaVersion)
	if claim.SchemaVersion == "" {
		claim.SchemaVersion = InterpretationSchemaV1
	}
	claim.Intent = normalizeInterpretationToken(claim.Intent)
	claim.AuthorityClass = normalizeInterpretationToken(claim.AuthorityClass)
	claim.Scope = normalizeInterpretationToken(claim.Scope)
	claim.ConsentSubject = normalizeInterpretationToken(claim.ConsentSubject)
	claim.Risk = normalizeInterpretationList(claim.Risk)
	claim.MissingContext = normalizeInterpretationList(claim.MissingContext)
	claim.Confidence = normalizeInterpretationToken(claim.Confidence)
	claim.EvidenceRefs = normalizeInterpretationList(claim.EvidenceRefs)
	claim.ProposedNextAction = normalizeInterpretationToken(claim.ProposedNextAction)
	claim.Source = normalizeInterpretationToken(claim.Source)
	return claim
}

func (c InterpretationClaim) Active() bool {
	claim := NormalizeInterpretationClaim(c)
	return claim.Intent != "" ||
		claim.AuthorityClass != "" ||
		claim.Scope != "" ||
		claim.ConsentSubject != "" ||
		len(claim.Risk) > 0 ||
		len(claim.MissingContext) > 0 ||
		claim.ProposedNextAction != ""
}

type DebugBreadcrumb struct {
	TraceID          string `json:"trace_id,omitempty"`
	CanonicalRecord  string `json:"canonical_record,omitempty"`
	Projection       string `json:"projection,omitempty"`
	InspectCommand   string `json:"inspect_command,omitempty"`
	CodeOwner        string `json:"code_owner,omitempty"`
	NextRepairAction string `json:"next_repair_action,omitempty"`
}

func NormalizeDebugBreadcrumb(b DebugBreadcrumb) DebugBreadcrumb {
	b.TraceID = strings.TrimSpace(b.TraceID)
	b.CanonicalRecord = strings.TrimSpace(b.CanonicalRecord)
	b.Projection = strings.TrimSpace(b.Projection)
	b.InspectCommand = strings.TrimSpace(b.InspectCommand)
	b.CodeOwner = strings.TrimSpace(b.CodeOwner)
	b.NextRepairAction = strings.TrimSpace(b.NextRepairAction)
	return b
}

func (b DebugBreadcrumb) Active() bool {
	crumb := NormalizeDebugBreadcrumb(b)
	return crumb.TraceID != "" ||
		crumb.CanonicalRecord != "" ||
		crumb.Projection != "" ||
		crumb.InspectCommand != "" ||
		crumb.CodeOwner != "" ||
		crumb.NextRepairAction != ""
}

func DebugBreadcrumbLines(b DebugBreadcrumb) []string {
	crumb := NormalizeDebugBreadcrumb(b)
	if !crumb.Active() {
		return nil
	}
	lines := make([]string, 0, 6)
	if crumb.TraceID != "" {
		lines = append(lines, "- trace_id: "+crumb.TraceID)
	}
	if crumb.CanonicalRecord != "" {
		lines = append(lines, "- canonical_record: "+crumb.CanonicalRecord)
	}
	if crumb.Projection != "" {
		lines = append(lines, "- projection: "+crumb.Projection)
	}
	if crumb.InspectCommand != "" {
		lines = append(lines, "- inspect_command: "+crumb.InspectCommand)
	}
	if crumb.CodeOwner != "" {
		lines = append(lines, "- code_owner: "+crumb.CodeOwner)
	}
	if crumb.NextRepairAction != "" {
		lines = append(lines, "- next_repair_action: "+crumb.NextRepairAction)
	}
	return lines
}

func ContinuationDebugBreadcrumb(chatID int64, decisionID string, projection string, codeOwner string, nextRepairAction string) DebugBreadcrumb {
	decisionID = strings.TrimSpace(decisionID)
	traceID := "continuation"
	canonical := "continuation_state"
	if chatID != 0 {
		traceID = fmt.Sprintf("continuation:%d", chatID)
		canonical = fmt.Sprintf("continuation_state chat_id=%d", chatID)
	}
	if decisionID != "" {
		traceID += ":" + decisionID
		canonical += " decision_id=" + decisionID
	}
	return NormalizeDebugBreadcrumb(DebugBreadcrumb{
		TraceID:          traceID,
		CanonicalRecord:  canonical,
		Projection:       projection,
		InspectCommand:   continuationInspectCommand(chatID),
		CodeOwner:        codeOwner,
		NextRepairAction: nextRepairAction,
	})
}

func continuationInspectCommand(chatID int64) string {
	if chatID == 0 {
		return "/health trace"
	}
	return fmt.Sprintf("/health trace %d", chatID)
}

func normalizeInterpretationToken(value string) string {
	return strings.TrimSpace(value)
}

func normalizeInterpretationList(values []string) []string {
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
