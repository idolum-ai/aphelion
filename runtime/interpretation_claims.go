//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
)

const (
	interpretationClaimsMarker = "INTERPRETATION_CLAIMS"
	interpretationClaimsSchema = "aphelion.interpretation_claims.v1"
)

type interpretationClaimsContract struct {
	SchemaVersion string                     `json:"schema_version"`
	Surface       string                     `json:"surface,omitempty"`
	Claims        []core.InterpretationClaim `json:"claims,omitempty"`
}

type interpretationRequest struct {
	Surface   string `json:"surface"`
	Text      string `json:"text,omitempty"`
	HasAudio  bool   `json:"has_audio,omitempty"`
	HasMedia  bool   `json:"has_media,omitempty"`
	ScopeHint string `json:"scope_hint,omitempty"`
}

func (r *Runtime) interpretCurrentTurnClaims(ctx context.Context, req interpretationRequest) []core.InterpretationClaim {
	provider := r.interpretationProvider()
	if provider == nil {
		return nil
	}
	req.Surface = strings.TrimSpace(req.Surface)
	req.Text = strings.TrimSpace(req.Text)
	if req.Surface == "" {
		return nil
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return nil
	}
	messages := []agent.Message{
		{
			Role: "system",
			Content: strings.Join([]string{
				"You are the runtime interpretation role.",
				"Deliberate only about what typed interpretation claims are present in the supplied surface.",
				"Do not grant authority and do not decide execution. Runtime validators will check claims against leases, grants, operation state, TES, and sandbox policy.",
				"Return exactly one line: " + interpretationClaimsMarker + `: {"schema_version":"` + interpretationClaimsSchema + `","surface":"...","claims":[...]}`,
				"Allowed claim intents include reply_execution_claim, pending_media_intent, media_reply_modality, authority_classification, consent_requirement, continuation_goal, and missing_context.",
				"For final replies, emit reply_execution_claim when the text claims current-turn completion, tool execution, test execution, durable-agent lifecycle work, active continuation execution, or granted approval/authority. Use risks completion, tool_execution, test_execution, durable_agent, continuation_execution, and approval_granted. Do not emit continuation_execution or approval_granted for explicit parked states such as needing fresh approval before continuing.",
				"For goal continuation, emit continuation_goal with scope goal_continuation only when the supplied surface explicitly supports a completed approved/consumed phase and a distinct follow-up approval proposal. Use risks prior_phase_completed, remaining_goal_work, branch_pr_followup, release_readiness_followup, or interrupted_or_failed. Use proposed_next_action propose_read_only_next_phase, propose_commit_push_pr, propose_release_readiness, or do_not_infer. If the phase was budget-interrupted, failed, unclear, or only asks to retry unfinished work, emit interrupted_or_failed or return no proposal action.",
				"For media turns, emit pending_media_intent for a next-audio transcription instruction, or media_reply_modality for a current audio transcription instruction.",
				"If no typed claim is present, return an empty claims array.",
			}, "\n"),
		},
		{
			Role:    "user",
			Content: string(raw),
		},
	}
	resp, err := provider.Complete(ctx, messages, nil)
	if err != nil || resp == nil {
		return nil
	}
	return parseInterpretationClaimsContract(resp.Content)
}

func (r *Runtime) interpretationProvider() agent.Provider {
	if r == nil {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(r.governorBackend), "codex") {
		return nil
	}
	if r.native != nil {
		return r.native
	}
	return r.provider
}

func parseInterpretationClaimsContract(raw string) []core.InterpretationClaim {
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil
	}
	if rest, ok := strings.CutPrefix(text, interpretationClaimsMarker+":"); ok {
		text = strings.TrimSpace(rest)
	} else if idx := strings.Index(text, interpretationClaimsMarker+":"); idx >= 0 {
		text = strings.TrimSpace(text[idx+len(interpretationClaimsMarker)+1:])
	}
	var contract interpretationClaimsContract
	if err := json.Unmarshal([]byte(text), &contract); err != nil {
		return nil
	}
	if schema := strings.TrimSpace(contract.SchemaVersion); schema != "" && schema != interpretationClaimsSchema {
		return nil
	}
	out := make([]core.InterpretationClaim, 0, len(contract.Claims))
	for _, claim := range contract.Claims {
		claim = core.NormalizeInterpretationClaim(claim)
		if !claim.Active() {
			continue
		}
		out = append(out, claim)
	}
	return out
}

func interpretationClaimsWithIntent(claims []core.InterpretationClaim, intent string) []core.InterpretationClaim {
	intent = strings.TrimSpace(intent)
	if intent == "" {
		return nil
	}
	out := make([]core.InterpretationClaim, 0, len(claims))
	for _, claim := range claims {
		claim = core.NormalizeInterpretationClaim(claim)
		if claim.Intent == intent {
			out = append(out, claim)
		}
	}
	return out
}
