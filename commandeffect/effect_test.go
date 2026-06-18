package commandeffect

import "testing"

func TestBoundaryForCommandClassifiesBoundaryCommands(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		command string
		kind    BoundaryKind
		reason  string
	}{
		{name: "git_push", command: "git push origin main", kind: BoundaryGitPush, reason: ReasonGitPush},
		{name: "git_push_with_global_option", command: "git -C repo push --force origin branch", kind: BoundaryGitPush, reason: ReasonGitPush},
		{name: "git_commit", command: "git commit -m test", kind: BoundaryGitCommit, reason: ReasonGitCommit},
		{name: "gh", command: "gh pr create --fill", kind: BoundaryExternalAccount, reason: ReasonExternalAccount},
		{name: "aws", command: "aws sts get-caller-identity", kind: BoundaryExternalAccount, reason: ReasonExternalAccount},
		{name: "op_token", command: "op item get production-token", kind: BoundaryExternalAccount, reason: ReasonExternalAccount},
		{name: "ssh", command: "ssh host.example uptime", kind: BoundaryRemoteHostOperation, reason: ReasonRemoteHostOperation},
		{name: "rsync", command: "rsync -av . host.example:/tmp/work", kind: BoundaryRemoteHostOperation, reason: ReasonRemoteHostOperation},
		{name: "systemctl_restart", command: "systemctl --user restart aphelion.service", kind: BoundaryServiceProcessChange, reason: ReasonServiceProcessChange},
		{name: "docker", command: "docker ps", kind: BoundaryServiceProcessChange, reason: ReasonServiceProcessChange},
		{name: "kubectl", command: "kubectl get pods", kind: BoundaryServiceProcessChange, reason: ReasonServiceProcessChange},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := BoundaryForCommand(tc.command)
			if !ok {
				t.Fatalf("BoundaryForCommand(%q) ok = false, want true", tc.command)
			}
			if got.Kind != tc.kind || got.Effect.Reason != tc.reason {
				t.Fatalf("BoundaryForCommand(%q) = kind=%q reason=%q, want %q/%q", tc.command, got.Kind, got.Effect.Reason, tc.kind, tc.reason)
			}
		})
	}
}

func TestClassifyCompoundScriptPrefersStrongEffectOverRedirection(t *testing.T) {
	t.Parallel()

	command := `set -euo pipefail
git commit -m "Add XPVENTA reconstruction packet artifacts" >/tmp/imexx_commit.out
cat /tmp/imexx_commit.out
printf '\nCOMMIT\n'; git rev-parse --short HEAD
printf '\nSTATUS_AFTER\n'; git status --short`
	effect := Classify(command)
	if effect.Kind != KindRepoHistory || effect.Reason != ReasonGitCommit {
		t.Fatalf("Classify(compound commit script) = %#v, want git commit repo-history effect", effect)
	}
}

func TestClassifyRedirectionRemainsFallbackSideEffect(t *testing.T) {
	t.Parallel()

	effect := Classify("cat README.md > out.txt")
	if effect.Kind != KindBuildArtifact || effect.Reason != "shell redirection" || !effect.SideEffects {
		t.Fatalf("Classify(read-only redirection) = %#v, want build artifact side-effect fallback", effect)
	}
}

func TestClassifyUnknownSegmentStaysConservativeAgainstLowRiskLaterSegments(t *testing.T) {
	t.Parallel()

	effect := Classify("custom-wrapper --maybe-mutates; go test ./...")
	if effect.Kind != KindUnknown || !effect.SideEffects {
		t.Fatalf("Classify(unknown then validation) = %#v, want conservative unknown side effect", effect)
	}
}

func TestBoundaryForCommandIgnoresQuotedAndSearchText(t *testing.T) {
	t.Parallel()

	for _, command := range []string{
		`rg -n "git push|gh pr merge|systemctl restart|kubectl delete" .`,
		`grep -R "rm -rf build" .`,
		`git grep "drop table users"`,
		`printf '%s\n' 'git push origin main'`,
		`systemctl --user status aphelion.service`,
	} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()

			if got, ok := BoundaryForCommand(command); ok {
				t.Fatalf("BoundaryForCommand(%q) = %#v, want no boundary effect", command, got)
			}
		})
	}
}

func TestClassifyReadOnlyAndSideEffects(t *testing.T) {
	t.Parallel()

	for _, command := range []string{
		"git status --short",
		"rg doctor runtime",
		"sed -n '1,40p' runtime/codex_work_lane.go",
		"hostname",
		"go env GOPATH",
		"systemctl --user status aphelion.service",
	} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()

			if effect := Classify(command); !effect.ReadOnlyAllowed() {
				t.Fatalf("Classify(%q) = %#v, want read-only", command, effect)
			}
		})
	}
	for _, command := range []string{
		"go test ./runtime",
		"go build ./...",
		"npm test",
		"git fetch origin",
		"git commit -am fix",
		"curl https://example.com",
		"mkdir out",
		"cat README.md > out.txt",
		"sqlite3 state.db 'drop table runs'",
		"systemctl --user restart aphelion",
		"unknown-tool --flag",
	} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()

			if effect := Classify(command); !effect.SideEffects {
				t.Fatalf("Classify(%q) = %#v, want side effects", command, effect)
			}
		})
	}
}
