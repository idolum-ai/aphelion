//go:build linux

package runtime

import (
	"context"
	"fmt"
	"hash/fnv"
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

type scheduledJobID string

type scheduledJobKind string

type scheduledDeliveryMode string

const (
	scheduledJobKindGeneric scheduledJobKind = "scheduled_job"
	scheduledJobKindCron    scheduledJobKind = "cron"

	scheduledDeliveryNone     scheduledDeliveryMode = "none"
	scheduledDeliveryAnnounce scheduledDeliveryMode = "announce"

	scheduledJobScopeKind session.ScopeKind = "scheduled_job"
)

type scheduledJob struct {
	ID       scheduledJobID
	Kind     scheduledJobKind
	Every    string
	Prompt   string
	Delivery scheduledJobDelivery
	Enabled  bool
}

type scheduledJobDelivery struct {
	Mode         scheduledDeliveryMode
	Label        string
	OutboundKind string
}

type scheduledJobExecution struct {
	Job         scheduledJob
	Key         session.SessionKey
	Session     *session.Session
	Scope       sandbox.Scope
	RequestText string
	Prepared    pipeline.TurnPrepareContract
	Exec        pipeline.TurnExecutionContract
}

type scheduledJobExecutionResult struct {
	Execution scheduledJobExecution
	Turn      *turn.Result
}

type scheduledJobDeliveryPlan struct {
	TargetChatID int64
	ReplyText    string
	FloorText    string
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
		ID:     scheduledJobID(job.ID),
		Kind:   scheduledJobKindCron,
		Every:  job.Every,
		Prompt: job.Prompt,
		Delivery: scheduledJobDelivery{
			Mode:         normalizeScheduledDeliveryMode(job.Delivery),
			Label:        job.Delivery,
			OutboundKind: "cron",
		},
		Enabled: job.Enabled,
	})
}

func normalizeScheduledJob(job scheduledJob) scheduledJob {
	job.ID = scheduledJobID(strings.TrimSpace(job.ID.String()))
	job.Kind = normalizeScheduledJobKind(job.Kind)
	job.Every = strings.TrimSpace(job.Every)
	job.Prompt = strings.TrimSpace(job.Prompt)
	job.Delivery = normalizeScheduledJobDelivery(job.Kind, job.Delivery)
	return job
}

func normalizeScheduledJobKind(kind scheduledJobKind) scheduledJobKind {
	switch scheduledJobKind(strings.TrimSpace(strings.ToLower(string(kind)))) {
	case "", scheduledJobKindGeneric:
		return scheduledJobKindGeneric
	case scheduledJobKindCron:
		return scheduledJobKindCron
	default:
		return scheduledJobKind(strings.TrimSpace(strings.ToLower(string(kind))))
	}
}

func normalizeScheduledJobDelivery(kind scheduledJobKind, delivery scheduledJobDelivery) scheduledJobDelivery {
	delivery.Label = strings.TrimSpace(delivery.Label)
	delivery.OutboundKind = strings.TrimSpace(delivery.OutboundKind)
	if delivery.Mode == "" {
		delivery.Mode = normalizeScheduledDeliveryMode(delivery.Label)
	} else {
		delivery.Mode = normalizeScheduledDeliveryMode(delivery.Mode.String())
	}
	if delivery.OutboundKind == "" {
		delivery.OutboundKind = kind.String()
	}
	return delivery
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

func (id scheduledJobID) String() string {
	return strings.TrimSpace(string(id))
}

func (kind scheduledJobKind) String() string {
	return string(normalizeScheduledJobKind(kind))
}

func (kind scheduledJobKind) requestHeading() string {
	switch normalizeScheduledJobKind(kind) {
	case scheduledJobKindCron:
		return "Cron job run"
	default:
		return "Scheduled job run"
	}
}

func (kind scheduledJobKind) turnRunKind() session.TurnRunKind {
	switch normalizeScheduledJobKind(kind) {
	case scheduledJobKindCron:
		return session.TurnRunKindCron
	default:
		return session.TurnRunKind(kind.String())
	}
}

func (kind scheduledJobKind) maintenanceSpecies() maintenanceTurnSpecies {
	switch normalizeScheduledJobKind(kind) {
	case scheduledJobKindCron:
		return maintenanceTurnCron
	default:
		return maintenanceTurnSpecies(kind.String())
	}
}

func (mode scheduledDeliveryMode) announces() bool {
	return mode == scheduledDeliveryAnnounce
}

func (mode scheduledDeliveryMode) String() string {
	if strings.TrimSpace(string(mode)) == "" {
		return string(scheduledDeliveryNone)
	}
	return strings.TrimSpace(string(mode))
}

func (delivery scheduledJobDelivery) labelForKind(kind scheduledJobKind) string {
	delivery = normalizeScheduledJobDelivery(kind, delivery)
	if normalizeScheduledJobKind(kind) == scheduledJobKindCron {
		return delivery.Label
	}
	if delivery.Label != "" {
		return delivery.Label
	}
	return delivery.Mode.String()
}

func (job scheduledJob) cadence() (time.Duration, error) {
	return time.ParseDuration(strings.TrimSpace(job.Every))
}

func (job scheduledJob) sourceLabel() string {
	return normalizeScheduledJob(job).Kind.String()
}

func scheduledJobSessionKey(job scheduledJob) session.SessionKey {
	job = normalizeScheduledJob(job)
	return session.SessionKey{ChatID: scheduledJobSessionChatID(job.Kind, job.ID), UserID: 0, Scope: scheduledJobScopeRef(job.Kind, job.ID)}
}

func scheduledJobSessionChatID(kind scheduledJobKind, id scheduledJobID) int64 {
	kind = normalizeScheduledJobKind(kind)
	if kind == scheduledJobKindCron {
		return cronSessionChatID(id.String())
	}
	h := fnvHash64(kind.String() + ":" + id.String())
	value := int64(h & 0x3fffffffffffffff)
	return -(value + 1000)
}

func scheduledJobScopeRef(kind scheduledJobKind, id scheduledJobID) session.ScopeRef {
	kind = normalizeScheduledJobKind(kind)
	if kind == scheduledJobKindCron {
		return cronScopeRef(id.String())
	}
	return session.ScopeRef{Kind: scheduledJobScopeKind, ID: id.String()}
}

func (r *Runtime) runScheduledJobOnce(ctx context.Context, job scheduledJob) error {
	job = normalizeScheduledJob(job)
	if job.ID.String() == "" {
		return fmt.Errorf("scheduled job id is required")
	}
	key := scheduledJobSessionKey(job)
	unlockJob := r.lockSession(key)
	defer unlockJob()

	execution, err := r.prepareScheduledJobExecution(job, key)
	if err != nil {
		return err
	}
	result, err := r.runScheduledJobExecution(ctx, execution)
	if err != nil {
		return err
	}
	return r.deliverScheduledJobResult(ctx, result)
}

func (r *Runtime) prepareScheduledJobExecution(job scheduledJob, key session.SessionKey) (scheduledJobExecution, error) {
	job = normalizeScheduledJob(job)
	jobSession, err := r.store.Load(key)
	if err != nil {
		return scheduledJobExecution{}, fmt.Errorf("load %s session: %w", job.sourceLabel(), err)
	}
	applySessionScope(jobSession, key)

	scope, err := r.scopeForPrincipal(principal.Principal{Role: principal.RoleAdmin})
	if err != nil {
		return scheduledJobExecution{}, fmt.Errorf("resolve %s scope: %w", job.sourceLabel(), err)
	}
	requestText := renderScheduledJobRequest(job)
	prepared := pipeline.TurnPrepareContract{
		UserText:   requestText,
		LedgerText: requestText,
	}
	return scheduledJobExecution{
		Job:         job,
		Key:         key,
		Session:     jobSession,
		Scope:       scope,
		RequestText: requestText,
		Prepared:    prepared,
		Exec:        r.executionForTurn(prepared),
	}, nil
}

func (r *Runtime) runScheduledJobExecution(ctx context.Context, execution scheduledJobExecution) (scheduledJobExecutionResult, error) {
	job := normalizeScheduledJob(execution.Job)
	runKind := job.Kind.turnRunKind()
	governorAwareness := r.governorRuntimeAwareness(execution.Scope, runKind, "system", execution.Exec)
	faceAwareness := r.governorRuntimeAwareness(execution.Scope, runKind, "telegram", execution.Exec)

	assembler := r.maintenanceAssembler
	if assembler == nil {
		assembler = newMaintenanceTurnAssembler(r)
	}
	turnResult, err := assembler.Run(ctx, maintenanceTurnAssemblyInput{
		Species:               job.Kind.maintenanceSpecies(),
		RunKind:               runKind,
		Key:                   execution.Key,
		Sess:                  execution.Session,
		Scope:                 execution.Scope,
		Prepared:              execution.Prepared,
		Exec:                  execution.Exec,
		UseMaterialFloor:      true,
		GovernorName:          r.governorName(),
		FaceName:              r.faceName(),
		Channel:               "telegram",
		PrincipalRole:         "admin",
		SessionUserName:       job.sourceLabel() + ":" + job.ID.String(),
		RenderLatestUserInput: "[" + job.sourceLabel() + ":" + job.ID.String() + "]",
		RenderDeliveryMode:    job.sourceLabel() + "_delivery",
		ScheduledJobID:        job.ID.String(),
		ScheduledJobKind:      job.Kind.String(),
		CronJobID:             job.ID.String(),
		CurrentFaceModel:      r.currentFaceRenderer(),
		BaseGovernorAwareness: governorAwareness,
		RuntimeAwareness:      faceAwareness,
		PolicyFunc: func(turn.Request) turn.Policy {
			return turn.Policy{
				Render: job.Delivery.Mode.announces(),
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
			ChatID: execution.Key.ChatID,
			Text:   execution.RequestText,
		},
		Now:         time.Now().UTC(),
		UseFacePort: true,
	})
	return scheduledJobExecutionResult{Execution: execution, Turn: turnResult}, err
}

func (r *Runtime) deliverScheduledJobResult(ctx context.Context, result scheduledJobExecutionResult) error {
	plan, ok, err := r.planScheduledJobDelivery(result)
	if err != nil || !ok {
		return err
	}

	msgID, err := r.outbound.SendMessage(ctx, core.OutboundMessage{
		ChatID: plan.TargetChatID,
		Text:   plan.ReplyText,
	})
	if err != nil {
		return fmt.Errorf("send %s outbound: %w", result.Execution.Job.sourceLabel(), err)
	}

	adminKey := session.SessionKey{ChatID: plan.TargetChatID, UserID: 0, Scope: telegramDMScopeRef(plan.TargetChatID)}
	unlockAdmin := r.lockSession(adminKey)
	defer unlockAdmin()

	adminSession, err := r.store.Load(adminKey)
	if err != nil {
		return fmt.Errorf("load %s target session: %w", result.Execution.Job.sourceLabel(), err)
	}
	applySessionScope(adminSession, adminKey)
	adminSession.ChatType = "dm"
	adminSession.SystemPrompt = result.Execution.Session.SystemPrompt
	if err := r.store.Save(adminSession, appendAssistantTurn(adminSession, plan.ReplyText, plan.FloorText, ""), core.TokenUsage{}); err != nil {
		return fmt.Errorf("save %s admin session: %w", result.Execution.Job.sourceLabel(), err)
	}
	if err := r.store.RecordOutbound(adminKey, adminSession.TurnCount, msgID, plan.OutboundKind); err != nil {
		return fmt.Errorf("record %s outbound: %w", result.Execution.Job.sourceLabel(), err)
	}

	return nil
}

func (r *Runtime) planScheduledJobDelivery(result scheduledJobExecutionResult) (scheduledJobDeliveryPlan, bool, error) {
	job := normalizeScheduledJob(result.Execution.Job)
	if result.Turn == nil || result.Turn.Turn == nil {
		return scheduledJobDeliveryPlan{}, false, fmt.Errorf("%s turn did not return a result", job.sourceLabel())
	}
	if !result.Turn.Commit.Persisted || !job.Delivery.Mode.announces() {
		return scheduledJobDeliveryPlan{}, false, nil
	}

	targetChatID := r.lastActiveAdminChat(uniquePositiveIDs(r.cfg.Principals.Telegram.AdminUserIDs))
	if targetChatID == 0 {
		targetChatID = r.cfg.Principals.Telegram.AdminUserIDs[0]
	}
	floorText := strings.TrimSpace(result.Turn.FloorText)
	replyText := strings.TrimSpace(result.Turn.VisibleReply)
	if replyText == "" {
		replyText = floorText
	}
	return scheduledJobDeliveryPlan{
		TargetChatID: targetChatID,
		ReplyText:    replyText,
		FloorText:    floorText,
		OutboundKind: job.Delivery.OutboundKind,
	}, true, nil
}

func renderScheduledJobRequest(job scheduledJob) string {
	job = normalizeScheduledJob(job)
	return strings.Join([]string{
		fmt.Sprintf("%s: %s", job.Kind.requestHeading(), job.ID.String()),
		"Delivery mode: " + job.Delivery.labelForKind(job.Kind),
		"Execute the following scheduled instruction. Return empty if there is nothing to report.",
		"",
		job.Prompt,
	}, "\n")
}

func fnvHash64(value string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(value))
	return h.Sum64()
}
