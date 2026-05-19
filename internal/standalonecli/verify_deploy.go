//go:build linux

package standalonecli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

const (
	verifyDeployDefaultTimeout = 2 * time.Minute
	verifyDeployBlessingPrefix = "DEPLOYMENT VERIFIED:"
	verifyDeployProbePrompt    = "This is a deployment verification probe. If the system is functioning, reply in one short sentence that starts exactly with \"DEPLOYMENT VERIFIED:\" and confirms you are ready. Do not mention internal layers or hidden mechanics unless something is broken."

	verifyDeployDurableChildrenStatus   = "status"
	verifyDeployDurableChildrenRequired = "required"
	verifyDeployDurableChildrenWarn     = "warn"
	verifyDeployDurableChildrenOff      = "off"
)

type deployProbeStatus string

const (
	deployProbeStatusPass deployProbeStatus = "pass"
	deployProbeStatusFail deployProbeStatus = "fail"
)

type deployProbeResult struct {
	Name       string            `json:"name"`
	Status     deployProbeStatus `json:"status"`
	DurationMS int64             `json:"duration_ms"`
	Detail     string            `json:"detail,omitempty"`
}

type deployVerificationReport struct {
	Status         string              `json:"status"`
	Blessed        bool                `json:"blessed"`
	ProbeChatID    int64               `json:"probe_chat_id"`
	ProbeSessionID string              `json:"probe_session_id"`
	Diagnosis      string              `json:"diagnosis,omitempty"`
	Probes         []deployProbeResult `json:"probes"`
}

type deployVerificationOptions struct {
	ConfigPath        string
	Timeout           time.Duration
	ProbeChatID       int64
	ProbeSenderID     int64
	KeepSession       bool
	KeepFailedSession bool
	DurableChildren   string
}

type deployTurnRunner interface {
	HandleInbound(ctx context.Context, msg core.InboundMessage) (*core.TurnResult, error)
}

type deployVerificationSender struct {
	messages []core.OutboundMessage
}

func (s *deployVerificationSender) SendMessage(_ context.Context, msg core.OutboundMessage) (int64, error) {
	s.messages = append(s.messages, msg)
	return int64(len(s.messages)), nil
}

func (s *deployVerificationSender) Last() (core.OutboundMessage, bool) {
	if len(s.messages) == 0 {
		return core.OutboundMessage{}, false
	}
	return s.messages[len(s.messages)-1], true
}

type builtDeployVerificationRuntime struct {
	Runner           deployTurnRunner
	Sender           *deployVerificationSender
	Probe            func(context.Context, session.SessionKey, principal.Principal) (string, error)
	DurableChildWake func(context.Context, string, time.Time) error
	Cleanup          func()
}

type VerifyDeployDeps struct {
	RuntimeBuilder                      func(*config.Config, *session.SQLiteStore) (BuiltDeployVerificationRuntime, error)
	TESRetentionConfigSafety            func(*config.Config) (config.SessionsTESRetentionConfig, time.Duration, string, error)
	PrepareFilesystem                   func(*config.Config) error
	SyncConfiguredTelegramDurableGroups func(*config.Config, *session.SQLiteStore) error
	VerifyRunner                        func(context.Context, *config.Config, DeployVerificationOptions) (DeployVerificationReport, error)
}

func RunVerifyDeployCommand(args []string, deps VerifyDeployDeps) error {
	return runVerifyDeployCommand(args, deps)
}

func runVerifyDeployCommand(args []string, deps VerifyDeployDeps) error {
	fs := flag.NewFlagSet("verify-deploy", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	timeout := fs.Duration("timeout", verifyDeployDefaultTimeout, "maximum time for the synthetic golden-path turn")
	probeChatID := fs.Int64("probe-chat", 0, "synthetic chat id for deployment verification")
	probeSenderID := fs.Int64("probe-sender", 0, "principal id to use for deployment verification (defaults to first admin)")
	keepSession := fs.Bool("keep-session", false, "retain the synthetic verification session even on success")
	keepFailedSession := fs.Bool("keep-failed-session", true, "retain the synthetic verification session on failure")
	durableChildren := fs.String("durable-children", verifyDeployDurableChildrenStatus, "durable child probe mode: status, required, warn, or off; status does not wake children")
	formatFlag := fs.String("format", commandOutputHuman, "output format: human, kv, json")
	jsonOut := fs.Bool("json", false, "emit a JSON report")
	if err := fs.Parse(args); err != nil {
		return err
	}
	format, err := normalizeCommandOutputFormat(*formatFlag, *jsonOut)
	if err != nil {
		return err
	}

	cfg, configPath, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}

	verifyRunner := deps.VerifyRunner
	if verifyRunner == nil {
		verifyRunner = func(ctx context.Context, cfg *config.Config, opts DeployVerificationOptions) (DeployVerificationReport, error) {
			return VerifyDeployment(ctx, cfg, opts, deps)
		}
	}
	report, verifyErr := verifyRunner(context.Background(), cfg, deployVerificationOptions{
		ConfigPath:        configPath,
		Timeout:           *timeout,
		ProbeChatID:       *probeChatID,
		ProbeSenderID:     *probeSenderID,
		KeepSession:       *keepSession,
		KeepFailedSession: *keepFailedSession,
		DurableChildren:   *durableChildren,
	})
	if renderErr := renderDeployVerificationReport(os.Stdout, report, format); renderErr != nil {
		return renderErr
	}
	if verifyErr != nil {
		return verifyErr
	}
	return nil
}

func VerifyDeployment(ctx context.Context, cfg *config.Config, opts DeployVerificationOptions, deps VerifyDeployDeps) (DeployVerificationReport, error) {
	return verifyDeployment(ctx, cfg, opts, deps)
}

func verifyDeployment(ctx context.Context, cfg *config.Config, opts deployVerificationOptions, deps VerifyDeployDeps) (deployVerificationReport, error) {
	report := deployVerificationReport{Status: "failed"}
	if cfg == nil {
		report.Diagnosis = "verification failed before startup: config is nil"
		return report, fmt.Errorf("config is nil")
	}
	durableChildrenMode, err := normalizeVerifyDeployDurableChildrenMode(opts.DurableChildren)
	if err != nil {
		report.Diagnosis = "verification failed before startup: " + err.Error()
		return report, err
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = verifyDeployDefaultTimeout
	}

	senderID := opts.ProbeSenderID
	if senderID == 0 {
		if len(cfg.Principals.Telegram.AdminUserIDs) == 0 {
			report.Diagnosis = "verification failed before startup: no admin principal is configured"
			return report, fmt.Errorf("verify-deploy requires at least one principals.telegram.admin_user_ids entry")
		}
		senderID = cfg.Principals.Telegram.AdminUserIDs[0]
	}
	chatID := opts.ProbeChatID
	if chatID == 0 {
		chatID = defaultVerifyDeployChatID(senderID)
	}
	key := session.SessionKey{ChatID: chatID, UserID: 0}
	report.ProbeChatID = chatID
	report.ProbeSessionID = session.SessionIDForKey(key)
	adminPrincipal := principal.Principal{
		TelegramUserID: senderID,
		Role:           principal.RoleAdmin,
	}

	var (
		store     *session.SQLiteStore
		built     builtDeployVerificationRuntime
		succeeded bool
	)
	defer func() {
		if store != nil && !succeeded && !opts.KeepSession && !opts.KeepFailedSession {
			_, _ = store.DeleteSession(key)
		}
		if built.Cleanup != nil {
			built.Cleanup()
		}
		if store != nil {
			_ = store.Close()
		}
	}()

	runProbe := func(name string, fn func() (string, error)) error {
		started := time.Now()
		detail, err := fn()
		result := deployProbeResult{
			Name:       name,
			DurationMS: time.Since(started).Milliseconds(),
		}
		if err != nil {
			result.Status = deployProbeStatusFail
			result.Detail = strings.TrimSpace(err.Error())
			report.Probes = append(report.Probes, result)
			report.Diagnosis = diagnoseDeployFailure(name, result.Detail)
			return err
		}
		result.Status = deployProbeStatusPass
		result.Detail = strings.TrimSpace(detail)
		report.Probes = append(report.Probes, result)
		return nil
	}

	if err := runProbe("boot", func() (string, error) {
		if deps.TESRetentionConfigSafety == nil {
			return "", fmt.Errorf("verify-deploy TES retention dependency is unavailable")
		}
		_, _, retentionSummary, retentionErr := deps.TESRetentionConfigSafety(cfg)
		if retentionErr != nil {
			return "", retentionErr
		}
		if deps.PrepareFilesystem == nil {
			return "", fmt.Errorf("verify-deploy filesystem dependency is unavailable")
		}
		if err := deps.PrepareFilesystem(cfg); err != nil {
			return "", err
		}
		var err error
		store, err = session.NewSQLiteStore(cfg.Sessions.DBPath)
		if err != nil {
			return "", err
		}
		if deps.SyncConfiguredTelegramDurableGroups == nil {
			return "", fmt.Errorf("verify-deploy durable-group sync dependency is unavailable")
		}
		if err := deps.SyncConfiguredTelegramDurableGroups(cfg, store); err != nil {
			return "", err
		}
		if _, err := store.DeleteSession(key); err != nil {
			return "", fmt.Errorf("clear prior verification session: %w", err)
		}
		if deps.RuntimeBuilder == nil {
			return "", fmt.Errorf("verify-deploy runtime builder dependency is unavailable")
		}
		built, err = deps.RuntimeBuilder(cfg, store)
		if err != nil {
			return "", err
		}
		if built.Runner == nil {
			return "", fmt.Errorf("verification runtime builder returned nil runner")
		}
		if built.Sender == nil {
			return "", fmt.Errorf("verification runtime builder returned nil sender")
		}
		return fmt.Sprintf("runtime initialized for session %s (%s)", report.ProbeSessionID, retentionSummary), nil
	}); err != nil {
		return report, err
	}

	if err := runProbe("tool_path", func() (string, error) {
		if built.Probe == nil {
			return "", fmt.Errorf("verification runtime builder returned nil tool probe")
		}
		return built.Probe(ctx, key, adminPrincipal)
	}); err != nil {
		return report, err
	}

	var blessedReply string
	if err := runProbe("golden_path", func() (string, error) {
		probeCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		result, err := built.Runner.HandleInbound(probeCtx, core.InboundMessage{
			ChatID:     chatID,
			ChatType:   "private",
			SenderID:   senderID,
			SenderName: "deployment verifier",
			Text:       verifyDeployProbePrompt,
			MessageID:  1,
			Timestamp:  time.Now().UTC(),
		})
		if err != nil {
			return "", err
		}
		if result == nil || strings.TrimSpace(result.Text) == "" {
			return "", fmt.Errorf("golden path returned an empty runtime result")
		}

		last, ok := built.Sender.Last()
		if !ok {
			return "", fmt.Errorf("golden path produced no outbound reply")
		}
		reply := strings.TrimSpace(last.Text)
		if reply == "" {
			return "", fmt.Errorf("golden path produced an empty outbound reply")
		}
		blessedReply = reply
		lower := strings.ToLower(reply)
		if !strings.HasPrefix(reply, verifyDeployBlessingPrefix) {
			return "", fmt.Errorf("verification reply missing blessing prefix %q: %q", verifyDeployBlessingPrefix, reply)
		}
		if !strings.Contains(lower, "ready") {
			return "", fmt.Errorf("verification reply missing readiness confirmation: %q", reply)
		}
		if strings.Contains(lower, "governor") || strings.Contains(lower, "aphelion") {
			return "", fmt.Errorf("verification reply leaked internal layer markers: %q", reply)
		}
		report.Blessed = true
		return reply, nil
	}); err != nil {
		return report, err
	}

	if err := runProbe("persistence", func() (string, error) {
		sess, err := store.Load(key)
		if err != nil {
			return "", err
		}
		if sess.TurnCount <= 0 {
			return "", fmt.Errorf("verification session turn_count = %d, want > 0", sess.TurnCount)
		}
		if strings.TrimSpace(sess.LastFloorText) == "" {
			return "", fmt.Errorf("verification session persisted empty last_floor_text")
		}
		if len(sess.Messages) == 0 {
			return "", fmt.Errorf("verification session has no persisted messages")
		}
		lastAssistant := ""
		for i := len(sess.Messages) - 1; i >= 0; i-- {
			if sess.Messages[i].Role == "assistant" {
				lastAssistant = strings.TrimSpace(sess.Messages[i].Content)
				break
			}
		}
		if lastAssistant == "" {
			return "", fmt.Errorf("verification session persisted no assistant scene reply")
		}
		if blessedReply != "" && lastAssistant != blessedReply {
			return "", fmt.Errorf("persisted assistant reply %q does not match outbound %q", lastAssistant, blessedReply)
		}
		return fmt.Sprintf("persisted turn=%d floor=%d chars", sess.TurnCount, len(sess.LastFloorText)), nil
	}); err != nil {
		return report, err
	}

	if durableChildrenMode != verifyDeployDurableChildrenOff {
		if err := runProbe("durable_children", func() (string, error) {
			if durableChildrenMode == verifyDeployDurableChildrenStatus {
				return verifyDeployDurableChildrenStatusSummary(store)
			}
			probeCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			detail, err := verifyDeployDurableChildren(probeCtx, store, built.DurableChildWake)
			if err != nil && durableChildrenMode == verifyDeployDurableChildrenWarn {
				return "warning: " + err.Error(), nil
			}
			return detail, err
		}); err != nil {
			return report, err
		}
	}

	report.Status = "passed"
	report.Diagnosis = "deployment verification passed"
	succeeded = true

	if !opts.KeepSession {
		if _, err := store.DeleteSession(key); err != nil {
			report.Diagnosis = fmt.Sprintf("deployment verification passed, but probe session cleanup failed: %v", err)
		}
	}
	return report, nil
}

func normalizeVerifyDeployDurableChildrenMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", verifyDeployDurableChildrenStatus:
		return verifyDeployDurableChildrenStatus, nil
	case verifyDeployDurableChildrenRequired:
		return verifyDeployDurableChildrenRequired, nil
	case verifyDeployDurableChildrenWarn:
		return verifyDeployDurableChildrenWarn, nil
	case verifyDeployDurableChildrenOff:
		return verifyDeployDurableChildrenOff, nil
	default:
		return "", fmt.Errorf("durable-children must be one of status|required|warn|off")
	}
}

func verifyDeployDurableChildrenStatusSummary(store *session.SQLiteStore) (string, error) {
	active, err := verifyDeployActiveDurableChildren(store)
	if err != nil {
		return "", err
	}
	if len(active) == 0 {
		return "active durable children: 0; non-invasive status check", nil
	}
	return fmt.Sprintf("active durable children: %d; wake probe skipped by default to avoid child wake side effects", len(active)), nil
}

func verifyDeployDurableChildren(ctx context.Context, store *session.SQLiteStore, wake func(context.Context, string, time.Time) error) (string, error) {
	active, err := verifyDeployActiveDurableChildren(store)
	if err != nil {
		return "", err
	}
	if len(active) == 0 {
		return "active durable children: 0", nil
	}
	if wake == nil {
		return "", fmt.Errorf("durable child wake probe unavailable for %d active child(ren)", len(active))
	}

	failures := make([]string, 0)
	now := time.Now().UTC()
	for _, agent := range active {
		if err := wake(ctx, strings.TrimSpace(agent.AgentID), now); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", strings.TrimSpace(agent.AgentID), err))
			if len(failures) >= 3 {
				break
			}
		}
	}
	if len(failures) > 0 {
		return "", fmt.Errorf("durable child wake failed for %d/%d active child(ren): %s", len(failures), len(active), strings.Join(failures, "; "))
	}
	return fmt.Sprintf("active durable children: %d; wake probe ok", len(active)), nil
}

func verifyDeployActiveDurableChildren(store *session.SQLiteStore) ([]core.DurableAgent, error) {
	if store == nil {
		return nil, fmt.Errorf("durable child probe has no session store")
	}
	agents, err := store.ListDurableAgents()
	if err != nil {
		return nil, err
	}
	active := make([]core.DurableAgent, 0, len(agents))
	for _, agent := range agents {
		if strings.EqualFold(firstNonEmpty(strings.TrimSpace(agent.Status), "active"), "active") {
			active = append(active, agent)
		}
	}
	return active, nil
}

func renderDeployVerificationReport(w io.Writer, report deployVerificationReport, format string) error {
	switch format {
	case commandOutputJSON:
		raw, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%s\n", raw)
		return err
	case commandOutputKV:
		return renderDeployVerificationReportKV(w, report)
	default:
		_, err := fmt.Fprintln(w, renderDeployVerificationReportHuman(report))
		return err
	}
}

func renderDeployVerificationReportKV(w io.Writer, report deployVerificationReport) error {
	if _, err := fmt.Fprintf(w, "action: verify-deploy\nstatus: %s\nblessed: %t\nprobe_chat_id: %d\nprobe_session_id: %s\n",
		report.Status,
		report.Blessed,
		report.ProbeChatID,
		report.ProbeSessionID,
	); err != nil {
		return err
	}
	if strings.TrimSpace(report.Diagnosis) != "" {
		if _, err := fmt.Fprintf(w, "diagnosis: %s\n", report.Diagnosis); err != nil {
			return err
		}
	}
	for _, probe := range report.Probes {
		if _, err := fmt.Fprintf(w, "- %s: %s (%dms)", probe.Name, probe.Status, probe.DurationMS); err != nil {
			return err
		}
		if strings.TrimSpace(probe.Detail) != "" {
			if _, err := fmt.Fprintf(w, " %s", probe.Detail); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprint(w, "\n"); err != nil {
			return err
		}
	}
	return nil
}

func renderDeployVerificationReportHuman(report deployVerificationReport) string {
	status := firstNonEmpty(strings.TrimSpace(report.Status), "unknown")
	why := "The release probes check runtime boot, governed reply, persistence, rollback evidence, and durable-child status without waking children by default."
	next := "Keep the service running and retain this commit as the rollback point."
	if status != "passed" {
		next = "Inspect the failed probe, repair or roll back, then rerun verify-deploy."
	}
	if diagnosis := strings.TrimSpace(report.Diagnosis); diagnosis != "" {
		why = diagnosis
	}
	details := []string{
		fmt.Sprintf("Blessed: %t", report.Blessed),
		fmt.Sprintf("Probe chat: %d", report.ProbeChatID),
		"Probe session: " + firstNonEmpty(strings.TrimSpace(report.ProbeSessionID), "-"),
	}
	evidence := []string{"Source: verify-deploy synthetic runtime probes."}
	for _, probe := range report.Probes {
		line := fmt.Sprintf("%s: %s (%dms)", probe.Name, probe.Status, probe.DurationMS)
		if detail := strings.TrimSpace(probe.Detail); detail != "" {
			line += " " + detail
		}
		evidence = append(evidence, line)
	}
	return face.RenderOperatorPanel(face.OperatorPanel{
		Title:    "Deploy Verification",
		State:    status,
		Why:      why,
		Next:     next,
		Details:  details,
		Evidence: evidence,
	})
}

func diagnoseDeployFailure(probe string, detail string) string {
	switch probe {
	case "boot":
		return "deployment verification failed during runtime startup: " + detail
	case "golden_path":
		return "deployment verification failed on the live governed reply path: " + detail
	case "persistence":
		return "deployment verification failed after the live turn ran, while checking persisted session state: " + detail
	default:
		if strings.TrimSpace(detail) == "" {
			return "deployment verification failed"
		}
		return "deployment verification failed: " + detail
	}
}

func defaultVerifyDeployChatID(senderID int64) int64 {
	if senderID < 0 {
		senderID = -senderID
	}
	return -(9_100_000_000 + senderID)
}
