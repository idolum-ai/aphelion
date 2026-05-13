//go:build linux

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/session"
)

type reviewRedactionRepairResult struct {
	Inspected     int
	Repaired      int
	StillRedacted int
	Skipped       int
	Errors        int
}

type reviewRedactionArtifactMetadata struct {
	AgentID       string            `json:"agent_id,omitempty"`
	Summary       string            `json:"summary,omitempty"`
	IntervalLabel string            `json:"interval_label,omitempty"`
	LocalActions  []string          `json:"local_actions,omitempty"`
	Questions     []string          `json:"questions,omitempty"`
	RiskFlags     []string          `json:"risk_flags,omitempty"`
	ArtifactRefs  []string          `json:"artifact_refs,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

func runRepairReviewRedactionsCommand(args []string) error {
	fs := flag.NewFlagSet("repair-review-redactions", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	limitFlag := fs.Int("limit", 100, "maximum redacted review events to inspect")
	dryRunFlag := fs.Bool("dry-run", false, "inspect and report without updating review_events")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, configPath, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	result, err := repairReviewRedactions(context.Background(), store, *limitFlag, *dryRunFlag, time.Now().UTC())
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "action: repair-review-redactions\n")
	fmt.Fprintf(os.Stdout, "config_path: %s\n", configPath)
	fmt.Fprintf(os.Stdout, "dry_run: %t\n", *dryRunFlag)
	fmt.Fprintf(os.Stdout, "inspected: %d\n", result.Inspected)
	fmt.Fprintf(os.Stdout, "repaired: %d\n", result.Repaired)
	fmt.Fprintf(os.Stdout, "still_redacted: %d\n", result.StillRedacted)
	fmt.Fprintf(os.Stdout, "skipped: %d\n", result.Skipped)
	fmt.Fprintf(os.Stdout, "errors: %d\n", result.Errors)
	return nil
}

func repairReviewRedactions(ctx context.Context, store *session.SQLiteStore, limit int, dryRun bool, now time.Time) (reviewRedactionRepairResult, error) {
	result := reviewRedactionRepairResult{}
	if store == nil {
		return result, fmt.Errorf("repair review redactions requires session store")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	events, err := store.ReviewEventsWithRedactedSummary(limit)
	if err != nil {
		return result, err
	}
	for _, event := range events {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}
		result.Inspected++
		repaired, stillRedacted, err := repairReviewRedactionEvent(store, event, dryRun, now.UTC())
		if err != nil {
			result.Errors++
			continue
		}
		if repaired {
			result.Repaired++
			continue
		}
		if stillRedacted {
			result.StillRedacted++
			continue
		}
		result.Skipped++
	}
	return result, nil
}

func repairReviewRedactionEvent(store *session.SQLiteStore, event session.ReviewEvent, dryRun bool, now time.Time) (repaired bool, stillRedacted bool, err error) {
	var meta reviewRedactionArtifactMetadata
	if strings.TrimSpace(event.MetadataJSON) == "" {
		return false, false, nil
	}
	if err := json.Unmarshal([]byte(event.MetadataJSON), &meta); err != nil {
		return false, false, err
	}
	if strings.TrimSpace(meta.Summary) != "[REDACTED: summary]" {
		return false, false, nil
	}
	if meta.Metadata == nil {
		return false, false, nil
	}
	ref := strings.TrimSpace(meta.Metadata["forensic_ref"])
	if ref == "" {
		return false, false, nil
	}
	agentID := strings.TrimSpace(firstNonEmpty(meta.AgentID, event.SourceScope.DurableAgentID, event.SourceScope.ID))
	if agentID == "" {
		return false, false, nil
	}
	agent, err := store.DurableAgent(agentID)
	if err != nil {
		return false, false, err
	}
	record, err := durableagent.ReadForensicRecord(*agent, ref)
	if err != nil {
		return false, false, err
	}
	rawSummary := strings.TrimSpace(record.Payload["summary"])
	if rawSummary == "" {
		return false, false, nil
	}
	if durableagent.ContainsConcreteSecretValue(rawSummary) {
		return false, true, nil
	}

	meta.Summary = normalizeMaintenanceWhitespace(rawSummary)
	meta.Metadata["redaction_source"] = "maintenance_repair"
	meta.Metadata["redaction_reason"] = "summary_repaired_secret_concept_without_value"
	meta.Metadata["summary_redaction_repaired_at"] = now.UTC().Format(time.RFC3339Nano)
	meta.Metadata["redacted_fields"] = removeCSVValue(meta.Metadata["redacted_fields"], "summary")
	if strings.TrimSpace(meta.Metadata["redacted_fields"]) == "" {
		delete(meta.Metadata, "redacted_fields")
		meta.Metadata["redaction_action"] = "none"
	}
	if strings.TrimSpace(meta.Metadata["operator_summary"]) == "[REDACTED: summary]" {
		delete(meta.Metadata, "operator_summary")
	}
	if strings.TrimSpace(meta.Metadata["safe_operator_summary"]) == "[REDACTED: summary]" {
		delete(meta.Metadata, "safe_operator_summary")
	}

	artifact := core.DurableReviewArtifact{
		AgentID:       meta.AgentID,
		Summary:       meta.Summary,
		IntervalLabel: meta.IntervalLabel,
		LocalActions:  meta.LocalActions,
		Questions:     meta.Questions,
		RiskFlags:     meta.RiskFlags,
		ArtifactRefs:  meta.ArtifactRefs,
		Metadata:      meta.Metadata,
	}
	nextSummary := durableagent.BuildReviewSummary(*agent, artifact, session.DefaultReviewSummaryMaxChars)
	nextMeta, err := json.Marshal(meta)
	if err != nil {
		return false, false, err
	}
	if !dryRun {
		if err := store.UpdateReviewEventProjection(event.ID, nextSummary, string(nextMeta)); err != nil {
			return false, false, err
		}
	}
	return true, false, nil
}

func removeCSVValue(raw string, remove string) string {
	remove = strings.TrimSpace(remove)
	if remove == "" {
		return strings.TrimSpace(raw)
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || part == remove {
			continue
		}
		out = append(out, part)
	}
	return strings.Join(out, ",")
}

func normalizeMaintenanceWhitespace(raw string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
}
