//go:build linux

package runtime

import (
	"context"
	"fmt"
	"hash/fnv"
	"log"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/turn"
)

func (r *Runtime) StartCronLoop(ctx context.Context, logger func(string, ...any)) {
	if r == nil || !r.cfg.Cron.Enabled || len(r.cfg.Cron.Jobs) == 0 {
		return
	}
	if logger == nil {
		logger = log.Printf
	}

	for _, job := range r.cfg.Cron.Jobs {
		job := job
		if !job.Enabled {
			continue
		}
		cadence, err := time.ParseDuration(strings.TrimSpace(job.Every))
		if err != nil || cadence <= 0 {
			logger("WARN cron job disabled due to invalid cadence id=%s every=%q err=%v", job.ID, job.Every, err)
			if err != nil {
				r.reportOperationalIssue(ctx, "cron:"+strings.TrimSpace(job.ID), fmt.Errorf("invalid cadence %q: %w", job.Every, err))
			} else {
				r.reportOperationalIssue(ctx, "cron:"+strings.TrimSpace(job.ID), fmt.Errorf("invalid cadence %q", job.Every))
			}
			continue
		}
		go runPeriodic(ctx, cadence, func(runCtx context.Context) {
			if err := r.runCronJobOnce(runCtx, job); err != nil {
				logger("WARN cron job failed id=%s err=%v", job.ID, err)
				r.reportOperationalIssue(runCtx, "cron:"+strings.TrimSpace(job.ID), err)
			}
		})
	}
}

func (r *Runtime) runCronJobOnce(ctx context.Context, job config.CronJobConfig) (err error) {
	key := session.SessionKey{ChatID: cronSessionChatID(job.ID), UserID: 0, Scope: cronScopeRef(job.ID)}
	unlockCron := r.lockSession(key)
	defer unlockCron()

	cronSession, err := r.store.Load(key)
	if err != nil {
		return fmt.Errorf("load cron session: %w", err)
	}
	applySessionScope(cronSession, key)

	scope, err := r.scopeForPrincipal(principal.Principal{Role: principal.RoleAdmin})
	if err != nil {
		return fmt.Errorf("resolve cron scope: %w", err)
	}
	requestText := renderCronRequest(job)
	prepared := pipeline.TurnPrepareContract{
		UserText:   requestText,
		LedgerText: requestText,
	}
	exec := r.executionForTurn(prepared)
	governorAwareness := r.governorRuntimeAwareness(scope, session.TurnRunKindCron, "system", exec)
	faceAwareness := r.governorRuntimeAwareness(scope, session.TurnRunKindCron, "telegram", exec)
	announce := strings.EqualFold(strings.TrimSpace(job.Delivery), "announce")

	assembler := r.maintenanceAssembler
	if assembler == nil {
		assembler = newMaintenanceTurnAssembler(r)
	}
	turnResult, err := assembler.Run(ctx, maintenanceTurnAssemblyInput{
		Species:               maintenanceTurnCron,
		RunKind:               session.TurnRunKindCron,
		Key:                   key,
		Sess:                  cronSession,
		Scope:                 scope,
		Prepared:              prepared,
		Exec:                  exec,
		UseMaterialFloor:      true,
		GovernorName:          r.governorName(),
		FaceName:              r.faceName(),
		Channel:               "telegram",
		PrincipalRole:         "admin",
		SessionUserName:       "cron:" + job.ID,
		RenderLatestUserInput: "[cron:" + job.ID + "]",
		RenderDeliveryMode:    "cron_delivery",
		CronJobID:             job.ID,
		CurrentFaceModel:      r.currentFaceRenderer(),
		BaseGovernorAwareness: governorAwareness,
		RuntimeAwareness:      faceAwareness,
		PolicyFunc: func(turn.Request) turn.Policy {
			return turn.Policy{
				Render: announce,
				Reason: "cron_delivery_policy",
			}
		},
		ErrContext: turnCommitErrorContext{
			ConvertMessages: "convert cron messages",
			LoadPlanState:   "load cron plan state before save",
			LoadOperation:   "load cron operation state before save",
			SaveSession:     "save cron session",
		},
		Inbound: core.InboundMessage{
			ChatID: key.ChatID,
			Text:   requestText,
		},
		Now:         time.Now().UTC(),
		UseFacePort: true,
	})
	if err != nil {
		return err
	}
	if turnResult == nil || turnResult.Turn == nil {
		return fmt.Errorf("cron turn did not return a result")
	}
	if !turnResult.Commit.Persisted {
		return nil
	}
	floorText := strings.TrimSpace(turnResult.FloorText)

	targetChatID := r.lastActiveAdminChat(uniquePositiveIDs(r.cfg.Principals.Telegram.AdminUserIDs))
	if targetChatID == 0 {
		targetChatID = r.cfg.Principals.Telegram.AdminUserIDs[0]
	}

	if !announce {
		return nil
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
		return fmt.Errorf("send cron outbound: %w", err)
	}

	adminKey := session.SessionKey{ChatID: targetChatID, UserID: 0, Scope: telegramDMScopeRef(targetChatID)}
	unlockAdmin := r.lockSession(adminKey)
	defer unlockAdmin()

	adminSession, err := r.store.Load(adminKey)
	if err != nil {
		return fmt.Errorf("load cron target session: %w", err)
	}
	applySessionScope(adminSession, adminKey)
	adminSession.ChatType = "dm"
	adminSession.SystemPrompt = cronSession.SystemPrompt
	if err := r.store.Save(adminSession, appendAssistantTurn(adminSession, replyText, floorText, ""), core.TokenUsage{}); err != nil {
		return fmt.Errorf("save cron admin session: %w", err)
	}
	if err := r.store.RecordOutbound(adminKey, adminSession.TurnCount, msgID, "cron"); err != nil {
		return fmt.Errorf("record cron outbound: %w", err)
	}

	return nil
}

func cronSessionChatID(id string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.TrimSpace(id)))
	value := int64(h.Sum64() & 0x3fffffffffffffff)
	return -(value + 1000)
}

func renderCronRequest(job config.CronJobConfig) string {
	return strings.Join([]string{
		fmt.Sprintf("Cron job run: %s", strings.TrimSpace(job.ID)),
		"Delivery mode: " + strings.TrimSpace(job.Delivery),
		"Execute the following scheduled instruction. Return empty if there is nothing to report.",
		"",
		strings.TrimSpace(job.Prompt),
	}, "\n")
}
