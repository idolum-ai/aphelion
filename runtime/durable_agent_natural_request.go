//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

type durableAgentNaturalMenuTokenKind string

const (
	durableAgentNaturalTokenBehaviorUpdate durableAgentNaturalMenuTokenKind = "durable_child_behavior_update"
	durableAgentNaturalTokenReportRequest  durableAgentNaturalMenuTokenKind = "durable_child_report_request"
)

type durableAgentNaturalMenuToken struct {
	TokenID       string
	AgentID       string
	Kind          durableAgentNaturalMenuTokenKind
	Notes         string
	Score         int
	AuthorityMenu AuthorityDiscoveryMenu
}

func (r *Runtime) maybeHandleNaturalDurableAgentRequest(ctx context.Context, key session.SessionKey, actor principal.Principal, msg core.InboundMessage, tools agent.ToolRegistry) (bool, *core.TurnResult, error) {
	if r == nil || r.store == nil || tools == nil {
		return false, nil, nil
	}
	if actor.Role != principal.RoleAdmin ||
		(msg.Origin != "" && msg.Origin != core.InboundOriginUser) ||
		(msg.ChatType != "" && msg.ChatType != "private") ||
		msg.TelegramThreadID != 0 ||
		strings.TrimSpace(msg.DurableAgentID) != "" {
		return false, nil, nil
	}
	text := stripReplyContext(strings.TrimSpace(msg.Text))
	if text == "" {
		return false, nil, nil
	}
	if naturalDurableAgentTypedRecoverySurface(text) {
		return false, nil, nil
	}
	agentRow, ok, err := r.resolveNaturalDurableAgentMention(text)
	if err != nil || !ok {
		return false, nil, err
	}
	token, ok := r.selectNaturalDurableAgentMenuToken(key, *agentRow, text)
	if !ok {
		return false, nil, nil
	}
	switch token.Kind {
	case durableAgentNaturalTokenBehaviorUpdate:
		result, err := r.applyNaturalDurableAgentBehaviorDirective(ctx, tools, *agentRow, token.Notes)
		return true, result, err
	case durableAgentNaturalTokenReportRequest:
		result, err := r.runNaturalDurableAgentRecommendationRequest(ctx, key, msg, tools, *agentRow, token.Notes)
		return true, result, err
	default:
		return false, nil, nil
	}
}

func (r *Runtime) selectNaturalDurableAgentMenuToken(key session.SessionKey, agentRow core.DurableAgent, text string) (durableAgentNaturalMenuToken, bool) {
	tokens := r.compileNaturalDurableAgentMenu(key, agentRow, text)
	if len(tokens) == 0 {
		return durableAgentNaturalMenuToken{}, false
	}
	sort.SliceStable(tokens, func(i, j int) bool {
		if tokens[i].Score == tokens[j].Score {
			return tokens[i].TokenID < tokens[j].TokenID
		}
		return tokens[i].Score > tokens[j].Score
	})
	if len(tokens) > 1 && tokens[0].Score == tokens[1].Score {
		return durableAgentNaturalMenuToken{}, false
	}
	return tokens[0], true
}

func (r *Runtime) compileNaturalDurableAgentMenu(key session.SessionKey, agentRow core.DurableAgent, text string) []durableAgentNaturalMenuToken {
	agentID := strings.TrimSpace(agentRow.AgentID)
	text = strings.TrimSpace(text)
	if agentID == "" || text == "" {
		return nil
	}
	authorityMenu := AuthorityDiscoveryMenu{}
	if r != nil && r.store != nil {
		if menu, err := r.BuildAuthorityDiscoveryMenu(AuthorityDiscoveryMenuBuildInput{Key: key, Now: time.Now().UTC(), Limit: 50}); err == nil {
			authorityMenu = menu
		}
	}
	var tokens []durableAgentNaturalMenuToken
	if score := durableAgentNaturalBehaviorTokenScore(text); score > 0 {
		tokens = append(tokens, durableAgentNaturalMenuToken{
			TokenID:       "durable_child:" + agentID + ":behavior_update",
			AgentID:       agentID,
			Kind:          durableAgentNaturalTokenBehaviorUpdate,
			Notes:         text,
			Score:         score,
			AuthorityMenu: authorityMenu,
		})
	}
	if score := durableAgentNaturalReportTokenScore(text); score > 0 {
		tokens = append(tokens, durableAgentNaturalMenuToken{
			TokenID:       "durable_child:" + agentID + ":report_request",
			AgentID:       agentID,
			Kind:          durableAgentNaturalTokenReportRequest,
			Notes:         text,
			Score:         score,
			AuthorityMenu: authorityMenu,
		})
	}
	return tokens
}

func (r *Runtime) resolveNaturalDurableAgentMention(text string) (*core.DurableAgent, bool, error) {
	agents, err := r.store.ListDurableAgents()
	if err != nil {
		return nil, false, err
	}
	if len(agents) == 0 {
		return nil, false, nil
	}
	tokens := naturalDurableAgentTokens(text)
	if len(tokens) == 0 {
		return nil, false, nil
	}
	tokenSet := map[string]struct{}{}
	for _, token := range tokens {
		tokenSet[token] = struct{}{}
	}
	hasAgentWord := naturalDurableAgentHasAnyToken(tokenSet, "agent", "child", "assistant")
	type candidate struct {
		agent core.DurableAgent
		score int
	}
	var candidates []candidate
	for _, item := range agents {
		agentID := strings.ToLower(strings.TrimSpace(item.AgentID))
		if agentID == "" || strings.TrimSpace(item.Status) != "active" {
			continue
		}
		idTokens := durableAgentIDTokens(agentID)
		score := 0
		if strings.Contains(strings.ToLower(text), agentID) {
			score = 100
		} else if len(idTokens) > 0 && naturalDurableAgentTokensContainAll(tokenSet, idTokens) {
			score = 80
		} else if hasAgentWord {
			distinctive := durableAgentDistinctiveTokens(idTokens)
			matched := 0
			for _, token := range distinctive {
				if _, ok := tokenSet[token]; ok {
					matched++
				}
			}
			switch {
			case matched >= 2:
				score = 70
			case matched == 1:
				score = 50
			}
		}
		if score > 0 {
			candidates = append(candidates, candidate{agent: item, score: score})
		}
	}
	if len(candidates) == 0 {
		return nil, false, nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].agent.AgentID < candidates[j].agent.AgentID
		}
		return candidates[i].score > candidates[j].score
	})
	if len(candidates) > 1 && candidates[0].score == candidates[1].score {
		return nil, false, nil
	}
	return &candidates[0].agent, true, nil
}

func durableAgentNaturalBehaviorDirective(text string) bool {
	value := strings.ToLower(text)
	if !naturalDurableAgentPhraseAny(value, "change", "set", "teach", "update", "configure", "make", "have") {
		return false
	}
	return strings.Contains(value, "behavior") ||
		strings.Contains(value, "whenever") ||
		strings.Contains(value, "anytime") ||
		strings.Contains(value, "always") ||
		strings.Contains(value, "automatically") ||
		strings.Contains(value, "profile")
}

func durableAgentNaturalRecommendationRequest(text string) bool {
	value := strings.ToLower(text)
	if !naturalDurableAgentPhraseAny(value, "recommend", "recommends", "recommendation", "suggest", "suggests", "pick", "rank", "which") {
		return false
	}
	return strings.Contains(value, "tell me") ||
		strings.Contains(value, "what") ||
		strings.Contains(value, "which") ||
		strings.Contains(value, "recommend") ||
		strings.Contains(value, "suggest")
}

func naturalDurableAgentTypedRecoverySurface(text string) bool {
	value := strings.ToLower(strings.TrimSpace(text))
	return strings.Contains(value, "recovery_contract") ||
		strings.Contains(value, "recovery_operation_kind") ||
		strings.Contains(value, session.NextActionOperationKindDurableChildRecovery)
}

func durableAgentNaturalBehaviorTokenScore(text string) int {
	if durableAgentNaturalBehaviorDirective(text) {
		return 100
	}
	value := strings.ToLower(text)
	if naturalDurableAgentPhraseAny(value, "remember", "use", "profile", "preference", "rule") &&
		naturalDurableAgentPhraseAny(value, "always", "whenever", "anytime", "automatically", "behavior") {
		return 70
	}
	return 0
}

func durableAgentNaturalReportTokenScore(text string) int {
	if durableAgentNaturalBehaviorDirective(text) {
		return 0
	}
	if durableAgentNaturalRecommendationRequest(text) {
		return 100
	}
	value := strings.ToLower(text)
	tokenSet := naturalDurableAgentTokenSet(value)
	switch {
	case naturalDurableAgentHasAnyToken(tokenSet, "finish", "complete", "continue", "resume", "setup", "blocked", "status", "check", "run", "process", "triage", "report"):
		return 80
	case naturalDurableAgentHasAnyToken(tokenSet, "help", "jobs", "job", "opportunity", "opportunities", "unread", "recommend"):
		return 70
	default:
		return 0
	}
}

func (r *Runtime) applyNaturalDurableAgentBehaviorDirective(ctx context.Context, tools agent.ToolRegistry, agentRow core.DurableAgent, text string) (*core.TurnResult, error) {
	policy := core.NormalizeDurableAgentLivePolicy(agentRow.LivePolicy)
	nextCharter := durableAgentCharterWithDirective(policy.Charter, text)
	input := map[string]any{
		"action":   "policy_apply",
		"agent_id": strings.TrimSpace(agentRow.AgentID),
		"reason":   "admin natural-language durable child behavior directive",
		"policy_patch": map[string]any{
			"charter": nextCharter,
		},
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	if _, err := tools.Execute(ctx, "durable_agent", raw); err != nil {
		return nil, err
	}
	return &core.TurnResult{Text: fmt.Sprintf("Updated %s's durable behavior policy. Future wakes will receive the new directive.", strings.TrimSpace(agentRow.AgentID))}, nil
}

func (r *Runtime) runNaturalDurableAgentRecommendationRequest(ctx context.Context, key session.SessionKey, msg core.InboundMessage, tools agent.ToolRegistry, agentRow core.DurableAgent, text string) (*core.TurnResult, error) {
	agentID := strings.TrimSpace(agentRow.AgentID)
	if agentID == "" {
		return nil, fmt.Errorf("durable child id is required")
	}
	guidance := durableAgentNaturalParentGuidance(text)
	rawSend, err := json.Marshal(map[string]any{
		"action":   "conversation_send",
		"agent_id": agentID,
		"message":  guidance,
	})
	if err != nil {
		return nil, err
	}
	if _, err := tools.Execute(ctx, "durable_agent", rawSend); err != nil {
		return nil, err
	}
	if state, ok, err := r.activeChildWakeContinuationForAgent(key, agentID); err != nil {
		return nil, err
	} else if ok {
		if retry := session.NormalizeContinuationRetryOperation(state.ContinuationLease.RetryOperation); retry.Active() {
			if err := r.TriggerContinuationForKey(ctx, key); err != nil {
				return nil, err
			}
			return r.naturalDurableAgentWakeResult(agentID, "I woke "+agentID+" with your request.")
		}
	}
	rawWake, err := json.Marshal(map[string]any{
		"action":   "wake_once",
		"agent_id": agentID,
		"reason":   "natural parent request for durable child recommendation",
	})
	if err != nil {
		return nil, err
	}
	if _, err := tools.Execute(ctx, "durable_agent", rawWake); err != nil {
		if naturalDurableAgentMissingLease(err) {
			if _, materializeErr := r.MaterializeRequestedApproval(ctx, key, msg, text); materializeErr != nil {
				return nil, materializeErr
			}
			return &core.TurnResult{Text: fmt.Sprintf("I queued %s with your request and surfaced the bounded wake approval it needs.", agentID)}, nil
		}
		if naturalDurableAgentMissingGrant(err) {
			return &core.TurnResult{Text: fmt.Sprintf("I queued %s with your request. It needs an approval for one bounded wake before it can run.", agentID)}, nil
		}
		return nil, err
	}
	return r.naturalDurableAgentWakeResult(agentID, "I woke "+agentID+" with your request.")
}

func (r *Runtime) activeChildWakeContinuationForAgent(key session.SessionKey, agentID string) (session.ContinuationState, bool, error) {
	state, exists, err := r.store.ContinuationStateIfExists(key)
	if err != nil || !exists {
		return session.ContinuationState{}, false, err
	}
	state = session.NormalizeContinuationState(state)
	lease := state.ContinuationLease
	if state.Status != session.ContinuationStatusApproved || state.RemainingTurns <= 0 {
		return session.ContinuationState{}, false, nil
	}
	if lease.LeaseClass != session.ContinuationLeaseClassChildWake || lease.Status != session.ContinuationLeaseStatusActive || lease.RemainingTurns <= 0 {
		return session.ContinuationState{}, false, nil
	}
	if !lease.ExpiresAt.IsZero() && !lease.ExpiresAt.After(time.Now().UTC()) {
		return session.ContinuationState{}, false, nil
	}
	if strings.TrimSpace(lease.Constraints["agent_id"]) != strings.TrimSpace(agentID) {
		return session.ContinuationState{}, false, nil
	}
	return state, true, nil
}

func (r *Runtime) naturalDurableAgentWakeResult(agentID string, fallback string) (*core.TurnResult, error) {
	reply, err := r.latestDurableAgentChildReply(agentID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(reply) == "" {
		reply = fallback
	}
	return &core.TurnResult{Text: reply}, nil
}

func (r *Runtime) latestDurableAgentChildReply(agentID string) (string, error) {
	state, err := r.store.DurableAgentState(strings.TrimSpace(agentID))
	if err != nil {
		return "", err
	}
	continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		return "", err
	}
	continuity = core.NormalizeDurableAgentContinuityState(continuity)
	if continuity.Conversation == nil {
		return "", nil
	}
	for _, message := range continuity.Conversation.Messages {
		if strings.TrimSpace(message.Role) == "child" {
			return strings.TrimSpace(message.Text), nil
		}
	}
	return "", nil
}

func durableAgentCharterWithDirective(charter string, directive string) string {
	charter = strings.TrimSpace(charter)
	directive = strings.TrimSpace(directive)
	if directive == "" {
		return charter
	}
	line := "Parent behavior directive: " + directive
	if strings.Contains(strings.ToLower(charter), strings.ToLower(line)) {
		return charter
	}
	if charter == "" {
		return line
	}
	return charter + "\n\n" + line
}

func durableAgentNaturalParentGuidance(text string) string {
	text = strings.TrimSpace(text)
	return "User request: " + text + "\n\nApply your current durable charter and existing approved child-local tools automatically within their approved scope. If another authority is required, report the exact typed blocker instead of looping."
}

func naturalDurableAgentMissingLease(err error) bool {
	if err == nil {
		return false
	}
	value := strings.ToLower(err.Error())
	return strings.Contains(value, "missing child_wake continuation lease") || strings.Contains(value, "missing continuation lease")
}

func naturalDurableAgentMissingGrant(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "missing capability grant")
}

func naturalDurableAgentPhraseAny(value string, phrases ...string) bool {
	for _, phrase := range phrases {
		if strings.Contains(value, phrase) {
			return true
		}
	}
	return false
}

func naturalDurableAgentHasAnyToken(tokens map[string]struct{}, values ...string) bool {
	for _, value := range values {
		if _, ok := tokens[value]; ok {
			return true
		}
	}
	return false
}

func naturalDurableAgentTokenSet(text string) map[string]struct{} {
	tokens := map[string]struct{}{}
	for _, token := range naturalDurableAgentTokens(text) {
		tokens[token] = struct{}{}
	}
	return tokens
}

func naturalDurableAgentTokensContainAll(tokenSet map[string]struct{}, tokens []string) bool {
	if len(tokens) == 0 {
		return false
	}
	for _, token := range tokens {
		if _, ok := tokenSet[token]; !ok {
			return false
		}
	}
	return true
}

func durableAgentDistinctiveTokens(tokens []string) []string {
	out := make([]string, 0, len(tokens))
	for _, token := range tokens {
		switch token {
		case "", "agent", "child", "durable", "bot", "assistant":
			continue
		default:
			out = append(out, token)
		}
	}
	return out
}

func durableAgentIDTokens(agentID string) []string {
	return naturalDurableAgentTokens(strings.ReplaceAll(agentID, "_", "-"))
}

func naturalDurableAgentTokens(text string) []string {
	var tokens []string
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		tokens = append(tokens, b.String())
		b.Reset()
	}
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return tokens
}

func stripReplyContext(text string) string {
	if idx := strings.Index(text, "\n\nReply context:"); idx >= 0 {
		return strings.TrimSpace(text[:idx])
	}
	return strings.TrimSpace(text)
}
