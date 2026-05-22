//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/turn"
)

type scheduledDeliveryMode string

const (
	scheduledDeliveryNone     scheduledDeliveryMode = "none"
	scheduledDeliveryAnnounce scheduledDeliveryMode = "announce"
)

type scheduledJob struct {
	ID           string
	Every        string
	Prompt       string
	Delivery     scheduledDeliveryMode
	DeliveryRaw  string
	Enabled      bool
	Source       string
	OutboundKind string
}

func scheduledJobsFromCronConfig(cfg config.CronConfig) []scheduledJob {
	if !cfg.Enabled {
		return nil
	}
	jobs := make([]scheduledJob, 0, len(cfg.Jobs))
	for _, job := range cfg.Jobs {
		adapted := scheduledJobFromCronConfig(job)
		if !adapted.Enabled {
			continue
		}
		jobs = append(jobs, adapted)
	}
	return jobs
}

func scheduledJobFromCronConfig(job config.CronJobConfig) scheduledJob {
	return normalizeScheduledJob(scheduledJob{
		ID:           job.ID,
		Every:        job.Every,
		Prompt:       job.Prompt,
		Delivery:     normalizeScheduledDeliveryMode(job.Delivery),
		DeliveryRaw:  job.Delivery,
		Enabled:      job.Enabled,
		Source:       "cron",
		OutboundKind: "cron",
	})
}

func normalizeScheduledJob(job scheduledJob) scheduledJob {
	job.ID = strings.TrimSpace(job.ID)
	job.Every = strings.TrimSpace(job.Every)
	job.Prompt = strings.TrimSpace(job.Prompt)
	job.DeliveryRaw = strings.TrimSpace(job.DeliveryRaw)
	job.Source = strings.TrimSpace(job.Source)
	job.OutboundKind = strings.TrimSpace(job.OutboundKind)
	if job.Source == "" {
		job.Source = "scheduled_job"
	}
	if job.OutboundKind == "" {
		job.OutboundKind = job.Source
	}
	if job.Delivery == "" {
		job.Delivery = normalizeScheduledDeliveryMode(job.DeliveryRaw)
	}
	return job
}

func normalizeScheduledDeliveryMode(raw string) scheduledDeliveryMode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(scheduledDeliveryNone):
		return scheduledDeliveryNone
	case string(scheduledDeliveryAnnounce):
		return scheduledDeliveryAnnounce
	default:
		return scheduledDeliveryMode(strings.ToLower(strings.TrimSpace(raw)))
	}
}

func (m scheduledDeliveryMode) announces() bool {
	return m == scheduledDeliveryAnnounce
}

func (m scheduledDeliveryMode) String() string {
	if strings.TrimSpace(string(m)) == "" {
		return string(scheduledDeliveryNone)
	}
	return strings.TrimSpace(string(m))
}

func (job scheduledJob) cadence() (time.Duration, error) {
	return time.ParseDuration(strings.TrimSpace(job.Every))
}

func (job scheduledJob) deliveryLabel() string {
	job = normalizeScheduledJob(job)
	if job.Source == "cron" {
		return strings.TrimSpace(job.DeliveryRaw)
	}
	if raw := strings.TrimSpace(job.DeliveryRaw); raw != "" {
		return raw
	}
	return job.Delivery.String()
}

func (job scheduledJob) sourceLabel() string {
	job = normalizeScheduledJob(job)
	return job.Source
}

func (job scheduledJob) requestHeading() string {
	job = normalizeScheduledJob(job)
	switch job.Source {
	case "cron":
		return "Cron job run"
	default:
		return "Scheduled job run"
	}
}

func scheduledJobSessionKey(job scheduledJob) session.SessionKey {
	job = normalizeScheduledJob(job)
	// Cron scopes are preserved as the compatibility storage surface for the
	// existing public [cron] config. The scheduled-job boundary is intentionally
	// internal so future local rituals can add their own scope shape without
	// changing current cron session identity.
	return session.SessionKey{ChatID: cronSessionChatID(job.ID), UserID: 0, Scope: cronScopeRef(job.ID)}
}

func (r *Runtime) runScheduledJobOnce(ctx context.Context, job scheduledJob) error {
	job = normalizeScheduledJob(job)
	if job.ID == "" {
		return fmt.Errorf("scheduled job id is required")
	}
	key := scheduledJobSessionKey(job)
	unlockJob := r.lockSession(key)
	defer unlockJob()

	jobSession, err := r.store.Load(key)
	if err != nil {
		return fmt.Errorf("load %s session: %w", job.sourceLabel(), err)
	}
	applySessionScope(jobSession, key)

	scope, err := r.scopeForPrincipal(principal.Principal{Role: principal.RoleAdmin})
	if err != nil {
		return fmt.Errorf("resolve %s scope: %w", job.sourceLabel(), err)
	}
	requestText := renderScheduledJobRequest(job)
	prepared := pipeline.TurnPrepareContract{
		UserText:   requestText,
		LedgerText: requestText,
	}
	exec := r.executionForTurn(prepared)

	turnResult, err := r.runScheduledJobTurn(ctx, job, key, jobSession, scope, prepared, exec)
	if err != nil {
		return err
	}
	return r.deliverScheduledJobResult(ctx, job, key, jobSession, turnResult)
}

func (r *Runtime) runScheduledJobTurn(ctx context.Context, job scheduledJob, key session.SessionKey, jobSession *session.Session, scope sandbox.Scope, prepared pipeline.TurnPrepareContract, exec pipeline.TurnExecutionContract) (*turn.Result, error) {
	job = normalizeScheduledJob(job)
	governorAwareness := r.governorRuntimeAwareness(scope, session.TurnRunKindCron, "system", exec)
	faceAwareness := r.governorRuntimeAwareness(scope, session.TurnRunKindCron, "telegram", exec)

	assembler := r.maintenanceAssembler
	if assembler == nil {
		assembler = newMaintenanceTurnAssembler(r)
	}
	return assembler.Run(ctx, maintenanceTurnAssemblyInput{
		Species:               maintenanceTurnCron,
		RunKind:               session.TurnRunKindCron,
		Key:                   key,
		Sess:                  jobSession,
		Scope:                 scope,
		Prepared:              prepared,
		Exec:                  exec,
		UseMaterialFloor:      true,
		GovernorName:          r.governorName(),
		FaceName:              r.faceName(),
		Channel:               "telegram",
		PrincipalRole:         "admin",
		SessionUserName:       job.sourceLabel() + ":" + job.ID,
		RenderLatestUserInput: "[" + job.sourceLabel() + ":" + job.ID + "]",
		RenderDeliveryMode:    job.sourceLabel() + "_delivery",
		CronJobID:             job.ID,
		CurrentFaceModel:      r.currentFaceRenderer(),
		BaseGovernorAwareness: governorAwareness,
		RuntimeAwareness:      faceAwareness,
		PolicyFunc: func(turn.Request) turn.Policy {
			return turn.Policy{
				Render: job.Delivery.announces(),
				Reason: job.sourceLabel() + "_delivery_policy",
			}
		},
		ErrContext: turnCommitErrorContext{
			ConvertMessages: "convert " + job.sourceLabel() + " messages",
			LoadPlanState:   "load " + job.sourceLabel() + " plan state before save",
			LoadOperation:   "load " + job.sourceLabel() + " operation state before save",
			SaveSession:     "save " + job.sourceLabel() + " session",
		},
		Inbound: core.InboundMessage{
			ChatID: key.ChatID,
			Text:   renderScheduledJobRequest(job),
		},
		Now:         time.Now().UTC(),
		UseFacePort: true,
	})
}

func (r *Runtime) deliverScheduledJobResult(ctx context.Context, job scheduledJob, _ session.SessionKey, jobSession *session.Session, turnResult *turn.Result) error {
	job = normalizeScheduledJob(job)
	if turnResult == nil || turnResult.Turn == nil {
		return fmt.Errorf("%s turn did not return a result", job.sourceLabel())
	}
	if !turnResult.Commit.Persisted {
		return nil
	}
	floorText := strings.TrimSpace(turnResult.FloorText)

	if !job.Delivery.announces() {
		return nil
	}

	targetChatID := r.lastActiveAdminChat(uniquePositiveIDs(r.cfg.Principals.Telegram.AdminUserIDs))
	if targetChatID == 0 {
		targetChatID = r.cfg.Principals.Telegram.AdminUserIDs[0]
	}

	replyText := strings.TrimSpace(turnResult.VisibleReply)
	if replyText == "" {
		replyText = floorText
	}

	msgID, err := r.outbound.SendMessage(ctx, core.OutboundMessage{
		ChatID: targetChatID,
		Text:   replyText,
	})
	if err != nil {
		return fmt.Errorf("send %s outbound: %w", job.sourceLabel(), err)
	}

	adminKey := session.SessionKey{ChatID: targetChatID, UserID: 0, Scope: telegramDMScopeRef(targetChatID)}
	unlockAdmin := r.lockSession(adminKey)
	defer unlockAdmin()

	adminSession, err := r.store.Load(adminKey)
	if err != nil {
		return fmt.Errorf("load %s target session: %w", job.sourceLabel(), err)
	}
	applySessionScope(adminSession, adminKey)
	adminSession.ChatType = "dm"
	adminSession.SystemPrompt = jobSession.SystemPrompt
	if err := r.store.Save(adminSession, appendAssistantTurn(adminSession, replyText, floorText, ""), core.TokenUsage{}); err != nil {
		return fmt.Errorf("save %s admin session: %w", job.sourceLabel(), err)
	}
	if err := r.store.RecordOutbound(adminKey, adminSession.TurnCount, msgID, job.OutboundKind); err != nil {
		return fmt.Errorf("record %s outbound: %w", job.sourceLabel(), err)
	}

	return nil
}

func renderScheduledJobRequest(job scheduledJob) string {
	job = normalizeScheduledJob(job)
	return strings.Join([]string{
		fmt.Sprintf("%s: %s", job.requestHeading(), job.ID),
		"Delivery mode: " + job.deliveryLabel(),
		"Execute the following scheduled instruction. Return empty if there is nothing to report.",
		"",
		job.Prompt,
	}, "\n")
}
