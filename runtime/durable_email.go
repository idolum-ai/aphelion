//go:build linux

package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/session"
)

const (
	durableEmailLoopCadence      = time.Minute
	defaultDurableEmailPollEvery = 5 * time.Minute
	durableEmailSearchLimit      = 10
	durableEmailCursorLimit      = 50
)

var durableEmailCommandRunner = func(ctx context.Context, args ...string) ([]byte, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("durable email command runner requires argv")
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	out, err := cmd.Output()
	if err == nil {
		return out, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		stderr := strings.TrimSpace(string(exitErr.Stderr))
		if stderr != "" {
			return nil, fmt.Errorf("%w: %s", err, stderr)
		}
	}
	return nil, err
}

type durableEmailThreadDigest struct {
	ThreadID        string
	MessageID       string
	From            string
	Subject         string
	Snippet         string
	Body            string
	ReceivedAt      time.Time
	Labels          []string
	Artifacts       []core.Artifact
	AttachmentNames []string
}

type durableEmailThreadResponse struct {
	ID        string                     `json:"id,omitempty"`
	HistoryID string                     `json:"historyId,omitempty"`
	Messages  []durableEmailMessageEntry `json:"messages,omitempty"`
}

type durableEmailMessageEntry struct {
	ID           string                  `json:"id,omitempty"`
	ThreadID     string                  `json:"threadId,omitempty"`
	Snippet      string                  `json:"snippet,omitempty"`
	InternalDate string                  `json:"internalDate,omitempty"`
	LabelIDs     []string                `json:"labelIds,omitempty"`
	Payload      durableEmailMessagePart `json:"payload,omitempty"`
}

type durableEmailMessagePart struct {
	MimeType string                    `json:"mimeType,omitempty"`
	Filename string                    `json:"filename,omitempty"`
	Headers  []durableEmailHeader      `json:"headers,omitempty"`
	Body     durableEmailPartBody      `json:"body,omitempty"`
	Parts    []durableEmailMessagePart `json:"parts,omitempty"`
}

type durableEmailHeader struct {
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

type durableEmailPartBody struct {
	Data         string `json:"data,omitempty"`
	AttachmentID string `json:"attachmentId,omitempty"`
}

func (r *Runtime) StartDurableEmailLoop(ctx context.Context, logger func(string, ...any)) {
	if r == nil || r.store == nil {
		return
	}
	if logger == nil {
		logger = func(string, ...any) {}
	}
	go runPeriodic(ctx, durableEmailLoopCadence, func(runCtx context.Context) {
		if err := r.pollDurableEmailAgents(runCtx, time.Now().UTC()); err != nil {
			logger("WARN durable email poll failed: %v", err)
		}
	})
}

func RunDurableEmailPollOnce(ctx context.Context, cfg *config.Config, store *session.SQLiteStore, agentID string, now time.Time) error {
	if cfg == nil {
		return fmt.Errorf("durable email poll config is nil")
	}
	if store == nil {
		return fmt.Errorf("durable email poll store is nil")
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return fmt.Errorf("durable email poll agent id is required")
	}
	agent, err := store.DurableAgent(agentID)
	if err != nil {
		return fmt.Errorf("load durable email agent: %w", err)
	}
	if agent == nil {
		return fmt.Errorf("durable email agent %q not found", agentID)
	}
	if !durableEmailWakeEnabled(*agent) {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	rt := &Runtime{
		cfg:   normalizeRuntimeConfig(cfg),
		store: store,
	}
	return rt.pollDurableEmailAgent(ctx, *agent, now.UTC())
}

func (r *Runtime) pollDurableEmailAgents(ctx context.Context, now time.Time) error {
	if r == nil || r.store == nil {
		return nil
	}
	agents, err := r.store.ListDurableAgents()
	if err != nil {
		return err
	}
	var errs []string
	for _, agent := range agents {
		if !durableEmailWakeEnabled(agent) {
			continue
		}
		if r.shouldRunDurableEmailInChild(agent) {
			if err := r.pollDurableEmailAgentViaChild(ctx, agent, now); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", agent.AgentID, err))
			}
			continue
		}
		if err := r.pollDurableEmailAgent(ctx, agent, now); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", agent.AgentID, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf(strings.Join(errs, "; "))
	}
	return nil
}

func (r *Runtime) shouldRunDurableEmailInChild(agent core.DurableAgent) bool {
	if r == nil || r.durableEmailChild == nil {
		return false
	}
	return core.NormalizeNodeLLMBootstrap(agent.BootstrapLLM).Configured()
}

func (r *Runtime) pollDurableEmailAgentViaChild(ctx context.Context, agent core.DurableAgent, now time.Time) error {
	if r == nil || r.durableEmailChild == nil {
		return r.pollDurableEmailAgent(ctx, agent, now)
	}
	scope, err := r.scopeForDurableAgent(agent)
	if err != nil {
		return err
	}
	if !r.durableEmailChild.Supports(scope, agent) {
		return r.pollDurableEmailAgent(ctx, agent, now)
	}
	return r.durableEmailChild.Run(ctx, scope, agent, now)
}

func durableEmailWakeEnabled(agent core.DurableAgent) bool {
	return strings.TrimSpace(agent.ChannelKind) == "email" &&
		durableEmailWakeMode(strings.TrimSpace(agent.WakeupMode)) != "" &&
		strings.TrimSpace(agent.Status) == "active" &&
		agent.ChannelConfig.Email != nil &&
		strings.TrimSpace(agent.ChannelConfig.Email.Adapter) == "gog_cli"
}

func (r *Runtime) pollDurableEmailAgent(ctx context.Context, agent core.DurableAgent, now time.Time) error {
	state, err := r.store.DurableAgentState(agent.AgentID)
	if err != nil {
		if !strings.Contains(err.Error(), "no rows") {
			return err
		}
		state = &core.DurableAgentState{AgentID: agent.AgentID}
	}
	continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		return fmt.Errorf("parse durable email continuity state: %w", err)
	}
	scope, err := r.scopeForDurableAgent(agent)
	if err != nil {
		return err
	}

	mode := durableEmailWakeMode(agent.WakeupMode)
	interval := durableEmailPollInterval(agent)
	pollDue := mode != "push" && (state.LastWakeAt.IsZero() || !now.Before(state.LastWakeAt.Add(interval)))
	pushThreadIDs, pushWake, err := durableEmailConsumePushWake(scope.WorkingRoot, agent.AgentID)
	if err != nil {
		return err
	}
	if !pollDue && !pushWake {
		return nil
	}

	threads, cursor, err := r.fetchDurableEmailThreads(ctx, agent, state.Cursor, pushThreadIDs)
	if err != nil {
		return err
	}
	var neverRetainRedactions int
	threads, neverRetainRedactions = durableEmailApplyNeverRetain(agent, threads)
	pendingThreads := append([]durableEmailThreadDigest(nil), durableEmailPendingThreads(continuity)...)
	pendingThreads = durableEmailAppendUniquePending(pendingThreads, threads)
	continuity.EmailPending = durableEmailPendingState(pendingThreads)

	state.Cursor = cursor
	state.Status = "dormant"
	state.LastWakeAt = now
	stateJSON, err := continuity.Marshal()
	if err != nil {
		return fmt.Errorf("marshal durable email continuity state: %w", err)
	}
	state.StateJSON = stateJSON
	if err := r.store.SaveDurableAgentState(*state); err != nil {
		return err
	}
	if len(pendingThreads) == 0 {
		return nil
	}
	if !durableEmailSynthesisDue(agent, state, now) {
		return nil
	}
	artifact := durableEmailReviewArtifact(agent, pendingThreads, now, neverRetainRedactions)
	if artifact == nil {
		return nil
	}
	continuity.EmailPending = nil
	stateJSON, err = continuity.Marshal()
	if err != nil {
		return fmt.Errorf("marshal durable email continuity state: %w", err)
	}
	state.StateJSON = stateJSON
	if err := r.store.SaveDurableAgentState(*state); err != nil {
		return err
	}
	_, err = durableagent.NewRuntime(r.store).QueueReviewArtifact(agent, *artifact)
	return err
}

func durableEmailPollInterval(agent core.DurableAgent) time.Duration {
	if agent.ChannelConfig.Email != nil {
		if d, err := time.ParseDuration(strings.TrimSpace(agent.ChannelConfig.Email.PollInterval)); err == nil && d > 0 {
			return d
		}
	}
	return defaultDurableEmailPollEvery
}

func (r *Runtime) fetchDurableEmailThreads(ctx context.Context, agent core.DurableAgent, cursor string, forcedThreadIDs []string) ([]durableEmailThreadDigest, string, error) {
	scope, err := r.scopeForDurableAgent(agent)
	if err != nil {
		return nil, "", err
	}
	threadIDs := normalizeDurableEmailThreadIDs(forcedThreadIDs)
	if len(threadIDs) == 0 {
		searchArgs := durableEmailBaseCommand(agent)
		searchArgs = append(searchArgs, "gmail", "search")
		searchArgs = append(searchArgs, firstNonEmpty(strings.TrimSpace(agent.ChannelConfig.Email.Query), "label:inbox"))
		searchArgs = append(searchArgs, "--json", "--results-only", "--max", strconv.Itoa(durableEmailSearchLimit), "--no-input")
		raw, err := durableEmailCommandRunner(ctx, searchArgs...)
		if err != nil {
			return nil, "", err
		}
		threadIDs, err = parseDurableEmailSearchThreadIDs(raw)
		if err != nil {
			return nil, "", err
		}
	}
	seen := durableEmailCursorSet(cursor)
	newestCursor := durableEmailMergeCursor(threadIDs, seen)
	newThreadIDs := make([]string, 0, len(threadIDs))
	for _, threadID := range threadIDs {
		if threadID == "" || seen[threadID] {
			continue
		}
		newThreadIDs = append(newThreadIDs, threadID)
	}
	if len(newThreadIDs) == 0 {
		return nil, newestCursor, nil
	}

	attachmentRoot := filepath.Join(scope.WorkingRoot, ".aphelion", "email")
	if err := os.MkdirAll(attachmentRoot, 0o700); err != nil {
		return nil, "", fmt.Errorf("create durable email attachment root: %w", err)
	}

	out := make([]durableEmailThreadDigest, 0, len(newThreadIDs))
	for _, threadID := range newThreadIDs {
		threadArgs := durableEmailBaseCommand(agent)
		threadArgs = append(threadArgs, "gmail", "thread", "get", threadID, "--json", "--results-only", "--full", "--no-input")
		threadRaw, err := durableEmailCommandRunner(ctx, threadArgs...)
		if err != nil {
			return nil, "", err
		}
		digest, err := decodeDurableEmailThread(agent, threadID, attachmentRoot, threadRaw, ctx)
		if err != nil {
			return nil, "", err
		}
		out = append(out, digest)
	}
	return out, newestCursor, nil
}

func durableEmailWakeMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "poll":
		return "poll"
	case "push":
		return "push"
	case "poll_or_push", "both":
		return "poll_or_push"
	default:
		return ""
	}
}

func durableEmailNormalizeSynthesisCadence(agent core.DurableAgent) time.Duration {
	if agent.ChannelConfig.Email == nil {
		return 0
	}
	raw := strings.TrimSpace(agent.ChannelConfig.Email.SynthesisCadence)
	if raw == "" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0
	}
	return d
}

func durableEmailSynthesisDue(agent core.DurableAgent, state *core.DurableAgentState, now time.Time) bool {
	if state == nil {
		return true
	}
	cadence := durableEmailNormalizeSynthesisCadence(agent)
	if cadence <= 0 {
		return true
	}
	if state.LastReviewAt.IsZero() {
		return true
	}
	return !now.Before(state.LastReviewAt.Add(cadence))
}

func durableEmailPushInboxDir(workingRoot string, agentID string) string {
	return filepath.Join(strings.TrimSpace(workingRoot), ".aphelion", "email-push", strings.TrimSpace(agentID), "inbox")
}

type durableEmailPushEvent struct {
	ThreadIDs []string `json:"thread_ids,omitempty"`
}

func durableEmailConsumePushWake(workingRoot string, agentID string) ([]string, bool, error) {
	dir := durableEmailPushInboxDir(workingRoot, agentID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if len(entries) == 0 {
		return nil, false, nil
	}
	threads := make([]string, 0, len(entries))
	triggered := false
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		triggered = true
		raw, readErr := os.ReadFile(path)
		if readErr == nil {
			var event durableEmailPushEvent
			if decodeErr := json.Unmarshal(raw, &event); decodeErr == nil {
				threads = append(threads, event.ThreadIDs...)
			}
		}
		_ = os.Remove(path)
	}
	return normalizeDurableEmailThreadIDs(threads), triggered, nil
}

func normalizeDurableEmailThreadIDs(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func durableEmailPendingThreads(continuity core.DurableAgentContinuityState) []durableEmailThreadDigest {
	if continuity.EmailPending == nil || len(continuity.EmailPending.Threads) == 0 {
		return nil
	}
	out := make([]durableEmailThreadDigest, 0, len(continuity.EmailPending.Threads))
	for _, thread := range continuity.EmailPending.Threads {
		out = append(out, durableEmailThreadDigest{
			ThreadID:        strings.TrimSpace(thread.ThreadID),
			MessageID:       strings.TrimSpace(thread.MessageID),
			From:            strings.TrimSpace(thread.From),
			Subject:         strings.TrimSpace(thread.Subject),
			Snippet:         strings.TrimSpace(thread.Snippet),
			Body:            strings.TrimSpace(thread.Body),
			ReceivedAt:      thread.ReceivedAt,
			Labels:          append([]string(nil), thread.Labels...),
			AttachmentNames: append([]string(nil), thread.AttachmentNames...),
		})
	}
	return out
}

func durableEmailPendingState(threads []durableEmailThreadDigest) *core.DurableAgentEmailPendingState {
	if len(threads) == 0 {
		return nil
	}
	out := make([]core.DurableAgentEmailPendingThread, 0, len(threads))
	for _, thread := range threads {
		id := strings.TrimSpace(thread.ThreadID)
		if id == "" {
			continue
		}
		out = append(out, core.DurableAgentEmailPendingThread{
			ThreadID:        id,
			MessageID:       strings.TrimSpace(thread.MessageID),
			From:            strings.TrimSpace(thread.From),
			Subject:         strings.TrimSpace(thread.Subject),
			Snippet:         strings.TrimSpace(thread.Snippet),
			Body:            strings.TrimSpace(thread.Body),
			ReceivedAt:      thread.ReceivedAt,
			Labels:          append([]string(nil), thread.Labels...),
			AttachmentNames: append([]string(nil), thread.AttachmentNames...),
		})
	}
	if len(out) == 0 {
		return nil
	}
	return &core.DurableAgentEmailPendingState{Threads: out}
}

func durableEmailAppendUniquePending(current []durableEmailThreadDigest, next []durableEmailThreadDigest) []durableEmailThreadDigest {
	if len(next) == 0 {
		return current
	}
	seen := make(map[string]struct{}, len(current)+len(next))
	out := make([]durableEmailThreadDigest, 0, len(current)+len(next))
	for _, thread := range current {
		id := strings.TrimSpace(thread.ThreadID)
		if id == "" {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, thread)
	}
	for _, thread := range next {
		id := strings.TrimSpace(thread.ThreadID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, thread)
	}
	return out
}

func durableEmailApplyNeverRetain(agent core.DurableAgent, threads []durableEmailThreadDigest) ([]durableEmailThreadDigest, int) {
	if len(threads) == 0 || agent.ChannelConfig.Email == nil || len(agent.ChannelConfig.Email.NeverRetain) == 0 {
		return threads, 0
	}
	rules := make([]string, 0, len(agent.ChannelConfig.Email.NeverRetain))
	for _, rule := range agent.ChannelConfig.Email.NeverRetain {
		rule = strings.ToLower(strings.TrimSpace(rule))
		if rule == "" {
			continue
		}
		rules = append(rules, rule)
	}
	if len(rules) == 0 {
		return threads, 0
	}
	redactions := 0
	out := make([]durableEmailThreadDigest, 0, len(threads))
	for _, thread := range threads {
		if durableEmailThreadMatchesNeverRetain(thread, rules) {
			redactions++
			thread.From = ""
			thread.Subject = ""
			thread.Snippet = ""
			thread.Body = ""
			thread.AttachmentNames = nil
		}
		out = append(out, thread)
	}
	return out, redactions
}

func durableEmailThreadMatchesNeverRetain(thread durableEmailThreadDigest, rules []string) bool {
	corpus := strings.ToLower(strings.Join([]string{
		thread.From,
		thread.Subject,
		thread.Snippet,
		thread.Body,
		strings.Join(thread.AttachmentNames, " "),
	}, "\n"))
	if corpus == "" {
		return false
	}
	for _, rule := range rules {
		if strings.Contains(corpus, rule) {
			return true
		}
	}
	return false
}

func durableEmailBaseCommand(agent core.DurableAgent) []string {
	args := []string{"gog"}
	if agent.ChannelConfig.Email != nil && strings.TrimSpace(agent.ChannelConfig.Email.Account) != "" {
		args = append(args, "--account", strings.TrimSpace(agent.ChannelConfig.Email.Account))
	}
	return args
}

func parseDurableEmailSearchThreadIDs(raw []byte) ([]string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, nil
	}
	var payload any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return nil, fmt.Errorf("decode durable email search payload: %w", err)
	}
	ids := collectDurableEmailIDs(payload)
	return uniqueStrings(ids), nil
}

func collectDurableEmailIDs(value any) []string {
	switch typed := value.(type) {
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, collectDurableEmailIDs(item)...)
		}
		return out
	case map[string]any:
		if rawID, ok := typed["id"].(string); ok && strings.TrimSpace(rawID) != "" {
			return []string{strings.TrimSpace(rawID)}
		}
		for _, key := range []string{"threads", "items", "messages", "result"} {
			if nested, ok := typed[key]; ok {
				return collectDurableEmailIDs(nested)
			}
		}
	}
	return nil
}

func decodeDurableEmailThread(agent core.DurableAgent, threadID string, attachmentRoot string, raw []byte, ctx context.Context) (durableEmailThreadDigest, error) {
	var thread durableEmailThreadResponse
	if err := json.Unmarshal(raw, &thread); err != nil {
		return durableEmailThreadDigest{}, fmt.Errorf("decode durable email thread %s: %w", threadID, err)
	}
	if len(thread.Messages) == 0 {
		return durableEmailThreadDigest{ThreadID: threadID}, nil
	}
	msg := thread.Messages[len(thread.Messages)-1]
	digest := durableEmailThreadDigest{
		ThreadID:   firstNonEmpty(strings.TrimSpace(msg.ThreadID), strings.TrimSpace(thread.ID), threadID),
		MessageID:  strings.TrimSpace(msg.ID),
		From:       durableEmailHeaderValue(msg.Payload.Headers, "From"),
		Subject:    durableEmailHeaderValue(msg.Payload.Headers, "Subject"),
		Snippet:    strings.TrimSpace(msg.Snippet),
		Body:       durableEmailExtractBody(msg.Payload),
		ReceivedAt: durableEmailInternalDate(msg.InternalDate),
		Labels:     append([]string(nil), msg.LabelIDs...),
	}
	artifacts, names, err := durableEmailAttachmentsForMessage(ctx, agent, msg, attachmentRoot, digest.ThreadID)
	if err != nil {
		return durableEmailThreadDigest{}, err
	}
	digest.Artifacts = artifacts
	digest.AttachmentNames = names
	return digest, nil
}

func durableEmailAttachmentsForMessage(ctx context.Context, agent core.DurableAgent, msg durableEmailMessageEntry, attachmentRoot string, threadID string) ([]core.Artifact, []string, error) {
	parts := durableEmailFlattenParts(msg.Payload)
	artifacts := make([]core.Artifact, 0, len(parts))
	names := make([]string, 0, len(parts))
	for _, part := range parts {
		filename := strings.TrimSpace(part.Filename)
		attachmentID := strings.TrimSpace(part.Body.AttachmentID)
		if filename == "" || attachmentID == "" {
			continue
		}
		data, err := durableEmailDownloadAttachment(ctx, agent, strings.TrimSpace(msg.ID), attachmentID, attachmentRoot, filename)
		if err != nil {
			return nil, nil, err
		}
		subtype := ""
		kind := "document"
		if strings.EqualFold(strings.TrimSpace(part.MimeType), "application/pdf") {
			subtype = "pdf"
		}
		artifacts = append(artifacts, core.Artifact{
			ID:               fmt.Sprintf("%s:%s", strings.TrimSpace(msg.ID), attachmentID),
			Channel:          "email",
			SourceType:       "gmail_attachment",
			Kind:             kind,
			Subtype:          subtype,
			Data:             data,
			MimeType:         strings.TrimSpace(part.MimeType),
			Filename:         filename,
			Scope:            threadID,
			DefaultRetention: "child_local",
			RetentionCeiling: "child_local",
			Metadata: map[string]string{
				"thread_id":  threadID,
				"message_id": strings.TrimSpace(msg.ID),
			},
		})
		names = append(names, filename)
	}
	return artifacts, names, nil
}

func durableEmailDownloadAttachment(ctx context.Context, agent core.DurableAgent, messageID string, attachmentID string, attachmentRoot string, filename string) ([]byte, error) {
	destPath := filepath.Join(attachmentRoot, safeAttachmentFilename(messageID, attachmentID, filename))
	args := durableEmailBaseCommand(agent)
	args = append(args, "gmail", "attachment", messageID, attachmentID, "--no-input", "--out", destPath)
	out, err := durableEmailCommandRunner(ctx, args...)
	if err != nil {
		return nil, err
	}
	if len(out) > 0 {
		return out, nil
	}
	data, readErr := os.ReadFile(destPath)
	if readErr != nil {
		return nil, readErr
	}
	return data, nil
}

func durableEmailHeaderValue(headers []durableEmailHeader, name string) string {
	for _, header := range headers {
		if strings.EqualFold(strings.TrimSpace(header.Name), strings.TrimSpace(name)) {
			return strings.TrimSpace(header.Value)
		}
	}
	return ""
}

func durableEmailExtractBody(payload durableEmailMessagePart) string {
	parts := durableEmailFlattenParts(payload)
	for _, part := range parts {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(part.MimeType)), "text/plain") {
			continue
		}
		if decoded := durableEmailDecodePartData(part.Body.Data); decoded != "" {
			return decoded
		}
	}
	return durableEmailDecodePartData(payload.Body.Data)
}

func durableEmailFlattenParts(root durableEmailMessagePart) []durableEmailMessagePart {
	if len(root.Parts) == 0 {
		return []durableEmailMessagePart{root}
	}
	out := make([]durableEmailMessagePart, 0, len(root.Parts))
	for _, part := range root.Parts {
		out = append(out, durableEmailFlattenParts(part)...)
	}
	return out
}

func durableEmailDecodePartData(data string) string {
	data = strings.TrimSpace(data)
	if data == "" {
		return ""
	}
	decoded, err := base64.RawURLEncoding.DecodeString(data)
	if err != nil {
		return data
	}
	return strings.TrimSpace(string(decoded))
}

func durableEmailInternalDate(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	ms, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

func durableEmailCursorSet(cursor string) map[string]bool {
	out := make(map[string]bool)
	for _, id := range strings.Split(strings.TrimSpace(cursor), ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		out[id] = true
	}
	return out
}

func durableEmailMergeCursor(latest []string, seen map[string]bool) string {
	ordered := make([]string, 0, durableEmailCursorLimit)
	appendID := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || durableEmailContains(ordered, id) {
			return
		}
		if len(ordered) >= durableEmailCursorLimit {
			return
		}
		ordered = append(ordered, id)
	}
	for _, id := range latest {
		appendID(id)
	}
	for id := range seen {
		appendID(id)
	}
	return strings.Join(ordered, ",")
}

func durableEmailReviewArtifact(agent core.DurableAgent, threads []durableEmailThreadDigest, now time.Time, neverRetainRedactions int) *core.DurableReviewArtifact {
	if len(threads) == 0 {
		return nil
	}
	important := make([]durableEmailThreadDigest, 0, len(threads))
	attachments := make([]string, 0, len(threads))
	artifactRefs := make([]string, 0, len(threads))
	riskFlags := make([]string, 0, len(threads))
	for _, thread := range threads {
		artifactRefs = append(artifactRefs, "email://"+strings.TrimSpace(agent.AgentID)+"/thread/"+strings.TrimSpace(thread.ThreadID))
		if len(thread.AttachmentNames) > 0 {
			attachments = append(attachments, thread.AttachmentNames...)
			riskFlags = append(riskFlags, "attachment_present")
		}
		if durableEmailThreadImportant(agent, thread) {
			important = append(important, thread)
		}
	}
	if len(attachments) > 0 && agent.ChannelConfig.Email != nil && agent.ChannelConfig.Email.SummarizePDFs {
		riskFlags = append(riskFlags, "pdf_attachment")
	}
	if neverRetainRedactions > 0 {
		riskFlags = append(riskFlags, "never_retain_enforced")
	}

	summary := fmt.Sprintf("Inbox check reviewed %d new email thread(s).", len(threads))
	if len(important) > 0 {
		summary = fmt.Sprintf("Inbox check surfaced %d important email thread(s), including %q.", len(important), firstNonEmpty(strings.TrimSpace(important[0].Subject), strings.TrimSpace(important[0].Snippet), "email thread"))
	}

	localActions := []string{
		"Checked the inbox in read_only mode via gog_cli.",
		"Did not send or draft any email.",
	}
	if len(attachments) > 0 {
		localActions = append(localActions, "Surfaced attachments for bounded parent review.")
	}
	if neverRetainRedactions > 0 {
		localActions = append(localActions, fmt.Sprintf("Applied never_retain scrubbing to %d thread(s).", neverRetainRedactions))
	}

	questions := []string{}
	if len(important) > 0 {
		questions = append(questions, "Should any of the surfaced threads be retained, followed up on later, or widen the child's charter?")
	}

	metadata := map[string]string{
		"channel_kind":      "email",
		"durable_agent_id":  strings.TrimSpace(agent.AgentID),
		"email_address":     firstNonEmpty(agent.ChannelConfig.Email.Address),
		"thread_ids":        strings.Join(threadIDsForEmailReview(threads), ","),
		"subjects":          strings.Join(threadSubjectsForEmailReview(threads), "; "),
		"attachments":       strings.Join(uniqueStrings(attachments), ","),
		"important_threads": strconv.Itoa(len(important)),
	}
	if neverRetainRedactions > 0 {
		metadata["never_retain_redactions"] = strconv.Itoa(neverRetainRedactions)
	}

	return &core.DurableReviewArtifact{
		AgentID:       strings.TrimSpace(agent.AgentID),
		Summary:       summary,
		IntervalLabel: now.UTC().Format(time.RFC3339),
		LocalActions:  localActions,
		Questions:     questions,
		RiskFlags:     uniqueStrings(riskFlags),
		ArtifactRefs:  artifactRefs,
		Metadata:      metadata,
	}
}

func durableEmailThreadImportant(agent core.DurableAgent, thread durableEmailThreadDigest) bool {
	if len(thread.AttachmentNames) > 0 {
		for _, name := range thread.AttachmentNames {
			if strings.HasSuffix(strings.ToLower(strings.TrimSpace(name)), ".pdf") {
				return true
			}
		}
	}
	rules := []string{}
	if agent.ChannelConfig.Email != nil {
		rules = agent.ChannelConfig.Email.SurfaceRules
	}
	corpus := strings.ToLower(strings.Join([]string{thread.Subject, thread.Snippet, thread.Body, thread.From}, "\n"))
	for _, rule := range rules {
		if strings.Contains(corpus, strings.ToLower(strings.TrimSpace(rule))) {
			return true
		}
	}
	return false
}

func threadIDsForEmailReview(threads []durableEmailThreadDigest) []string {
	out := make([]string, 0, len(threads))
	for _, thread := range threads {
		if strings.TrimSpace(thread.ThreadID) == "" {
			continue
		}
		out = append(out, strings.TrimSpace(thread.ThreadID))
	}
	return out
}

func threadSubjectsForEmailReview(threads []durableEmailThreadDigest) []string {
	out := make([]string, 0, len(threads))
	for _, thread := range threads {
		subject := firstNonEmpty(strings.TrimSpace(thread.Subject), strings.TrimSpace(thread.Snippet))
		if subject == "" {
			continue
		}
		out = append(out, subject)
	}
	return out
}

func safeAttachmentFilename(messageID string, attachmentID string, filename string) string {
	base := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '.', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, strings.TrimSpace(filename))
	if base == "" {
		base = "attachment"
	}
	return strings.TrimSpace(messageID) + "-" + strings.TrimSpace(attachmentID) + "-" + base
}

func durableEmailContains(values []string, want string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == strings.TrimSpace(want) {
			return true
		}
	}
	return false
}
