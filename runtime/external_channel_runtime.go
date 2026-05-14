//go:build linux

package runtime

import (
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

type externalChannelCommandSpec struct {
	Adapter       string
	Name          string
	ReadOnly      bool
	Mutates       bool
	GrantRequired string
	RetentionRisk []string
}

type externalChannelCommandLifecycle struct {
	Adapter      string
	Command      string
	SessionRef   string
	LastArtifact string
	LastStatus   string
	LastError    string
	BackoffUntil time.Time
	ResetBackoff bool
}

func externalChannelAdapter(agent core.DurableAgent) string {
	external := agent.ChannelConfig.ExternalConfig()
	if external == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(external.Adapter))
}

func externalChannelStateForAdapter(continuity core.DurableAgentContinuityState, adapter string) core.DurableAgentExternalChannelRuntimeState {
	state := core.DurableAgentExternalChannelRuntimeState{}
	if continuity.ExternalChannel != nil {
		state = *continuity.ExternalChannel
	}
	adapter = strings.ToLower(strings.TrimSpace(adapter))
	if strings.TrimSpace(state.Adapter) != "" && adapter != "" && !strings.EqualFold(strings.TrimSpace(state.Adapter), adapter) {
		return core.DurableAgentExternalChannelRuntimeState{}
	}
	return state
}

func externalChannelPollDue(state core.DurableAgentExternalChannelRuntimeState, intervalRaw string, now time.Time) bool {
	now = now.UTC()
	if !state.BackoffUntil.IsZero() && now.Before(state.BackoffUntil.UTC()) {
		return false
	}
	last := state.LastSuccessAt
	if state.LastAttemptAt.After(last) {
		last = state.LastAttemptAt
	}
	if last.IsZero() {
		return true
	}
	intervalRaw = strings.TrimSpace(intervalRaw)
	if intervalRaw == "" {
		return false
	}
	interval, err := time.ParseDuration(intervalRaw)
	if err != nil || interval <= 0 {
		return false
	}
	return !now.Before(last.UTC().Add(interval))
}

func externalChannelRecordAttempt(state core.DurableAgentExternalChannelRuntimeState, adapter string, command string, now time.Time) core.DurableAgentExternalChannelRuntimeState {
	state.Adapter = strings.ToLower(strings.TrimSpace(adapter))
	state.LastCommand = strings.TrimSpace(command)
	state.LastAttemptAt = now.UTC()
	return state
}

func externalChannelRecordSuccess(state core.DurableAgentExternalChannelRuntimeState, lifecycle externalChannelCommandLifecycle, now time.Time) core.DurableAgentExternalChannelRuntimeState {
	state = externalChannelRecordAttempt(state, lifecycle.Adapter, lifecycle.Command, now)
	if strings.TrimSpace(lifecycle.SessionRef) != "" {
		state.SessionRef = strings.TrimSpace(lifecycle.SessionRef)
	}
	if strings.TrimSpace(lifecycle.LastArtifact) != "" {
		state.LastArtifact = strings.TrimSpace(lifecycle.LastArtifact)
	}
	state.LastSuccessAt = now.UTC()
	state.LastStatus = firstNonEmpty(strings.TrimSpace(lifecycle.LastStatus), "ok")
	state.LastError = ""
	state.LastErrorAt = time.Time{}
	if lifecycle.ResetBackoff {
		state.BackoffUntil = time.Time{}
		state.FailureCount = 0
	}
	return state
}

func externalChannelRecordFailure(state core.DurableAgentExternalChannelRuntimeState, lifecycle externalChannelCommandLifecycle, now time.Time) core.DurableAgentExternalChannelRuntimeState {
	state = externalChannelRecordAttempt(state, lifecycle.Adapter, lifecycle.Command, now)
	if strings.TrimSpace(lifecycle.SessionRef) != "" {
		state.SessionRef = strings.TrimSpace(lifecycle.SessionRef)
	}
	if strings.TrimSpace(lifecycle.LastArtifact) != "" {
		state.LastArtifact = strings.TrimSpace(lifecycle.LastArtifact)
	}
	state.LastStatus = firstNonEmpty(strings.TrimSpace(lifecycle.LastStatus), "blocked")
	state.LastError = strings.TrimSpace(lifecycle.LastError)
	state.LastErrorAt = now.UTC()
	state.FailureCount++
	if !lifecycle.BackoffUntil.IsZero() {
		state.BackoffUntil = lifecycle.BackoffUntil.UTC()
	} else {
		state.BackoffUntil = externalChannelBackoffUntil(now, state.FailureCount)
	}
	return state
}

func externalChannelBackoffUntil(now time.Time, failures int) time.Time {
	return durableWakeBackoffUntil(now, failures)
}
