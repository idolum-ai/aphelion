//go:build linux

package runtime

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/session"
)

type operationalAlertState struct {
	LastSent   time.Time
	Suppressed int
}

func (r *Runtime) ReportOperationalIssue(ctx context.Context, component string, err error) {
	r.reportOperationalIssue(ctx, component, err)
}

func (r *Runtime) reportOperationalIssueAsync(component string, err error) {
	if r == nil || err == nil {
		return
	}
	go r.reportOperationalIssue(context.Background(), component, err)
}

func (r *Runtime) reportOperationalIssue(ctx context.Context, component string, err error) {
	if r == nil || r.store == nil || r.outbound == nil || r.cfg == nil || err == nil {
		return
	}
	if r.expectedShutdownNoise(ctx, err) {
		log.Printf("INFO suppressing expected shutdown operational alert component=%s err=%v", strings.TrimSpace(component), err)
		return
	}
	component = strings.TrimSpace(component)
	if component == "" {
		component = "runtime"
	}
	detail := strings.TrimSpace(err.Error())
	if detail == "" {
		return
	}
	now := r.operationalNow()
	signature := operationalAlertSignature(component, detail)
	suppressed, shouldSend := r.consumeOperationalAlertWindow(signature, now)
	if !shouldSend {
		return
	}
	text := renderOperationalIssueMessage(component, detail, suppressed, now)
	if sendErr := r.sendOperationalNoticeToAdmin(ctx, text); sendErr != nil {
		if r.expectedShutdownNoise(ctx, sendErr) {
			log.Printf("INFO suppressing expected shutdown operational alert delivery failure component=%s err=%v", component, sendErr)
			return
		}
		log.Printf("WARN operational alert delivery failed component=%s err=%v", component, sendErr)
	}
}

func (r *Runtime) consumeOperationalAlertWindow(signature string, now time.Time) (suppressed int, shouldSend bool) {
	if r == nil {
		return 0, false
	}
	key := strings.TrimSpace(signature)
	if key == "" {
		return 0, false
	}
	window := r.operationalAlertWindow
	if window <= 0 {
		window = 10 * time.Minute
	}

	r.operationalAlertMu.Lock()
	defer r.operationalAlertMu.Unlock()

	state := r.operationalAlerts[key]
	if !state.LastSent.IsZero() && now.Sub(state.LastSent) < window {
		state.Suppressed++
		r.operationalAlerts[key] = state
		return 0, false
	}
	suppressed = state.Suppressed
	state.LastSent = now
	state.Suppressed = 0
	r.operationalAlerts[key] = state
	return suppressed, true
}

func (r *Runtime) operationalNow() time.Time {
	if r == nil {
		return time.Now().UTC()
	}
	if r.operationalAlertClock == nil {
		return time.Now().UTC()
	}
	return r.operationalAlertClock().UTC()
}

func renderOperationalIssueMessage(component string, detail string, suppressed int, now time.Time) string {
	component = strings.TrimSpace(component)
	if component == "" {
		component = "runtime"
	}
	detail = strings.TrimSpace(detail)
	if detail == "" {
		detail = "unspecified error"
	}
	if len(detail) > 1000 {
		detail = detail[:1000] + "..."
	}
	details := []string{
		"Component: " + component,
		"Time: " + now.UTC().Format(time.RFC3339),
		"Error: " + detail,
	}
	evidence := []string(nil)
	if suppressed > 0 {
		evidence = append(evidence, fmt.Sprintf("Suppressed repeats: %d", suppressed))
	}
	return renderRuntimeCompactPanel(face.OperatorPanel{
		Title:    "System warning",
		State:    "needs attention",
		Why:      "A runtime component reported an operational issue.",
		Next:     "Run /health diagnose or /health trace if the warning persists.",
		Details:  details,
		Evidence: evidence,
	})
}

func operationalAlertSignature(component string, detail string) string {
	component = strings.ToLower(strings.TrimSpace(component))
	detail = strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(detail)), " "))
	if len(detail) > 512 {
		detail = detail[:512]
	}
	return component + "|" + detail
}

func (r *Runtime) sendOperationalNoticeToAdmin(ctx context.Context, text string) error {
	if r == nil || r.cfg == nil || r.store == nil || r.outbound == nil {
		return fmt.Errorf("runtime is unavailable")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("operational notice text is required")
	}
	adminIDs := uniquePositiveIDs(r.cfg.Principals.Telegram.AdminUserIDs)
	if len(adminIDs) == 0 {
		return nil
	}
	targetChatID := r.lastActiveAdminChat(adminIDs)
	if targetChatID == 0 {
		targetChatID = adminIDs[0]
	}
	msgID, err := r.outbound.SendMessage(ctx, core.OutboundMessage{
		ChatID: targetChatID,
		Text:   text,
	})
	if err != nil {
		return fmt.Errorf("send operational notice outbound: %w", err)
	}

	adminKey := session.SessionKey{ChatID: targetChatID, UserID: 0, Scope: telegramDMScopeRef(targetChatID)}
	unlockAdmin := r.lockSession(adminKey)
	defer unlockAdmin()

	adminSession, err := r.store.Load(adminKey)
	if err != nil {
		return fmt.Errorf("load operational notice target session: %w", err)
	}
	applySessionScope(adminSession, adminKey)
	adminSession.ChatType = "dm"
	if err := r.store.Save(adminSession, appendAssistantTurn(adminSession, text, text, ""), core.TokenUsage{}); err != nil {
		return fmt.Errorf("save operational notice admin session: %w", err)
	}
	if err := r.store.RecordOutbound(adminKey, adminSession.TurnCount, msgID, "system_warning"); err != nil {
		return fmt.Errorf("record operational notice outbound: %w", err)
	}
	return nil
}
