//go:build linux

package runtime

import (
	"context"
	"fmt"
	"hash/fnv"
	"log"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
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
			continue
		}
		go runPeriodic(ctx, cadence, func(runCtx context.Context) {
			if err := r.runCronJobOnce(runCtx, job); err != nil {
				logger("WARN cron job failed id=%s err=%v", job.ID, err)
			}
		})
	}
}

func (r *Runtime) runCronJobOnce(ctx context.Context, job config.CronJobConfig) error {
	key := session.SessionKey{ChatID: cronSessionChatID(job.ID), UserID: 0}
	cronSession, err := r.store.Load(key)
	if err != nil {
		return fmt.Errorf("load cron session: %w", err)
	}

	systemPrompt := prompt.BuildGovernorPrompt(prompt.GovernorRequest{
		GovernorName:    prompt.DefaultGovernorName,
		GovernorBackend: r.governorBackend,
		PrincipalRole:   "admin",
	})

	history, err := session.ToAgentHistory(cronSession.Messages)
	if err != nil {
		return fmt.Errorf("assemble cron history: %w", err)
	}

	input := make([]agent.Message, 0, len(history)+2)
	if systemPrompt != "" {
		input = append(input, agent.Message{Role: "system", Content: systemPrompt})
	}
	input = append(input, history...)
	input = append(input, agent.Message{Role: "user", Content: renderCronRequest(job)})

	result, outHistory, err := agent.RunTurn(ctx, r.provider, nil, &agent.Budget{
		Max:     r.cfg.Agent.MaxIterations,
		Caution: 0.7,
		Warning: 0.9,
	}, input)
	if err != nil {
		return fmt.Errorf("run cron turn: %w", err)
	}
	if len(outHistory) < len(input) {
		return fmt.Errorf("invalid cron output: history shrank from %d to %d", len(input), len(outHistory))
	}

	canonicalReply := strings.TrimSpace(result.Text)
	if canonicalReply == "" {
		return nil
	}

	cronSession.ChatType = "system"
	cronSession.UserName = "cron:" + job.ID
	cronSession.SystemPrompt = systemPrompt
	cronSession.TurnCount++
	cronSession.LastCanonicalReply = canonicalReply
	if err := r.store.Save(cronSession, []session.Message{{
		Role:         "assistant",
		Content:      canonicalReply,
		ContentChars: len(canonicalReply),
		TurnIndex:    cronSession.TurnCount,
	}}, result.TokenUsage); err != nil {
		return fmt.Errorf("save cron session: %w", err)
	}

	if strings.ToLower(strings.TrimSpace(job.Delivery)) != "announce" {
		return nil
	}

	targetChatID := r.lastActiveAdminChat(uniquePositiveIDs(r.cfg.Principals.Telegram.AdminUserIDs))
	if targetChatID == 0 {
		targetChatID = r.cfg.Principals.Telegram.AdminUserIDs[0]
	}

	replyText := canonicalReply
	if r.faceBackend != face.BackendGovernorPassthrough && r.faceModel != nil {
		renderedReply, renderErr := r.faceModel.Render(ctx, face.RenderRequest{
			GovernorName:    prompt.DefaultGovernorName,
			FaceName:        face.DefaultFaceName,
			Channel:         "telegram",
			PrincipalRole:   "admin",
			WorkspaceRoot:   r.cfg.Agent.Workspace,
			CanonicalReply:  canonicalReply,
			LatestUserInput: "[cron:" + job.ID + "]",
		})
		if renderErr != nil {
			log.Printf("WARN cron face render failed backend=%s job=%s err=%v; using governor_passthrough", r.faceBackend, job.ID, renderErr)
		} else if strings.TrimSpace(renderedReply) != "" {
			replyText = strings.TrimSpace(renderedReply)
		}
	}

	if _, err := r.outbound.SendMessage(ctx, core.OutboundMessage{
		ChatID: targetChatID,
		Text:   replyText,
	}); err != nil {
		return fmt.Errorf("send cron outbound: %w", err)
	}

	adminSession, err := r.store.Load(session.SessionKey{ChatID: targetChatID, UserID: 0})
	if err != nil {
		return fmt.Errorf("load cron target session: %w", err)
	}
	adminSession.ChatType = "dm"
	adminSession.SystemPrompt = systemPrompt
	adminSession.TurnCount++
	adminSession.LastCanonicalReply = canonicalReply
	if err := r.store.Save(adminSession, []session.Message{{
		Role:         "assistant",
		Content:      replyText,
		ContentChars: len(replyText),
		TurnIndex:    adminSession.TurnCount,
	}}, core.TokenUsage{}); err != nil {
		return fmt.Errorf("save cron admin session: %w", err)
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
