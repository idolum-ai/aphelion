//go:build linux

package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/commandeffect"
	"github.com/idolum-ai/aphelion/effectauth"
	runtimecodex "github.com/idolum-ai/aphelion/runtime/codex"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) recordCodexCommandApprovalAttempt(req WorkRequest, command string, decision effectauth.Decision, params map[string]any, ordinal int, now time.Time) error {
	if r == nil || r.store == nil {
		return fmt.Errorf("effect attempt store unavailable")
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	effect := commandeffect.Classify(command)
	if !effect.SideEffects {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	key := workRequestEffectAttemptKey(req)
	safeCommand := redactRuntimeEvidenceText(commandeffect.NormalizeCommand(command))
	boundaryKind := ""
	if boundary, ok := commandeffect.BoundaryForCommand(command); ok {
		boundaryKind = string(boundary.Kind)
	}
	_, err := r.store.UpsertEffectAttempt(session.EffectAttemptInput{
		AttemptID:           codexApprovalAttemptID(key, req, "command", command, params, ordinal),
		Key:                 key,
		OperationID:         firstNonEmptyContinuation(req.OperationID, req.Operation.ID),
		PhaseID:             effectAttemptPhaseID(req),
		LeaseID:             req.LeaseID,
		ProposalID:          req.State.ActionProposal.ID,
		WorkMode:            string(req.Mode),
		Executor:            "codex",
		Tool:                "codex_command_approval",
		Command:             safeCommand,
		EffectKind:          string(effect.Kind),
		EffectReason:        effect.Reason,
		BoundaryKind:        boundaryKind,
		AuthorizationReason: strings.TrimSpace(decision.Reason),
		SubjectJSON:         effectAttemptSubjectJSON(command),
		Status:              session.EffectAttemptStatusAttempted,
		EvidenceRefs:        codexApprovalEvidenceRefs(params),
		StartedAt:           now,
		UpdatedAt:           now,
	})
	if err != nil {
		return err
	}
	return nil
}

func (r *Runtime) recordCodexFileChangeApprovalAttempt(req WorkRequest, params map[string]any, decision effectauth.Decision, ordinal int, now time.Time) error {
	if r == nil || r.store == nil {
		return fmt.Errorf("effect attempt store unavailable")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	key := workRequestEffectAttemptKey(req)
	paths := codexFileChangePaths(params)
	fingerprint := codexFileChangeFingerprint(params, paths)
	command := "codex file_change " + fingerprint
	subject := codexFileChangeSubjectJSON(params, paths, fingerprint)
	_, err := r.store.UpsertEffectAttempt(session.EffectAttemptInput{
		AttemptID:           codexApprovalAttemptID(key, req, "file_change", command, params, ordinal),
		Key:                 key,
		OperationID:         firstNonEmptyContinuation(req.OperationID, req.Operation.ID),
		PhaseID:             effectAttemptPhaseID(req),
		LeaseID:             req.LeaseID,
		ProposalID:          req.State.ActionProposal.ID,
		WorkMode:            string(req.Mode),
		Executor:            "codex",
		Tool:                "codex_file_change_approval",
		Command:             command,
		EffectKind:          string(commandeffect.KindWorkspaceMutation),
		EffectReason:        "codex file change approval",
		AuthorizationReason: strings.TrimSpace(decision.Reason),
		SubjectJSON:         subject,
		Status:              session.EffectAttemptStatusAttempted,
		EvidenceRefs:        codexApprovalEvidenceRefs(params),
		StartedAt:           now,
		UpdatedAt:           now,
	})
	if err != nil {
		return err
	}
	return nil
}

func workRequestEffectAttemptKey(req WorkRequest) session.SessionKey {
	key := req.Key
	if key.ChatID == 0 {
		key.ChatID = req.ChatID
	}
	if key.ChatID != 0 && strings.TrimSpace(string(key.Scope.Kind)) == "" && strings.TrimSpace(key.Scope.ID) == "" {
		key.Scope = telegramDMScopeRef(key.ChatID)
	}
	return key
}

func codexApprovalAttemptID(key session.SessionKey, req WorkRequest, kind string, command string, params map[string]any, ordinal int) string {
	seed := strings.Join([]string{
		session.SessionIDForKey(key),
		firstNonEmptyContinuation(req.OperationID, req.Operation.ID),
		effectAttemptPhaseID(req),
		strings.TrimSpace(req.LeaseID),
		strings.TrimSpace(req.State.ActionProposal.ID),
		string(req.Mode),
		strings.TrimSpace(kind),
		firstNonEmptyContinuation(
			runtimecodex.StringField(params, "item_id"),
			runtimecodex.StringField(params, "itemId"),
			runtimecodex.StringField(params, "request_id"),
			runtimecodex.StringField(params, "requestId"),
			fmt.Sprintf("ordinal:%d", ordinal),
		),
		session.EffectAttemptCommandHash(command),
	}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "eff_" + hex.EncodeToString(sum[:16])
}

func codexApprovalEvidenceRefs(params map[string]any) []string {
	var refs []string
	for _, key := range []string{"item_id", "itemId", "request_id", "requestId", "turn_id", "turnId"} {
		if value := runtimecodex.StringField(params, key); strings.TrimSpace(value) != "" {
			refs = append(refs, "codex_approval:"+strings.TrimSpace(value))
		}
	}
	return refs
}

func codexFileChangeFingerprint(params map[string]any, paths []string) string {
	patch := firstNonEmptyContinuation(
		runtimecodex.StringField(params, "patch"),
		runtimecodex.StringField(params, "diff"),
		runtimecodex.StringField(params, "preview"),
		runtimecodex.StringField(params, "reason"),
	)
	seed := strings.Join(append(append([]string(nil), paths...), patch), "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func codexFileChangeSubjectJSON(params map[string]any, paths []string, fingerprint string) string {
	payload := map[string]any{
		"kind":        "codex_file_change",
		"paths":       paths,
		"patch_hash":  strings.TrimSpace(fingerprint),
		"operation":   firstNonEmptyContinuation(runtimecodex.StringField(params, "operation"), runtimecodex.StringField(params, "op")),
		"provider_id": firstNonEmptyContinuation(runtimecodex.StringField(params, "item_id"), runtimecodex.StringField(params, "itemId"), runtimecodex.StringField(params, "request_id"), runtimecodex.StringField(params, "requestId")),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}
	return session.RedactEvidenceText(string(raw)).Text
}
