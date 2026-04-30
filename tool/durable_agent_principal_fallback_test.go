//go:build linux

package tool

import (
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func TestDurableAgentPrincipalFallbackEnablesChildRunToolIdentity(t *testing.T) {
	registry := NewRegistryWithSandbox(t.TempDir(), time.Second, mustSandboxResolver(t)).WithDurableAgentPrincipalFallback()
	p := principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "child-alpha"}
	if !registry.SupportsPrincipal(p) {
		t.Fatal("SupportsPrincipal(durable agent) = false, want fallback support for child-run registry")
	}
	scope, err := registry.scopeForPrincipalToolExecution(p)
	if err != nil {
		t.Fatalf("scopeForPrincipalToolExecution() err = %v", err)
	}
	if scope.Principal.Role != principal.RoleDurableAgent || scope.Principal.DurableAgentID != "child-alpha" {
		t.Fatalf("scope principal = %#v, want durable child identity", scope.Principal)
	}
}

func mustSandboxResolver(t *testing.T) *sandbox.Resolver {
	t.Helper()
	resolver, err := sandbox.NewResolver(sandbox.Roots{GlobalRoot: t.TempDir(), AdminExecRoot: t.TempDir(), SharedMemoryRoot: t.TempDir(), UserWorkspaceRoot: t.TempDir(), UserMemoryRoot: t.TempDir()}, sandbox.DefaultProfiles())
	if err != nil {
		t.Fatal(err)
	}
	return resolver
}
