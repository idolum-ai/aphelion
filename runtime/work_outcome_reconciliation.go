//go:build linux

package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/commandeffect"
	"github.com/idolum-ai/aphelion/session"
)

var errWorkExecutorOutcomeUnverified = errors.New("work executor side effects require verification before retry")

const workOutcomeReconciliationCommitClockTolerance = 5 * time.Second

type workOutcomeReconciliation struct {
	Reconciled bool
	BlockRetry bool
	Reason     string
	Err        error
	Payload    map[string]any
}

type reconciledGitCommit struct {
	Hash      string
	ShortHash string
	Subject   string
	Committed time.Time
	Files     []string
}

func (r *Runtime) reconcileWorkOutcomeAfterMissingEvidence(ctx context.Context, _ session.SessionKey, req WorkRequest, result WorkResult, windowStart time.Time, windowEnd time.Time) (WorkResult, workOutcomeReconciliation) {
	if !workResultHasOutcomeSideEffectSignal(result) {
		return result, workOutcomeReconciliation{}
	}
	if req.Mode == WorkModeCommit {
		if commit, ok := reconcileLocalGitCommitOutcome(ctx, req, result, windowStart, windowEnd); ok {
			result = workResultWithReconciledGitCommit(result, commit)
			return result, workOutcomeReconciliation{
				Reconciled: true,
				Reason:     "local_git_commit_verified",
				Payload: map[string]any{
					"reason":       "local_git_commit_verified",
					"commit":       commit.ShortHash,
					"commit_hash":  commit.Hash,
					"subject":      commit.Subject,
					"files_count":  len(commit.Files),
					"side_effects": result.SideEffects,
				},
			}
		}
	}
	return result, workOutcomeReconciliation{
		BlockRetry: true,
		Reason:     "side_effects_outcome_unverified",
		Err:        errWorkExecutorOutcomeUnverified,
		Payload: map[string]any{
			"reason":         "side_effects_outcome_unverified",
			"mode":           strings.TrimSpace(string(req.Mode)),
			"commands_count": len(result.Commands),
			"side_effects":   result.SideEffects,
			"tool_successes": result.ToolSuccesses,
		},
	}
}

func workResultHasOutcomeSideEffectSignal(result WorkResult) bool {
	if result.SideEffects {
		return true
	}
	for _, command := range result.Commands {
		if commandeffect.Classify(command).SideEffects {
			return true
		}
	}
	return false
}

func reconcileLocalGitCommitOutcome(ctx context.Context, req WorkRequest, result WorkResult, windowStart time.Time, windowEnd time.Time) (reconciledGitCommit, bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	workdir := firstRuntimeWorkNonEmpty(req.Workdir, req.RepoRoot)
	if strings.TrimSpace(workdir) == "" {
		return reconciledGitCommit{}, false
	}
	if !workResultCommandsMentionGitCommit(result) && !workResultMentionsLikelyCommit(result) {
		return reconciledGitCommit{}, false
	}
	commit, err := latestGitCommit(ctx, workdir)
	if err != nil || strings.TrimSpace(commit.Hash) == "" {
		return reconciledGitCommit{}, false
	}
	if !reconciledGitCommitWithinWindow(commit, windowStart, windowEnd) {
		return reconciledGitCommit{}, false
	}
	if !workResultMentionsCommit(result, commit) {
		return reconciledGitCommit{}, false
	}
	return commit, true
}

func reconciledGitCommitWithinWindow(commit reconciledGitCommit, windowStart time.Time, windowEnd time.Time) bool {
	if commit.Committed.IsZero() || windowStart.IsZero() || windowEnd.IsZero() {
		return true
	}
	start := windowStart.UTC().Add(-workOutcomeReconciliationCommitClockTolerance)
	end := windowEnd.UTC().Add(workOutcomeReconciliationCommitClockTolerance)
	committed := commit.Committed.UTC()
	return !committed.Before(start) && !committed.After(end)
}

func latestGitCommit(ctx context.Context, workdir string) (reconciledGitCommit, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "git", "-C", workdir, "rev-parse", "--is-inside-work-tree").CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "true" {
		if err == nil {
			err = fmt.Errorf("not inside work tree")
		}
		return reconciledGitCommit{}, err
	}
	out, err := exec.CommandContext(ctx, "git", "-C", workdir, "log", "-1", "--format=%H%x00%h%x00%s%x00%cI").CombinedOutput()
	if err != nil {
		return reconciledGitCommit{}, err
	}
	parts := strings.SplitN(strings.TrimRight(string(bytes.TrimSpace(out)), "\n"), "\x00", 4)
	if len(parts) < 4 {
		return reconciledGitCommit{}, fmt.Errorf("unexpected git log format")
	}
	committed, _ := time.Parse(time.RFC3339, strings.TrimSpace(parts[3]))
	commit := reconciledGitCommit{
		Hash:      strings.TrimSpace(parts[0]),
		ShortHash: strings.TrimSpace(parts[1]),
		Subject:   strings.TrimSpace(parts[2]),
		Committed: committed,
	}
	if filesOut, err := exec.CommandContext(ctx, "git", "-C", workdir, "show", "--name-only", "--format=", "--no-renames", commit.Hash).CombinedOutput(); err == nil {
		for _, line := range strings.Split(string(filesOut), "\n") {
			if file := strings.TrimSpace(line); file != "" {
				commit.Files = appendUniqueRuntimeString(commit.Files, file)
			}
		}
	}
	return commit, nil
}

func workResultWithReconciledGitCommit(result WorkResult, commit reconciledGitCommit) WorkResult {
	if strings.TrimSpace(result.CommitLaneStatus) == "" {
		result.CommitLaneStatus = "reconciled_local_git_commit:" + commit.ShortHash
	}
	if strings.TrimSpace(result.CompletionKind) == "" {
		result.CompletionKind = "work_outcome_reconciled_local_git_commit"
	}
	for _, file := range commit.Files {
		result.ChangedFiles = appendUniqueRuntimeString(result.ChangedFiles, file)
	}
	line := strings.TrimSpace("Reconciled local git commit " + commit.ShortHash + " " + commit.Subject + ".")
	if summary := strings.TrimSpace(result.Summary); summary == "" {
		result.Summary = line
	} else if !strings.Contains(summary, commit.ShortHash) && !strings.Contains(summary, commit.Hash) {
		result.Summary = summary + "\n\n" + line
	}
	return result
}

func workResultCommandsMentionGitCommit(result WorkResult) bool {
	for _, command := range result.Commands {
		effect := commandeffect.Classify(command)
		if effect.Kind == commandeffect.KindRepoHistory && effect.GitSubcommand == "commit" {
			return true
		}
		compact := strings.ToLower(commandeffect.NormalizeCommand(command))
		if strings.Contains(compact, "git commit") {
			return true
		}
	}
	return false
}

func workResultMentionsLikelyCommit(result WorkResult) bool {
	return strings.Contains(strings.ToLower(workResultOutcomeText(result)), "commit")
}

func workResultMentionsCommit(result WorkResult, commit reconciledGitCommit) bool {
	text := strings.ToLower(workResultOutcomeText(result))
	for _, value := range []string{commit.Hash, commit.ShortHash} {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && strings.Contains(text, value) {
			return true
		}
	}
	return false
}

func workResultOutcomeText(result WorkResult) string {
	parts := []string{result.Summary, result.CommitLaneStatus, result.CompletionKind}
	parts = append(parts, result.Commands...)
	return strings.Join(parts, "\n")
}

func appendUniqueRuntimeString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if strings.TrimSpace(existing) == value {
			return values
		}
	}
	return append(values, value)
}
