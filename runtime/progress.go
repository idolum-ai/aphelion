//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

var turnRunActivityHeartbeatInterval = 30 * time.Second

type messageEditor interface {
	EditMessageText(ctx context.Context, chatID int64, messageID int64, text string, parseMode string) error
}

type messageKeyboardEditor interface {
	EditMessageTextWithInlineKeyboard(ctx context.Context, chatID int64, messageID int64, text string, parseMode string, rows [][]telegram.InlineButton) error
}

type messageKeyboardClearer interface {
	EditMessageTextWithoutInlineKeyboard(ctx context.Context, chatID int64, messageID int64, text string, parseMode string) error
}

type messageDeleter interface {
	DeleteMessage(ctx context.Context, chatID int64, messageID int64) error
}

type inlineKeyboardSender interface {
	SendInlineKeyboard(ctx context.Context, chatID int64, text string, rows [][]telegram.InlineButton, replyTo *int64) (int64, error)
}

type toolObserver interface {
	ToolStarted(ctx context.Context, name string, input json.RawMessage)
	ToolFinished(ctx context.Context, name string, input json.RawMessage, output string, err error)
}

type observedToolRegistry struct {
	base     agent.ToolRegistry
	observer toolObserver
}

func (o *observedToolRegistry) Definitions() []agent.ToolDef {
	if o.base == nil {
		return nil
	}
	return o.base.Definitions()
}

func (o *observedToolRegistry) Execute(ctx context.Context, name string, input json.RawMessage) (string, error) {
	if o.observer != nil {
		o.observer.ToolStarted(ctx, name, input)
	}
	out, err := o.base.Execute(ctx, name, input)
	if o.observer != nil {
		o.observer.ToolFinished(ctx, name, input, out, err)
	}
	return out, err
}

type turnMonitor struct {
	runtime                  *Runtime
	key                      session.SessionKey
	runID                    int64
	progress                 *toolProgressReporter
	audit                    *turnAuditRecorder
	startedAt                time.Time
	toolStarts               map[string]time.Time
	ctx                      context.Context
	cancelTurn               context.CancelFunc
	stopRunActivityHeartbeat context.CancelFunc
	ingressSurface           string
	ingressUpdateID          int64
}

func (r *Runtime) startTurnMonitor(ctx context.Context, key session.SessionKey, kind session.TurnRunKind, requestText string, progress *toolProgressReporter, audit *turnAuditRecorder, msg core.InboundMessage) (*turnMonitor, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	turnCtx, cancelTurn := context.WithCancel(ctx)
	monitor := &turnMonitor{
		runtime:         r,
		key:             key,
		progress:        progress,
		audit:           audit,
		startedAt:       time.Now().UTC(),
		toolStarts:      make(map[string]time.Time),
		ctx:             turnCtx,
		cancelTurn:      cancelTurn,
		ingressSurface:  strings.TrimSpace(msg.IngressSurface),
		ingressUpdateID: msg.IngressUpdateID,
	}

	var (
		run *session.TurnRun
		err error
	)
	if monitor.ingressSurface != "" && monitor.ingressUpdateID > 0 {
		run, err = r.store.BeginTurnRunForTelegramIngress(key, kind, requestText, monitor.ingressSurface, monitor.ingressUpdateID)
	} else {
		run, err = r.store.BeginTurnRun(key, kind, requestText)
	}
	if err != nil {
		cancelTurn()
		return nil, fmt.Errorf("begin turn run kind=%s chat_id=%d user_id=%d: %w", kind, key.ChatID, key.UserID, err)
	}
	monitor.runID = run.ID
	r.registerActiveTurn(run.ID, cancelTurn)
	payload := map[string]any{
		"run_id":       run.ID,
		"run_kind":     strings.TrimSpace(string(kind)),
		"request_text": truncatePreview(strings.TrimSpace(requestText), 220),
	}
	if monitor.ingressSurface != "" && monitor.ingressUpdateID > 0 {
		payload["ingress_surface"] = monitor.ingressSurface
		payload["ingress_update_id"] = monitor.ingressUpdateID
	}
	r.recordExecutionEvent(key, core.ExecutionEventTurnStarted, "turn", string(session.TurnRunStatusRunning), payload, time.Now().UTC())
	if progress != nil {
		progress.BindTurnRun(run.ID)
		progress.recordMessageID = func(messageID int64) {
			if err := r.store.UpdateTurnRunProgressMessage(run.ID, messageID); err != nil {
				log.Printf("WARN update turn run progress id=%d msg_id=%d err=%v", run.ID, messageID, err)
			}
		}
	}
	monitor.startRunActivityHeartbeat()
	return monitor, nil
}

func (m *turnMonitor) Context() context.Context {
	if m == nil || m.ctx == nil {
		return context.Background()
	}
	return m.ctx
}

func (m *turnMonitor) observeTools(base agent.ToolRegistry) agent.ToolRegistry {
	if base == nil {
		return nil
	}
	return &observedToolRegistry{base: base, observer: m}
}

func (m *turnMonitor) startRunActivityHeartbeat() {
	if m == nil || m.runtime == nil || m.runID == 0 {
		return
	}
	interval := turnRunActivityHeartbeatInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	heartbeatCtx, cancel := context.WithCancel(context.Background())
	m.stopRunActivityHeartbeat = cancel
	go runPeriodic(heartbeatCtx, interval, func(runCtx context.Context) {
		select {
		case <-runCtx.Done():
			return
		default:
		}
		if err := m.runtime.store.TouchTurnRunActivity(m.runID); err != nil {
			if m.runtime.expectedShutdownNoise(runCtx, err) {
				log.Printf("INFO suppressing expected shutdown turn activity touch failure id=%d err=%v", m.runID, err)
			} else {
				log.Printf("WARN touch turn run activity id=%d err=%v", m.runID, err)
			}
		}
		if m.progress != nil {
			m.progress.Heartbeat(runCtx)
		}
	})
}
