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
	scheduledReviewChannelKind             = "scheduled_review"
	defaultScheduledReviewTimeUTC          = "00:10"
	defaultScheduledReviewWindow           = "previous_day"
	defaultScheduledReviewMessageLimit     = 1200
	defaultScheduledReviewArtifactKind     = "scheduled_check_in"
	defaultScheduledReviewTranscriptDir    = ".aphelion/scheduled-review"
	defaultScheduledReviewGuidanceQuestion = "What guidance should I apply before the next scheduled review?"
)

type scheduledReviewDurableWakeAdapter struct{}

func newScheduledReviewDurableWakeAdapter() durableWakeIngressAdapter {
	return scheduledReviewDurableWakeAdapter{}
}

func (scheduledReviewDurableWakeAdapter) Name() string { return scheduledReviewChannelKind }

func (scheduledReviewDurableWakeAdapter) Supports(agent core.DurableAgent) bool {
	return strings.TrimSpace(agent.ChannelKind) == scheduledReviewChannelKind && agent.ChannelConfig.ScheduledReviewConfig() != nil
}

func (scheduledReviewDurableWakeAdapter) Prepare(_ context.Context, rt *Runtime, agent core.DurableAgent, now time.Time) (*durableWakeTurnPlan, error) {
	if rt == nil || rt.store == nil {
		return nil, fmt.Errorf("scheduled review adapter runtime is unavailable")
	}
	cfg := scheduledReviewConfigForAgent(agent)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	reviewStart, reviewEnd, reviewDate, dueAt, err := scheduledReviewWindow(now, cfg)
	if err != nil {
		return nil, err
	}
	if now.Before(dueAt) {
		return nil, nil
	}

	state, err := rt.store.DurableAgentState(strings.TrimSpace(agent.AgentID))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("load scheduled review state: %w", err)
	}
	if state != nil && strings.TrimSpace(state.Cursor) == reviewDate {
		return nil, nil
	}

	scope, err := rt.scopeForDurableAgent(agent)
	if err != nil {
		return nil, fmt.Errorf("resolve scheduled review scope: %w", err)
	}
	hits, err := rt.store.MessagesInWindow(reviewStart, reviewEnd, cfg.MaxMessages)
	if err != nil {
		return nil, fmt.Errorf("load scheduled review message window: %w", err)
	}
	transcriptPath, err := stageScheduledReviewTranscript(scope.WorkingRoot, cfg.TranscriptDir, reviewStart, reviewEnd, hits)
	if err != nil {
		return nil, fmt.Errorf("stage scheduled review transcript: %w", err)
	}

	key := session.SessionKey{ChatID: durableWakeSyntheticChatID(agent.AgentID), Scope: durableAgentScopeRef(agent)}
	return &durableWakeTurnPlan{
		Channel:      scheduledReviewChannelKind,
		AuditChannel: scheduledReviewChannelKind,
		Key:          key,
		Inbound: core.InboundMessage{
			ChatID:         key.ChatID,
			ChatType:       scheduledReviewChannelKind,
			ChatTitle:      scheduledReviewChatTitle(cfg),
			SenderName:     "scheduler",
			Text:           scheduledReviewWakePrompt(cfg, reviewDate, transcriptPath, len(hits)),
			MessageID:      durableWakeMessageID(now),
			DurableAgentID: agent.AgentID,
			Timestamp:      now,
		},
		SessionChatType:      scheduledReviewChannelKind,
		SessionUserName:      "scheduler",
		PromptContextErrHint: "load scheduled review prompt context",
		PolicyReason:         "mapped from interactive face policy for scheduled review durable wakes",
		PersistenceErrCtx: turnCommitErrorContext{
			ConvertMessages: "convert scheduled review wake messages",
			LoadPlanState:   "load scheduled review plan state before save",
			LoadOperation:   "load scheduled review operation state before save",
			SaveSession:     "save scheduled review session",
			RecordOutbound:  "record scheduled review outbound reply",
		},
		SendErrCtx:   "send scheduled review reply",
		RecordErrCtx: "record scheduled review outbound reply",
		GovernorContext: func(agent core.DurableAgent, policy core.DurableAgentLivePolicy, _ core.InboundMessage, pending []core.DurableAgentConversationMessage) string {
			lines := []string{
				"You are running a scheduled parent-child review check-in.",
				"Treat this as a normal child-to-parent chat initiation, not a special control-plane report.",
				"Read the staged transcript file first, then summarize what worked, what failed, and concrete action items for the next period.",
				"Keep proposals bounded to child charter and ask the parent for guidance when uncertain.",
			}
			if title := strings.TrimSpace(cfg.Title); title != "" {
				lines = append(lines, "Scheduled review title: "+title)
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
				Summary:       scheduledReviewArtifactSummary(cfg, reviewDate, turnSummary),
				IntervalLabel: reviewDate,
				LocalActions: []string{
					fmt.Sprintf("Reviewed staged transcript for %s and drafted next-period actions.", reviewDate),
				},
				Questions: []string{cfg.GuidanceQuestion},
				RiskFlags: []string{cfg.ArtifactKind},
				Metadata: map[string]string{
					"channel_kind":        scheduledReviewChannelKind,
					"trigger_kinds":       cfg.ArtifactKind + ",scheduled_review",
					"review_date":         reviewDate,
					"window_start":        reviewStart.Format(time.RFC3339),
					"window_end":          reviewEnd.Format(time.RFC3339),
					"message_count":       strconv.Itoa(len(hits)),
					"transcript_path":     transcriptPath,
					"child_local_subject": "false",
				},
			}
			if title := strings.TrimSpace(cfg.Title); title != "" {
				artifact.Metadata["operator_title"] = title
				artifact.Metadata["channel_label"] = title
			}
			if _, err := durableagent.NewRuntime(rt.store).QueueReviewArtifact(agent, artifact); err != nil {
				return fmt.Errorf("queue scheduled review artifact: %w", err)
			}
			if err := rt.markDurableScheduledReviewCursor(agent.AgentID, reviewDate); err != nil {
				return fmt.Errorf("mark scheduled review cursor: %w", err)
			}
			return nil
		},
	}, nil
}

type scheduledReviewConfig struct {
	Title            string
	ScheduleKind     string
	TimeUTC          string
	Window           string
	MaxMessages      int
	ArtifactKind     string
	TranscriptDir    string
	PromptTemplate   string
	GuidanceQuestion string
}

func scheduledReviewConfigForAgent(agent core.DurableAgent) scheduledReviewConfig {
	cfg := scheduledReviewConfig{
		Title:            "Scheduled review",
		ScheduleKind:     "daily",
		TimeUTC:          defaultScheduledReviewTimeUTC,
		Window:           defaultScheduledReviewWindow,
		MaxMessages:      defaultScheduledReviewMessageLimit,
		ArtifactKind:     defaultScheduledReviewArtifactKind,
		TranscriptDir:    defaultScheduledReviewTranscriptDir,
		GuidanceQuestion: defaultScheduledReviewGuidanceQuestion,
	}
	if raw := agent.ChannelConfig.ScheduledReviewConfig(); raw != nil {
		if strings.TrimSpace(raw.Title) != "" {
			cfg.Title = strings.TrimSpace(raw.Title)
		}
		if strings.TrimSpace(raw.ScheduleKind) != "" {
			cfg.ScheduleKind = strings.TrimSpace(raw.ScheduleKind)
		}
		if strings.TrimSpace(raw.TimeUTC) != "" {
			cfg.TimeUTC = strings.TrimSpace(raw.TimeUTC)
		}
		if strings.TrimSpace(raw.Window) != "" {
			cfg.Window = strings.TrimSpace(raw.Window)
		}
		if raw.MaxMessages > 0 {
			cfg.MaxMessages = raw.MaxMessages
		}
		if strings.TrimSpace(raw.ArtifactKind) != "" {
			cfg.ArtifactKind = strings.TrimSpace(raw.ArtifactKind)
		}
		if strings.TrimSpace(raw.TranscriptDir) != "" {
			cfg.TranscriptDir = strings.TrimSpace(raw.TranscriptDir)
		}
		if strings.TrimSpace(raw.PromptTemplate) != "" {
			cfg.PromptTemplate = strings.TrimSpace(raw.PromptTemplate)
		}
		if strings.TrimSpace(raw.GuidanceQuestion) != "" {
			cfg.GuidanceQuestion = strings.TrimSpace(raw.GuidanceQuestion)
		}
	}
	cfg.ScheduleKind = strings.ToLower(strings.TrimSpace(cfg.ScheduleKind))
	cfg.Window = strings.ToLower(strings.TrimSpace(cfg.Window))
	cfg.ArtifactKind = strings.ToLower(strings.TrimSpace(cfg.ArtifactKind))
	cfg.TranscriptDir = strings.Trim(strings.TrimSpace(cfg.TranscriptDir), "/")
	if cfg.MaxMessages <= 0 {
		cfg.MaxMessages = defaultScheduledReviewMessageLimit
	}
	if cfg.GuidanceQuestion == "" {
		cfg.GuidanceQuestion = defaultScheduledReviewGuidanceQuestion
	}
	return cfg
}

func scheduledReviewWindow(now time.Time, cfg scheduledReviewConfig) (time.Time, time.Time, string, time.Time, error) {
	now = now.UTC()
	if cfg.ScheduleKind != "daily" {
		return time.Time{}, time.Time{}, "", time.Time{}, fmt.Errorf("scheduled review schedule kind %q is unsupported", cfg.ScheduleKind)
	}
	if cfg.Window != "previous_day" {
		return time.Time{}, time.Time{}, "", time.Time{}, fmt.Errorf("scheduled review window %q is unsupported", cfg.Window)
	}
	tod, err := time.Parse("15:04", cfg.TimeUTC)
	if err != nil {
		return time.Time{}, time.Time{}, "", time.Time{}, fmt.Errorf("parse scheduled review time_utc %q: %w", cfg.TimeUTC, err)
	}
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	reviewStart := dayStart.AddDate(0, 0, -1)
	reviewEnd := dayStart
	reviewDate := reviewStart.Format("2006-01-02")
	dueAt := time.Date(now.Year(), now.Month(), now.Day(), tod.Hour(), tod.Minute(), 0, 0, time.UTC)
	return reviewStart, reviewEnd, reviewDate, dueAt, nil
}

func scheduledReviewWakePrompt(cfg scheduledReviewConfig, reviewDate string, transcriptPath string, messageCount int) string {
	if cfg.PromptTemplate != "" {
		return strings.NewReplacer(
			"{{review_date}}", strings.TrimSpace(reviewDate),
			"{{transcript_path}}", strings.TrimSpace(transcriptPath),
			"{{message_count}}", strconv.Itoa(messageCount),
			"{{title}}", strings.TrimSpace(cfg.Title),
		).Replace(cfg.PromptTemplate)
	}
	lines := []string{
		fmt.Sprintf("Scheduled review check-in for review_date=%s.", strings.TrimSpace(reviewDate)),
		fmt.Sprintf("Transcript file: %s", strings.TrimSpace(transcriptPath)),
		fmt.Sprintf("Message count in staged window: %d", messageCount),
		"Read the transcript file, then reply as a normal child chat to the parent with:",
		"1) what worked in the review window",
		"2) what did not work",
		"3) concrete action items for the next period (max 5)",
		"Keep the message concise and operational.",
	}
	return strings.Join(lines, "\n")
}

func scheduledReviewArtifactSummary(cfg scheduledReviewConfig, reviewDate string, turnSummary string) string {
	title := strings.TrimSpace(cfg.Title)
	if title == "" {
		title = "Scheduled review"
	}
	parts := []string{fmt.Sprintf("%s from child for %s.", title, strings.TrimSpace(reviewDate))}
	if trimmed := strings.TrimSpace(turnSummary); trimmed != "" {
		parts = append(parts, truncateRunes(trimmed, 900))
	}
	parts = append(parts, "Reply with guidance and I will apply it on the next wake.")
	return strings.Join(parts, " ")
}

func (r *Runtime) markDurableScheduledReviewCursor(agentID string, reviewDate string) error {
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

func stageScheduledReviewTranscript(workingRoot string, transcriptDir string, reviewStart time.Time, reviewEnd time.Time, hits []session.SearchHit) (string, error) {
	path := scheduledReviewTranscriptPath(workingRoot, transcriptDir, reviewStart)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].CreatedAt.Before(hits[j].CreatedAt) })
	var b strings.Builder
	b.WriteString("# Scheduled Review Transcript\n\n")
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

func scheduledReviewTranscriptPath(workingRoot string, transcriptDir string, reviewStart time.Time) string {
	root := strings.TrimSpace(workingRoot)
	if root == "" {
		root = "."
	}
	dir := strings.Trim(strings.TrimSpace(transcriptDir), "/")
	if dir == "" {
		dir = defaultScheduledReviewTranscriptDir
	}
	return filepath.Join(root, filepath.FromSlash(dir), reviewStart.UTC().Format("2006-01-02"), "transcript.md")
}

func scheduledReviewChatTitle(cfg scheduledReviewConfig) string {
	title := strings.ToLower(strings.TrimSpace(cfg.Title))
	title = strings.ReplaceAll(title, " ", "-")
	if title == "" {
		return "scheduled-review"
	}
	return title
}
