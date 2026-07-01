//go:build linux

package runtime

import (
	"context"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) newDurableWakeProgressReporter(key session.SessionKey, msg core.InboundMessage, agent core.DurableAgent) *toolProgressReporter {
	if strings.TrimSpace(msg.DurableAgentID) == "" {
		msg.DurableAgentID = strings.TrimSpace(agent.AgentID)
	}
	if strings.TrimSpace(msg.ChatType) == "" {
		msg.ChatType = "durable_wake"
	}
	progress := r.newToolProgressReporter(key, msg, nil)
	if progress == nil {
		return nil
	}
	label := durableWakeProgressAgentLabel(agent.AgentID)
	progress.SetHeadings(label+" is working...", label+" finished.")
	return progress
}

func durableWakeProgressEnabled(agent core.DurableAgent, plan durableWakeTurnPlan) bool {
	if strings.EqualFold(strings.TrimSpace(agent.ChannelKind), scheduledReviewChannelKind) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(plan.Channel), scheduledReviewChannelKind) {
		return false
	}
	return true
}

func durableWakeProgressAgentLabel(agentID string) string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return "durable child"
	}
	var b strings.Builder
	for _, r := range agentID {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		}
	}
	if label := strings.TrimSpace(b.String()); label != "" {
		return label
	}
	return "durable child"
}

func durableWakeProgressSurface(ctx context.Context, progress *toolProgressReporter, text string) {
	if progress == nil {
		return
	}
	progress.Surface(ctx, strings.TrimSpace(text))
}

func durableWakeProgressDone(ctx context.Context, progress *toolProgressReporter, agent core.DurableAgent, done string, surface string) {
	if progress == nil {
		return
	}
	label := durableWakeProgressAgentLabel(agent.AgentID)
	if done = strings.TrimSpace(done); done != "" {
		progress.SetHeadings("", label+" "+done+".")
	}
	durableWakeProgressSurface(ctx, progress, surface)
	progress.Finish(ctx)
}

func durableWakeProgressDoneStatus(status session.ChildTaskResultStatus) string {
	switch status {
	case session.ChildTaskResultCompleted:
		return "completed"
	case session.ChildTaskResultBlocked:
		return "blocked"
	case session.ChildTaskResultFailed:
		return "failed"
	case session.ChildTaskResultUpdate:
		return "updated"
	default:
		return "finished"
	}
}

func durableWakeProgressResultSurface(result session.ChildTaskResult) string {
	switch result.Status {
	case session.ChildTaskResultCompleted:
		return "Child completed"
	case session.ChildTaskResultBlocked:
		if blocker := durableWakeProgressBlockerLabel(result.BlockerKind); blocker != "" {
			return "Child blocked: " + blocker + ". Repair next action recorded."
		}
		return "Child blocked. Repair next action recorded."
	case session.ChildTaskResultFailed:
		return "Child failed. Repair next action recorded."
	case session.ChildTaskResultUpdate:
		return "Child reported an update. Next child task action recorded."
	default:
		return "Child finished"
	}
}

func durableWakeProgressBlockerLabel(kind string) string {
	kind = normalizeDurableWakeChildBlockerKind(kind)
	if kind == "" {
		return ""
	}
	if durableWakeProgressKnownBlockerKind(kind) {
		return kind
	}
	return ""
}

func durableWakeProgressKnownBlockerKind(kind string) bool {
	switch strings.TrimSpace(kind) {
	case
		"capability_grant_missing",
		"child_control_plane_tool_misroute",
		"child_reported_blocked",
		"credential_unverified",
		"external_transient",
		"grant_missing_or_stale",
		"host_permission_denied",
		"host_resource_missing",
		"lifecycle_unregistered",
		"missing_child_runtime_probe",
		"missing_continuation_lease",
		"resource_permission_denied",
		"runtime_bin_missing_or_not_executable",
		"runtime_capability_stale",
		"sandbox_exec_failed",
		"schema_mismatch",
		"tool_lifecycle_unregistered",
		"tool_runtime_not_executable",
		"tool_runtime_probe_missing",
		"wake_failed",
		"unknown_child_blocker":
		return true
	default:
		return false
	}
}
