//go:build linux

package runtime

import (
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestGogCLIReadinessSelectsReadableGrantWhenArchiveGrantIsNewer(t *testing.T) {
	principalID := core.DurableAgentPrincipal("idolum-email")
	grants := []session.CapabilityGrant{
		{
			GrantID:        "grant-idolum-email-gog-cli-archive-approved-threads",
			Kind:           session.CapabilityKindTool,
			TargetResource: gogCLIAdapterName,
			GrantedTo:      principalID,
			AllowedActions: []string{"invoke_archive_approved_threads", "dry_run", "audit_log"},
			Status:         session.CapabilityGrantStatusActive,
		},
		{
			GrantID:        "grant-idolum-email-archive-approved-threads",
			Kind:           session.CapabilityKindExternalAccount,
			TargetResource: "gog_cli:host@idolum.ai:gmail-thread-archive-approved-batches",
			GrantedTo:      principalID,
			AllowedActions: []string{"prepare_archive_candidates", "dry_run_archive_batch", "archive_parent_approved_thread_ids", "audit_log"},
			Status:         session.CapabilityGrantStatusActive,
		},
		{
			GrantID:        "grant-idolum-email-gog-cli-tool-readonly",
			Kind:           session.CapabilityKindTool,
			TargetResource: gogCLIAdapterName,
			GrantedTo:      principalID,
			AllowedActions: []string{"invoke", "read", "search", "metadata", "connection_test"},
			Status:         session.CapabilityGrantStatusActive,
			Contract:       `{"child_runtime":{"readonly_binds":[{"source":"/tmp/runtime-bin","target":"/usr/local/bin"}],"env_from_parent":["GOG_KEYRING_PASSWORD"]}}`,
		},
		{
			GrantID:        "grant-idolum-email-gog-cli-host-readonly",
			Kind:           session.CapabilityKindExternalAccount,
			TargetResource: "gog_cli:host@idolum.ai",
			GrantedTo:      principalID,
			AllowedActions: []string{"read", "search", "metadata", "connection_test"},
			Status:         session.CapabilityGrantStatusActive,
			Contract:       `{"child_runtime":{"readonly_paths":["/tmp/gogcli-config"]}}`,
		},
	}

	toolGrant, _, toolOK, toolEvidence := selectGogCLIToolGrant(grants, principalID)
	if !toolOK || toolGrant.GrantID != "grant-idolum-email-gog-cli-tool-readonly" {
		t.Fatalf("tool grant = %s ok=%v evidence=%q, want read-only grant", toolGrant.GrantID, toolOK, toolEvidence)
	}
	if strings.Contains(toolEvidence, "archive") {
		t.Fatalf("tool evidence = %q, want selected read-only runtime grant", toolEvidence)
	}

	accountGrant, _, accountOK, accountEvidence := selectGogCLIAccountGrant(grants, principalID)
	if !accountOK || accountGrant.GrantID != "grant-idolum-email-gog-cli-host-readonly" {
		t.Fatalf("account grant = %s ok=%v evidence=%q, want read-only grant", accountGrant.GrantID, accountOK, accountEvidence)
	}
	if strings.Contains(accountEvidence, "archive") {
		t.Fatalf("account evidence = %q, want selected read-only account grant", accountEvidence)
	}
}
