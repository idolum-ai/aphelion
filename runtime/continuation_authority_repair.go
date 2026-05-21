//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) repairContinuationAuthorityContradictions(ctx context.Context, now time.Time) (int, error) {
	if r == nil || r.store == nil {
		return 0, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	records, err := r.store.ContinuationStates()
	if err != nil {
		return 0, fmt.Errorf("load continuation states for authority repair: %w", err)
	}
	repaired := 0
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return repaired, err
		}
		if !rawContinuationStateAuthorityNeedsSanitization(record.RawJSON, record.State) {
			continue
		}
		compilation := session.CompileContinuationAuthorityContract(record.State)
		state := session.NormalizeContinuationState(record.State)
		status := "authority_sanitized"
		payload := map[string]any{
			"phase":       "continuation_authority_sanitizer",
			"status":      string(state.Status),
			"lease_class": string(state.ContinuationLease.LeaseClass),
		}
		if compilation.Invalid() {
			state = continuationStateWithInvalidAuthorityContract(state, compilation, now)
			status = "authority_contract_invalid_superseded"
			payload["authority_contract_status"] = string(compilation.Status)
			payload["authority_contract_work_action"] = strings.TrimSpace(compilation.WorkAction)
			payload["authority_contract_suggested_repair"] = strings.TrimSpace(compilation.SuggestedRepair)
			payload["authority_contract_contradictions"] = compilation.Contradictions
			payload["reason"] = "invalid_authority_contract"
		}
		if err := r.store.UpdateContinuationState(record.Key, state); err != nil {
			return repaired, fmt.Errorf("repair continuation authority chat_id=%d: %w", record.Key.ChatID, err)
		}
		repaired++
		r.recordExecutionEvent(record.Key, core.ExecutionEventRecoveryCompleted, "recovery", status, payload, now)
	}
	return repaired, nil
}

func rawContinuationStateAuthorityNeedsSanitization(raw string, fallback session.ContinuationState) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return session.ContinuationStateAuthorityNeedsSanitization(fallback)
	}
	var state session.ContinuationState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return session.ContinuationStateAuthorityNeedsSanitization(fallback)
	}
	return session.ContinuationStateAuthorityNeedsSanitization(state)
}
