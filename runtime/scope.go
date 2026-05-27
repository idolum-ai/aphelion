//go:build linux

package runtime

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/workspace"
)

// sessionLock pairs a per-session mutex with a refcount so the map entry can
// be reclaimed once no goroutine is holding or waiting on it. The refcount is
// guarded by Runtime.sessionMu, not by sessionLock.mu, so a waiter that has
// already incremented its share is visible to the current holder's unlock.
type sessionLock struct {
	mu       sync.Mutex
	refCount int
}

func (r *Runtime) lockSession(key session.SessionKey) func() {
	lockKey := session.SessionIDForKey(key)
	if strings.TrimSpace(lockKey) == "" {
		lockKey = strconv.FormatInt(key.ChatID, 10) + ":" + strconv.FormatInt(key.UserID, 10)
	}

	r.sessionMu.Lock()
	lock := r.sessionLocks[lockKey]
	if lock == nil {
		lock = &sessionLock{}
		r.sessionLocks[lockKey] = lock
	}
	lock.refCount++
	r.sessionMu.Unlock()

	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		r.sessionMu.Lock()
		lock.refCount--
		if lock.refCount == 0 {
			delete(r.sessionLocks, lockKey)
		}
		r.sessionMu.Unlock()
	}
}

func (r *Runtime) scopeForPrincipal(p principal.Principal) (sandbox.Scope, error) {
	if r.scopeResolver == nil {
		promptRoot := strings.TrimSpace(r.cfg.Agent.PromptRoot)
		execRoot := strings.TrimSpace(r.cfg.Agent.ExecRoot)
		sharedMemoryRoot := strings.TrimSpace(r.cfg.Agent.SharedMemoryRoot)
		if promptRoot == "" {
			return sandbox.Scope{}, fmt.Errorf("agent.prompt_root is required")
		}
		if execRoot == "" {
			return sandbox.Scope{}, fmt.Errorf("agent.exec_root is required")
		}
		if sharedMemoryRoot == "" {
			return sandbox.Scope{}, fmt.Errorf("agent.shared_memory_root is required")
		}
		return sandbox.Scope{
			Principal:        p,
			GlobalRoot:       promptRoot,
			SharedMemoryRoot: sharedMemoryRoot,
			WorkingRoot:      execRoot,
		}, nil
	}
	return r.scopeResolver.Resolve(p)
}

func (r *Runtime) promptContextForScope(scope sandbox.Scope, now time.Time) (*workspace.PromptContext, error) {
	stableCfg := r.cfg.Agent
	stableCfg.Workspace = scope.GlobalRoot
	stableCfg.DynamicFiles = nil
	stableCfg.DailyNotes = false

	stable, err := workspace.LoadPromptContext(stableCfg, now)
	if err != nil {
		return nil, err
	}

	dynamicCfg := r.cfg.Agent
	dynamicCfg.BootstrapFiles = nil
	dynamicCfg.Workspace = dynamicPromptRoot(scope)

	dynamic, err := workspace.LoadPromptContext(dynamicCfg, now)
	if err != nil {
		return nil, err
	}

	return &workspace.PromptContext{
		Workspace: scope.WorkingRoot,
		Stable:    stable.Stable,
		Dynamic:   dynamic.Dynamic,
	}, nil
}

func dynamicPromptRoot(scope sandbox.Scope) string {
	if scope.Principal.Role == principal.RoleApprovedUser && strings.TrimSpace(scope.UserMemory) != "" {
		return scope.UserMemory
	}
	if strings.TrimSpace(scope.SharedMemoryRoot) != "" {
		return scope.SharedMemoryRoot
	}
	return scope.WorkingRoot
}

func faceWorkspaceRoot(scope sandbox.Scope) string {
	if strings.TrimSpace(scope.GlobalRoot) != "" {
		return scope.GlobalRoot
	}
	return scope.WorkingRoot
}

func voiceTempRoot(scope sandbox.Scope, cfg config.AgentConfig) string {
	base := strings.TrimSpace(scope.WorkingRoot)
	if scope.Principal.Role == principal.RoleApprovedUser && strings.TrimSpace(scope.UserMemory) != "" {
		base = scope.UserMemory
	}
	if base == "" {
		base = strings.TrimSpace(cfg.ExecRoot)
	}
	return filepath.Join(base, ".aphelion", "tmp")
}

func setLastAssistantFloor(messages []session.Message, floorText string) []session.Message {
	floorText = strings.TrimSpace(floorText)
	if floorText == "" {
		return messages
	}

	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			messages[i].FloorContent = floorText
			return messages
		}
	}
	return messages
}

func setLastAssistantFloorMetadata(messages []session.Message, floorMetadata string) []session.Message {
	floorMetadata = strings.TrimSpace(floorMetadata)
	if floorMetadata == "" {
		return messages
	}

	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			messages[i].FloorMetadata = floorMetadata
			return messages
		}
	}
	return messages
}

func appendAssistantTurn(sess *session.Session, text string, floorText string, floorMetadata string) []session.Message {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}
	sess.TurnCount++
	floorText = strings.TrimSpace(floorText)
	if floorText == "" {
		floorText = trimmed
	}
	sess.LastFloorText = floorText
	sess.LastFloorMetadata = strings.TrimSpace(floorMetadata)
	return []session.Message{{
		Role:          "assistant",
		Content:       trimmed,
		FloorContent:  floorText,
		FloorMetadata: strings.TrimSpace(floorMetadata),
		ContentChars:  len(trimmed),
		TurnIndex:     sess.TurnCount,
	}}
}

func appendSyntheticTurn(sess *session.Session, requestText string, replyText string, floorText string, floorMetadata string) []session.Message {
	requestText = strings.TrimSpace(requestText)
	replyText = strings.TrimSpace(replyText)
	if requestText == "" || replyText == "" {
		return nil
	}

	sess.TurnCount++
	floorText = strings.TrimSpace(floorText)
	if floorText == "" {
		floorText = replyText
	}
	sess.LastFloorText = floorText
	sess.LastFloorMetadata = strings.TrimSpace(floorMetadata)

	return []session.Message{
		{
			Role:         "user",
			Content:      requestText,
			ContentChars: len(requestText),
			TurnIndex:    sess.TurnCount,
		},
		{
			Role:          "assistant",
			Content:       replyText,
			FloorContent:  floorText,
			FloorMetadata: strings.TrimSpace(floorMetadata),
			ContentChars:  len(replyText),
			TurnIndex:     sess.TurnCount,
		},
	}
}

func telegramDMScopeRef(chatID int64) session.ScopeRef {
	if chatID == 0 {
		return session.ScopeRef{}
	}
	return session.ScopeRef{
		Kind: session.ScopeKindTelegramDM,
		ID:   strconv.FormatInt(chatID, 10),
	}
}

func telegramThreadScopeRef(chatID int64, threadID int64) session.ScopeRef {
	return session.TelegramThreadScopeRef(chatID, threadID)
}

func telegramThreadIDFromScope(chatID int64, scope session.ScopeRef) int64 {
	scope = session.NormalizeScopeRef(scope)
	if scope.Kind != session.ScopeKindTelegramThread || chatID == 0 {
		return 0
	}
	prefix := strconv.FormatInt(chatID, 10) + ":"
	raw := strings.TrimSpace(scope.ID)
	if !strings.HasPrefix(raw, prefix) {
		return 0
	}
	threadID, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(raw, prefix)), 10, 64)
	if err != nil || threadID <= 0 {
		return 0
	}
	return threadID
}

func telegramInboundScopeRef(msg core.InboundMessage) session.ScopeRef {
	if msg.TelegramThreadID > 0 {
		return telegramThreadScopeRef(msg.ChatID, msg.TelegramThreadID)
	}
	return telegramDMScopeRef(msg.ChatID)
}

func telegramScopedInboundForKey(key session.SessionKey, senderID int64) core.InboundMessage {
	msg := core.InboundMessage{
		ChatID:   key.ChatID,
		SenderID: senderID,
	}
	if key.ChatID == 0 {
		return msg
	}
	if threadID := telegramThreadIDFromScope(key.ChatID, key.Scope); threadID > 0 {
		msg.TelegramThreadID = threadID
	}
	return msg
}

func continuationPromptInboundForKey(key session.SessionKey, text string, origin core.InboundOrigin, originDetail string) core.InboundMessage {
	msg := telegramScopedInboundForKey(key, 0)
	msg.Text = text
	msg.Origin = origin
	msg.OriginDetail = originDetail
	return msg
}

func continuationInboundForKey(key session.SessionKey, actor principal.Principal, text string, origin core.InboundOrigin, originDetail string) core.InboundMessage {
	msg := telegramScopedInboundForKey(key, actor.TelegramUserID)
	msg.SenderName = actorLabel(actor)
	msg.Text = text
	msg.Origin = origin
	msg.OriginDetail = originDetail
	return msg
}

func telegramGroupScopeRef(chatID int64) session.ScopeRef {
	if chatID == 0 {
		return session.ScopeRef{}
	}
	return session.ScopeRef{
		Kind: session.ScopeKindTelegramGroup,
		ID:   strconv.FormatInt(chatID, 10),
	}
}

func heartbeatScopeRef() session.ScopeRef {
	return session.ScopeRef{
		Kind: session.ScopeKindHeartbeat,
		ID:   "admin-house",
	}
}

func cronScopeRef(id string) session.ScopeRef {
	id = strings.TrimSpace(id)
	return session.ScopeRef{
		Kind: session.ScopeKindCron,
		ID:   id,
	}
}

func applySessionScope(sess *session.Session, key session.SessionKey) {
	if sess == nil {
		return
	}
	if !key.Scope.IsZero() {
		sess.Scope = session.NormalizeScopeRef(key.Scope)
	}
}
