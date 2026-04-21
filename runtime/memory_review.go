//go:build linux

package runtime

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

const (
	memoryReviewItemLimit     = 6
	memoryReviewExcerptMaxLen = 260
	memoryReviewDefaultQuery  = "current priorities and open questions"
)

func (r *Runtime) MemoryReviewSnapshot(ctx context.Context, chatID int64, senderID int64, source core.MemoryReviewSource) (core.MemoryReviewSnapshot, error) {
	if r == nil || r.store == nil {
		return core.MemoryReviewSnapshot{}, fmt.Errorf("runtime unavailable")
	}
	actor, ok := r.resolver.ResolveTelegramUser(senderID)
	if !ok {
		return core.MemoryReviewSnapshot{}, ErrPrincipalDenied
	}

	source = core.NormalizeMemoryReviewSource(string(source))
	switch source {
	case core.MemoryReviewSourceSemanticShared, core.MemoryReviewSourceSemanticLocal:
		return r.memoryReviewSemantic(ctx, chatID, actor, source)
	default:
		return r.memoryReviewSessionRecent(chatID, source)
	}
}

func (r *Runtime) MemoryFocus(chatID int64) (core.MemoryFocus, bool) {
	if r == nil || chatID == 0 {
		return core.MemoryFocus{}, false
	}
	r.memoryFocusMu.RLock()
	defer r.memoryFocusMu.RUnlock()
	focus, ok := r.memoryFocusByChat[chatID]
	if !ok || !focus.Active() {
		return core.MemoryFocus{}, false
	}
	return focus, true
}

func (r *Runtime) SetMemoryFocus(chatID int64, focus core.MemoryFocus) {
	if r == nil || chatID == 0 || !focus.Active() {
		return
	}
	r.memoryFocusMu.Lock()
	defer r.memoryFocusMu.Unlock()
	if r.memoryFocusByChat == nil {
		r.memoryFocusByChat = make(map[int64]core.MemoryFocus)
	}
	r.memoryFocusByChat[chatID] = focus
}

func (r *Runtime) ClearMemoryFocus(chatID int64) bool {
	if r == nil || chatID == 0 {
		return false
	}
	r.memoryFocusMu.Lock()
	defer r.memoryFocusMu.Unlock()
	if r.memoryFocusByChat == nil {
		return false
	}
	if _, ok := r.memoryFocusByChat[chatID]; !ok {
		return false
	}
	delete(r.memoryFocusByChat, chatID)
	return true
}

func (r *Runtime) memoryReviewSessionRecent(chatID int64, source core.MemoryReviewSource) (core.MemoryReviewSnapshot, error) {
	snapshot := core.MemoryReviewSnapshot{
		GeneratedAt: time.Now().UTC(),
		Source:      source,
	}
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	sess, err := r.store.Load(key)
	if err != nil {
		if err == sql.ErrNoRows {
			return snapshot, nil
		}
		return core.MemoryReviewSnapshot{}, err
	}
	snapshot.Query = memoryReviewSeedQueryFromSession(sess)
	if strings.TrimSpace(snapshot.Query) == "" {
		snapshot.Query = memoryReviewDefaultQuery
	}

	items := make([]core.MemoryReviewItem, 0, memoryReviewItemLimit)
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		msg := sess.Messages[i]
		role := strings.TrimSpace(strings.ToLower(msg.Role))
		if role != "user" && role != "assistant" {
			continue
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		if role == "user" && strings.HasPrefix(content, "/") {
			continue
		}
		items = append(items, core.MemoryReviewItem{
			ID:      fmt.Sprintf("session:%d:%s:%d", msg.TurnIndex, role, len(items)+1),
			Label:   fmt.Sprintf("turn=%d role=%s", msg.TurnIndex, role),
			Excerpt: truncateMemoryReviewText(content, memoryReviewExcerptMaxLen),
		})
		if len(items) >= memoryReviewItemLimit {
			break
		}
	}
	snapshot.Items = items
	return snapshot, nil
}

func (r *Runtime) memoryReviewSemantic(ctx context.Context, chatID int64, actor principal.Principal, source core.MemoryReviewSource) (core.MemoryReviewSnapshot, error) {
	snapshot := core.MemoryReviewSnapshot{
		GeneratedAt: time.Now().UTC(),
		Source:      source,
	}
	if r.semantic == nil || !r.semantic.Enabled() {
		return snapshot, fmt.Errorf("semantic memory review is not configured")
	}
	scope, err := r.scopeForPrincipal(actor)
	if err != nil {
		return core.MemoryReviewSnapshot{}, fmt.Errorf("resolve principal scope: %w", err)
	}
	query := r.memoryReviewSeedQuery(chatID)
	if strings.TrimSpace(query) == "" {
		query = memoryReviewDefaultQuery
	}
	snapshot.Query = query

	semanticScope := "shared"
	principalID := ""
	if source == core.MemoryReviewSourceSemanticLocal && actor.Role == principal.RoleApprovedUser && actor.TelegramUserID > 0 {
		semanticScope = "principal"
		principalID = fmt.Sprintf("%d", actor.TelegramUserID)
	}
	hits, err := r.semantic.Search(ctx, memstore.SemanticSearchRequest{
		Root:        dynamicPromptRoot(scope),
		Scope:       semanticScope,
		PrincipalID: principalID,
		Query:       query,
		Mode:        memstore.SemanticModeInteractive,
		Limit:       memoryReviewItemLimit,
		MaxLen:      2400,
		Now:         time.Now().UTC(),
	})
	if err != nil {
		return core.MemoryReviewSnapshot{}, err
	}
	items := make([]core.MemoryReviewItem, 0, len(hits))
	for idx, hit := range hits {
		label := fmt.Sprintf("source=%s kind=%s score=%.2f", strings.TrimSpace(hit.Source), strings.TrimSpace(hit.Kind), hit.Score)
		items = append(items, core.MemoryReviewItem{
			ID:      fmt.Sprintf("semantic:%s:%d", semanticScope, idx+1),
			Label:   label,
			Excerpt: truncateMemoryReviewText(strings.TrimSpace(hit.Excerpt), memoryReviewExcerptMaxLen),
			Score:   hit.Score,
		})
	}
	snapshot.Items = items
	return snapshot, nil
}

func (r *Runtime) memoryReviewSeedQuery(chatID int64) string {
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	sess, err := r.store.Load(key)
	if err != nil {
		return ""
	}
	return memoryReviewSeedQueryFromSession(sess)
}

func memoryReviewSeedQueryFromSession(sess *session.Session) string {
	if sess == nil {
		return ""
	}
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		msg := sess.Messages[i]
		if strings.TrimSpace(strings.ToLower(msg.Role)) != "user" {
			continue
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" || strings.HasPrefix(content, "/") {
			continue
		}
		return truncateMemoryReviewText(content, 240)
	}
	return ""
}

func truncateMemoryReviewText(text string, max int) string {
	text = strings.TrimSpace(text)
	if max <= 0 {
		max = 240
	}
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return strings.TrimSpace(string(runes[:max])) + "..."
}
