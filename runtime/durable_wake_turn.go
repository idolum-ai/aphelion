//go:build linux

package runtime

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"
	"time"
)

func (r *Runtime) RunDurableAgentChildWake(ctx context.Context, agentID string, now time.Time) error {
	if r == nil || r.store == nil {
		return fmt.Errorf("durable child wake runtime is unavailable")
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return fmt.Errorf("durable child wake agent id is required")
	}
	agent, err := r.store.DurableAgent(agentID)
	if err != nil {
		return fmt.Errorf("load durable child wake agent: %w", err)
	}
	if agent == nil {
		return fmt.Errorf("durable agent %q not found", agentID)
	}
	if plan, err := prepareDurableParentConversationWakePlan(r, *agent, now, true); err != nil {
		return err
	} else if plan != nil {
		if now.IsZero() {
			now = time.Now().UTC()
		}
		return r.runDurableWakeTurn(ctx, *agent, *plan, now.UTC())
	}
	return r.runDurableAgentChildWakeLoaded(ctx, *agent, now)
}

func durableWakeSyntheticChatID(agentID string) int64 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.TrimSpace(agentID)))
	return int64(920000000 + h.Sum32())
}

func durableWakeMessageID(now time.Time) int64 {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if id := now.UnixMilli(); id > 0 {
		return id
	}
	return 1
}
