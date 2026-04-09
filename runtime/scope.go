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
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/workspace"
)

func (r *Runtime) lockSession(key session.SessionKey) func() {
	lockKey := strconv.FormatInt(key.ChatID, 10) + ":" + strconv.FormatInt(key.UserID, 10)

	r.sessionMu.Lock()
	lock := r.sessionLocks[lockKey]
	if lock == nil {
		lock = &sync.Mutex{}
		r.sessionLocks[lockKey] = lock
	}
	r.sessionMu.Unlock()

	lock.Lock()
	return lock.Unlock
}

func (r *Runtime) scopeForPrincipal(p principal.Principal) (sandbox.Scope, error) {
	if r.scopeResolver == nil {
		root := strings.TrimSpace(r.cfg.Agent.Workspace)
		if root == "" {
			return sandbox.Scope{}, fmt.Errorf("agent.workspace is required")
		}
		return sandbox.Scope{
			Principal:        p,
			GlobalRoot:       root,
			SharedMemoryRoot: root,
			WorkingRoot:      root,
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
		base = strings.TrimSpace(cfg.Workspace)
	}
	return filepath.Join(base, ".aphelion", "tmp")
}

func setLastAssistantCanonical(messages []session.Message, canonical string) []session.Message {
	canonical = strings.TrimSpace(canonical)
	if canonical == "" {
		return messages
	}

	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			messages[i].CanonicalContent = canonical
			return messages
		}
	}
	return messages
}

func appendAssistantTurn(sess *session.Session, text string, canonical string) []session.Message {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}
	sess.TurnCount++
	canonical = strings.TrimSpace(canonical)
	if canonical == "" {
		canonical = trimmed
	}
	sess.LastCanonicalReply = canonical
	return []session.Message{{
		Role:             "assistant",
		Content:          trimmed,
		CanonicalContent: canonical,
		ContentChars:     len(trimmed),
		TurnIndex:        sess.TurnCount,
	}}
}
