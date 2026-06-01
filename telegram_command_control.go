//go:build linux

package main

import (
	"context"
	"encoding/json"
	"github.com/idolum-ai/aphelion/internal/telegramcontrol"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/router"
	"github.com/idolum-ai/aphelion/runtime"
	"github.com/idolum-ai/aphelion/session"
)

type telegramCommandControl struct {
	router                 *router.Router
	ingress                *telegramcontrol.IngressSequencer
	rt                     *runtime.Runtime
	store                  *session.SQLiteStore
	resolver               *principal.Resolver
	decisionDetacher       pendingDecisionDetacher
	detachPendingOnRestart bool
	durableTools           durableWizardToolExecutor
}

type pendingDecisionDetacher interface {
	DetachByOwner(ctx context.Context, ownerKey string) (int, error)
	DetachAll(ctx context.Context) (int, error)
}

type pendingDecisionChatSenderDetacher interface {
	DetachByChatSender(ctx context.Context, chatID int64, senderID int64) (int, error)
}

type durableWizardToolExecutor interface {
	ExecuteForSessionPrincipal(ctx context.Context, p principal.Principal, key session.SessionKey, name string, input json.RawMessage) (string, error)
}
