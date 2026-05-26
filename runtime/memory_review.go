//go:build linux

package runtime

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"
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
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	return r.MemoryReviewSnapshotForKey(ctx, key, senderID, source)
}

func (r *Runtime) MemoryReviewSnapshotForKey(ctx context.Context, key session.SessionKey, senderID int64, source core.MemoryReviewSource) (core.MemoryReviewSnapshot, error) {
	if r == nil || r.store == nil {
		return core.MemoryReviewSnapshot{}, fmt.Errorf("runtime unavailable")
	}
	actor, ok := r.resolver.ResolveTelegramUser(senderID)
	if !ok {
		return core.MemoryReviewSnapshot{}, ErrPrincipalDenied
	}

	source = core.NormalizeMemoryReviewSource(string(source))
	var (
		snapshot core.MemoryReviewSnapshot
		err      error
	)
	switch source {
	case core.MemoryReviewSourceSemanticShared, core.MemoryReviewSourceSemanticLocal:
		snapshot, err = r.memoryReviewSemantic(ctx, key, actor, source)
	default:
		snapshot, err = r.memoryReviewSessionRecent(key, source)
	}
	if err != nil {
		return core.MemoryReviewSnapshot{}, err
	}
	r.enrichMemoryReviewStats(actor, &snapshot)
	return snapshot, nil
}

func (r *Runtime) memoryReviewSessionRecent(key session.SessionKey, source core.MemoryReviewSource) (core.MemoryReviewSnapshot, error) {
	snapshot := core.MemoryReviewSnapshot{
		GeneratedAt: time.Now().UTC(),
		Source:      source,
	}
	sess, err := r.store.Load(key)
	if err != nil {
		if err != sql.ErrNoRows {
			return core.MemoryReviewSnapshot{}, err
		}
	}
	snapshot.Query = memoryReviewSeedQueryFromSession(sess)
	if strings.TrimSpace(snapshot.Query) == "" {
		snapshot.Query = memoryReviewDefaultQuery
	}

	candidates := memoryReviewCandidatesFromSession("session", "", sess)
	if session.NormalizeScopeRef(key.Scope).Kind == session.ScopeKindTelegramThread {
		sort.SliceStable(candidates, func(i, j int) bool {
			return candidates[i].createdAt.After(candidates[j].createdAt)
		})
		items := make([]core.MemoryReviewItem, 0, memoryReviewItemLimit)
		for _, candidate := range candidates {
			items = append(items, candidate.item)
			if len(items) == memoryReviewItemLimit {
				break
			}
		}
		snapshot.Items = items
		snapshot.Stats.SessionRecentCount = len(items)
		return snapshot, nil
	}
	threads, err := r.store.ListTelegramThreads(key.ChatID, 12)
	if err != nil {
		return core.MemoryReviewSnapshot{}, err
	}
	for _, thread := range threads {
		threadKey := session.SessionKey{ChatID: key.ChatID, UserID: 0, Scope: telegramThreadScopeRef(key.ChatID, thread.ThreadID)}
		threadSess, err := r.store.Load(threadKey)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return core.MemoryReviewSnapshot{}, err
		}
		candidates = append(candidates, memoryReviewCandidatesFromSession(
			fmt.Sprintf("thread:%d", thread.ThreadID),
			fmt.Sprintf("thread=%d ", thread.ThreadID),
			threadSess,
		)...)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].createdAt.After(candidates[j].createdAt)
	})
	items := make([]core.MemoryReviewItem, 0, memoryReviewItemLimit)
	for _, candidate := range candidates {
		items = append(items, candidate.item)
		if len(items) == memoryReviewItemLimit {
			break
		}
	}
	snapshot.Items = items
	snapshot.Stats.SessionRecentCount = len(items)
	return snapshot, nil
}

type memoryReviewCandidate struct {
	item      core.MemoryReviewItem
	createdAt time.Time
}

func memoryReviewCandidatesFromSession(idPrefix string, labelPrefix string, sess *session.Session) []memoryReviewCandidate {
	if sess == nil {
		return nil
	}
	var out []memoryReviewCandidate
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
		out = append(out, memoryReviewCandidate{
			item: core.MemoryReviewItem{
				ID:      fmt.Sprintf("%s:%d:%s:%d", strings.TrimSpace(idPrefix), msg.TurnIndex, role, len(out)+1),
				Label:   fmt.Sprintf("%sturn=%d role=%s", labelPrefix, msg.TurnIndex, role),
				Excerpt: truncateMemoryReviewText(content, memoryReviewExcerptMaxLen),
			},
			createdAt: msg.CreatedAt,
		})
	}
	return out
}

func (r *Runtime) memoryReviewSemantic(ctx context.Context, key session.SessionKey, actor principal.Principal, source core.MemoryReviewSource) (core.MemoryReviewSnapshot, error) {
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
	query := r.memoryReviewSeedQuery(key)
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
	if source == core.MemoryReviewSourceSemanticLocal {
		snapshot.Stats.SemanticLocalCount = len(items)
	} else {
		snapshot.Stats.SemanticSharedCount = len(items)
	}
	return snapshot, nil
}

func (r *Runtime) enrichMemoryReviewStats(actor principal.Principal, snapshot *core.MemoryReviewSnapshot) {
	if snapshot == nil {
		return
	}
	if snapshot.Stats.StoreCounts == nil {
		snapshot.Stats.StoreCounts = map[string]int{}
	}
	scope, err := r.scopeForPrincipal(actor)
	if err != nil {
		snapshot.Stats.Partial = true
		snapshot.Stats.Missing = append(snapshot.Stats.Missing, "durable store counts")
		return
	}
	root := dynamicPromptRoot(scope)
	for _, store := range []string{memstore.StoreMemory, memstore.StoreKnowledge, memstore.StoreDecisions, memstore.StoreQuestions, memstore.StoreRhizome, memstore.StoreDreams} {
		count, err := countMemoryStoreLines(root, store)
		if err != nil {
			snapshot.Stats.Partial = true
			snapshot.Stats.Missing = append(snapshot.Stats.Missing, store)
			continue
		}
		snapshot.Stats.StoreCounts[store] = count
	}
}

func countMemoryStoreLines(root string, store string) (int, error) {
	path, _, err := memstore.ResolveStorePath(root, store)
	if err != nil {
		return 0, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- ") {
			count++
		}
	}
	return count, nil
}

func (r *Runtime) memoryReviewSeedQuery(key session.SessionKey) string {
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
