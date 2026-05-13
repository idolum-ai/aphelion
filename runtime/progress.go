//go:build linux

package runtime

import (
	"context"
	"encoding/json"
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
	stopRunActivityHeartbeat context.CancelFunc
}

func (r *Runtime) startTurnMonitor(key session.SessionKey, kind session.TurnRunKind, requestText string, progress *toolProgressReporter, audit *turnAuditRecorder) *turnMonitor {
	monitor := &turnMonitor{
		runtime:  r,
		key:      key,
		progress: progress,
		audit:    audit,
	}

	run, err := r.store.BeginTurnRun(key, kind, requestText)
	if err != nil {
		log.Printf("WARN begin turn run kind=%s chat_id=%d user_id=%d err=%v", kind, key.ChatID, key.UserID, err)
		return monitor
	}
	monitor.runID = run.ID
	r.recordExecutionEvent(key, core.ExecutionEventTurnStarted, "turn", string(session.TurnRunStatusRunning), map[string]any{
		"run_id":       run.ID,
		"run_kind":     strings.TrimSpace(string(kind)),
		"request_text": truncatePreview(strings.TrimSpace(requestText), 220),
	}, time.Now().UTC())
	if progress != nil {
		progress.BindTurnRun(run.ID)
		progress.recordMessageID = func(messageID int64) {
			if err := r.store.UpdateTurnRunProgressMessage(run.ID, messageID); err != nil {
				log.Printf("WARN update turn run progress id=%d msg_id=%d err=%v", run.ID, messageID, err)
			}
		}
	}
	monitor.startRunActivityHeartbeat()
	return monitor
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
