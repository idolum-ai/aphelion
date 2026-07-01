//go:build linux

package runtime

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	runtimecodex "github.com/idolum-ai/aphelion/runtime/codex"
)

func (r *Runtime) preflightDurableWakeAgent(agent core.DurableAgent, now time.Time) error {
	if r == nil || r.store == nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	external := agent.ChannelConfig.ExternalConfig()
	if external == nil {
		return nil
	}
	adapterName := externalChannelAdapter(agent)
	if adapterName == "" {
		return fmt.Errorf("external channel adapter is not configured")
	}
	if strings.EqualFold(adapterName, runtimecodex.AdapterName) {
		return nil
	}
	readiness := r.externalChannelReadinessForAgent(agent, now.UTC())
	if readiness.Status != externalChannelReadinessStatusBlocked {
		return nil
	}
	return externalChannelReadinessPreflightError{Readiness: readiness}
}

type externalChannelReadinessPreflightError struct {
	Readiness externalChannelAdapterReadiness
}

func (e externalChannelReadinessPreflightError) Error() string {
	return fmt.Sprintf(
		"child_runtime_blocked: preflight_failed adapter=%s failure_code=%s repair_kind=%s next_repair=%s",
		strings.TrimSpace(e.Readiness.Adapter),
		strings.TrimSpace(e.Readiness.FailureCode),
		strings.TrimSpace(e.Readiness.RepairKind),
		strings.TrimSpace(e.Readiness.NextRepair),
	)
}

func externalChannelReadinessFromError(err error) (externalChannelAdapterReadiness, bool) {
	var typed externalChannelReadinessPreflightError
	if errors.As(err, &typed) {
		return typed.Readiness, true
	}
	return externalChannelAdapterReadiness{}, false
}
