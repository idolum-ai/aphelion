//go:build linux

package tool

import (
	"context"
	"regexp"
	"strings"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type ExecApprover interface {
	ConfirmExec(ctx context.Context, req ExecApprovalRequest) (ExecApprovalDecision, error)
}

type ExecApprovalRequest struct {
	Principal  principal.Principal
	SessionKey session.SessionKey
	Scope      sandbox.Scope
	Command    string
	Workdir    string
	Reason     string
}

type ExecApprovalDecision struct {
	Approved bool
}

type execApprovalPattern struct {
	re          *regexp.Regexp
	description string
}

var execApprovalPatterns = []execApprovalPattern{
	{re: regexp.MustCompile(`\brm\s+-[^\n\r\s]*r`), description: "recursive delete"},
	{re: regexp.MustCompile(`\brm\s+--recursive\b`), description: "recursive delete"},
	{re: regexp.MustCompile(`\bmkfs\b`), description: "format filesystem"},
	{re: regexp.MustCompile(`\bdd\s+.*if=`), description: "disk copy"},
	{re: regexp.MustCompile(`\bdrop\s+(table|database)\b`), description: "sql drop"},
	{re: regexp.MustCompile(`\btruncate\s+(table\s+)?\w`), description: "sql truncate"},
	{re: regexp.MustCompile(`\bsystemctl\s+(stop|disable|mask)\b`), description: "stop or disable system service"},
	{re: regexp.MustCompile(`\bkill\s+-9\s+-1\b`), description: "kill all processes"},
	{re: regexp.MustCompile(`\bfind\b.*-delete\b`), description: "bulk delete via find -delete"},
	{re: regexp.MustCompile(`\b(curl|wget)\b.*\|\s*(ba)?sh\b`), description: "pipe remote content to shell"},
}

func approvalReasonForCommand(command string) string {
	lower := strings.ToLower(strings.TrimSpace(command))
	if lower == "" {
		return ""
	}
	for _, pattern := range execApprovalPatterns {
		if pattern.re.MatchString(lower) {
			return pattern.description
		}
	}
	if strings.Contains(lower, "delete from") && !strings.Contains(lower, " where ") {
		return "sql delete without where"
	}
	return ""
}
