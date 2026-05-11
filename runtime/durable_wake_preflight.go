//go:build linux

package runtime

import (
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
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
	if strings.EqualFold(adapterName, codexAppServerAdapterName) {
		return nil
	}
	readiness := r.externalChannelReadinessForAgent(agent, now.UTC())
	if readiness.Status != externalChannelReadinessStatusBlocked {
		return nil
	}
	return fmt.Errorf(
		"child_runtime_blocked: preflight_failed adapter=%s failure_code=%s next_repair=%s",
		strings.TrimSpace(readiness.Adapter),
		strings.TrimSpace(readiness.FailureCode),
		strings.TrimSpace(readiness.NextRepair),
	)
}
