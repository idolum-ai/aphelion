//go:build linux

package runtime

import (
	"context"
	"encoding/json"
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
	if strings.ToLower(strings.TrimSpace(agent.Status)) != "active" {
		return false
	}
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

	state, continuity, err := loadDurableAgentContinuityFromStore(rt.store, agent.AgentID)
	if err != nil {
		return nil, fmt.Errorf("load scheduled review state: %w", err)
	}
	if state != nil && strings.TrimSpace(state.Cursor) == reviewDate {
		return nil, nil
	}
	if scheduledReviewBackoffActive(continuity, reviewDate, now) {
		return nil, nil
	}

	scope, err := rt.scopeForDurableAgent(agent)
	if err != nil {
		return nil, rt.wrapScheduledReviewPrepareFailure(agent, cfg, reviewDate, "prepare_scope_failed", "resolve scheduled review scope", err, now)
	}
	hits, err := rt.store.MessagesInWindow(reviewStart, reviewEnd, cfg.MaxMessages)
	if err != nil {
		return nil, rt.wrapScheduledReviewPrepareFailure(agent, cfg, reviewDate, "message_window_failed", "load scheduled review message window", err, now)
	}
	transcriptPath, err := stageScheduledReviewTranscript(scope.WorkingRoot, cfg.TranscriptDir, reviewStart, reviewEnd, hits)
	if err != nil {
		return nil, rt.wrapScheduledReviewPrepareFailure(agent, cfg, reviewDate, "transcript_staging_failed", "stage scheduled review transcript", err, now)
	}
	if err := rt.recordScheduledReviewAttempt(state, continuity, reviewDate, now); err != nil {
		return nil, fmt.Errorf("record scheduled review attempt: %w", err)
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
		OutcomeIntents: func(status session.ChildTaskResultStatus, turnSummary string, cause error, intentAt time.Time) []session.ChildTaskOutcomeIntentInput {
			payload := scheduledReviewOutcomeIntentPayload{
				AgentID:        strings.TrimSpace(agent.AgentID),
				Config:         cfg,
				ReviewDate:     reviewDate,
				WindowStart:    reviewStart.UTC().Format(time.RFC3339),
				WindowEnd:      reviewEnd.UTC().Format(time.RFC3339),
				MessageCount:   len(hits),
				TranscriptPath: transcriptPath,
				TurnSummary:    strings.TrimSpace(turnSummary),
				Status:         "completed",
				CreatedAt:      now.UTC().Format(time.RFC3339Nano),
			}
			if cause != nil || status == session.ChildTaskResultFailed {
				payload.Status = "wake_failed"
				if cause != nil {
					payload.Error = trimError(cause.Error())
				}
			}
			raw, _ := json.Marshal(payload)
			return []session.ChildTaskOutcomeIntentInput{{
				Kind:        session.ChildTaskOutcomeIntentScheduledReview,
				Sequence:    10,
				PayloadJSON: string(raw),
				ResultRef:   "scheduled_review:" + strings.TrimSpace(agent.AgentID),
				CreatedAt:   intentAt,
			}}
		},
		FinalizeFailure: func(turnSummary string, cause error) error {
			return rt.recordScheduledReviewFailure(agent, cfg, reviewDate, turnSummary, cause, now, "wake_failed")
		},
	}, nil
}

type scheduledReviewOutcomeIntentPayload struct {
	AgentID        string                `json:"agent_id"`
	Config         scheduledReviewConfig `json:"config"`
	ReviewDate     string                `json:"review_date"`
	WindowStart    string                `json:"window_start"`
	WindowEnd      string                `json:"window_end"`
	MessageCount   int                   `json:"message_count"`
	TranscriptPath string                `json:"transcript_path"`
	TurnSummary    string                `json:"turn_summary"`
	Status         string                `json:"status"`
	Error          string                `json:"error,omitempty"`
	CreatedAt      string                `json:"created_at"`
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
	RecipeID         string
	RecipeVersion    string
	RecipeSource     string
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
		cfg.RecipeID = strings.TrimSpace(raw.RecipeID)
		cfg.RecipeVersion = strings.TrimSpace(raw.RecipeVersion)
		cfg.RecipeSource = strings.TrimSpace(raw.RecipeSource)
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
	_, err := r.store.UpdateDurableAgentState(strings.TrimSpace(agentID), func(state *core.DurableAgentState) error {
		state.Cursor = strings.TrimSpace(reviewDate)
		return nil
	})
	return err
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

func (r *Runtime) shouldSuppressScheduledReviewChildPoll(agent core.DurableAgent, now time.Time) (bool, error) {
	if r == nil || r.store == nil {
		return false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	cfg := scheduledReviewConfigForAgent(agent)
	_, _, reviewDate, dueAt, err := scheduledReviewWindow(now, cfg)
	if err != nil {
		return false, err
	}
	if now.Before(dueAt) {
		return true, nil
	}
	state, continuity, err := loadDurableAgentContinuityFromStore(r.store, agent.AgentID)
	if err != nil {
		return false, err
	}
	if state != nil && strings.TrimSpace(state.Cursor) == strings.TrimSpace(reviewDate) {
		return true, nil
	}
	if scheduledReviewBackoffActive(continuity, reviewDate, now) {
		return true, nil
	}
	return false, nil
}

func (r *Runtime) recordOrSuppressScheduledReviewWakeFailure(agent core.DurableAgent, cause error, now time.Time) (bool, error) {
	if r == nil || r.store == nil || cause == nil {
		return false, nil
	}
	if strings.TrimSpace(agent.ChannelKind) != scheduledReviewChannelKind || agent.ChannelConfig.ScheduledReviewConfig() == nil {
		return false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	cfg := scheduledReviewConfigForAgent(agent)
	_, _, reviewDate, _, err := scheduledReviewWindow(now, cfg)
	if err != nil {
		return false, nil
	}
	if r.scheduledReviewFailureAlreadyRecorded(agent.AgentID, reviewDate) {
		return true, nil
	}
	if err := r.recordScheduledReviewFailure(agent, cfg, reviewDate, "", cause, now, "wake_failed"); err != nil {
		return true, err
	}
	return true, nil
}

func (r *Runtime) scheduledReviewFailureAlreadyRecorded(agentID string, reviewDate string) bool {
	if r == nil || r.store == nil {
		return false
	}
	_, continuity, err := loadDurableAgentContinuityFromStore(r.store, agentID)
	if err != nil || continuity.ScheduledReview == nil {
		return false
	}
	state := continuity.ScheduledReview
	if strings.TrimSpace(state.ReviewDate) != strings.TrimSpace(reviewDate) {
		return false
	}
	if state.FailureCount > 0 || !state.LastErrorAt.IsZero() {
		return true
	}
	status := strings.TrimSpace(state.LastStatus)
	return strings.Contains(status, "failed") || strings.Contains(status, "blocked")
}

func scheduledReviewBackoffActive(continuity core.DurableAgentContinuityState, reviewDate string, now time.Time) bool {
	state := continuity.ScheduledReview
	if state == nil {
		return false
	}
	if strings.TrimSpace(state.ReviewDate) != strings.TrimSpace(reviewDate) {
		return false
	}
	return !state.BackoffUntil.IsZero() && now.UTC().Before(state.BackoffUntil.UTC())
}

func scheduledReviewRecordAttempt(state core.DurableAgentScheduledReviewRuntimeState, reviewDate string, now time.Time) core.DurableAgentScheduledReviewRuntimeState {
	if strings.TrimSpace(state.ReviewDate) != strings.TrimSpace(reviewDate) {
		state = core.DurableAgentScheduledReviewRuntimeState{ReviewDate: strings.TrimSpace(reviewDate)}
	}
	state.ReviewDate = strings.TrimSpace(reviewDate)
	state.LastAttemptAt = now.UTC()
	state.LastStatus = "wake_started"
	state.LastError = ""
	state.LastErrorAt = time.Time{}
	return state
}

func scheduledReviewRecordSuccess(state core.DurableAgentScheduledReviewRuntimeState, reviewDate string, now time.Time) core.DurableAgentScheduledReviewRuntimeState {
	state = scheduledReviewRecordAttempt(state, reviewDate, now)
	state.LastSuccessAt = now.UTC()
	state.LastStatus = "wake_completed"
	state.LastError = ""
	state.LastErrorAt = time.Time{}
	state.BackoffUntil = time.Time{}
	state.FailureCount = 0
	return state
}

func scheduledReviewRecordFailure(state core.DurableAgentScheduledReviewRuntimeState, reviewDate string, cause error, now time.Time, status string) core.DurableAgentScheduledReviewRuntimeState {
	state = scheduledReviewRecordAttempt(state, reviewDate, now)
	state.LastStatus = firstNonEmpty(strings.TrimSpace(status), "wake_failed")
	if cause != nil {
		state.LastError = truncateRunes(cause.Error(), 900)
	}
	state.LastErrorAt = now.UTC()
	state.FailureCount++
	state.BackoffUntil = scheduledReviewBackoffUntil(now, state.FailureCount)
	return state
}

func scheduledReviewBackoffUntil(now time.Time, failures int) time.Time {
	return durableWakeBackoffUntil(now, failures)
}

func (r *Runtime) recordScheduledReviewAttempt(state *core.DurableAgentState, _ core.DurableAgentContinuityState, reviewDate string, now time.Time) error {
	if r == nil || r.store == nil || state == nil {
		return nil
	}
	_, _, err := r.store.UpdateDurableAgentContinuity(state.AgentID, func(continuity core.DurableAgentContinuityState) (core.DurableAgentContinuityState, error) {
		current := core.DurableAgentScheduledReviewRuntimeState{}
		if continuity.ScheduledReview != nil {
			current = *continuity.ScheduledReview
		}
		updated := scheduledReviewRecordAttempt(current, reviewDate, now)
		continuity.ScheduledReview = &updated
		return continuity, nil
	})
	return err
}

func (r *Runtime) recordScheduledReviewSuccess(agentID string, reviewDate string, now time.Time) error {
	if r == nil || r.store == nil {
		return nil
	}
	_, _, err := r.store.UpdateDurableAgentContinuity(agentID, func(continuity core.DurableAgentContinuityState) (core.DurableAgentContinuityState, error) {
		current := core.DurableAgentScheduledReviewRuntimeState{}
		if continuity.ScheduledReview != nil {
			current = *continuity.ScheduledReview
		}
		updated := scheduledReviewRecordSuccess(current, reviewDate, now)
		continuity.ScheduledReview = &updated
		return continuity, nil
	})
	return err
}

func (r *Runtime) applyScheduledReviewOutcomeIntent(intent session.ChildTaskOutcomeIntent) error {
	var payload scheduledReviewOutcomeIntentPayload
	if err := json.Unmarshal([]byte(intent.PayloadJSON), &payload); err != nil {
		return fmt.Errorf("parse scheduled review outcome intent payload: %w", err)
	}
	agentID := strings.TrimSpace(payload.AgentID)
	if agentID == "" {
		return fmt.Errorf("scheduled review outcome intent missing agent_id")
	}
	agent, err := r.store.DurableAgent(agentID)
	if err != nil {
		return fmt.Errorf("load scheduled review intent agent: %w", err)
	}
	now, _ := time.Parse(time.RFC3339Nano, strings.TrimSpace(payload.CreatedAt))
	if now.IsZero() {
		now = time.Now().UTC()
	}
	status := firstNonEmpty(strings.TrimSpace(payload.Status), "completed")
	if status != "completed" {
		cause := errors.New(firstNonEmpty(strings.TrimSpace(payload.Error), "scheduled review wake failed"))
		return r.recordScheduledReviewFailureWithIdempotencyKey(*agent, payload.Config, payload.ReviewDate, payload.TurnSummary, cause, now, status, intent.IdempotencyKey)
	}
	reviewStart, _ := time.Parse(time.RFC3339, payload.WindowStart)
	reviewEnd, _ := time.Parse(time.RFC3339, payload.WindowEnd)
	artifact := scheduledReviewSuccessArtifact(*agent, payload.Config, payload.ReviewDate, reviewStart, reviewEnd, payload.TranscriptPath, payload.MessageCount, payload.TurnSummary)
	if _, err := durableagent.NewRuntime(r.store).QueueReviewArtifactWithIdempotencyKey(*agent, artifact, intent.IdempotencyKey); err != nil {
		return fmt.Errorf("queue scheduled review artifact: %w", err)
	}
	if err := r.recordScheduledReviewSuccess(agentID, payload.ReviewDate, now); err != nil {
		return fmt.Errorf("record scheduled review success: %w", err)
	}
	if err := r.markDurableScheduledReviewCursor(agentID, payload.ReviewDate); err != nil {
		return fmt.Errorf("mark scheduled review cursor: %w", err)
	}
	return nil
}

func scheduledReviewSuccessArtifact(agent core.DurableAgent, cfg scheduledReviewConfig, reviewDate string, reviewStart time.Time, reviewEnd time.Time, transcriptPath string, messageCount int, turnSummary string) core.DurableReviewArtifact {
	artifact := core.DurableReviewArtifact{
		AgentID:       strings.TrimSpace(agent.AgentID),
		Summary:       scheduledReviewArtifactSummary(cfg, reviewDate, strings.TrimSpace(turnSummary)),
		IntervalLabel: reviewDate,
		LocalActions: []string{
			fmt.Sprintf("Reviewed staged transcript for %s and drafted next-period actions.", reviewDate),
		},
		Questions: []string{cfg.GuidanceQuestion},
		RiskFlags: []string{cfg.ArtifactKind},
		Metadata: scheduledReviewArtifactMetadata(cfg, map[string]string{
			"channel_kind":        scheduledReviewChannelKind,
			"trigger_kinds":       cfg.ArtifactKind + ",scheduled_review",
			"review_date":         reviewDate,
			"window_start":        reviewStart.UTC().Format(time.RFC3339),
			"window_end":          reviewEnd.UTC().Format(time.RFC3339),
			"message_count":       strconv.Itoa(messageCount),
			"transcript_path":     transcriptPath,
			"child_local_subject": "false",
		}),
	}
	if title := strings.TrimSpace(cfg.Title); title != "" {
		artifact.Metadata["operator_title"] = title
		artifact.Metadata["channel_label"] = title
	}
	return artifact
}

func (r *Runtime) recordScheduledReviewFailure(agent core.DurableAgent, cfg scheduledReviewConfig, reviewDate string, turnSummary string, cause error, now time.Time, status string) error {
	return r.recordScheduledReviewFailureWithIdempotencyKey(agent, cfg, reviewDate, turnSummary, cause, now, status, "")
}

func (r *Runtime) recordScheduledReviewFailureWithIdempotencyKey(agent core.DurableAgent, cfg scheduledReviewConfig, reviewDate string, turnSummary string, cause error, now time.Time, status string, idempotencyKey string) error {
	if r == nil || r.store == nil {
		return nil
	}
	if cause == nil {
		cause = fmt.Errorf("scheduled review wake failed")
	}
	var updated core.DurableAgentScheduledReviewRuntimeState
	_, _, err := r.store.UpdateDurableAgentContinuity(agent.AgentID, func(continuity core.DurableAgentContinuityState) (core.DurableAgentContinuityState, error) {
		current := core.DurableAgentScheduledReviewRuntimeState{}
		if continuity.ScheduledReview != nil {
			current = *continuity.ScheduledReview
		}
		updated = scheduledReviewRecordFailure(current, reviewDate, cause, now, status)
		continuity.ScheduledReview = &updated
		return continuity, nil
	})
	if err != nil {
		return err
	}
	artifact := scheduledReviewFailureArtifact(agent, cfg, reviewDate, turnSummary, cause, updated, status)
	if _, err := durableagent.NewRuntime(r.store).QueueReviewArtifactWithIdempotencyKey(agent, artifact, idempotencyKey); err != nil {
		return fmt.Errorf("queue scheduled review failure artifact: %w", err)
	}
	return nil
}

func scheduledReviewFailureArtifact(agent core.DurableAgent, cfg scheduledReviewConfig, reviewDate string, turnSummary string, cause error, state core.DurableAgentScheduledReviewRuntimeState, status string) core.DurableReviewArtifact {
	title := strings.TrimSpace(cfg.Title)
	if title == "" {
		title = "Scheduled review"
	}
	errorText := "scheduled review wake failed"
	if cause != nil {
		errorText = truncateRunes(cause.Error(), 900)
	}
	status = firstNonEmpty(strings.TrimSpace(status), "wake_failed")
	summary := fmt.Sprintf("%s %s for %s; backoff recorded until %s.", title, strings.ReplaceAll(status, "_", " "), strings.TrimSpace(reviewDate), state.BackoffUntil.UTC().Format(time.RFC3339))
	if trimmed := strings.TrimSpace(turnSummary); trimmed != "" {
		summary += " " + truncateRunes(trimmed, 600)
	}
	metadata := scheduledReviewArtifactMetadata(cfg, map[string]string{
		"channel_kind":        scheduledReviewChannelKind,
		"trigger_kinds":       cfg.ArtifactKind + ",scheduled_review," + status,
		"review_date":         strings.TrimSpace(reviewDate),
		"failure_count":       strconv.Itoa(state.FailureCount),
		"backoff_until":       state.BackoffUntil.UTC().Format(time.RFC3339),
		"last_error":          errorText,
		"failure_status":      status,
		"operator_status":     "blocked",
		"child_local_subject": "false",
	})
	if title != "" {
		metadata["operator_title"] = title + " wake blocked"
		metadata["channel_label"] = title
	}
	return core.DurableReviewArtifact{
		AgentID:       strings.TrimSpace(agent.AgentID),
		Summary:       summary,
		IntervalLabel: strings.TrimSpace(reviewDate),
		LocalActions:  []string{"Scheduled-review wake failed; recorded failure lifecycle state and backoff instead of retry-spamming."},
		Questions:     []string{"Inspect the failure only if this scheduled review is currently needed; otherwise let backoff suppress retries."},
		RiskFlags:     []string{cfg.ArtifactKind, "wake_failed", "backoff_recorded"},
		Metadata:      metadata,
	}
}

func (r *Runtime) wrapScheduledReviewPrepareFailure(agent core.DurableAgent, cfg scheduledReviewConfig, reviewDate string, status string, contextLabel string, cause error, now time.Time) error {
	wrapped := fmt.Errorf("%s: %w", strings.TrimSpace(contextLabel), cause)
	if err := r.recordScheduledReviewFailure(agent, cfg, reviewDate, "", wrapped, now, status); err != nil {
		return fmt.Errorf("%w (and failed to record scheduled review failure: %v)", wrapped, err)
	}
	return wrapped
}

func scheduledReviewArtifactMetadata(cfg scheduledReviewConfig, metadata map[string]string) map[string]string {
	if metadata == nil {
		metadata = map[string]string{}
	}
	if recipeID := strings.TrimSpace(cfg.RecipeID); recipeID != "" {
		metadata["recipe_id"] = recipeID
	}
	if recipeVersion := strings.TrimSpace(cfg.RecipeVersion); recipeVersion != "" {
		metadata["recipe_version"] = recipeVersion
	}
	if recipeSource := strings.TrimSpace(cfg.RecipeSource); recipeSource != "" {
		metadata["recipe_source"] = recipeSource
	}
	return metadata
}
