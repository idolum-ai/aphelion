//go:build linux

package runtime

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const nocturneSessionID = "nocturne"

func (r *Runtime) StartNocturneLoop(ctx context.Context, logger func(string, ...any)) {
	if r == nil || !r.cfg.Nocturne.Enabled {
		return
	}
	if logger == nil {
		logger = log.Printf
	}
	cadence, err := time.ParseDuration(firstNonEmpty(strings.TrimSpace(r.cfg.Nocturne.CheckEvery), "15m"))
	if err != nil || cadence <= 0 {
		logger("WARN nocturne disabled due to invalid cadence: %q err=%v", r.cfg.Nocturne.CheckEvery, err)
		return
	}
	r.startBackgroundLoop("nocturne", func() {
		runPeriodic(ctx, cadence, func(runCtx context.Context) {
			if err := r.runNocturneTick(runCtx, time.Now()); err != nil {
				logger("WARN nocturne failed: %v", err)
				r.reportOperationalIssue(runCtx, "nocturne", err)
			}
		})
	})
}

func (r *Runtime) runNocturneTick(ctx context.Context, now time.Time) error {
	loc, err := nocturneLocation(r.cfg.Nocturne.Timezone)
	if err != nil {
		return err
	}
	local := now.In(loc)
	start, end, err := nocturneWindow(r.cfg.Nocturne.WindowStart, r.cfg.Nocturne.WindowEnd)
	if err != nil {
		return err
	}
	date := nocturneDate(local, start, end)
	inWindow := nocturneInWindow(local, start, end)
	artifactPath, confirmedPath, err := r.nocturnePaths(date)
	if err != nil {
		return err
	}
	if inWindow {
		if _, err := os.Stat(artifactPath); err == nil {
			return nil
		}
		return r.runNocturneOnce(ctx, date, artifactPath)
	}
	if _, err := os.Stat(artifactPath); err == nil {
		if _, err := os.Stat(confirmedPath); os.IsNotExist(err) && nocturneAfterWindow(local, start, end, date) {
			return r.confirmNocturne(ctx, date, artifactPath, confirmedPath)
		}
	}
	return nil
}

func (r *Runtime) runNocturneOnce(ctx context.Context, date string, artifactPath string) error {
	if r.provider == nil {
		return fmt.Errorf("nocturne provider unavailable")
	}
	promptText := strings.TrimSpace(r.cfg.Nocturne.Prompt)
	if promptText == "" {
		promptText = "Write privately from the agent's own continuity: one quiet observation, no task posture, no performance for the operator. Keep it bounded."
	}
	system := strings.Join([]string{
		"You are the configured Aphelion face in Nocturne, a private nightly writing ritual.",
		"Write for yourself inside the local system only. This is not a user-facing reply.",
		"No tools, no external accounts, no web, no email, no public contact, no claims of actions beyond this writing.",
		"Return a short markdown piece with a title on the first line.",
	}, "\n")
	resp, err := r.provider.Complete(ctx, []agent.Message{{Role: "system", Content: system}, {Role: "user", Content: fmt.Sprintf("Nocturne date: %s\n%s", date, promptText)}}, nil)
	if err != nil {
		return fmt.Errorf("nocturne completion: %w", err)
	}
	text := strings.TrimSpace(resp.Content)
	if text == "" {
		return fmt.Errorf("nocturne returned empty text")
	}
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o700); err != nil {
		return err
	}
	body := fmt.Sprintf("---\ndate: %s\nprivate: true\nritual: nocturne\n---\n\n%s\n", date, text)
	if err := os.WriteFile(artifactPath, []byte(body), 0o600); err != nil {
		return err
	}
	key := session.SessionKey{ChatID: cronSessionChatID(nocturneSessionID), UserID: 0, Scope: cronScopeRef(nocturneSessionID)}
	unlock := r.lockSession(key)
	defer unlock()
	sess, err := r.store.Load(key)
	if err != nil {
		return err
	}
	applySessionScope(sess, key)
	sess.ChatType = "system"
	sess.UserName = "nocturne"
	return r.store.Save(sess, appendSyntheticTurn(sess, "[nocturne "+date+"]", text, text, ""), core.TokenUsage{})
}

func (r *Runtime) confirmNocturne(ctx context.Context, date string, artifactPath string, confirmedPath string) error {
	raw, err := os.ReadFile(artifactPath)
	if err != nil {
		return err
	}
	title := nocturneTitle(string(raw))
	msg := strings.TrimSpace(firstNonEmpty(r.cfg.Nocturne.Confirmation, "Nocturne happened"))
	if title != "" {
		msg += ": " + title
	}
	target := r.lastActiveAdminChat(uniquePositiveIDs(r.cfg.Principals.Telegram.AdminUserIDs))
	if target == 0 && len(r.cfg.Principals.Telegram.AdminUserIDs) > 0 {
		target = r.cfg.Principals.Telegram.AdminUserIDs[0]
	}
	if target == 0 {
		return nil
	}
	msgID, err := r.outbound.SendMessage(ctx, core.OutboundMessage{ChatID: target, Text: msg})
	if err != nil {
		return err
	}
	key := session.SessionKey{ChatID: target, UserID: 0, Scope: telegramDMScopeRef(target)}
	unlock := r.lockSession(key)
	defer unlock()
	sess, err := r.store.Load(key)
	if err != nil {
		return err
	}
	applySessionScope(sess, key)
	if err := r.store.Save(sess, appendAssistantTurn(sess, msg, msg, ""), core.TokenUsage{}); err != nil {
		return err
	}
	if err := r.store.RecordOutbound(key, sess.TurnCount, msgID, "nocturne_confirmation"); err != nil {
		return err
	}
	return os.WriteFile(confirmedPath, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
}

func (r *Runtime) nocturnePaths(date string) (string, string, error) {
	root := strings.TrimSpace(r.cfg.Agent.SharedMemoryRoot)
	if root == "" {
		root = strings.TrimSpace(r.cfg.Agent.PromptRoot)
	}
	dir := firstNonEmpty(strings.TrimSpace(r.cfg.Nocturne.ArtifactDir), "memory/nocturne")
	path := filepath.Clean(filepath.Join(root, filepath.FromSlash(dir), date+".md"))
	if rel, err := filepath.Rel(root, path); err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", "", fmt.Errorf("nocturne artifact path escapes shared memory root")
	}
	return path, path + ".confirmed", nil
}

func nocturneLocation(name string) (*time.Location, error) {
	if strings.TrimSpace(name) == "" {
		return time.Local, nil
	}
	return time.LoadLocation(strings.TrimSpace(name))
}
func nocturneWindow(a, b string) (time.Duration, time.Duration, error) {
	s, e := firstNonEmpty(strings.TrimSpace(a), "23:00"), firstNonEmpty(strings.TrimSpace(b), "07:00")
	sd, err := parseClock(s)
	if err != nil {
		return 0, 0, err
	}
	ed, err := parseClock(e)
	return sd, ed, err
}
func parseClock(s string) (time.Duration, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, err
	}
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute, nil
}
func clockDur(t time.Time) time.Duration {
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute + time.Duration(t.Second())*time.Second
}
func nocturneInWindow(t time.Time, start, end time.Duration) bool {
	c := clockDur(t)
	if start < end {
		return c >= start && c < end
	}
	return c >= start || c < end
}
func nocturneDate(t time.Time, start, end time.Duration) string {
	if start > end && clockDur(t) < start {
		t = t.AddDate(0, 0, -1)
	}
	return t.Format("2006-01-02")
}
func nocturneAfterWindow(t time.Time, start, end time.Duration, date string) bool {
	return !nocturneInWindow(t, start, end) && nocturneDate(t, start, end) == date
}
func nocturneTitle(text string) string {
	for _, l := range strings.Split(text, "\n") {
		l = strings.Trim(strings.TrimSpace(l), "# ")
		if l != "" && !strings.HasPrefix(l, "---") && !strings.Contains(l, ": true") && !strings.HasPrefix(l, "date:") && !strings.HasPrefix(l, "ritual:") {
			return l
		}
	}
	return ""
}
