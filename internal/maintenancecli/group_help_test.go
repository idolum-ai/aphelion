//go:build linux

package maintenancecli

import (
	"strings"
	"testing"
)

func TestNestedCommandGroupsRenderHelp(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
		want []string
	}{
		{name: "github-app", run: func() error { return runGitHubAppCommand([]string{"--help"}) }, want: []string{"Usage: aphelion github-app <subcommand>", "status", "token"}},
		{name: "sandbox-net", run: func() error { return runSandboxNetCommand([]string{"--help"}) }, want: []string{"Usage: aphelion sandbox-net <subcommand>", "check", "helper"}},
		{name: "sandbox-net helper", run: func() error { return runSandboxNetCommand([]string{"helper", "--help"}) }, want: []string{"Usage: aphelion sandbox-net helper <subcommand>", "serve"}},
		{name: "tailnet", run: func() error { return runTailnetCommand([]string{"--help"}) }, want: []string{"Usage: aphelion tailnet <subcommand>", "status", "revoke"}},
		{name: "durable-agent", run: func() error { return RunDurableAgentCommand([]string{"--help"}, DurableAgentDeps{}) }, want: []string{"Usage: aphelion durable-agent <subcommand>", "list", "reconcile"}},
		{name: "authority", run: func() error { return RunAuthorityCommand([]string{"--help"}, AuthorityDeps{}) }, want: []string{"Usage: aphelion authority <subcommand>", "doctor", "revoke-continuation"}},
		{name: "schema", run: func() error { return RunSchemaMaintenanceCommand([]string{"--help"}) }, want: []string{"Usage: aphelion schema <subcommand>", "verify"}},
		{name: "telegram-threads", run: func() error { return runTelegramThreadsMaintenanceCommand([]string{"--help"}) }, want: []string{"Usage: aphelion telegram-threads <subcommand>", "sanitize"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := captureStdout(t, tt.run)
			if err != nil {
				t.Fatalf("%s --help err = %v", tt.name, err)
			}
			for _, want := range tt.want {
				if !strings.Contains(out, want) {
					t.Fatalf("%s help output = %q, want %q", tt.name, out, want)
				}
			}
		})
	}
}
