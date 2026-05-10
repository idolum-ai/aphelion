//go:build linux

package stoplabels

import (
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/session"
)

func TestLabelsForDeployContextProjectsReleaseGuardrails(t *testing.T) {
	t.Parallel()

	labels := Labels(Context{LeaseClass: session.ContinuationLeaseClassDeployRestart}, []string{
		"deploy_without_handoff",
		"restart_without_recovery_artifact",
		"skip_build_or_tests_before_restart",
		"skip_post_deploy_verification",
		"unbounded_restart_loop",
		"credentials_or_tokens",
	}, Options{Limit: 10})
	joined := strings.Join(labels, ", ")
	if strings.Contains(joined, "deploy/restart") {
		t.Fatalf("labels = %#v, did not want broad deploy/restart in deploy context", labels)
	}
	for _, want := range []string{"release without handoff", "restart without recovery artifact", "skip build/tests before restart", "skip post-release verification", "unbounded restart loop", "credentials/tokens"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("labels = %#v, want %q", labels, want)
		}
	}
}

func TestLabelsForNonDeployContextKeepsBroadDeployRestart(t *testing.T) {
	t.Parallel()

	labels := Labels(Context{RiskClass: "read_only_review", AllowedActions: []string{"read_only"}}, []string{"deploy_restart_without_explicit_approval", "credentials_or_tokens"}, Options{Limit: 10})
	joined := strings.Join(labels, ", ")
	if !strings.Contains(joined, "deploy/restart") {
		t.Fatalf("labels = %#v, want broad deploy/restart outside deploy context", labels)
	}
	if !strings.Contains(joined, "credentials/tokens") {
		t.Fatalf("labels = %#v, want credentials/tokens", labels)
	}
}

func TestLabelsForDeployContextUsesReleaseDefaultWhenForbiddenActionsMissing(t *testing.T) {
	t.Parallel()

	labels := Labels(Context{LeaseClass: session.ContinuationLeaseClassDeployRestart}, nil, Options{
		Defaults: []string{"anything outside scope", "hard gates", "deploy/restart", "policy or permission changes"},
		Limit:    10,
	})
	joined := strings.Join(labels, ", ")
	if strings.Contains(joined, "deploy/restart") {
		t.Fatalf("labels = %#v, did not want broad deploy/restart fallback for deploy lease", labels)
	}
	if !strings.Contains(joined, "release outside approved scope") {
		t.Fatalf("labels = %#v, want deploy-safe release fallback", labels)
	}
}

func TestLabelsForNonDeployContextKeepsBroadDeployRestartDefault(t *testing.T) {
	t.Parallel()

	labels := Labels(Context{RiskClass: "read_only_review", AllowedActions: []string{"read_only"}}, nil, Options{
		Defaults: []string{"anything outside scope", "hard gates", "deploy/restart"},
		Limit:    10,
	})
	joined := strings.Join(labels, ", ")
	if !strings.Contains(joined, "deploy/restart") {
		t.Fatalf("labels = %#v, want broad deploy/restart fallback outside deploy lease", labels)
	}
}
