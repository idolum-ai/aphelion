//go:build linux

package doctor

import (
	"strconv"
	"strings"

	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) writeDoctorEvidenceLedger(b *strings.Builder, key session.SessionKey) {
	if r == nil || r.store == nil || key.ChatID == 0 {
		WriteLine(b, "evidence_ledger: unavailable")
		return
	}
	stats, err := r.store.EvidenceLedgerStatsForChat(key.ChatID)
	if err != nil {
		WriteKV(b, "evidence_ledger_error", strconv.Quote(err.Error()))
		return
	}
	WriteKV(b, "evidence_objects", strconv.Itoa(stats.ObjectCount))
	WriteKV(b, "evidence_hydration_runs", strconv.Itoa(stats.HydrationRunCount))
	if stats.LatestEvidenceID != "" {
		WriteKV(b, "latest_evidence_id", stats.LatestEvidenceID)
	}
	if stats.LatestSourceKind != "" {
		WriteKV(b, "latest_evidence_source", stats.LatestSourceKind)
	}
	if !stats.LatestObservedAt.IsZero() {
		WriteKV(b, "latest_evidence_observed_at", stats.LatestObservedAt.UTC().Format(TimeFormat))
	}
	if stats.LatestHydrationID != "" {
		WriteKV(b, "latest_hydration_run", stats.LatestHydrationID)
	}
	if !stats.LatestHydratedAt.IsZero() {
		WriteKV(b, "latest_hydration_at", stats.LatestHydratedAt.UTC().Format(TimeFormat))
	}
}
