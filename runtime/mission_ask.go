//go:build linux

package runtime

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
	"github.com/idolum-ai/aphelion/turn"
)

const (
	hiddenInputMissionAsk = "mission_ask_pending"

	missionAskMinScore       = 2
	missionAskHighScore      = 4
	missionAskMaxQuestionLen = 260
)

type missionAskInlineSender interface {
	SendInlineKeyboard(ctx context.Context, chatID int64, text string, rows [][]telegram.InlineButton, replyTo *int64) (int64, error)
}

type missionAskCandidate struct {
	Mission session.MissionState
	Score   int
	Reasons []string
}

type missionAskObservation struct {
	Owner       string
	Query       string
	MissionID   string
	MissionName string
	Question    string
	Confidence  session.MissionAskConfidence
	Fingerprint string
	Evidence    map[string]any
	ShouldAsk   bool
	NeedsModel  bool
}

func (r *Runtime) maybeOfferMissionAsk(ctx context.Context, key session.SessionKey, msg core.InboundMessage, ledgerText string, result *turn.Result) error {
	if !r.missionAskObserverReady(msg, result) {
		return nil
	}
	actor, ok := r.resolver.ResolveTelegramUser(msg.SenderID)
	if !ok {
		return nil
	}
	if r.missionAskHasActiveApprovalSurface(key) {
		return nil
	}
	observation, err := r.observeMissionAsk(ctx, key, msg, actor, ledgerText, result)
	if err != nil {
		log.Printf("WARN mission ask observer failed chat_id=%d err=%v", msg.ChatID, err)
		return nil
	}
	if !observation.ShouldAsk {
		return nil
	}
	if observation.NeedsModel {
		if refined, ok := r.classifyMissionAsk(ctx, observation); ok {
			observation = refined
		}
	}
	if !observation.ShouldAsk {
		return nil
	}
	prompt, allowed, reason, err := r.store.CreateMissionAskPromptIfAllowed(session.MissionAskPrompt{
		Owner:             observation.Owner,
		ChatID:            msg.ChatID,
		SenderID:          msg.SenderID,
		SessionID:         session.SessionIDForKey(key),
		Scope:             key.Scope,
		SourceMessageID:   msg.MessageID,
		MissionID:         observation.MissionID,
		Confidence:        observation.Confidence,
		Status:            session.MissionAskStatusPending,
		QuestionText:      clampMissionAskText(observation.Question, missionAskMaxQuestionLen),
		SourceFingerprint: observation.Fingerprint,
		EvidenceJSON:      encodeMissionAskEvidence(observation.Evidence),
	}, time.Now().UTC())
	if err != nil {
		log.Printf("WARN mission ask prompt create failed chat_id=%d err=%v", msg.ChatID, err)
		return nil
	}
	if !allowed {
		r.recordExecutionEvent(key, core.ExecutionEventMissionAskSuppressed, "mission_ask", "suppressed", map[string]any{
			"reason":      strings.TrimSpace(reason),
			"confidence":  string(observation.Confidence),
			"mission_id":  observation.MissionID,
			"fingerprint": observation.Fingerprint,
		}, time.Now().UTC())
		return nil
	}
	return r.sendMissionAskPrompt(ctx, key, msg, prompt)
}

func (r *Runtime) missionAskObserverReady(msg core.InboundMessage, result *turn.Result) bool {
	if r == nil || r.store == nil || r.outbound == nil || r.resolver == nil {
		return false
	}
	if msg.ChatID == 0 || msg.SenderID == 0 {
		return false
	}
	if strings.TrimSpace(msg.DurableAgentID) != "" || msg.Origin == core.InboundOriginTurnAuthorization {
		return false
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" || strings.HasPrefix(text, "/") {
		return false
	}
	if surface := strings.TrimSpace(msg.IngressSurface); strings.Contains(surface, "callback-work") || strings.Contains(surface, "decision-resume") {
		return false
	}
	if result != nil && (result.PersonaIntent.Decision != "" || result.GovernorIntent.Decision != "") {
		return false
	}
	return result != nil && result.Commit.Persisted && strings.TrimSpace(result.VisibleReply) != ""
}

func (r *Runtime) missionAskHasActiveApprovalSurface(key session.SessionKey) bool {
	cont, ok, err := r.store.ContinuationStateIfExists(key)
	if err == nil && ok && session.NormalizeContinuationState(cont).Active() {
		return true
	}
	sess, err := r.store.Load(key)
	if err != nil || sess == nil {
		return false
	}
	op := session.NormalizeOperationState(sess.OperationState)
	if op.Proposal.Status == session.ProposalStatusPending || op.Status == session.OperationStatusBlocked && strings.Contains(strings.ToLower(op.Stage), "approval") {
		return true
	}
	return false
}

func (r *Runtime) observeMissionAsk(ctx context.Context, key session.SessionKey, msg core.InboundMessage, actor principal.Principal, ledgerText string, result *turn.Result) (missionAskObservation, error) {
	owner := missionCommandOwner(actor, msg.SenderID)
	query := strings.TrimSpace(firstRuntimeNonEmpty(ledgerText, msg.Text))
	if query == "" {
		return missionAskObservation{}, nil
	}
	working, _ := r.store.WorkingObjective(key)
	candidates, err := r.missionAskCandidates(owner, query+" "+working.Objective)
	if err != nil {
		return missionAskObservation{}, err
	}
	behavior := r.missionAskBehaviorSignals(key, msg, working, result)
	best := missionAskCandidate{}
	if len(candidates) > 0 {
		best = candidates[0]
	}
	score := len(behavior)
	if best.Score > 0 {
		score += best.Score
	}
	if score < missionAskMinScore {
		return missionAskObservation{Owner: owner, Query: query}, nil
	}
	confidence := session.MissionAskConfidenceLow
	if score >= missionAskHighScore {
		confidence = session.MissionAskConfidenceHigh
	}
	missionID := ""
	missionName := strings.TrimSpace(working.Objective)
	if best.Mission.ID != "" {
		missionID = best.Mission.ID
		missionName = firstRuntimeNonEmpty(best.Mission.Title, best.Mission.Objective, best.Mission.ID)
	}
	question := renderMissionAskQuestion(missionName, missionID != "")
	fingerprint := missionAskFingerprint(owner, missionID, missionName, query)
	evidence := map[string]any{
		"behavior":      behavior,
		"candidate":     missionID,
		"candidate_hit": best.Score,
		"query_preview": clampMissionAskText(query, 180),
	}
	if len(best.Reasons) > 0 {
		evidence["candidate_reasons"] = best.Reasons
	}
	return missionAskObservation{
		Owner:       owner,
		Query:       query,
		MissionID:   missionID,
		MissionName: missionName,
		Question:    question,
		Confidence:  confidence,
		Fingerprint: fingerprint,
		Evidence:    evidence,
		ShouldAsk:   true,
		NeedsModel:  len(candidates) > 1 || confidence == session.MissionAskConfidenceLow,
	}, nil
}

func (r *Runtime) missionAskCandidates(owner string, query string) ([]missionAskCandidate, error) {
	missions, err := r.store.Missions(session.MissionFilter{Owner: owner, Limit: 80})
	if err != nil {
		return nil, err
	}
	queryTokens := missionAskTokens(query)
	out := make([]missionAskCandidate, 0, len(missions))
	for _, mission := range missions {
		switch mission.Status {
		case session.MissionStatusArchived, session.MissionStatusCompleted, session.MissionStatusExpired:
			continue
		}
		score, reasons := scoreMissionAskCandidate(mission, queryTokens)
		if score <= 0 {
			continue
		}
		out = append(out, missionAskCandidate{Mission: mission, Score: score, Reasons: reasons})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Mission.UpdatedAt.After(out[j].Mission.UpdatedAt)
	})
	if len(out) > 5 {
		out = out[:5]
	}
	return out, nil
}

func scoreMissionAskCandidate(mission session.MissionState, queryTokens map[string]struct{}) (int, []string) {
	if len(queryTokens) == 0 {
		return 0, nil
	}
	score := 0
	reasons := make([]string, 0, 4)
	fields := []struct {
		name   string
		text   string
		weight int
	}{
		{"title", mission.Title, 2},
		{"objective", mission.Objective, 2},
		{"next_action", mission.NextAllowedAction, 1},
		{"blocked_reason", mission.BlockedReason, 1},
	}
	for _, field := range fields {
		matches := missionAskTokenMatches(queryTokens, missionAskTokens(field.text))
		if matches == 0 {
			continue
		}
		score += matches * field.weight
		reasons = append(reasons, field.name)
	}
	for _, tag := range mission.Tags {
		if _, ok := queryTokens[strings.ToLower(strings.TrimSpace(tag))]; ok {
			score += 2
			reasons = append(reasons, "tag:"+tag)
		}
	}
	if mission.Pinned {
		score++
	}
	if mission.Status == session.MissionStatusActive {
		score++
	}
	if score > 6 {
		score = 6
	}
	return score, reasons
}

func (r *Runtime) missionAskBehaviorSignals(key session.SessionKey, msg core.InboundMessage, working session.WorkingObjective, result *turn.Result) []string {
	signals := make([]string, 0, 4)
	if msg.TelegramThreadID > 0 || key.Scope.Kind == session.ScopeKindTelegramThread {
		signals = append(signals, "side_thread")
	}
	if strings.TrimSpace(working.Objective) != "" {
		signals = append(signals, "working_objective")
	}
	if result != nil && strings.Contains(result.FloorMetadata, hiddenInputSemanticRecurrence) {
		signals = append(signals, "semantic_recurrence")
	}
	if r.missionAskRecentDowntime(key, msg) {
		signals = append(signals, "downtime")
	}
	if result != nil && result.PlanState.Active() {
		signals = append(signals, "active_plan")
	}
	return signals
}

func (r *Runtime) missionAskRecentDowntime(key session.SessionKey, msg core.InboundMessage) bool {
	sess, err := r.store.Load(key)
	if err != nil || sess == nil {
		return false
	}
	var latest, previous time.Time
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		entry := sess.Messages[i]
		if strings.TrimSpace(strings.ToLower(entry.Role)) != "user" {
			continue
		}
		if msg.MessageID > 0 && strings.TrimSpace(entry.Content) != "" && strings.TrimSpace(entry.Content) != strings.TrimSpace(msg.Text) && latest.IsZero() {
			latest = entry.CreatedAt
			continue
		}
		if latest.IsZero() {
			latest = entry.CreatedAt
		} else {
			previous = entry.CreatedAt
			break
		}
	}
	if latest.IsZero() || previous.IsZero() {
		return false
	}
	return latest.Sub(previous) >= 12*time.Hour
}

type missionAskClassifierOutput struct {
	Action     string `json:"action"`
	MissionID  string `json:"mission_id,omitempty"`
	Confidence string `json:"confidence,omitempty"`
	Question   string `json:"question,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

func (r *Runtime) classifyMissionAsk(ctx context.Context, observation missionAskObservation) (missionAskObservation, bool) {
	provider := r.missionAskClassifierProvider()
	if provider == nil {
		return missionAskObservation{}, false
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	resp, err := provider.Complete(ctx, missionAskClassifierMessages(observation), nil)
	if err != nil || resp == nil {
		return missionAskObservation{}, false
	}
	var out missionAskClassifierOutput
	if err := json.Unmarshal([]byte(extractMissionAskJSON(resp.Content)), &out); err != nil {
		return missionAskObservation{}, false
	}
	action := strings.ToLower(strings.TrimSpace(out.Action))
	switch action {
	case "same_objective":
		if strings.TrimSpace(out.MissionID) != "" && observation.MissionID != "" && strings.TrimSpace(out.MissionID) != observation.MissionID {
			return missionAskObservation{}, false
		}
	case "new_objective":
		observation.MissionID = ""
	case "ignore", "unclear", "":
		observation.ShouldAsk = false
		return observation, true
	default:
		return missionAskObservation{}, false
	}
	if strings.TrimSpace(out.Question) != "" {
		observation.Question = clampMissionAskText(out.Question, missionAskMaxQuestionLen)
	}
	observation.Confidence = session.NormalizeMissionAskConfidence(session.MissionAskConfidence(out.Confidence))
	if observation.Evidence == nil {
		observation.Evidence = map[string]any{}
	}
	observation.Evidence["classifier"] = map[string]string{"action": action, "reason": strings.TrimSpace(out.Reason)}
	observation.Fingerprint = missionAskFingerprint(observation.Owner, observation.MissionID, observation.MissionName, observation.Query+" "+action)
	return observation, true
}

func (r *Runtime) missionAskClassifierProvider() agent.Provider {
	if r == nil {
		return nil
	}
	if provider, _, ok := r.modelSlotProvider(core.ModelSlotPersona); ok {
		return provider
	}
	return nil
}

func missionAskClassifierMessages(observation missionAskObservation) []agent.Message {
	return []agent.Message{
		{Role: "system", Content: missionAskClassifierSystemPrompt()},
		{Role: "user", Content: renderMissionAskClassifierInput(observation)},
	}
}

func missionAskClassifierSystemPrompt() string {
	return strings.Join([]string{
		"## Role",
		"You are Aphelion's Mission Question classifier.",
		"## Goal",
		"Decide whether Aphelion should ask exactly one proactive mission clarification for the current user turn.",
		"## Success Criteria",
		"- Ask only when the supplied query points to a durable objective, recurring project, or meaningful change in intent.",
		"- Use same_objective only when the query clearly belongs to the supplied candidate mission.",
		"- Use new_objective only when the query clearly introduces a durable objective outside the supplied candidate.",
		"- Use ignore for thanks, acknowledgements, tiny corrections, one-off requests, or turns that do not benefit from a mission question.",
		"- Use unclear when the evidence is too weak or the candidate fit is ambiguous.",
		"## Output",
		"- Return JSON only with fields action, mission_id, confidence, question, and reason.",
		"- action must be one of same_objective|new_objective|ignore|unclear.",
		"- mission_id may be copied from the supplied candidate only; never invent a mission_id.",
		"- For same_objective, include mission_id from the supplied candidate only when present.",
		"- For same_objective or new_objective, include a non-empty compact question.",
		"- For ignore or unclear, leave question empty and mission_id empty.",
		"## Confidence",
		"- confidence high means the ask decision is clear from the supplied fields.",
		"- confidence low means the classification is useful but evidence is partial.",
		"## Stop Rules",
		"- Do not ask because the wording merely resembles a mission keyword.",
		"- Do not infer facts, history, or mission identity beyond the supplied candidate and query.",
		"- When a question would feel like pestering, choose ignore.",
	}, "\n")
}

func renderMissionAskClassifierInput(observation missionAskObservation) string {
	return fmt.Sprintf("candidate_mission_id=%s\ncandidate_name=%s\nlocal_confidence=%s\nquery=%s\nquestion=%s", observation.MissionID, observation.MissionName, observation.Confidence, clampMissionAskText(observation.Query, 600), observation.Question)
}

func extractMissionAskJSON(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "{") && strings.HasSuffix(text, "}") {
		return text
	}
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end > start {
		return text[start : end+1]
	}
	return text
}

func (r *Runtime) sendMissionAskPrompt(ctx context.Context, key session.SessionKey, msg core.InboundMessage, prompt session.MissionAskPrompt) error {
	sender, ok := r.outbound.(missionAskInlineSender)
	if !ok {
		return nil
	}
	text := renderMissionAskPromptCard(prompt)
	messageID, err := sender.SendInlineKeyboard(ctx, msg.ChatID, r.prefixTelegramPresentedText(r.telegramPresentationForMessage(msg), text), missionAskPromptRows(prompt.ID), nil)
	if err != nil {
		_, _ = r.store.UpdateMissionAskPromptStatus(prompt.ID, prompt.Owner, session.MissionAskStatusExpired, "mission ask card delivery failed", time.Now().UTC())
		log.Printf("WARN mission ask prompt delivery failed chat_id=%d err=%v", msg.ChatID, err)
		return nil
	}
	if messageID > 0 {
		if sess, loadErr := r.store.Load(key); loadErr == nil && sess != nil {
			if recordErr := r.store.RecordOutbound(key, sess.TurnCount, messageID, "mission_ask"); recordErr != nil {
				return fmt.Errorf("record mission ask outbound: %w", recordErr)
			}
		}
		if msg.TelegramThreadID > 0 {
			if err := r.store.RecordTelegramCallbackMessageThread(msg.ChatID, messageID, msg.TelegramThreadID, "mission_ask", time.Now().UTC()); err != nil {
				return fmt.Errorf("record mission ask callback thread: %w", err)
			}
		}
	}
	r.recordExecutionEvent(key, core.ExecutionEventMissionAskOffered, "mission_ask", "offered", map[string]any{
		"prompt_id":   prompt.ID,
		"mission_id":  prompt.MissionID,
		"confidence":  string(prompt.Confidence),
		"fingerprint": prompt.SourceFingerprint,
	}, time.Now().UTC())
	return nil
}

func (r *Runtime) MissionAskPrompt(ctx context.Context, senderID int64, promptID string) (session.MissionAskPrompt, bool, error) {
	_ = ctx
	if r == nil || r.store == nil {
		return session.MissionAskPrompt{}, false, fmt.Errorf("Mission Question is unavailable")
	}
	actor, owner, err := r.missionCommandActor(senderID)
	if err != nil {
		return session.MissionAskPrompt{}, false, err
	}
	prompt, ok, err := r.store.MissionAskPrompt(strings.TrimSpace(promptID))
	if err != nil || !ok {
		return prompt, ok, err
	}
	if actor.Role != principal.RoleAdmin && prompt.Owner != owner {
		return session.MissionAskPrompt{}, false, ErrPrincipalDenied
	}
	return prompt, true, nil
}

func (r *Runtime) ResolveMissionAskPrompt(ctx context.Context, senderID int64, promptID string, status session.MissionAskStatus, summary string) (session.MissionAskPrompt, error) {
	_ = ctx
	if r == nil || r.store == nil {
		return session.MissionAskPrompt{}, fmt.Errorf("Mission Question is unavailable")
	}
	actor, owner, err := r.missionCommandActor(senderID)
	if err != nil {
		return session.MissionAskPrompt{}, err
	}
	if actor.Role == principal.RoleAdmin {
		if prompt, ok, err := r.store.MissionAskPrompt(strings.TrimSpace(promptID)); err != nil {
			return session.MissionAskPrompt{}, err
		} else if ok {
			owner = prompt.Owner
		}
	}
	return r.store.UpdateMissionAskPromptStatus(promptID, owner, status, summary, time.Now().UTC())
}

func renderMissionAskPromptCard(prompt session.MissionAskPrompt) string {
	question := strings.TrimSpace(prompt.QuestionText)
	if question == "" {
		target := strings.TrimSpace(prompt.MissionID)
		if target == "" {
			target = "a possible objective"
		}
		question = "This may belong with " + target + "."
	}
	details := []string{"Question: " + question}
	if missionID := strings.TrimSpace(prompt.MissionID); missionID != "" {
		details = append(details, "Mission candidate: "+missionID)
	}
	evidence := []string{"confidence: " + string(prompt.Confidence)}
	if promptID := strings.TrimSpace(prompt.ID); promptID != "" {
		evidence = append([]string{"prompt: " + promptID}, evidence...)
	}
	return face.RenderCompactOperatorPanel(face.OperatorPanel{
		Title:    "Mission Question",
		State:    "waiting for choice",
		Why:      "This looks related to mission work, but mission state should not change without your direction.",
		Next:     "Ask one clarification, or ignore this connection.",
		Details:  details,
		Evidence: evidence,
	}, face.OperatorPanelCompactOptions{DetailLimit: 3, EvidenceLimit: 2})
}

func missionAskPromptRows(promptID string) [][]telegram.InlineButton {
	return [][]telegram.InlineButton{{
		{Text: "Ignore", CallbackData: core.EncodeMissionAskCallbackData(promptID, core.MissionAskCallbackIgnore)},
		{Text: "Ask Me", CallbackData: core.EncodeMissionAskCallbackData(promptID, core.MissionAskCallbackAsk)},
	}}
}

func renderMissionAskQuestion(name string, hasMission bool) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "this objective"
	}
	if hasMission {
		return "This sounds related to " + strconv.Quote(name) + ". Should I treat this work as part of it, keep it separate, or ignore the connection?"
	}
	return "This sounds like it might be a durable objective. Should I remember it as a mission candidate, keep it only in this chat, or ignore the connection?"
}

func missionAskFingerprint(owner string, missionID string, missionName string, query string) string {
	sum := sha1.Sum([]byte(strings.Join([]string{
		strings.TrimSpace(owner),
		strings.TrimSpace(missionID),
		strings.ToLower(strings.TrimSpace(missionName)),
		strings.Join(missionAskSortedTokens(query), " "),
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

func missionAskSortedTokens(text string) []string {
	tokens := missionAskTokens(text)
	out := make([]string, 0, len(tokens))
	for token := range tokens {
		out = append(out, token)
	}
	sort.Strings(out)
	if len(out) > 24 {
		out = out[:24]
	}
	return out
}

func missionAskTokenMatches(a map[string]struct{}, b map[string]struct{}) int {
	matches := 0
	for token := range b {
		if _, ok := a[token]; ok {
			matches++
		}
	}
	return matches
}

func missionAskTokens(text string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, token := range hiddenInputTokens(text) {
		if len([]rune(token)) < 4 {
			continue
		}
		out[token] = struct{}{}
	}
	return out
}

func encodeMissionAskEvidence(evidence map[string]any) string {
	if len(evidence) == 0 {
		return "{}"
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func clampMissionAskText(text string, max int) string {
	text = strings.TrimSpace(strings.Join(strings.Fields(text), " "))
	if max <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	if max <= 3 {
		return strings.TrimSpace(string(runes[:max]))
	}
	return strings.TrimSpace(string(runes[:max-3])) + "..."
}
