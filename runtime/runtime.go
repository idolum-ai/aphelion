//go:build linux

package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

type OutboundSender interface {
	SendMessage(ctx context.Context, msg core.OutboundMessage) (int64, error)
}

type Runtime struct {
	cfg      *config.Config
	store    *session.SQLiteStore
	provider agent.Provider
	tools    agent.ToolRegistry
	outbound OutboundSender
	resolver *principal.Resolver
}

var ErrPrincipalDenied = errors.New("principal is not admitted")

func New(
	cfg *config.Config,
	store *session.SQLiteStore,
	provider agent.Provider,
	tools agent.ToolRegistry,
	outbound OutboundSender,
) (*Runtime, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	if store == nil {
		return nil, fmt.Errorf("session store is nil")
	}
	if provider == nil {
		return nil, fmt.Errorf("provider is nil")
	}
	if outbound == nil {
		return nil, fmt.Errorf("outbound sender is nil")
	}

	return &Runtime{
		cfg:      cfg,
		store:    store,
		provider: provider,
		tools:    tools,
		outbound: outbound,
		resolver: principal.NewResolver(
			cfg.Principals.Telegram.AdminUserIDs,
			cfg.Principals.Telegram.ApprovedUserIDs,
		),
	}, nil
}

func (r *Runtime) AgentFunc() core.AgentFunc {
	return func(ctx context.Context, _ *core.SessionState, msg core.InboundMessage) (*core.TurnResult, error) {
		return r.HandleInbound(ctx, msg)
	}
}
