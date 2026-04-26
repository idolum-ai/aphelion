//go:build linux

package runtime

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

const (
	aggressiveMemoryCaptureMarker = "BEGIN_TURN_MEMORY_CAPTURE"
	aggressiveMemoryFlushMarker   = "BEGIN_SESSION_MEMORY_FLUSH"
)

type aggressiveMemoryCaptureInput struct {
	Marker        string
	Reason        string
	RunKind       session.TurnRunKind
	Scope         sandbox.Scope
	UserText      string
	AssistantText string
	ToolLog       []string
	Now           time.Time
}

func (r *Runtime) aggressiveMemoryEnabled() bool {
	if r == nil || r.cfg == nil {
		return false
	}
	return r.cfg.Memory.Aggressive.Enabled
}

func (r *Runtime) aggressiveCaptureEnabled() bool {
	if !r.aggressiveMemoryEnabled() {
		return false
	}
	return r.cfg.Memory.Aggressive.CaptureEveryTurn
}

func (r *Runtime) aggressivePrefetchEnabled() bool {
	if !r.aggressiveMemoryEnabled() {
		return false
	}
	if !r.cfg.Memory.Aggressive.PrefetchEveryTurn {
		return false
	}
	if r.semantic == nil || !r.semantic.Enabled() {
		return false
	}
	return true
}

func (r *Runtime) aggressiveFlushEnabled() bool {
	if !r.aggressiveMemoryEnabled() {
		return false
	}
	return r.cfg.Memory.Aggressive.FlushOnSessionBoundary
}

func (r *Runtime) maybeAggressivePrefetchSystemMessage(ctx context.Context, scope sandbox.Scope, runKind session.TurnRunKind, query string, now time.Time) string {
	if !r.aggressivePrefetchEnabled() {
		return ""
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return ""
	}
	mode := aggressiveSemanticModeForRun(runKind)
	semanticScope, principalID := splitSemanticScope(semanticScopeForPrincipal(scope.Principal))
	plan := r.aggressiveRecallPlan(runKind, query)

	hits, err := r.semantic.Search(ctx, memstore.SemanticSearchRequest{
		Root:        dynamicPromptRoot(scope),
		Scope:       semanticScope,
		PrincipalID: principalID,
		Query:       query,
		Mode:        mode,
		Limit:       plan.TopK,
		MaxLen:      plan.MaxChars,
		Now:         now,
	})
	if err != nil || len(hits) == 0 {
		return ""
	}
	return renderAggressiveRecallBlock(hits, plan)
}

func aggressiveSemanticModeForRun(runKind session.TurnRunKind) memstore.SemanticMode {
	switch runKind {
	case session.TurnRunKindHeartbeat:
		return memstore.SemanticModeHeartbeat
	default:
		return memstore.SemanticModeInteractive
	}
}

func aggressiveSemanticMaxLen(cfg *config.Config, mode memstore.SemanticMode) int {
	if cfg == nil {
		return 4000
	}
	if mode == memstore.SemanticModeHeartbeat {
		if cfg.Memory.Semantic.HeartbeatMaxChars > 0 {
			return cfg.Memory.Semantic.HeartbeatMaxChars
		}
		return 12000
	}
	if cfg.Memory.Semantic.InteractiveMaxChars > 0 {
		return cfg.Memory.Semantic.InteractiveMaxChars
	}
	return 4000
}

func aggressiveSemanticLimit(cfg *config.Config, mode memstore.SemanticMode) int {
	defaultLimit := 5
	if cfg == nil {
		return defaultLimit
	}
	if mode == memstore.SemanticModeHeartbeat {
		if cfg.Memory.Semantic.HeartbeatTopK > 0 {
			return minInt(cfg.Memory.Semantic.HeartbeatTopK, 8)
		}
		return defaultLimit
	}
	if cfg.Memory.Semantic.InteractiveTopK > 0 {
		return minInt(cfg.Memory.Semantic.InteractiveTopK, 8)
	}
	return defaultLimit
}

func (r *Runtime) aggressiveRecallPlan(runKind session.TurnRunKind, query string) memstore.AdaptiveRecallPlan {
	mode := aggressiveSemanticModeForRun(runKind)
	purpose := memstore.RecallPurposeInteractive
	if mode == memstore.SemanticModeHeartbeat {
		purpose = memstore.RecallPurposeHeartbeat
	}
	if runKind == session.TurnRunKindRecovery {
		purpose = memstore.RecallPurposeRecovery
	}
	return memstore.PlanAdaptiveRecall(memstore.AdaptiveRecallRequest{
		Query:            query,
		Purpose:          purpose,
		ContextWindow:    r.governorContextWindow(),
		MaxContextRatio:  r.cfg.Sessions.MaxContextRatio,
		BaselineTopK:     aggressiveSemanticLimit(r.cfg, mode),
		BaselineMaxChars: aggressiveSemanticMaxLen(r.cfg, mode),
	})
}

func renderAggressiveRecallBlock(hits []memstore.SemanticHit, plan memstore.AdaptiveRecallPlan) string {
	if len(hits) == 0 {
		return ""
	}
	maxHits := aggressiveRecallRenderLimit(len(hits), plan)
	var b strings.Builder
	b.WriteString("AUTO_RECALL_MEMORY\n")
	b.WriteString("Use the following recalled context only when it materially improves this turn.\n")
	if plan.Mode != "" {
		fmt.Fprintf(&b, "recall_mode=%s recall_budget_chars=%d\n", plan.Mode, plan.MaxChars)
	}
	for i := 0; i < maxHits; i++ {
		hit := hits[i]
		source := strings.TrimSpace(hit.Source)
		if source == "" {
			source = "memory"
		}
		kind := strings.TrimSpace(hit.Kind)
		if kind == "" {
			kind = "memory"
		}
		fmt.Fprintf(&b, "%d. source=%s kind=%s score=%.2f\n", i+1, source, kind, hit.Score)
		excerpt := strings.TrimSpace(hit.Excerpt)
		if excerpt != "" {
			b.WriteString(excerpt)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func aggressiveRecallRenderLimit(hitCount int, plan memstore.AdaptiveRecallPlan) int {
	if hitCount <= 0 {
		return 0
	}
	limit := 5
	switch plan.Mode {
	case memstore.RecallModeLean:
		limit = 1
	case memstore.RecallModeDeep:
		limit = 8
	case memstore.RecallModeDoctor:
		limit = 10
	}
	if plan.TopK > 0 {
		limit = minInt(limit, plan.TopK)
	}
	return minInt(hitCount, limit)
}

func (r *Runtime) maybeCaptureTurnMemory(ctx context.Context, input turnCommitInput) {
	if !r.aggressiveCaptureEnabled() {
		return
	}
	if input.RunKind != session.TurnRunKindInteractive {
		return
	}
	if strings.TrimSpace(input.Prepared.LedgerText) == "" && strings.TrimSpace(input.ReplyText) == "" {
		return
	}
	req := aggressiveMemoryCaptureInput{
		Marker:        aggressiveMemoryCaptureMarker,
		Reason:        "turn_capture",
		RunKind:       input.RunKind,
		Scope:         input.Scope,
		UserText:      strings.TrimSpace(input.Prepared.LedgerText),
		AssistantText: strings.TrimSpace(input.ReplyText),
		ToolLog:       append([]string(nil), input.Result.ToolLog...),
		Now:           time.Now().UTC(),
	}
	if err := r.captureAggressiveMemory(ctx, req); err != nil {
		log.Printf("WARN aggressive memory capture failed chat_id=%d err=%v", input.Key.ChatID, err)
		r.reportOperationalIssueAsync("memory_capture", err)
	}
}

func (r *Runtime) captureAggressiveMemory(ctx context.Context, input aggressiveMemoryCaptureInput) error {
	if r == nil {
		return nil
	}
	scopeRoot := dynamicPromptRoot(input.Scope)
	if strings.TrimSpace(scopeRoot) == "" {
		return nil
	}

	messages := []agent.Message{
		{
			Role: "system",
			Content: strings.TrimSpace(`You distill conversation context into durable memory.
Output only the exact tagged sections shown in the request.
Be selective: include only durable facts, reusable knowledge, changed decisions, or open questions worth carrying forward.
Ignore ephemeral chatter, one-off requests, and transient tool noise.`),
		},
		{
			Role:    "user",
			Content: renderAggressiveMemoryCaptureRequest(input),
		},
	}

	result, _, err := agent.RunTurn(ctx, r.provider, nil, &agent.Budget{Max: 2, Caution: 0.8, Warning: 0.9}, r.reasoningOptionsForRun(input.RunKind), messages)
	if err != nil {
		return err
	}
	sections := parseReflectionSections(result.Text)
	return r.applyAggressiveMemorySections(scopeRoot, sections)
}

func renderAggressiveMemoryCaptureRequest(input aggressiveMemoryCaptureInput) string {
	marker := strings.TrimSpace(input.Marker)
	if marker == "" {
		marker = aggressiveMemoryCaptureMarker
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		reason = "turn_capture"
	}
	var b strings.Builder
	b.WriteString(marker)
	b.WriteString("\n")
	fmt.Fprintf(&b, "reason=%s run_kind=%s\n", reason, strings.TrimSpace(string(input.RunKind)))
	b.WriteString("Output only these sections and no extra prose:\n")
	b.WriteString(reflectionMemoryTag + "\n" + reflectionMemoryEndTag + "\n")
	b.WriteString(reflectionKnowledgeTag + "\n" + reflectionKnowledgeEndTag + "\n")
	b.WriteString(reflectionDecisionsTag + "\n" + reflectionDecisionsEndTag + "\n")
	b.WriteString(reflectionQuestionsTag + "\n" + reflectionQuestionsEndTag + "\n")
	b.WriteString(reflectionRhizomeTag + "\n" + reflectionRhizomeEndTag + "\n\n")
	if userText := strings.TrimSpace(input.UserText); userText != "" {
		b.WriteString("## User\n")
		b.WriteString(userText)
		b.WriteString("\n\n")
	}
	if assistant := strings.TrimSpace(input.AssistantText); assistant != "" {
		b.WriteString("## Assistant\n")
		b.WriteString(assistant)
		b.WriteString("\n\n")
	}
	toolLog := compactAggressiveToolLog(input.ToolLog)
	if len(toolLog) > 0 {
		b.WriteString("## Tool Log\n")
		for _, entry := range toolLog {
			b.WriteString("- ")
			b.WriteString(entry)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func compactAggressiveToolLog(toolLog []string) []string {
	if len(toolLog) == 0 {
		return nil
	}
	items := make([]string, 0, len(toolLog))
	for _, entry := range toolLog {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		if len(trimmed) > 240 {
			trimmed = trimmed[:237] + "..."
		}
		items = append(items, trimmed)
		if len(items) == 8 {
			break
		}
	}
	sort.Strings(items)
	return items
}

func (r *Runtime) applyAggressiveMemorySections(scopeRoot string, sections map[string]string) error {
	if strings.TrimSpace(scopeRoot) == "" || len(sections) == 0 {
		return nil
	}
	scopeName := dynamicScopeName(scopeRoot)
	if r.memoryAggressiveMode() == "propose" {
		_, err := proposeMemorySections(scopeRoot, scopeName, sections, "aggressive_capture", "turn_or_session_capture", time.Now().UTC())
		return err
	}
	for _, store := range []string{memstore.StoreMemory, memstore.StoreKnowledge, memstore.StoreDecisions, memstore.StoreQuestions} {
		content := strings.TrimSpace(sections[store])
		if content == "" {
			continue
		}
		if _, err := memstore.ApplyWrite(memstore.WriteRequest{
			Root:      scopeRoot,
			Store:     store,
			Action:    "add",
			Content:   content,
			SourceTag: "aggressive_capture",
			SourceRef: "turn_or_session_capture",
			Scope:     scopeName,
		}); err != nil {
			return fmt.Errorf("write aggressive %s memory: %w", store, err)
		}
	}
	if rhizome := strings.TrimSpace(sections[memstore.StoreRhizome]); rhizome != "" {
		if err := r.updateRhizome(scopeName, scopeRoot, rhizome); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) FlushChatMemory(ctx context.Context, chatID int64, reason string) error {
	if r == nil || !r.aggressiveFlushEnabled() {
		return nil
	}
	if chatID == 0 {
		return nil
	}
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	sess, err := r.store.Load(key)
	if err != nil {
		return fmt.Errorf("load session for flush: %w", err)
	}
	if sess == nil || len(sess.Messages) == 0 {
		return nil
	}
	actor, ok := principalFromSessionMessages(sess)
	if !ok {
		actor = principal.Principal{Role: principal.RoleAdmin}
	}
	scope, err := r.scopeForPrincipal(actor)
	if err != nil {
		return fmt.Errorf("resolve scope for flush: %w", err)
	}

	userText, assistantText := summarizeSessionForAggressiveFlush(sess)
	if strings.TrimSpace(userText) == "" && strings.TrimSpace(assistantText) == "" {
		return nil
	}

	flushCtx := ctx
	if flushCtx == nil {
		flushCtx = context.Background()
	}
	flushCtx, cancel := context.WithTimeout(flushCtx, 12*time.Second)
	defer cancel()

	req := aggressiveMemoryCaptureInput{
		Marker:        aggressiveMemoryFlushMarker,
		Reason:        strings.TrimSpace(reason),
		RunKind:       session.TurnRunKindInteractive,
		Scope:         scope,
		UserText:      userText,
		AssistantText: assistantText,
		Now:           time.Now().UTC(),
	}
	if req.Reason == "" {
		req.Reason = "session_boundary"
	}
	return r.captureAggressiveMemory(flushCtx, req)
}

func principalFromSessionMessages(sess *session.Session) (principal.Principal, bool) {
	if sess == nil {
		return principal.Principal{}, false
	}
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		msg := sess.Messages[i]
		role := principal.Role(strings.TrimSpace(msg.ActorRole))
		switch role {
		case principal.RoleAdmin, principal.RoleApprovedUser:
			if msg.ActorUserID > 0 {
				return principal.Principal{Role: role, TelegramUserID: msg.ActorUserID}, true
			}
		}
	}
	return principal.Principal{}, false
}

func summarizeSessionForAggressiveFlush(sess *session.Session) (string, string) {
	if sess == nil || len(sess.Messages) == 0 {
		return "", ""
	}
	from := len(sess.Messages) - 12
	if from < 0 {
		from = 0
	}
	var userParts []string
	var assistantParts []string
	for _, msg := range sess.Messages[from:] {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		if len(content) > 400 {
			content = content[:397] + "..."
		}
		switch msg.Role {
		case "user":
			userParts = append(userParts, content)
		case "assistant":
			assistantParts = append(assistantParts, content)
		}
	}
	return strings.Join(userParts, "\n"), strings.Join(assistantParts, "\n")
}
