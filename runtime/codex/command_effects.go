//go:build linux

package codex

import (
	"strings"

	"github.com/idolum-ai/aphelion/commandeffect"
	"github.com/idolum-ai/aphelion/effectauth"
)

func CommandAllowed(mode WorkMode, repoRoot string, workdir string, command string) bool {
	return effectauth.AuthorizeWorkModeCommand(effectauth.WorkModeRequest{
		Mode:     effectauth.WorkMode(mode),
		RepoRoot: repoRoot,
		Workdir:  workdir,
		Command:  command,
	}).Allowed
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
