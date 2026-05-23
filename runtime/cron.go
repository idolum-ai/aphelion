//go:build linux

package runtime

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/idolum-ai/aphelion/config"
)

func (r *Runtime) StartCronLoop(ctx context.Context, logger func(string, ...any)) {
	if r == nil {
		return
	}
	jobs := scheduledJobsFromCronConfig(r.cfg.Cron)
	if len(jobs) == 0 {
		return
	}
	if logger == nil {
		logger = log.Printf
	}

	for _, job := range jobs {
		job := job
		cadence, err := job.cadence()
		if err != nil || cadence <= 0 {
			logger("WARN cron job disabled due to invalid cadence id=%s every=%q err=%v", job.ID, job.Every, err)
			if err != nil {
				r.reportOperationalIssue(ctx, "cron:"+job.ID.String(), fmt.Errorf("invalid cadence %q: %w", job.Every, err))
			} else {
				r.reportOperationalIssue(ctx, "cron:"+job.ID.String(), fmt.Errorf("invalid cadence %q", job.Every))
			}
			continue
		}
		go runPeriodic(ctx, cadence, func(runCtx context.Context) {
			if err := r.runScheduledJobOnce(runCtx, job); err != nil {
				logger("WARN cron job failed id=%s err=%v", job.ID, err)
				r.reportOperationalIssue(runCtx, "cron:"+job.ID.String(), err)
			}
		})
	}
}

func (r *Runtime) runCronJobOnce(ctx context.Context, job config.CronJobConfig) error {
	return r.runScheduledJobOnce(ctx, scheduledJobFromCronConfig(job))
}

func cronSessionChatID(id string) int64 {
	value := int64(fnvHash64(strings.TrimSpace(id)) & 0x3fffffffffffffff)
	return -(value + 1000)
}

func renderCronRequest(job config.CronJobConfig) string {
	return renderScheduledJobRequest(scheduledJobFromCronConfig(job))
}
