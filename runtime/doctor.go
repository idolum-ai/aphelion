//go:build linux

package runtime

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/workspace"
)

const (
	doctorRequestMarker       = "DOCTOR_DIAGNOSTIC_REQUEST"
	doctorSummaryMarker       = "DOCTOR_TELEGRAM_SUMMARY_REQUEST"
	doctorReportFallbackText  = "Doctor diagnostics finished, but the model returned an empty report."
	doctorMaintainerArchetype = "aphelion-maintainer"
	doctorRunTimeout          = 5 * time.Minute
	doctorPacketMaxChars      = 120000
	doctorLogTailBytes        = 16000
	doctorFilePreviewChars    = 700
	doctorMessageLimit        = 12
	doctorTelegramMaxChars    = 3800
	doctorTelegramHardLimit   = 4096
)

type doctorDiagnosticInput struct {
	Message       core.InboundMessage
	Actor         principal.Principal
	Key           session.SessionKey
	Session       *session.Session
	Scope         sandbox.Scope
	PromptContext *workspace.PromptContext
	Exec          pipeline.TurnExecutionContract
	Maintainer    *doctorMaintainerDelegate
	Now           time.Time
}

type doctorMaintainerDelegate struct {
	Agent        core.DurableAgent
	MemoryRoot   string
	ProfileRoot  string
	RuntimeRules string
	Charter      string
	Capabilities string
}

type doctorMaintainerArchetypeProvenance struct {
	Name string `json:"name"`
}

type doctorArtifactManifest struct {
	AgentID   string                        `json:"agent_id"`
	UpdatedAt time.Time                     `json:"updated_at"`
	Artifacts []doctorArtifactManifestEntry `json:"artifacts"`
}

type doctorArtifactManifestEntry struct {
	Path      string    `json:"path"`
	Kind      string    `json:"kind,omitempty"`
	Source    string    `json:"source,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	SHA256    string    `json:"sha256"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (r *Runtime) StartDoctor(ctx context.Context, msg core.InboundMessage) error {
	if r == nil {
		return fmt.Errorf("runtime is unavailable")
	}
	if _, err := r.resolveDoctorAdmin(msg); err != nil {
		return err
	}
	go func() {
		runCtx, cancel := context.WithTimeout(context.Background(), doctorRunTimeout)
		defer cancel()
		if err := r.runDoctorOnce(runCtx, msg, time.Now().UTC()); err != nil {
			log.Printf("WARN doctor diagnostics failed chat_id=%d sender_id=%d err=%v", msg.ChatID, msg.SenderID, err)
			r.reportOperationalIssueAsync("doctor", err)
		}
	}()
	_ = ctx
	return nil
}

func (r *Runtime) runDoctorOnce(ctx context.Context, msg core.InboundMessage, now time.Time) (err error) {
	if r == nil || r.store == nil || r.provider == nil || r.outbound == nil {
		return fmt.Errorf("runtime doctor dependencies are unavailable")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	actor, err := r.resolveDoctorAdmin(msg)
	if err != nil {
		return err
	}
	scope, err := r.scopeForPrincipal(actor)
	if err != nil {
		return fmt.Errorf("resolve doctor scope: %w", err)
	}
	key := session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramDMScopeRef(msg.ChatID)}
	unlock := r.lockSession(key)
	defer unlock()

	sess, err := r.store.Load(key)
	if err != nil {
		return fmt.Errorf("load doctor session: %w", err)
	}
	applySessionScope(sess, key)
	if strings.TrimSpace(sess.ChatType) == "" {
		sess.ChatType = firstNonEmpty(strings.TrimSpace(msg.ChatType), "dm")
	}
	if strings.TrimSpace(sess.UserName) == "" {
		sess.UserName = strings.TrimSpace(msg.SenderName)
	}

	progress := r.newDoctorProgressReporter(key, msg)
	monitor := r.startTurnMonitor(key, session.TurnRunKindDoctor, "/doctor", progress, nil)
	var monitorErr error
	defer func() {
		if monitorErr != nil {
			surfaceDoctorProgress(ctx, progress, "Doctor diagnostics failed: "+trimError(monitorErr.Error()))
		}
		monitor.Finish(ctx, monitorErr)
	}()

	maintainer, err := r.doctorMaintainerDelegate()
	if err != nil {
		monitorErr = fmt.Errorf("load doctor maintainer delegate: %w", err)
		return monitorErr
	}

	surfaceDoctorProgress(ctx, progress, "Loading prompt and memory context")
	promptContext, err := r.promptContextForScope(scope, now)
	if err != nil {
		monitorErr = fmt.Errorf("load doctor prompt context: %w", err)
		return monitorErr
	}
	prepared := pipeline.TurnPrepareContract{
		UserText:   "/doctor",
		LedgerText: "/doctor",
	}
	exec := r.executionForTurn(prepared)
	r.applyModelSlotExecution(&exec, core.ModelSlotDoctor)
	surfaceDoctorProgress(ctx, progress, "Collecting session, memory, log, and runtime evidence")
	packet := r.buildDoctorDiagnosticPacket(ctx, doctorDiagnosticInput{
		Message:       msg,
		Actor:         actor,
		Key:           key,
		Session:       sess,
		Scope:         scope,
		PromptContext: promptContext,
		Exec:          exec,
		Maintainer:    maintainer,
		Now:           now,
	})

	awareness := r.governorRuntimeAwareness(scope, session.TurnRunKindDoctor, "telegram", exec)
	systemBlocks := prompt.BuildGovernorPromptBlocks(prompt.GovernorRequest{
		GovernorName:    prompt.DefaultGovernorName,
		GovernorBackend: exec.Backend,
		PrincipalRole:   "admin",
		WorkspaceRoot:   scope.WorkingRoot,
		Workspace:       promptContext,
		Runtime:         awareness,
	})
	systemPrompt := prompt.RenderSystemBlocks(systemBlocks)
	sess.SystemPrompt = systemPrompt

	input := []agent.Message{
		{Role: "system", Content: systemPrompt, SystemBlocks: systemBlocks},
		{Role: "system", Content: doctorReadOnlySystemNote()},
		{Role: "user", Content: packet},
	}
	if note := doctorMaintainerSystemNote(maintainer); note != "" {
		input = []agent.Message{input[0], input[1], {Role: "system", Content: note}, input[2]}
	}
	r.recordExecutionEvent(key, core.ExecutionEventProviderAttemptStarted, "provider", "started", map[string]any{
		"backend":       strings.TrimSpace(exec.Backend),
		"provider":      strings.TrimSpace(exec.ProviderName),
		"model":         strings.TrimSpace(exec.ModelName),
		"provider_path": strings.Join(exec.ProviderPath, ","),
		"run_kind":      string(session.TurnRunKindDoctor),
	}, time.Now().UTC())

	surfaceDoctorProgress(ctx, progress, "Asking the model to write the read-only diagnosis")
	turnResult, _, runErr := agent.RunTurn(ctx, exec.Provider, nil, &agent.Budget{
		Max:     r.cfg.Agent.MaxIterations,
		Caution: 0.7,
		Warning: 0.9,
	}, r.reasoningOptionsForRun(session.TurnRunKindDoctor), input)
	if runErr != nil {
		r.recordExecutionEvent(key, core.ExecutionEventProviderAttemptFailed, "provider", "failed", map[string]any{
			"backend":  strings.TrimSpace(exec.Backend),
			"provider": strings.TrimSpace(exec.ProviderName),
			"model":    strings.TrimSpace(exec.ModelName),
			"error":    trimError(runErr.Error()),
			"run_kind": string(session.TurnRunKindDoctor),
		}, time.Now().UTC())
		monitorErr = fmt.Errorf("run doctor diagnostics: %w", runErr)
		return monitorErr
	}
	if turnResult == nil {
		monitorErr = fmt.Errorf("doctor diagnostics returned no turn result")
		return monitorErr
	}
	if strings.TrimSpace(turnResult.ProviderFailure) != "" {
		r.recordExecutionEvent(key, core.ExecutionEventProviderAttemptFailed, "provider", "failed", map[string]any{
			"backend":  strings.TrimSpace(exec.Backend),
			"provider": strings.TrimSpace(exec.ProviderName),
			"model":    strings.TrimSpace(exec.ModelName),
			"error":    trimError(turnResult.ProviderFailure),
			"run_kind": string(session.TurnRunKindDoctor),
		}, time.Now().UTC())
		r.reportOperationalIssueAsync("doctor", fmt.Errorf("%s", strings.TrimSpace(turnResult.ProviderFailure)))
	} else {
		r.recordExecutionEvent(key, core.ExecutionEventProviderAttemptSucceeded, "provider", "succeeded", map[string]any{
			"backend":  strings.TrimSpace(exec.Backend),
			"provider": strings.TrimSpace(exec.ProviderName),
			"model":    strings.TrimSpace(exec.ModelName),
			"run_kind": string(session.TurnRunKindDoctor),
		}, time.Now().UTC())
	}

	report := strings.TrimSpace(turnResult.Text)
	if report == "" {
		report = doctorReportFallbackText
	}
	report = redactDoctorText(report)
	telegramReport, summaryUsage := r.telegramDoctorReport(ctx, key, exec, systemPrompt, systemBlocks, report, progress)
	var maintainerArtifact string
	if maintainer != nil {
		surfaceDoctorProgress(ctx, progress, "Storing the full report in maintainer child artifacts")
		if artifact, artifactErr := r.writeDoctorMaintainerReport(*maintainer, report, telegramReport, now); artifactErr != nil {
			r.reportOperationalIssueAsync("doctor_maintainer_artifact", artifactErr)
		} else {
			maintainerArtifact = artifact
		}
	}
	surfaceDoctorProgress(ctx, progress, "Saving the doctor report into chat history")
	newMessages := appendSyntheticTurn(sess, "/doctor", report, telegramReport, doctorFloorMetadata(report, telegramReport, maintainer, maintainerArtifact))
	if err := r.store.Save(sess, newMessages, addTokenUsage(turnResult.TokenUsage, summaryUsage)); err != nil {
		monitorErr = fmt.Errorf("save doctor report: %w", err)
		return monitorErr
	}
	surfaceDoctorProgress(ctx, progress, "Sending the doctor report to Telegram")
	msgID, err := r.outbound.SendMessage(ctx, core.OutboundMessage{
		ChatID:  msg.ChatID,
		Text:    telegramReport,
		ReplyTo: replyToMessageID(msg.MessageID),
	})
	if err != nil {
		monitorErr = fmt.Errorf("send doctor report: %w", err)
		return monitorErr
	}
	if err := r.store.RecordOutbound(key, sess.TurnCount, msgID, "doctor"); err != nil {
		monitorErr = fmt.Errorf("record doctor outbound: %w", err)
		return monitorErr
	}
	return nil
}

func (r *Runtime) resolveDoctorAdmin(msg core.InboundMessage) (principal.Principal, error) {
	if r == nil || r.resolver == nil {
		return principal.Principal{}, fmt.Errorf("principal resolver is unavailable")
	}
	if chatType := strings.TrimSpace(msg.ChatType); chatType != "" && chatType != "private" && chatType != "dm" {
		return principal.Principal{}, fmt.Errorf("doctor diagnostics must be run from an admin private chat")
	}
	actor, ok := r.resolver.ResolveTelegramUser(msg.SenderID)
	if !ok || actor.Role != principal.RoleAdmin {
		return principal.Principal{}, ErrPrincipalDenied
	}
	return actor, nil
}

func (r *Runtime) newDoctorProgressReporter(key session.SessionKey, msg core.InboundMessage) *toolProgressReporter {
	if r == nil {
		return nil
	}
	progress := r.newToolProgressReporter(key, msg, nil)
	if progress == nil {
		return nil
	}
	progress.suppressControls = true
	progress.controls = nil
	progress.taskSummary = "doctor diagnostics"
	progress.currentPlanStep = ""
	return progress
}

func (r *Runtime) doctorMaintainerDelegate() (*doctorMaintainerDelegate, error) {
	if r == nil || r.store == nil {
		return nil, nil
	}
	agents, err := r.store.ListDurableAgents()
	if err != nil {
		return nil, err
	}
	sort.SliceStable(agents, func(i, j int) bool {
		return strings.TrimSpace(agents[i].AgentID) < strings.TrimSpace(agents[j].AgentID)
	})
	for _, agent := range agents {
		if !strings.EqualFold(strings.TrimSpace(agent.Status), "active") {
			continue
		}
		delegate, ok, err := r.doctorMaintainerDelegateFromAgent(agent)
		if err != nil {
			return nil, err
		}
		if ok {
			return delegate, nil
		}
	}
	return nil, nil
}

func (r *Runtime) doctorMaintainerDelegateFromAgent(agent core.DurableAgent) (*doctorMaintainerDelegate, bool, error) {
	memoryRoot, err := r.doctorDurableAgentMemoryRoot(agent)
	if err != nil {
		return nil, false, err
	}
	profileRoot := filepath.Join(memoryRoot, "profile")
	raw, err := os.ReadFile(filepath.Join(profileRoot, "ARCHETYPE.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read maintainer archetype provenance for %s: %w", strings.TrimSpace(agent.AgentID), err)
	}
	var provenance doctorMaintainerArchetypeProvenance
	if err := json.Unmarshal(raw, &provenance); err != nil {
		return nil, false, fmt.Errorf("decode maintainer archetype provenance for %s: %w", strings.TrimSpace(agent.AgentID), err)
	}
	if !strings.EqualFold(strings.TrimSpace(provenance.Name), doctorMaintainerArchetype) {
		return nil, false, nil
	}
	return &doctorMaintainerDelegate{
		Agent:        agent,
		MemoryRoot:   memoryRoot,
		ProfileRoot:  profileRoot,
		RuntimeRules: readDoctorProfileFile(filepath.Join(profileRoot, "archetype", "profile", "runtime.md")),
		Charter:      readDoctorProfileFile(filepath.Join(profileRoot, "archetype", "profile", "charter.md")),
		Capabilities: readDoctorProfileFile(filepath.Join(profileRoot, "archetype", "profile", "capabilities.md")),
	}, true, nil
}

func (r *Runtime) doctorDurableAgentMemoryRoot(agent core.DurableAgent) (string, error) {
	_, memoryRoot := durableagent.LocalRoots(agent.AgentID, agent.LocalStorageRoots)
	if strings.TrimSpace(memoryRoot) == "" && r != nil && r.store != nil {
		if dbPath := strings.TrimSpace(r.store.DBPath()); dbPath != "" {
			_, memoryRoot = durableagent.DefaultLocalRoots(dbPath, strings.TrimSpace(agent.AgentID))
		}
	}
	if strings.TrimSpace(memoryRoot) == "" {
		return "", fmt.Errorf("durable agent %q has no local memory root", strings.TrimSpace(agent.AgentID))
	}
	return memoryRoot, nil
}

func readDoctorProfileFile(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func (r *Runtime) writeDoctorMaintainerReport(maintainer doctorMaintainerDelegate, report string, telegramReport string, now time.Time) (string, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	artifactRoot := filepath.Join(maintainer.MemoryRoot, "artifacts")
	rel := filepath.ToSlash(filepath.Join("reports", now.UTC().Format("20060102T150405Z")+"-doctor.md"))
	target := filepath.Join(artifactRoot, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return "", fmt.Errorf("create doctor maintainer artifact directory: %w", err)
	}
	content := strings.Join([]string{
		"# Doctor Report",
		"",
		"generated_at_utc: " + now.UTC().Format(time.RFC3339),
		"delegate_agent_id: " + strings.TrimSpace(maintainer.Agent.AgentID),
		"delegate_archetype: " + doctorMaintainerArchetype,
		"mode: read_only",
		"",
		"## Telegram Summary",
		"",
		strings.TrimSpace(telegramReport),
		"",
		"## Full Report",
		"",
		strings.TrimSpace(report),
		"",
	}, "\n")
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		return "", fmt.Errorf("write doctor maintainer artifact: %w", err)
	}
	sum := sha256.Sum256([]byte(content))
	hash := "sha256:" + hex.EncodeToString(sum[:])
	if err := writeDoctorMaintainerArtifactManifest(artifactRoot, strings.TrimSpace(maintainer.Agent.AgentID), doctorArtifactManifestEntry{
		Path:      rel,
		Kind:      "doctor_report",
		Source:    "doctor_delegate",
		Reason:    "/doctor delegated read-only diagnosis",
		SHA256:    hash,
		UpdatedAt: now.UTC(),
	}); err != nil {
		return "", err
	}
	return filepath.ToSlash(filepath.Join("artifacts", rel)), nil
}

func writeDoctorMaintainerArtifactManifest(artifactRoot string, agentID string, entry doctorArtifactManifestEntry) error {
	manifestPath := filepath.Join(artifactRoot, "ARTIFACTS.json")
	manifest := doctorArtifactManifest{
		AgentID:   strings.TrimSpace(agentID),
		Artifacts: []doctorArtifactManifestEntry{},
	}
	if raw, err := os.ReadFile(manifestPath); err == nil {
		if err := json.Unmarshal(raw, &manifest); err != nil {
			return fmt.Errorf("decode doctor maintainer artifact manifest: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read doctor maintainer artifact manifest: %w", err)
	}
	manifest.AgentID = strings.TrimSpace(agentID)
	entry.Path = strings.TrimSpace(filepath.ToSlash(entry.Path))
	entry.Kind = strings.TrimSpace(entry.Kind)
	entry.Source = strings.TrimSpace(entry.Source)
	entry.Reason = strings.TrimSpace(entry.Reason)
	entry.SHA256 = strings.TrimSpace(entry.SHA256)
	if entry.UpdatedAt.IsZero() {
		entry.UpdatedAt = time.Now().UTC()
	}
	replaced := false
	for i := range manifest.Artifacts {
		if manifest.Artifacts[i].Path == entry.Path {
			manifest.Artifacts[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		manifest.Artifacts = append(manifest.Artifacts, entry)
	}
	sort.SliceStable(manifest.Artifacts, func(i, j int) bool {
		return manifest.Artifacts[i].Path < manifest.Artifacts[j].Path
	})
	manifest.UpdatedAt = entry.UpdatedAt.UTC()
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode doctor maintainer artifact manifest: %w", err)
	}
	raw = append(raw, '\n')
	if err := os.MkdirAll(artifactRoot, 0o700); err != nil {
		return fmt.Errorf("create doctor maintainer artifact root: %w", err)
	}
	if err := os.WriteFile(manifestPath, raw, 0o600); err != nil {
		return fmt.Errorf("write doctor maintainer artifact manifest: %w", err)
	}
	return nil
}

func surfaceDoctorProgress(ctx context.Context, progress *toolProgressReporter, text string) {
	if progress == nil {
		return
	}
	progress.Surface(ctx, text)
}

func (r *Runtime) telegramDoctorReport(ctx context.Context, key session.SessionKey, exec pipeline.TurnExecutionContract, systemPrompt string, systemBlocks []agent.SystemBlock, report string, progress *toolProgressReporter) (string, core.TokenUsage) {
	report = strings.TrimSpace(redactDoctorText(report))
	if report == "" {
		return doctorReportFallbackText, core.TokenUsage{}
	}
	if doctorCharCount(report) <= doctorTelegramMaxChars {
		return report, core.TokenUsage{}
	}
	surfaceDoctorProgress(ctx, progress, "Condensing the doctor report for one Telegram message")
	limitText := strconv.Itoa(doctorTelegramMaxChars)
	input := []agent.Message{
		{Role: "system", Content: systemPrompt, SystemBlocks: systemBlocks},
		{Role: "system", Content: doctorTelegramSummarySystemNote()},
		{Role: "user", Content: strings.Join([]string{
			doctorSummaryMarker,
			"telegram_hard_limit_chars=" + strconv.Itoa(doctorTelegramHardLimit),
			"service_single_message_limit_chars=" + limitText,
			"full_report_chars=" + strconv.Itoa(doctorCharCount(report)),
			"",
			"Full report to condense:",
			report,
		}, "\n")},
	}
	r.recordExecutionEvent(key, core.ExecutionEventProviderAttemptStarted, "provider", "started", map[string]any{
		"backend":              strings.TrimSpace(exec.Backend),
		"provider":             strings.TrimSpace(exec.ProviderName),
		"model":                strings.TrimSpace(exec.ModelName),
		"provider_path":        strings.Join(exec.ProviderPath, ","),
		"run_kind":             string(session.TurnRunKindDoctor),
		"doctor_summary_stage": "telegram_condense",
		"target_chars":         doctorTelegramMaxChars,
	}, time.Now().UTC())
	turnResult, _, err := agent.RunTurn(ctx, exec.Provider, nil, &agent.Budget{
		Max:     2,
		Caution: 0.7,
		Warning: 0.9,
	}, r.reasoningOptionsForRun(session.TurnRunKindDoctor), input)
	if err != nil {
		r.recordExecutionEvent(key, core.ExecutionEventProviderAttemptFailed, "provider", "failed", map[string]any{
			"backend":              strings.TrimSpace(exec.Backend),
			"provider":             strings.TrimSpace(exec.ProviderName),
			"model":                strings.TrimSpace(exec.ModelName),
			"error":                trimError(err.Error()),
			"run_kind":             string(session.TurnRunKindDoctor),
			"doctor_summary_stage": "telegram_condense",
		}, time.Now().UTC())
		r.reportOperationalIssueAsync("doctor_summary", err)
		return doctorFitTelegramReport(report, doctorTelegramMaxChars), core.TokenUsage{}
	}
	if turnResult == nil {
		err := fmt.Errorf("doctor telegram summary returned no turn result")
		r.reportOperationalIssueAsync("doctor_summary", err)
		return doctorFitTelegramReport(report, doctorTelegramMaxChars), core.TokenUsage{}
	}
	if strings.TrimSpace(turnResult.ProviderFailure) != "" {
		r.recordExecutionEvent(key, core.ExecutionEventProviderAttemptFailed, "provider", "failed", map[string]any{
			"backend":              strings.TrimSpace(exec.Backend),
			"provider":             strings.TrimSpace(exec.ProviderName),
			"model":                strings.TrimSpace(exec.ModelName),
			"error":                trimError(turnResult.ProviderFailure),
			"run_kind":             string(session.TurnRunKindDoctor),
			"doctor_summary_stage": "telegram_condense",
		}, time.Now().UTC())
		r.reportOperationalIssueAsync("doctor_summary", fmt.Errorf("%s", strings.TrimSpace(turnResult.ProviderFailure)))
		return doctorFitTelegramReport(report, doctorTelegramMaxChars), turnResult.TokenUsage
	}
	r.recordExecutionEvent(key, core.ExecutionEventProviderAttemptSucceeded, "provider", "succeeded", map[string]any{
		"backend":              strings.TrimSpace(exec.Backend),
		"provider":             strings.TrimSpace(exec.ProviderName),
		"model":                strings.TrimSpace(exec.ModelName),
		"run_kind":             string(session.TurnRunKindDoctor),
		"doctor_summary_stage": "telegram_condense",
		"target_chars":         doctorTelegramMaxChars,
	}, time.Now().UTC())

	summary := strings.TrimSpace(redactDoctorText(turnResult.Text))
	if summary == "" {
		summary = doctorFitTelegramReport(report, doctorTelegramMaxChars)
	}
	return doctorFitTelegramReport(summary, doctorTelegramMaxChars), turnResult.TokenUsage
}

func doctorTelegramSummarySystemNote() string {
	return strings.Join([]string{
		"Role: You are compressing a /doctor report for Telegram.",
		"## Goal",
		"Produce the shortest useful operator-facing health summary from the provided report.",
		"## Success Criteria",
		"- The operator can identify the most important current issue and the next sensible action.",
		"- Evidence is preserved only when it justifies priority, status, or risk.",
		"- Read-only status is clear: do not claim to have changed files, memory, services, branches, or commits.",
		"## Constraints",
		"- Stay under the provided service_single_message_limit_chars, which is below Telegram's 4096-character ceiling.",
		"- Pick the most important thing to fix first. If there is only one thing the operator should do next, make that obvious.",
		"- Prefer at most three findings. Include only evidence needed to justify the priority.",
		"- Preserve resolved/current status labels when relevant: active, likely_fixed, historical_resolved, residual_risk, unknown.",
		"## Output",
		"- Return one operator-facing message only.",
		"## Stop Rules",
		"- Do not include exhaustive logs, full inventories, or every recommendation.",
	}, "\n")
}

func doctorFloorMetadata(fullReport string, telegramReport string, maintainer *doctorMaintainerDelegate, maintainerArtifact string) string {
	fullChars := doctorCharCount(fullReport)
	telegramChars := doctorCharCount(telegramReport)
	parts := make([]string, 0, 5)
	if fullChars > 0 || telegramChars > 0 {
		parts = append(parts, fmt.Sprintf("doctor_full_report_chars=%d doctor_telegram_report_chars=%d doctor_telegram_limit_chars=%d", fullChars, telegramChars, doctorTelegramMaxChars))
	}
	if maintainer != nil {
		parts = append(parts, "doctor_delegate_agent_id="+strings.TrimSpace(maintainer.Agent.AgentID))
	}
	if strings.TrimSpace(maintainerArtifact) != "" {
		parts = append(parts, "doctor_delegate_artifact="+strings.TrimSpace(maintainerArtifact))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func doctorFitTelegramReport(text string, limit int) string {
	text = strings.TrimSpace(redactDoctorText(text))
	if text == "" {
		return doctorReportFallbackText
	}
	if limit <= 0 {
		limit = doctorTelegramMaxChars
	}
	if doctorCharCount(text) <= limit {
		return text
	}
	suffix := "\n\n[trimmed to fit one Telegram message]"
	suffixChars := doctorCharCount(suffix)
	if suffixChars >= limit {
		return string([]rune(text)[:limit])
	}
	headLimit := limit - suffixChars
	runes := []rune(text)
	cut := headLimit
	searchFloor := headLimit - 400
	if searchFloor < 0 {
		searchFloor = 0
	}
	for i := headLimit; i >= searchFloor; i-- {
		if runes[i] == '\n' {
			cut = i
			break
		}
		if i < headLimit && (runes[i] == '.' || runes[i] == ';') {
			cut = i + 1
			break
		}
	}
	return strings.TrimSpace(string(runes[:cut])) + suffix
}

func doctorCharCount(text string) int {
	return utf8.RuneCountInString(text)
}

func doctorReadOnlySystemNote() string {
	return strings.Join([]string{
		"You are running the /doctor command.",
		"This is a read-only diagnostic pass. Do not claim to have edited files, run commands, restarted services, changed memory, or committed code.",
		"Use the diagnostic packet and the loaded prompt/memory context to produce an operator-facing report.",
		"For every issue you report, classify it as active, likely_fixed, historical_resolved, residual_risk, or unknown by comparing old failure evidence with current-state checks.",
		"Do not present an old failure as active when the current-state checks indicate it is likely fixed; instead call out remaining verification gaps.",
		"Include concrete code recommendations when the evidence points to code changes, but frame them as recommendations only.",
		"Required sections: State of Things, Recent Failures or Risks, Memory and Prompt Health, Runtime and Session Health, Recommendations, Code Recommendations, Confidence and Unknowns.",
	}, "\n")
}

func doctorMaintainerSystemNote(maintainer *doctorMaintainerDelegate) string {
	if maintainer == nil {
		return ""
	}
	return strings.Join([]string{
		"This /doctor run is delegated to the aphelion-maintainer durable child in read-only mode.",
		"Durable agent: " + strings.TrimSpace(maintainer.Agent.AgentID),
		"Use the maintainer archetype and profile as the operating boundary for diagnosis and recommendations.",
		"Do not mutate the local Aphelion clone. If recommending implementation, specify the approved path: isolated /tmp clone, tests there, GitHub PR via a separately approved GitHub App PEM.",
		"Do not claim active grants, repository edits, service restarts, commits, pushes, or PRs unless the diagnostic packet contains concrete evidence that they happened.",
	}, "\n")
}

func (r *Runtime) buildDoctorDiagnosticPacket(ctx context.Context, input doctorDiagnosticInput) string {
	now := input.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var b strings.Builder
	writeDoctorLine(&b, doctorRequestMarker)
	writeDoctorKV(&b, "generated_at_utc", now.Format(time.RFC3339))
	writeDoctorKV(&b, "chat_id", strconv.FormatInt(input.Key.ChatID, 10))
	writeDoctorKV(&b, "sender_id", strconv.FormatInt(input.Message.SenderID, 10))
	writeDoctorKV(&b, "run_kind", string(session.TurnRunKindDoctor))
	writeDoctorKV(&b, "mode", "read_only")

	writeDoctorSection(&b, "Effective Runtime")
	r.writeDoctorRuntimeConfig(&b, input.Exec, input.Scope)

	writeDoctorSection(&b, "Current Session")
	writeDoctorSessionSummary(&b, input.Session)
	writeDoctorRecentMessages(&b, input.Session, doctorMessageLimit)

	writeDoctorSection(&b, "Mission Ledger")
	r.writeDoctorMissionLedger(&b, input.Key, now)

	writeDoctorSection(&b, "Prompt Context Inventory")
	writeDoctorPromptInventory(&b, input.PromptContext)

	writeDoctorSection(&b, "Memory Footprint")
	r.writeDoctorMemoryFootprint(&b, input.Scope, now)

	writeDoctorSection(&b, "Maintainer Delegate")
	writeDoctorMaintainerDelegate(&b, input.Maintainer)

	writeDoctorSection(&b, "Known Issue Status Checks")
	r.writeDoctorIssueStatusChecks(&b, input)

	writeDoctorSection(&b, "Execution Events")
	r.writeDoctorExecutionEvents(ctx, &b, input.Key, now)

	writeDoctorSection(&b, "Turn Runs")
	r.writeDoctorTurnRuns(ctx, &b, now)

	writeDoctorSection(&b, "Semantic Store")
	r.writeDoctorSemanticStats(&b)

	writeDoctorSection(&b, "Tailnet")
	r.writeDoctorTailnetDiagnostics(ctx, &b)

	writeDoctorSection(&b, "Recent Service Log Tail")
	r.writeDoctorLogTail(&b)

	writeDoctorSection(&b, "Codex Work Migration Review")
	r.writeDoctorCodexWorkMigrationReview(ctx, &b, input)

	writeDoctorSection(&b, "Doctor Instructions")
	writeDoctorLine(&b, "Analyze the evidence above and the loaded prompt/memory context. Identify likely causes, residual risks, and specific follow-up work. Do not perform actions. Before reporting a failure as current, check whether the Known Issue Status Checks or newer runtime evidence indicate it has already been fixed.")

	packet := redactDoctorText(b.String())
	if len(packet) > doctorPacketMaxChars {
		packet = strings.TrimSpace(packet[:doctorPacketMaxChars]) + "\n\n[doctor diagnostic packet truncated]"
	}
	return packet
}

func (r *Runtime) writeDoctorRuntimeConfig(b *strings.Builder, exec pipeline.TurnExecutionContract, scope sandbox.Scope) {
	if r == nil || r.cfg == nil {
		writeDoctorLine(b, "runtime_config: unavailable")
		return
	}
	writeDoctorKV(b, "governor_backend", strings.TrimSpace(exec.Backend))
	writeDoctorKV(b, "governor_provider", strings.TrimSpace(exec.ProviderName))
	writeDoctorKV(b, "governor_model", strings.TrimSpace(exec.ModelName))
	writeDoctorKV(b, "provider_path", strings.Join(exec.ProviderPath, " -> "))
	writeDoctorKV(b, "configured_provider_chain", strings.Join(config.EffectiveProviderChain(r.cfg), " -> "))
	writeDoctorKV(b, "codex_context_window", strconv.Itoa(r.cfg.Governor.Codex.ContextWindow))
	writeDoctorKV(b, "codex_transport_retries", strconv.Itoa(r.cfg.Governor.Codex.TransportRetries))
	workStatus := WorkExecutorStatus{}
	if r.workExecutor != nil {
		workStatus = r.workExecutor.Status()
	}
	writeDoctorKV(b, "work_executor_configured", firstNonEmpty(strings.TrimSpace(workStatus.Configured), strings.TrimSpace(r.cfg.Work.Executor)))
	writeDoctorKV(b, "work_executor_preferred", firstNonEmpty(strings.TrimSpace(workStatus.Preferred), firstRuntimeWorkExecutor(r.cfg.Work)))
	writeDoctorKV(b, "work_executor_active", strings.TrimSpace(workStatus.Active))
	writeDoctorKV(b, "work_executor_last_attempted", strings.TrimSpace(workStatus.LastAttempted))
	writeDoctorKV(b, "work_executor_fallback_reason", strings.TrimSpace(workStatus.FallbackReason))
	writeDoctorKV(b, "work_executor_last_error", strings.TrimSpace(workStatus.LastError))
	writeDoctorKV(b, "codex_work_app_server", strings.TrimSpace(r.cfg.Work.Codex.AppServerAddress))
	writeDoctorKV(b, "session_max_context_ratio", fmt.Sprintf("%.2f", r.cfg.Sessions.MaxContextRatio))
	writeDoctorKV(b, "session_compaction_ratio", fmt.Sprintf("%.2f", r.cfg.Sessions.CompactionRatio))
	writeDoctorKV(b, "bootstrap_total_max_chars", strconv.Itoa(r.cfg.Agent.BootstrapTotalMaxChars))
	writeDoctorKV(b, "memory_semantic_enabled", strconv.FormatBool(r.cfg.Memory.Semantic.Enabled))
	writeDoctorKV(b, "memory_aggressive_enabled", strconv.FormatBool(r.cfg.Memory.Aggressive.Enabled))
	writeDoctorKV(b, "memory_aggressive_flush_on_boundary", strconv.FormatBool(r.cfg.Memory.Aggressive.FlushOnSessionBoundary))
	writeDoctorKV(b, "heartbeat_enabled", strconv.FormatBool(r.cfg.Heartbeat.Enabled))
	writeDoctorKV(b, "cron_enabled", strconv.FormatBool(r.cfg.Cron.Enabled))
	writeDoctorKV(b, "prompt_root", r.cfg.Agent.PromptRoot)
	writeDoctorKV(b, "exec_root", r.cfg.Agent.ExecRoot)
	writeDoctorKV(b, "shared_memory_root", strings.TrimSpace(scope.SharedMemoryRoot))
	writeDoctorKV(b, "working_root", strings.TrimSpace(scope.WorkingRoot))
}

func (r *Runtime) writeDoctorCodexWorkMigrationReview(ctx context.Context, b *strings.Builder, input doctorDiagnosticInput) {
	if r == nil || r.store == nil {
		writeDoctorLine(b, "codex_work_migration_review: unavailable")
		return
	}
	opState, err := r.store.OperationState(input.Key)
	if err != nil {
		writeDoctorLine(b, "codex_work_operation_error="+strconv.Quote(err.Error()))
		return
	}
	opState = session.NormalizeOperationState(opState)
	work := opState.Work
	writeDoctorKV(b, "codex_work_executor", strings.TrimSpace(work.Executor))
	writeDoctorKV(b, "codex_work_configured_executor", strings.TrimSpace(work.ConfiguredExecutor))
	writeDoctorKV(b, "codex_work_preferred_executor", strings.TrimSpace(work.PreferredExecutor))
	writeDoctorKV(b, "codex_work_lane_mode", strings.TrimSpace(work.CodexLaneMode))
	writeDoctorKV(b, "codex_work_thread_id", strings.TrimSpace(work.CodexThreadID))
	writeDoctorKV(b, "codex_work_turn_id", strings.TrimSpace(work.CodexLastTurnID))
	writeDoctorKV(b, "codex_work_commit_lane_status", strings.TrimSpace(work.CommitLaneStatus))
	writeDoctorKV(b, "codex_work_changed_files", strconv.Itoa(len(work.ChangedFiles)))
	writeDoctorKV(b, "codex_work_commands", strconv.Itoa(len(work.Commands)))
	counts := codexWorkEventCounts(work.CodexEvents)
	writeDoctorKV(b, "codex_work_event_count", strconv.Itoa(len(work.CodexEvents)))
	writeDoctorKV(b, "codex_work_file_change_events", strconv.Itoa(counts["file_change"]))
	writeDoctorKV(b, "codex_work_command_events", strconv.Itoa(counts["command"]))
	writeDoctorKV(b, "codex_work_user_input_events", strconv.Itoa(counts["user_input"]))
	writeDoctorKV(b, "codex_work_subagent_events", strconv.Itoa(counts["subagent"]))
	writeDoctorKV(b, "codex_work_mcp_events", strconv.Itoa(counts["mcp"]))
	writeDoctorKV(b, "codex_work_auto_review_events", strconv.Itoa(counts["auto_review"]))
	writeDoctorKV(b, "codex_work_rollout_history_events", strconv.Itoa(counts["rollout_history"]))
	if len(work.CodexEvents) > 0 {
		writeDoctorLine(b, "codex_work_recent_events:")
		start := len(work.CodexEvents) - 8
		if start < 0 {
			start = 0
		}
		for _, event := range work.CodexEvents[start:] {
			writeDoctorLine(b, fmt.Sprintf("- kind=%s method=%s status=%s subject=%q path=%q command=%q",
				strings.TrimSpace(event.Kind),
				strings.TrimSpace(event.Method),
				strings.TrimSpace(event.Status),
				truncatePreview(event.Subject, 180),
				truncatePreview(event.Path, 180),
				truncatePreview(event.Command, 220),
			))
		}
	}
	if preview := strings.TrimSpace(work.PatchPreview); preview != "" {
		writeDoctorKV(b, "codex_work_patch_preview_chars", strconv.Itoa(len(preview)))
	}
	continuation, continuationErr := r.store.ContinuationState(input.Key)
	if continuationErr != nil {
		writeDoctorLine(b, "codex_work_continuation_error="+strconv.Quote(continuationErr.Error()))
	} else {
		continuation = session.NormalizeContinuationState(continuation)
		writeDoctorKV(b, "codex_work_continuation_status", string(continuation.Status))
		writeDoctorKV(b, "codex_work_lease_status", string(continuation.ContinuationLease.Status))
		writeDoctorKV(b, "codex_work_lease_id", strings.TrimSpace(continuation.ContinuationLease.ID))
		writeDoctorKV(b, "codex_work_lease_expires_at", continuation.ContinuationLease.ExpiresAt.UTC().Format(time.RFC3339))
		writeDoctorKV(b, "codex_work_continuation_mode", string(continuationWorkMode(continuation)))
		writeDoctorKV(b, "codex_work_continuation_eligible", strconv.FormatBool(r.shouldRouteContinuationThroughWorkExecutor(continuation)))
	}
	if r.cfg != nil {
		writeDoctorKV(b, "codex_work_config_executor", strings.TrimSpace(r.cfg.Work.Executor))
		writeDoctorKV(b, "codex_work_config_auto_order", strings.Join(r.cfg.Work.AutoOrder, " -> "))
		writeDoctorKV(b, "codex_work_config_app_server", strings.TrimSpace(r.cfg.Work.Codex.AppServerAddress))
	}
	recentWorkEvents := 0
	if events, eventErr := r.store.ExecutionEventsRecent(120); eventErr == nil {
		for _, event := range events {
			if strings.HasPrefix(strings.TrimSpace(event.EventType), "work.executor.") {
				recentWorkEvents++
			}
		}
		writeDoctorKV(b, "codex_work_recent_executor_events", strconv.Itoa(recentWorkEvents))
	} else {
		writeDoctorLine(b, "codex_work_recent_events_error="+strconv.Quote(eventErr.Error()))
	}
	status := "not_started"
	switch {
	case len(work.CodexEvents) > 0:
		status = "evidence_present"
	case strings.EqualFold(strings.TrimSpace(work.Executor), "codex") || strings.TrimSpace(work.CodexThreadID) != "":
		status = "needs_event_migration_review"
	case r.cfg != nil && strings.TrimSpace(r.cfg.Work.Codex.AppServerAddress) == "":
		status = "codex_app_server_unconfigured"
	}
	writeDoctorKV(b, "codex_work_migration_status", status)
	writeDoctorLine(b, "codex_work_migration_next=\"Before expanding Codex runtime features, confirm live operation_state.work carries codex_events, patch_preview, commit_lane_status, thread ids, and recent work.executor execution events.\"")
	_ = ctx
}

func codexWorkEventCounts(events []session.WorkCodexEvent) map[string]int {
	counts := map[string]int{}
	for _, event := range events {
		if kind := strings.TrimSpace(event.Kind); kind != "" {
			counts[kind]++
		}
	}
	return counts
}

func writeDoctorSessionSummary(b *strings.Builder, sess *session.Session) {
	if sess == nil {
		writeDoctorLine(b, "session: unavailable")
		return
	}
	writeDoctorKV(b, "session_id", sess.SessionID)
	writeDoctorKV(b, "turn_count", strconv.Itoa(sess.TurnCount))
	writeDoctorKV(b, "message_count", strconv.Itoa(len(sess.Messages)))
	writeDoctorKV(b, "last_provider", sess.LastProvider)
	writeDoctorKV(b, "last_model", sess.LastModel)
	writeDoctorKV(b, "last_error", truncatePreview(sess.LastError, 500))
	writeDoctorKV(b, "last_floor_preview", truncatePreview(sess.LastFloorText, 800))
	writeDoctorKV(b, "total_input_tokens", strconv.FormatInt(sess.TotalInputTokens, 10))
	writeDoctorKV(b, "total_output_tokens", strconv.FormatInt(sess.TotalOutputTokens, 10))
	writeDoctorKV(b, "total_cache_read", strconv.FormatInt(sess.TotalCacheRead, 10))
	writeDoctorKV(b, "total_cache_write", strconv.FormatInt(sess.TotalCacheWrite, 10))
	writeDoctorKV(b, "continuation_status", string(sess.ContinuationState.Status))
	writeDoctorKV(b, "active_tool_calls", strconv.Itoa(sess.ActiveToolCalls))
}

func writeDoctorRecentMessages(b *strings.Builder, sess *session.Session, limit int) {
	if sess == nil || len(sess.Messages) == 0 || limit == 0 {
		return
	}
	if limit < 0 || limit > len(sess.Messages) {
		limit = len(sess.Messages)
	}
	start := len(sess.Messages) - limit
	if start < 0 {
		start = 0
	}
	writeDoctorLine(b, "recent_messages:")
	for _, msg := range sess.Messages[start:] {
		writeDoctorLine(b, fmt.Sprintf("- turn=%d role=%s compacted=%t chars=%d preview=%q",
			msg.TurnIndex,
			strings.TrimSpace(msg.Role),
			msg.Compacted,
			msg.ContentChars,
			truncatePreview(msg.Content, 300),
		))
	}
}

func (r *Runtime) writeDoctorMissionLedger(b *strings.Builder, key session.SessionKey, now time.Time) {
	if r == nil || r.store == nil {
		writeDoctorLine(b, "mission_ledger: unavailable")
		return
	}
	health, err := r.store.MissionLedgerHealth(now)
	if err != nil {
		writeDoctorLine(b, "mission_ledger_error="+strconv.Quote(err.Error()))
		return
	}
	writeDoctorKV(b, "mission_active", strconv.Itoa(health.ActiveCount))
	writeDoctorKV(b, "mission_pinned", strconv.Itoa(health.PinnedCount))
	writeDoctorKV(b, "mission_recurring", strconv.Itoa(health.RecurringCount))
	writeDoctorKV(b, "mission_blocked", strconv.Itoa(health.BlockedCount))
	writeDoctorKV(b, "mission_self_continuation_enabled", strconv.Itoa(health.SelfContinuationEnabledCount))
	writeDoctorKV(b, "mission_stale_candidates", strconv.Itoa(health.StaleCandidateCount))
	writeDoctorKV(b, "mission_pending_handoffs", strconv.Itoa(health.PendingHandoffCount))
	if working, err := r.store.WorkingObjective(key); err == nil && strings.TrimSpace(working.Objective) != "" {
		writeDoctorKV(b, "working_objective", truncatePreview(working.Objective, 400))
	}
	missions, err := r.store.Missions(session.MissionFilter{Limit: 12})
	if err != nil {
		writeDoctorLine(b, "mission_list_error="+strconv.Quote(err.Error()))
		return
	}
	writeDoctorLine(b, "recent_missions:")
	if len(missions) == 0 {
		writeDoctorLine(b, "- none")
		return
	}
	for _, mission := range missions {
		writeDoctorLine(b, fmt.Sprintf("- id=%s status=%s pinned=%t owner=%s title=%q self_continue=%t", mission.ID, mission.Status, mission.Pinned, mission.Owner, truncatePreview(mission.Title, 120), mission.Authority.CanSelfContinue))
	}
}

func writeDoctorPromptInventory(b *strings.Builder, ctx *workspace.PromptContext) {
	if ctx == nil {
		writeDoctorLine(b, "prompt_context: unavailable")
		return
	}
	writeDoctorKV(b, "prompt_workspace", ctx.Workspace)
	writeDoctorLine(b, "stable_files:")
	for _, file := range ctx.Stable {
		writeDoctorLoadedFile(b, file)
	}
	writeDoctorLine(b, "dynamic_files:")
	for _, file := range ctx.Dynamic {
		writeDoctorLoadedFile(b, file)
	}
}

func writeDoctorLoadedFile(b *strings.Builder, file workspace.LoadedFile) {
	writeDoctorLine(b, fmt.Sprintf("- path=%s chars=%d truncated=%t preview=%q",
		strings.TrimSpace(file.Path),
		len(file.Content),
		file.Truncated,
		truncatePreview(file.Content, doctorFilePreviewChars),
	))
}

func (r *Runtime) writeDoctorMemoryFootprint(b *strings.Builder, scope sandbox.Scope, now time.Time) {
	root := dynamicPromptRoot(scope)
	writeDoctorKV(b, "memory_root", root)
	if strings.TrimSpace(root) == "" {
		return
	}
	paths := uniqueDoctorPaths(append([]string{
		"MEMORY.md",
		"HEARTBEAT.md",
		"SKILLS.md",
		"memory/knowledge.md",
		"memory/decisions.md",
		"memory/questions.md",
		"memory/rhizome.md",
		"memory/dreams.md",
	}, r.cfg.Agent.DynamicFiles...))
	for _, rel := range paths {
		writeDoctorFileStat(b, root, rel)
	}
	writeDoctorDirStat(b, filepath.Join(root, "memory", "inbox"), "memory/inbox")
	writeDoctorDirStat(b, filepath.Join(root, "memory", "daily"), "memory/daily")
	if r.cfg.Agent.DailyNotes {
		today := filepath.ToSlash(filepath.Join("memory", "daily", now.Format("2006-01-02")+".md"))
		yesterday := filepath.ToSlash(filepath.Join("memory", "daily", now.AddDate(0, 0, -1).Format("2006-01-02")+".md"))
		writeDoctorFileStat(b, root, today)
		writeDoctorFileStat(b, root, yesterday)
	}
}

func writeDoctorMaintainerDelegate(b *strings.Builder, maintainer *doctorMaintainerDelegate) {
	if maintainer == nil {
		writeDoctorKV(b, "maintainer_delegate_status", "absent")
		writeDoctorLine(b, "maintainer_delegate_next=\"create and activate a durable_agent from archetype aphelion-maintainer to route /doctor through the maintained child profile\"")
		return
	}
	writeDoctorKV(b, "maintainer_delegate_status", "active")
	writeDoctorKV(b, "maintainer_delegate_agent_id", strings.TrimSpace(maintainer.Agent.AgentID))
	writeDoctorKV(b, "maintainer_delegate_archetype", doctorMaintainerArchetype)
	writeDoctorKV(b, "maintainer_delegate_memory_root", strings.TrimSpace(maintainer.MemoryRoot))
	writeDoctorKV(b, "maintainer_delegate_profile_root", strings.TrimSpace(maintainer.ProfileRoot))
	writeDoctorKV(b, "maintainer_delegate_channel_kind", strings.TrimSpace(maintainer.Agent.ChannelKind))
	writeDoctorKV(b, "maintainer_delegate_outbound_mode", strings.TrimSpace(maintainer.Agent.LivePolicy.OutboundMode))
	writeDoctorKV(b, "maintainer_delegate_capabilities", strings.Join(maintainer.Agent.LivePolicy.CapabilityEnvelope, ","))
	if strings.TrimSpace(maintainer.RuntimeRules) != "" {
		writeDoctorLine(b, "Maintainer runtime boundary:")
		writeDoctorLine(b, truncatePreview(maintainer.RuntimeRules, 1200))
	}
	if strings.TrimSpace(maintainer.Charter) != "" {
		writeDoctorLine(b, "Maintainer charter:")
		writeDoctorLine(b, truncatePreview(maintainer.Charter, 700))
	}
	if strings.TrimSpace(maintainer.Capabilities) != "" {
		writeDoctorLine(b, "Maintainer archetype capabilities:")
		writeDoctorLine(b, truncatePreview(maintainer.Capabilities, 700))
	}
}

func (r *Runtime) writeDoctorIssueStatusChecks(b *strings.Builder, input doctorDiagnosticInput) {
	writeDoctorLine(b, "classification_contract: before reporting an issue as current, compare historical failure evidence with current runtime, prompt, memory, and source-state evidence.")
	writeDoctorLine(b, "allowed_statuses: active, likely_fixed, historical_resolved, residual_risk, unknown")
	writeDoctorLine(b, "reporting_rule: if evidence is old and the current-state check passes, report it as historical/resolved or residual risk, not as an active failure.")

	identityStatus, identityEvidence := doctorPromptIdentityStatus(input.PromptContext)
	writeDoctorIssueCheck(b, "prompt_identity_canonical", identityStatus, identityEvidence)

	workingRoot := strings.TrimSpace(input.Scope.WorkingRoot)
	retrySourceOK := doctorSourceContainsAll(workingRoot, "agent/turn.go", []string{"completeWithRetry", "isRetryableProviderError", "maxProviderRetries"})
	transportRetries := 0
	if r != nil && r.cfg != nil {
		transportRetries = r.cfg.Governor.Codex.TransportRetries
	}
	switch {
	case retrySourceOK && transportRetries > 0:
		writeDoctorIssueCheck(b, "codex_timeout_retries", "likely_fixed", fmt.Sprintf("codex_transport_retries=%d and provider retry loop is present in agent/turn.go", transportRetries))
	case retrySourceOK:
		writeDoctorIssueCheck(b, "codex_timeout_retries", "residual_risk", fmt.Sprintf("agent provider retry loop is present, but codex_transport_retries=%d", transportRetries))
	default:
		writeDoctorIssueCheck(b, "codex_timeout_retries", "unknown", "could not confirm retry-loop source evidence from working_root")
	}

	skillsConfigured := r != nil && r.cfg != nil && doctorPathListContains(r.cfg.Agent.DynamicFiles, "SKILLS.md")
	skillsLoaded := doctorPromptContextHasFile(input.PromptContext, "SKILLS.md")
	if skillsConfigured && skillsLoaded {
		writeDoctorIssueCheck(b, "dynamic_skills_prompt_loading", "likely_fixed", "SKILLS.md is configured as a dynamic file and is present in loaded prompt context")
	} else if skillsConfigured {
		writeDoctorIssueCheck(b, "dynamic_skills_prompt_loading", "active", "SKILLS.md is configured but was not present in loaded prompt context")
	} else if skillsLoaded {
		writeDoctorIssueCheck(b, "dynamic_skills_prompt_loading", "residual_risk", "SKILLS.md loaded, but it is not explicitly listed in configured dynamic files")
	} else {
		writeDoctorIssueCheck(b, "dynamic_skills_prompt_loading", "active", "SKILLS.md is not configured or loaded as dynamic context")
	}

	memoryConfigured := r != nil && r.cfg != nil &&
		doctorPathListContains(r.cfg.Agent.DynamicFiles, "memory/knowledge.md") &&
		doctorPathListContains(r.cfg.Agent.DynamicFiles, "memory/decisions.md")
	memoryLoaded := doctorPromptContextHasFile(input.PromptContext, "memory/knowledge.md") ||
		doctorPromptContextHasFile(input.PromptContext, "memory/decisions.md")
	recoverySourceOK := doctorSourceContainsAll(workingRoot, "main.go", []string{"StartStartupRecovery"}) &&
		doctorSourceContainsAll(workingRoot, "runtime/recovery.go", []string{"StartStartupRecovery", "PendingRecoveryTurnRuns"})
	switch {
	case memoryConfigured && memoryLoaded && recoverySourceOK:
		writeDoctorIssueCheck(b, "memory_survives_restart_and_dynamic_files_load", "likely_fixed", "structured memory files load dynamically and startup recovery source evidence is present")
	case memoryConfigured && memoryLoaded:
		writeDoctorIssueCheck(b, "memory_survives_restart_and_dynamic_files_load", "residual_risk", "structured memory files load dynamically, but startup recovery source evidence was not confirmed")
	case memoryConfigured:
		writeDoctorIssueCheck(b, "memory_survives_restart_and_dynamic_files_load", "active", "structured memory files are configured but were not present in loaded prompt context")
	default:
		writeDoctorIssueCheck(b, "memory_survives_restart_and_dynamic_files_load", "active", "structured memory files are not fully configured as dynamic context")
	}

	productionEmailMatches := doctorSourceMatches(workingRoot, []string{"runtime", "tool", "core", "session"}, []string{"email", "e-mail"}, false, 8)
	if len(productionEmailMatches) == 0 {
		writeDoctorIssueCheck(b, "feature_specific_email_subagent_code", "likely_fixed", "no production source matches for email-specific terms under runtime/tool/core/session")
	} else {
		writeDoctorIssueCheck(b, "feature_specific_email_subagent_code", "residual_risk", "production source still contains email-specific matches: "+strings.Join(productionEmailMatches, ", "))
	}

	operationalAlertSourceOK := doctorSourceContainsAll(workingRoot, "runtime/operational_alerts.go", []string{"reportOperationalIssueAsync", "sendOperationalNoticeToAdmin", "system_warning"}) &&
		doctorSourceContainsAll(workingRoot, "runtime/turn_coordinator_common.go", []string{"reportOperationalIssueAsync", "provider"})
	adminConfigured := r != nil && r.cfg != nil && len(uniquePositiveIDs(r.cfg.Principals.Telegram.AdminUserIDs)) > 0
	if operationalAlertSourceOK && adminConfigured {
		writeDoctorIssueCheck(b, "runtime_failures_surface_to_telegram", "likely_fixed", "operational alert delivery source is present and at least one admin Telegram ID is configured")
	} else if operationalAlertSourceOK {
		writeDoctorIssueCheck(b, "runtime_failures_surface_to_telegram", "residual_risk", "operational alert delivery source is present, but no admin Telegram ID is configured")
	} else {
		writeDoctorIssueCheck(b, "runtime_failures_surface_to_telegram", "unknown", "could not confirm operational alert delivery source evidence from working_root")
	}

	workspaceEscapeGateOK := doctorSourceContainsAll(workingRoot, "tool/exec.go", []string{"workspace_escape", "ConfirmExec", "ProposalStatusApproved"})
	if workspaceEscapeGateOK {
		writeDoctorIssueCheck(b, "admin_workspace_escape_requires_approval", "likely_fixed", "exec workspace escape path is gated by capability/proposal source evidence")
	} else {
		writeDoctorIssueCheck(b, "admin_workspace_escape_requires_approval", "unknown", "could not confirm workspace escape approval gate source evidence from working_root")
	}
}

func writeDoctorIssueCheck(b *strings.Builder, issue string, status string, evidence string) {
	writeDoctorLine(b, fmt.Sprintf("- issue=%s status=%s evidence=%q",
		strings.TrimSpace(issue),
		strings.TrimSpace(status),
		truncatePreview(evidence, 600),
	))
}

func writeDoctorFileStat(b *strings.Builder, root string, rel string) {
	path := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			writeDoctorLine(b, fmt.Sprintf("- file=%s missing=true", rel))
			return
		}
		writeDoctorLine(b, fmt.Sprintf("- file=%s error=%q", rel, err.Error()))
		return
	}
	if info.IsDir() {
		writeDoctorLine(b, fmt.Sprintf("- file=%s directory=true", rel))
		return
	}
	writeDoctorLine(b, fmt.Sprintf("- file=%s bytes=%d modified=%s", rel, info.Size(), info.ModTime().UTC().Format(time.RFC3339)))
}

func writeDoctorDirStat(b *strings.Builder, dir string, label string) {
	var count int
	var bytes int64
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		count++
		bytes += info.Size()
		_ = path
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			writeDoctorLine(b, fmt.Sprintf("- dir=%s missing=true", label))
			return
		}
		writeDoctorLine(b, fmt.Sprintf("- dir=%s error=%q", label, err.Error()))
		return
	}
	writeDoctorLine(b, fmt.Sprintf("- dir=%s files=%d bytes=%d", label, count, bytes))
}

func (r *Runtime) writeDoctorExecutionEvents(ctx context.Context, b *strings.Builder, key session.SessionKey, now time.Time) {
	if r == nil || r.store == nil {
		return
	}
	chatEvents, err := r.store.ExecutionEventsByChat(key.ChatID, now.Add(-24*time.Hour), 60)
	if err != nil {
		writeDoctorLine(b, "chat_events_error="+strconv.Quote(err.Error()))
	} else {
		writeDoctorLine(b, "chat_events_last_24h:")
		writeDoctorEvents(b, chatEvents, 20)
	}
	recentEvents, err := r.store.ExecutionEventsRecent(80)
	if err != nil {
		writeDoctorLine(b, "recent_events_error="+strconv.Quote(err.Error()))
	} else {
		writeDoctorLine(b, "recent_system_events:")
		writeDoctorEvents(b, recentEvents, 25)
	}
	_ = ctx
}

func writeDoctorEvents(b *strings.Builder, events []session.ExecutionEvent, limit int) {
	if len(events) == 0 {
		writeDoctorLine(b, "- none")
		return
	}
	if limit <= 0 || limit > len(events) {
		limit = len(events)
	}
	for i := 0; i < limit; i++ {
		event := events[i]
		writeDoctorLine(b, fmt.Sprintf("- time=%s chat_id=%d seq=%d type=%s stage=%s status=%s payload=%s",
			event.CreatedAt.UTC().Format(time.RFC3339),
			event.ChatID,
			event.Seq,
			strings.TrimSpace(event.EventType),
			strings.TrimSpace(event.Stage),
			strings.TrimSpace(event.Status),
			strconv.Quote(truncatePreview(event.PayloadJSON, 500)),
		))
	}
}

func (r *Runtime) writeDoctorTurnRuns(ctx context.Context, b *strings.Builder, now time.Time) {
	if r == nil || r.store == nil {
		return
	}
	latest, err := r.store.LatestTurnRunsByChat(40)
	if err != nil {
		writeDoctorLine(b, "latest_turn_runs_error="+strconv.Quote(err.Error()))
	} else {
		writeDoctorLine(b, "latest_turn_runs_by_chat:")
		writeDoctorRuns(b, latest, 20)
	}
	pending, err := r.store.PendingRecoveryTurnRuns(40)
	if err != nil {
		writeDoctorLine(b, "pending_recovery_error="+strconv.Quote(err.Error()))
	} else {
		writeDoctorLine(b, "pending_recovery_runs:")
		writeDoctorRuns(b, pending, 12)
	}
	stale, err := r.staleRunningTurnRuns(now)
	if err != nil {
		writeDoctorLine(b, "stale_turn_runs_error="+strconv.Quote(err.Error()))
	} else {
		writeDoctorLine(b, "stale_running_turns:")
		writeDoctorRuns(b, stale, 12)
	}
	_ = ctx
}

func writeDoctorRuns(b *strings.Builder, runs []session.TurnRun, limit int) {
	if len(runs) == 0 {
		writeDoctorLine(b, "- none")
		return
	}
	if limit <= 0 || limit > len(runs) {
		limit = len(runs)
	}
	for i := 0; i < limit; i++ {
		run := runs[i]
		writeDoctorLine(b, fmt.Sprintf("- id=%d chat_id=%d kind=%s status=%s started=%s last_activity=%s tools=%d/%d request=%q last_tool=%q last_error=%q",
			run.ID,
			run.ChatID,
			run.Kind,
			run.Status,
			run.StartedAt.UTC().Format(time.RFC3339),
			run.LastActivityAt.UTC().Format(time.RFC3339),
			run.ToolCallsFinished,
			run.ToolCallsStarted,
			truncatePreview(run.RequestText, 260),
			truncatePreview(run.LastToolName, 120),
			truncatePreview(run.ErrorText, 220),
		))
	}
}

func (r *Runtime) writeDoctorSemanticStats(b *strings.Builder) {
	if r == nil || r.cfg == nil {
		return
	}
	dbPath := filepath.Join(filepath.Dir(r.cfg.Sessions.DBPath), "semantic.db")
	writeDoctorKV(b, "semantic_enabled", strconv.FormatBool(r.cfg.Memory.Semantic.Enabled))
	writeDoctorKV(b, "semantic_db_path", dbPath)
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			writeDoctorLine(b, "semantic_db_missing=true")
			return
		}
		writeDoctorLine(b, "semantic_db_stat_error="+strconv.Quote(err.Error()))
		return
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		writeDoctorLine(b, "semantic_db_open_error="+strconv.Quote(err.Error()))
		return
	}
	defer db.Close()
	var docs, chunks int
	if err := db.QueryRow(`SELECT COUNT(*) FROM semantic_documents`).Scan(&docs); err != nil {
		writeDoctorLine(b, "semantic_documents_error="+strconv.Quote(err.Error()))
		return
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM semantic_chunks`).Scan(&chunks); err != nil {
		writeDoctorLine(b, "semantic_chunks_error="+strconv.Quote(err.Error()))
		return
	}
	writeDoctorKV(b, "semantic_documents", strconv.Itoa(docs))
	writeDoctorKV(b, "semantic_chunks", strconv.Itoa(chunks))
	rows, err := db.Query(`SELECT import_state, COUNT(*) FROM semantic_documents GROUP BY import_state ORDER BY import_state`)
	if err != nil {
		writeDoctorLine(b, "semantic_import_state_error="+strconv.Quote(err.Error()))
		return
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			writeDoctorLine(b, "semantic_import_state_scan_error="+strconv.Quote(err.Error()))
			return
		}
		writeDoctorLine(b, fmt.Sprintf("- import_state=%s documents=%d", state, count))
	}
}

func (r *Runtime) writeDoctorTailnetDiagnostics(ctx context.Context, b *strings.Builder) {
	if r == nil || r.cfg == nil {
		writeDoctorLine(b, "tailnet: runtime unavailable")
		return
	}
	writeDoctorKV(b, "tailscale_enabled", strconv.FormatBool(r.cfg.Tailscale.Enabled))
	writeDoctorKV(b, "tailscale_backend", strings.TrimSpace(r.cfg.Tailscale.Backend))
	writeDoctorKV(b, "tailscale_expected_tailnet", strings.TrimSpace(r.cfg.Tailscale.ExpectedTailnet))
	writeDoctorKV(b, "tailscale_expected_hostname", strings.TrimSpace(r.cfg.Tailscale.ExpectedHostname))
	writeDoctorKV(b, "tailscale_expected_tags", strings.Join(r.cfg.Tailscale.ExpectedTags, ","))
	snapshot, err := r.TailnetStatusSnapshot(ctx)
	if err != nil {
		writeDoctorLine(b, "tailnet_snapshot_error="+strconv.Quote(err.Error()))
		return
	}
	writeDoctorKV(b, "tailnet_status", snapshot.Status)
	writeDoctorKV(b, "tailnet_summary", snapshot.Summary)
	writeDoctorKV(b, "tailnet_backend_state", snapshot.BackendState)
	writeDoctorKV(b, "tailnet_node", firstNonEmpty(strings.TrimSpace(snapshot.DNSName), strings.TrimSpace(snapshot.HostName)))
	writeDoctorKV(b, "tailnet_name", snapshot.TailnetName)
	writeDoctorKV(b, "tailnet_ips", strings.Join(snapshot.TailscaleIPs, ","))
	writeDoctorKV(b, "tailnet_tags", strings.Join(snapshot.Tags, ","))
	writeDoctorKV(b, "tailnet_netcheck", snapshot.NetcheckSummary)
	if snapshot.Parent != nil {
		parent := snapshot.Parent
		writeDoctorKV(b, "tailnet_parent_enabled", strconv.FormatBool(parent.Enabled))
		writeDoctorKV(b, "tailnet_parent_running", strconv.FormatBool(parent.Running))
		writeDoctorKV(b, "tailnet_parent_hostname", parent.Hostname)
		writeDoctorKV(b, "tailnet_parent_state_dir", parent.StateDir)
		writeDoctorKV(b, "tailnet_parent_listen_addr", parent.ListenAddr)
		writeDoctorKV(b, "tailnet_parent_magic_url", parent.MagicDNSURL)
		writeDoctorKV(b, "tailnet_parent_auth_key_source", parent.AuthKeySource)
		writeDoctorKV(b, "tailnet_parent_last_error", parent.LastError)
	}
	if len(snapshot.Surfaces) == 0 {
		writeDoctorLine(b, "tailnet_surfaces: none")
	} else {
		writeDoctorLine(b, "tailnet_surfaces:")
		for _, surface := range snapshot.Surfaces {
			writeDoctorLine(b, fmt.Sprintf("- id=%s status=%s kind=%s name=%s url=%q error=%q", strings.TrimSpace(surface.SurfaceID), strings.TrimSpace(surface.Status), strings.TrimSpace(surface.SurfaceKind), strings.TrimSpace(surface.Name), truncatePreview(surface.URL, 220), truncatePreview(surface.LastError, 220)))
		}
	}
	if len(snapshot.Issues) == 0 {
		writeDoctorLine(b, "tailnet_issues: none")
		return
	}
	writeDoctorLine(b, "tailnet_issues:")
	for _, issue := range snapshot.Issues {
		writeDoctorLine(b, fmt.Sprintf("- code=%s severity=%s summary=%q", strings.TrimSpace(issue.Code), strings.TrimSpace(issue.Severity), truncatePreview(issue.Summary, 300)))
	}
}

func (r *Runtime) writeDoctorLogTail(b *strings.Builder) {
	if r == nil || r.cfg == nil {
		return
	}
	logPath := filepath.Join(filepath.Dir(r.cfg.Sessions.DBPath), "aphelion.log")
	writeDoctorKV(b, "log_path", logPath)
	data, err := readDoctorTail(logPath, doctorLogTailBytes)
	if err != nil {
		if os.IsNotExist(err) {
			writeDoctorLine(b, "log_missing=true")
			return
		}
		writeDoctorLine(b, "log_tail_error="+strconv.Quote(err.Error()))
		return
	}
	text := strings.TrimSpace(redactDoctorText(string(data)))
	if text == "" {
		writeDoctorLine(b, "log_tail_empty=true")
		return
	}
	writeDoctorLine(b, text)
}

func readDoctorTail(path string, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = doctorLogTailBytes
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	offset := int64(0)
	if size > limit {
		offset = size - limit
	}
	if _, err := file.Seek(offset, 0); err != nil {
		return nil, err
	}
	return io.ReadAll(file)
}

func doctorPathListContains(paths []string, want string) bool {
	want = filepath.ToSlash(strings.TrimSpace(want))
	if want == "" {
		return false
	}
	for _, path := range paths {
		if filepath.ToSlash(strings.TrimSpace(path)) == want {
			return true
		}
	}
	return false
}

func doctorPromptContextHasFile(ctx *workspace.PromptContext, want string) bool {
	if ctx == nil {
		return false
	}
	want = filepath.ToSlash(strings.TrimSpace(want))
	if want == "" {
		return false
	}
	for _, file := range append(append([]workspace.LoadedFile{}, ctx.Stable...), ctx.Dynamic...) {
		path := filepath.ToSlash(strings.TrimSpace(file.Path))
		if path == want || strings.HasSuffix(path, "/"+want) {
			return true
		}
	}
	return false
}

func doctorPromptIdentityStatus(ctx *workspace.PromptContext) (string, string) {
	if ctx == nil {
		return "unknown", "prompt context unavailable"
	}

	var stale []string
	var sawSystem bool
	var sawHarness bool
	for _, file := range ctx.Stable {
		path := filepath.ToSlash(strings.TrimSpace(file.Path))
		content := strings.TrimSpace(file.Content)
		if path == "" || content == "" {
			continue
		}
		lower := strings.ToLower(content)
		switch {
		case strings.Contains(lower, "aphelion is the governor"),
			strings.Contains(lower, "aphelion decides"),
			strings.Contains(lower, "final authority still belongs to aphelion"),
			strings.Contains(lower, "aphelion authorizes"):
			stale = append(stale, path)
		}
		if strings.Contains(content, "Idolum (System)") {
			sawSystem = true
		}
		if strings.Contains(lower, "aphelion") &&
			(strings.Contains(lower, "repo/service/harness") ||
				strings.Contains(lower, "repo") ||
				strings.Contains(lower, "service") ||
				strings.Contains(lower, "harness")) {
			sawHarness = true
		}
	}
	if len(stale) > 0 {
		return "active", "stable prompt files still contain stale Aphelion-governor claims: " + strings.Join(uniqueDoctorPaths(stale), ", ")
	}
	if sawSystem && sawHarness {
		return "likely_fixed", "stable prompt files identify Idolum (System) as governor/system and Aphelion as repo/service/harness"
	}
	if sawSystem {
		return "residual_risk", "stable prompt files name Idolum (System), but did not clearly bind Aphelion to repo/service/harness"
	}
	return "unknown", "canonical governor/system identity was not confirmed in loaded stable prompt files"
}

func doctorSourceContainsAll(root string, rel string, needles []string) bool {
	root = strings.TrimSpace(root)
	rel = filepath.Clean(filepath.FromSlash(strings.TrimSpace(rel)))
	if root == "" || rel == "" || rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return false
	}
	text := string(data)
	for _, needle := range needles {
		if !strings.Contains(text, needle) {
			return false
		}
	}
	return true
}

func doctorSourceMatches(root string, dirs []string, needles []string, includeTests bool, limit int) []string {
	root = strings.TrimSpace(root)
	if root == "" || limit == 0 {
		return nil
	}
	if limit < 0 {
		limit = 8
	}
	lowerNeedles := make([]string, 0, len(needles))
	for _, needle := range needles {
		needle = strings.ToLower(strings.TrimSpace(needle))
		if needle != "" {
			lowerNeedles = append(lowerNeedles, needle)
		}
	}
	if len(lowerNeedles) == 0 {
		return nil
	}

	var matches []string
	for _, dir := range dirs {
		if len(matches) >= limit {
			break
		}
		relDir := filepath.Clean(filepath.FromSlash(strings.TrimSpace(dir)))
		if relDir == "" || relDir == "." || strings.HasPrefix(relDir, "..") {
			continue
		}
		base := filepath.Join(root, relDir)
		if err := filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
			if err != nil || len(matches) >= limit {
				return nil
			}
			if entry.IsDir() {
				name := entry.Name()
				if name == ".git" || name == "vendor" || name == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			name := entry.Name()
			if !strings.HasSuffix(name, ".go") || (!includeTests && strings.HasSuffix(name, "_test.go")) {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			text := strings.ToLower(string(data))
			for _, needle := range lowerNeedles {
				if strings.Contains(text, needle) {
					if rel, relErr := filepath.Rel(root, path); relErr == nil {
						rel = filepath.ToSlash(rel)
						if rel == "runtime/doctor.go" {
							break
						}
						matches = append(matches, rel)
					} else {
						matches = append(matches, filepath.ToSlash(path))
					}
					break
				}
			}
			return nil
		}); err != nil {
			continue
		}
	}
	sort.Strings(matches)
	return matches
}

func uniqueDoctorPaths(paths []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.ToSlash(strings.TrimSpace(path))
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func writeDoctorSection(b *strings.Builder, title string) {
	writeDoctorLine(b, "")
	writeDoctorLine(b, "## "+strings.TrimSpace(title))
}

func writeDoctorKV(b *strings.Builder, key string, value string) {
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" {
		return
	}
	writeDoctorLine(b, key+"="+strconv.Quote(value))
}

func writeDoctorLine(b *strings.Builder, line string) {
	if b == nil {
		return
	}
	b.WriteString(strings.TrimRight(line, "\n"))
	b.WriteByte('\n')
}

var doctorSecretRedactions = []*regexp.Regexp{
	regexp.MustCompile(`(?i)((?:bot_token|telegram_bot_token|api_key|openai_api_key|elevenlabs_api_key|access_token|refresh_token|secret|password)\s*=\s*")[^"]*(")`),
	regexp.MustCompile(`(?i)("(?:bot_token|telegram_bot_token|api_key|openai_api_key|elevenlabs_api_key|access_token|refresh_token|secret|password)"\s*:\s*")[^"]*(")`),
	regexp.MustCompile(`(?i)(\b[A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|API_KEY)[A-Z0-9_]*=)[^\s]+`),
	regexp.MustCompile(`(?i)(authorization\s*[:=]\s*bearer\s+)[^\s,;"}]+`),
	regexp.MustCompile(`(?i)("(?:authorization)"\s*:\s*"bearer\s+)[^"]*(")`),
	regexp.MustCompile(`(?i)((?:x-api-key|api-key)\s*[:=]\s*)[^\s,;"}]+`),
}

func redactDoctorText(text string) string {
	out := text
	for _, re := range doctorSecretRedactions {
		out = re.ReplaceAllString(out, `${1}<redacted>${2}`)
	}
	return out
}
