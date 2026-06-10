//go:build linux

package runtime

import (
	"fmt"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

const (
	heartbeatSemanticPressureThreshold = 0.60
	// Reflection support is intentionally enough to meet the support lane once
	// semantic pressure also crosses; self-sustaining curiosity is blocked at
	// curiosity candidacy instead of by making heartbeat outreach harder.
	heartbeatSupportPressureThreshold  = 0.20
	heartbeatCombinedPressureThreshold = 0.80
)

type heartbeatInteriorSignalEvaluation struct {
	Eligible bool
	Reason   string
	Refs     []session.InteriorSignalRef
}

func evaluateHeartbeatInteriorSignals(states []session.InteriorSignalState, now time.Time) heartbeatInteriorSignalEvaluation {
	semantic := strongestInteriorSignalState(states, hiddenInputSemanticRecurrence)
	if semantic == nil {
		return heartbeatInteriorSignalEvaluation{Reason: "semantic recurrence pressure absent"}
	}
	if session.InteriorSignalInCooldown(*semantic, now) {
		return heartbeatInteriorSignalEvaluation{Reason: "semantic recurrence pressure is cooling down"}
	}
	unresolved := strongestInteriorSignalState(states, hiddenInputUnresolvedMemory)
	temporal := strongestInteriorSignalState(states, hiddenInputTemporalPressure)
	support := strongerInteriorSignalState(unresolved, temporal)
	if support == nil {
		return heartbeatInteriorSignalEvaluation{Reason: "support pressure absent"}
	}
	if session.InteriorSignalInCooldown(*support, now) {
		return heartbeatInteriorSignalEvaluation{Reason: "support pressure is cooling down"}
	}
	combined := semantic.Intensity + support.Intensity
	if semantic.Intensity < heartbeatSemanticPressureThreshold {
		return heartbeatInteriorSignalEvaluation{Reason: fmt.Sprintf("semantic recurrence pressure %.2f below %.2f", semantic.Intensity, heartbeatSemanticPressureThreshold)}
	}
	if support.Intensity < heartbeatSupportPressureThreshold {
		return heartbeatInteriorSignalEvaluation{Reason: fmt.Sprintf("support pressure %.2f below %.2f", support.Intensity, heartbeatSupportPressureThreshold)}
	}
	if combined < heartbeatCombinedPressureThreshold {
		return heartbeatInteriorSignalEvaluation{Reason: fmt.Sprintf("combined pressure %.2f below %.2f", combined, heartbeatCombinedPressureThreshold)}
	}
	return heartbeatInteriorSignalEvaluation{
		Eligible: true,
		Reason:   fmt.Sprintf("pressure crossed threshold: semantic=%.2f support=%.2f combined=%.2f", semantic.Intensity, support.Intensity, combined),
		Refs: []session.InteriorSignalRef{
			{Category: semantic.Category, SubjectKey: semantic.SubjectKey},
			{Category: support.Category, SubjectKey: support.SubjectKey},
		},
	}
}

func strongestInteriorSignalState(states []session.InteriorSignalState, category string) *session.InteriorSignalState {
	var best *session.InteriorSignalState
	for i := range states {
		state := &states[i]
		if state.Category != category || state.Intensity <= 0.05 {
			continue
		}
		if best == nil || state.Intensity > best.Intensity {
			best = state
		}
	}
	return best
}

func strongerInteriorSignalState(a, b *session.InteriorSignalState) *session.InteriorSignalState {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if b.Intensity > a.Intensity {
		return b
	}
	return a
}
