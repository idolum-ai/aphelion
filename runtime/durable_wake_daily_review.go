//go:build linux

package runtime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/session"
)

const (
	dailyReviewDurableChannelKind    = "daily_review"
	defaultDailyReviewDurableAgentID = "idolum-daily-review"
	dailyReviewWakeHourUTC           = 0
	dailyReviewWakeMinuteUTC         = 10
	dailyReviewWindowMessageLimit    = 1200
)

type dailyReviewDurableWakeAdapter struct{}

func newDailyReviewDurableWakeAdapter() durableWakeIngressAdapter {
	return dailyReviewDurableWakeAdapter{}
}

func (dailyReviewDurableWakeAdapter) Name() string {
	return dailyReviewDurableChannelKind
}

func (dailyReviewDurableWakeAdapter) Supports(agent core.DurableAgent) bool {
	return strings.TrimSpace(agent.ChannelKind) == dailyReviewDurableChannelKind
}

func (dailyReviewDurableWakeAdapter) Prepare(_ context.Context, rt *Runtime, agent core.DurableAgent, now time.Time) (*durableWakeTurnPlan, error) {
	if rt == nil || rt.store == nil {
		return nil, fmt.Errorf("daily review adapter runtime is unavailable")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	reviewStart, reviewEnd, reviewDate, dueAt := dailyReviewWindow(now)
	if now.Before(dueAt) {
		return nil, nil
	}

	state, err := rt.store.DurableAgentState(strings.TrimSpace(agent.AgentID))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("load daily review state: %w", err)
	}
	if state != nil && strings.TrimSpace(state.Cursor) == reviewDate {
		return nil, nil
	}

	scope, err := rt.scopeForDurableAgent(agent)
	if err != nil {
		return nil, fmt.Errorf("resolve daily review scope: %w", err)
	}
	hits, err := rt.store.MessagesInWindow(reviewStart, reviewEnd, dailyReviewWindowMessageLimit)
	if err != nil {
		return nil, fmt.Errorf("load daily review message window: %w", err)
	}
	transcriptPath, err := stageDailyReviewTranscript(scope.WorkingRoot, reviewStart, reviewEnd, hits)
	if err != nil {
		return nil, fmt.Errorf("stage daily review transcript: %w", err)
	}

	key := session.SessionKey{
		ChatID: durableWakeSyntheticChatID(agent.AgentID),
		Scope:  durableAgentScopeRef(agent),
	}
	return &durableWakeTurnPlan{
		Channel:      dailyReviewDurableChannelKind,
		AuditChannel: dailyReviewDurableChannelKind,
		Key:          key,
		Inbound: core.InboundMessage{
			ChatID:         key.ChatID,
			ChatType:       dailyReviewDurableChannelKind,
			ChatTitle:      "daily-review",
			SenderName:     "scheduler",
			Text:           dailyReviewWakePrompt(reviewDate, transcriptPath, len(hits)),
			MessageID:      durableWakeMessageID(now),
			DurableAgentID: agent.AgentID,
			Timestamp:      now,
		},
		SessionChatType:      dailyReviewDurableChannelKind,
		SessionUserName:      "scheduler",
		PromptContextErrHint: "load daily review prompt context",
		PolicyReason:         "mapped from interactive face policy for scheduled daily review durable wakes",
		PersistenceErrCtx: turnCommitErrorContext{
			ConvertMessages: "convert daily review wake messages",
			LoadPlanState:   "load daily review plan state before save",
			LoadOperation:   "load daily review operation state before save",
			SaveSession:     "save daily review session",
			RecordOutbound:  "record daily review outbound reply",
		},
		SendErrCtx:   "send daily review reply",
		RecordErrCtx: "record daily review outbound reply",
		GovernorContext: func(agent core.DurableAgent, policy core.DurableAgentLivePolicy, _ core.InboundMessage, pending []core.DurableAgentConversationMessage) string {
			lines := []string{
				"You are running a scheduled daily parent-child check-in.",
				"Treat this as a normal child-to-parent chat initiation, not a special control-plane report.",
				"Read the staged transcript file first, then summarize: what worked, what failed, and concrete action items for tomorrow.",
				"Keep proposals bounded to child charter and ask the parent for guidance when uncertain.",
			}
			if charter := strings.TrimSpace(policy.Charter); charter != "" {
				lines = append(lines, "Charter: "+charter)
			}
			lines = append(lines, "Durable child agent id: "+strings.TrimSpace(agent.AgentID))
			lines = append(lines, durableParentConversationGovernorLines(pending)...)
			return strings.Join(lines, "\n")
		},
		Finalize: func(turnSummary string) error {
			turnSummary = strings.TrimSpace(turnSummary)
			artifact := core.DurableReviewArtifact{
				AgentID:       strings.TrimSpace(agent.AgentID),
				Summary:       dailyReviewScheduledCheckInSummary(reviewDate, turnSummary),
				IntervalLabel: reviewDate,
				LocalActions: []string{
					fmt.Sprintf("Reviewed staged transcript for %s and drafted next-day actions.", reviewDate),
				},
				Questions: []string{
					"What guidance should I apply before the next daily check-in?",
				},
				RiskFlags: []string{"scheduled_check_in"},
				Metadata: map[string]string{
					"channel_kind":        dailyReviewDurableChannelKind,
					"trigger_kinds":       "scheduled_check_in,daily_review",
					"review_date":         reviewDate,
					"window_start":        reviewStart.Format(time.RFC3339),
					"window_end":          reviewEnd.Format(time.RFC3339),
					"message_count":       strconv.Itoa(len(hits)),
					"transcript_path":     transcriptPath,
					"child_local_subject": "false",
				},
			}
			if _, err := durableagent.NewRuntime(rt.store).QueueReviewArtifact(agent, artifact); err != nil {
				return fmt.Errorf("queue daily review scheduled check-in: %w", err)
			}
			if err := rt.markDurableDailyReviewCursor(agent.AgentID, reviewDate); err != nil {
				return fmt.Errorf("mark daily review cursor: %w", err)
			}
			return nil
		},
	}, nil
}

func dailyReviewWindow(now time.Time) (time.Time, time.Time, string, time.Time) {
	now = now.UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	reviewStart := dayStart.AddDate(0, 0, -1)
	reviewEnd := dayStart
	reviewDate := reviewStart.Format("2006-01-02")
	dueAt := time.Date(now.Year(), now.Month(), now.Day(), dailyReviewWakeHourUTC, dailyReviewWakeMinuteUTC, 0, 0, time.UTC)
	return reviewStart, reviewEnd, reviewDate, dueAt
}

func dailyReviewWakePrompt(reviewDate string, transcriptPath string, messageCount int) string {
	lines := []string{
		fmt.Sprintf("Daily scheduled child-parent check-in for review_date=%s.", strings.TrimSpace(reviewDate)),
		fmt.Sprintf("Transcript file: %s", strings.TrimSpace(transcriptPath)),
		fmt.Sprintf("Message count in staged window: %d", messageCount),
		"Read the transcript file, then reply as a normal child chat to the parent with:",
		"1) what worked yesterday",
		"2) what did not work",
		"3) concrete action items for tomorrow (max 5)",
		"Keep the message concise and operational.",
	}
	return strings.Join(lines, "\n")
}

func dailyReviewScheduledCheckInSummary(reviewDate string, turnSummary string) string {
	parts := []string{
		fmt.Sprintf("Scheduled check-in from child for %s.", strings.TrimSpace(reviewDate)),
	}
	if trimmed := strings.TrimSpace(turnSummary); trimmed != "" {
		parts = append(parts, truncateRunes(trimmed, 900))
	}
	parts = append(parts, "Reply with guidance and I will apply it on the next wake.")
	return strings.Join(parts, " ")
}

func (r *Runtime) markDurableDailyReviewCursor(agentID string, reviewDate string) error {
	state, err := r.store.DurableAgentState(strings.TrimSpace(agentID))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if state == nil {
		state = &core.DurableAgentState{AgentID: strings.TrimSpace(agentID)}
	}
	state.Cursor = strings.TrimSpace(reviewDate)
	return r.store.SaveDurableAgentState(*state)
}

func stageDailyReviewTranscript(workingRoot string, reviewStart time.Time, reviewEnd time.Time, hits []session.SearchHit) (string, error) {
	path := dailyReviewTranscriptPath(workingRoot, reviewStart)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	sort.SliceStable(hits, func(i, j int) bool {
		return hits[i].CreatedAt.Before(hits[j].CreatedAt)
	})
	var b strings.Builder
	b.WriteString("# Daily Transcript\n\n")
	b.WriteString(fmt.Sprintf("review_date: %s\n", reviewStart.UTC().Format("2006-01-02")))
	b.WriteString(fmt.Sprintf("window_start: %s\n", reviewStart.UTC().Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("window_end: %s\n", reviewEnd.UTC().Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("message_count: %d\n\n", len(hits)))
	b.WriteString("## Entries\n")
	if len(hits) == 0 {
		b.WriteString("- none\n")
	} else {
		for _, hit := range hits {
			line := fmt.Sprintf(
				"- [%s] chat=%d role=%s turn=%d content=%s",
				hit.CreatedAt.UTC().Format(time.RFC3339),
				hit.ChatID,
				strings.TrimSpace(hit.Role),
				hit.TurnIndex,
				truncateRunes(strings.Join(strings.Fields(strings.TrimSpace(hit.Content)), " "), 360),
			)
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func dailyReviewTranscriptPath(workingRoot string, reviewStart time.Time) string {
	root := strings.TrimSpace(workingRoot)
	if root == "" {
		root = "."
	}
	return filepath.Join(
		root,
		".aphelion",
		"daily-review",
		reviewStart.UTC().Format("2006-01-02"),
		"transcript.md",
	)
}
