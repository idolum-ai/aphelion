//go:build linux

package codex

import (
	"path/filepath"
	"strings"

	"github.com/idolum-ai/aphelion/commandeffect"
)

func CommandAllowed(mode WorkMode, repoRoot string, workdir string, command string) bool {
	compact := commandeffect.NormalizeCommand(command)
	if compact == "" {
		return false
	}
	effect := commandeffect.Classify(compact)
	if mode == WorkModeReadOnly {
		return effect.ReadOnlyAllowed()
	}
	if effect.Kind == commandeffect.KindRepoHistory && effect.Reason == commandeffect.ReasonGitPush {
		return false
	}
	if effect.Kind == commandeffect.KindService && mode != WorkModeDeploy {
		return false
	}
	if effect.Kind == commandeffect.KindRepoHistory && effect.Reason == commandeffect.ReasonGitCommit && mode != WorkModeCommit && mode != WorkModeDeploy {
		return false
	}
	if effect.Kind == commandeffect.KindHighImpactStorage {
		return false
	}
	return commandWithinWorkRoot(repoRoot, workdir)
}

func ApprovalLogHasSideEffects(log []ApprovalDecision) bool {
	for _, decision := range log {
		if decision.Decision != "accept" {
			continue
		}
		if decision.Method == "item/fileChange/requestApproval" {
			return true
		}
		cmd := strings.ToLower(strings.TrimSpace(decision.Command))
		if cmd == "" {
			continue
		}
		if ApprovedCommandHasSideEffects(cmd) {
			return true
		}
	}
	return false
}

func ApprovedCommandHasSideEffects(command string) bool {
	return commandeffect.Classify(command).SideEffects
}

func commandWithinWorkRoot(root string, workdir string) bool {
	root = strings.TrimSpace(root)
	workdir = strings.TrimSpace(workdir)
	if root == "" || workdir == "" {
		return true
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(workdir))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, "../"))
}
