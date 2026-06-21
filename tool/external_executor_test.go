//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func TestExternalProcessExecutorRunsManifestBackedTool(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	script := filepath.Join(registry.workspace, "run.sh")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env bash\nread INPUT\necho '{\"summary\":\"ok\",\"seen\":true}'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(run.sh) err = %v", err)
	}
	manifest := ExternalToolManifest{
		Name:      "browse_page",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
		IO: ExternalToolManifestIO{
			InputSchema:  json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string"},"seen":{"type":"boolean"}},"required":["summary"]}`),
		},
	}
	_, err := registry.WithExternalToolManifests([]ExternalToolManifest{manifest})
	if err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	seedVerifiedExternalToolLifecycle(t, registry, store, manifest, sandbox.Scope{WorkingRoot: registry.workspace})
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: "browse_page", ImplementationRef: "external:browse_page", Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	grantToolInvoke(t, store, "browse_page", "telegram:1001")

	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	key := adminSessionKey()
	ctx := authorityRunContextForPrincipal(t, store, key, actor)
	out, err := registry.ExecuteForSessionPrincipal(ctx, actor, key, "browse_page", json.RawMessage(`{"url":"https://example.com"}`))
	if err != nil {
		t.Fatalf("Execute() err = %v", err)
	}
	if out != `{"summary":"ok","seen":true}` {
		t.Fatalf("out = %q, want structured json output", out)
	}
	invocations, err := store.CapabilityInvocationsByGrant("grant:browse_page:telegram:1001", 10)
	if err != nil {
		t.Fatalf("CapabilityInvocationsByGrant() err = %v", err)
	}
	if len(invocations) != 1 {
		t.Fatalf("invocations = %#v, want one invocation row", invocations)
	}
	if invocations[0].Status != "allowed" || invocations[0].OutcomeStatus != "completed" || invocations[0].CompletedAt.IsZero() {
		t.Fatalf("invocation = %#v, want allowed decision with completed outcome", invocations[0])
	}
	grant, ok, err := store.CapabilityGrant("grant:browse_page:telegram:1001")
	if err != nil {
		t.Fatalf("CapabilityGrant() err = %v", err)
	}
	if !ok || grant.InvocationCount != 1 || grant.FailureCount != 0 {
		t.Fatalf("grant counters = %#v ok=%t, want one successful logical invocation", grant, ok)
	}
}

func TestExternalProcessOutcomeFinalizesOriginalPermitAfterAuthorityRevocation(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(registry.workspace, "run.sh"), []byte("#!/usr/bin/env bash\necho '{}'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(run.sh) err = %v", err)
	}
	executor := &blockingExternalExecutor{
		started: make(chan struct{}),
		release: make(chan struct{}),
		output:  `{"summary":"ok"}`,
	}
	registry.WithExternalToolExecutor(executor)
	manifest := ExternalToolManifest{
		Name:      "browse_page",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
		IO:        ExternalToolManifestIO{OutputSchema: json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string"}},"required":["summary"]}`)},
	}
	_, err := registry.WithExternalToolManifests([]ExternalToolManifest{manifest})
	if err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	seedVerifiedExternalToolLifecycle(t, registry, store, manifest, sandbox.Scope{WorkingRoot: registry.workspace})
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: "browse_page", ImplementationRef: "external:browse_page", Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	grantToolInvoke(t, store, "browse_page", "telegram:1001")

	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	key := adminSessionKey()
	ctx := authorityRunContextForPrincipal(t, store, key, actor)
	errCh := make(chan error, 1)
	go func() {
		_, err := registry.ExecuteForSessionPrincipal(ctx, actor, key, "browse_page", json.RawMessage(`{"url":"https://example.com"}`))
		errCh <- err
	}()
	<-executor.started
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status: session.ContinuationStatusRevoked,
		ContinuationLease: session.ContinuationLease{
			ID:             "revoked-while-external-runs",
			Status:         session.ContinuationLeaseStatusRevoked,
			RemainingTurns: 0,
			RevokedAt:      time.Now().UTC(),
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState(revoke) err = %v", err)
	}
	close(executor.release)
	if err := <-errCh; err != nil {
		t.Fatalf("ExecuteForSessionPrincipal() err = %v, want outcome finalized without reauthorization", err)
	}
	invocations, err := store.CapabilityInvocationsByGrant("grant:browse_page:telegram:1001", 10)
	if err != nil {
		t.Fatalf("CapabilityInvocationsByGrant() err = %v", err)
	}
	if len(invocations) != 1 || invocations[0].Status != "allowed" || invocations[0].OutcomeStatus != "completed" {
		t.Fatalf("invocations = %#v, want one allowed invocation finalized as completed", invocations)
	}
}

type blockingExternalExecutor struct {
	started chan struct{}
	release chan struct{}
	output  string
	err     error
}

func (e *blockingExternalExecutor) Supports(ExternalToolManifest) bool {
	return true
}

func (e *blockingExternalExecutor) Execute(ctx context.Context, manifest ExternalToolManifest, input json.RawMessage, scope sandbox.Scope, runner *sandbox.Runner, maxOutputBytes int, access ExternalToolExecutionAccess) (string, error) {
	close(e.started)
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-e.release:
		return e.output, e.err
	}
}

func TestExternalProcessExecutorUsesSandboxRunnerForApprovedUser(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	actor := principal.Principal{Role: principal.RoleApprovedUser, TelegramUserID: 42}
	scope, err := registry.sandbox.Resolve(actor)
	if err != nil {
		t.Fatalf("Resolve() err = %v", err)
	}
	if err := os.MkdirAll(scope.WorkingRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(working root) err = %v", err)
	}
	script := filepath.Join(scope.WorkingRoot, "run.sh")
	if err := os.WriteFile(script, []byte(`#!/usr/bin/env bash
if [[ "${APHELION_FAKE_BWRAP:-}" == "1" ]]; then
  echo '{"summary":"sandbox-runner"}'
else
  echo '{"summary":"direct-host"}'
fi
`), 0o755); err != nil {
		t.Fatalf("WriteFile(run.sh) err = %v", err)
	}
	manifest := ExternalToolManifest{
		Name:      "browse_page",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
		IO:        ExternalToolManifestIO{OutputSchema: json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string"}},"required":["summary"]}`)},
	}
	_, err = registry.WithExternalToolManifests([]ExternalToolManifest{manifest})
	if err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	seedVerifiedExternalToolLifecycle(t, registry, store, manifest, scope)
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: "browse_page", ImplementationRef: "external:browse_page", Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	grantToolInvoke(t, store, "browse_page", "telegram:42")

	key := adminSessionKey()
	ctx := authorityRunContextForPrincipal(t, store, key, actor)
	out, err := registry.ExecuteForSessionPrincipal(ctx, actor, key, "browse_page", json.RawMessage(`{"url":"https://example.com"}`))
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal() err = %v", err)
	}
	if out != `{"summary":"sandbox-runner"}` {
		t.Fatalf("out = %q, want sandbox runner path", out)
	}
}

func TestExternalProcessExecutorRejectsInvalidInputAgainstSchema(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	script := filepath.Join(registry.workspace, "run.sh")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env bash\necho '{\"summary\":\"ok\"}'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(run.sh) err = %v", err)
	}
	manifest := ExternalToolManifest{
		Name:      "browse_page",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
		IO:        ExternalToolManifestIO{InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`)},
	}
	_, err := registry.WithExternalToolManifests([]ExternalToolManifest{manifest})
	if err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	seedVerifiedExternalToolLifecycle(t, registry, store, manifest, sandbox.Scope{WorkingRoot: registry.workspace})
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: "browse_page", ImplementationRef: "external:browse_page", Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	grantToolInvoke(t, store, "browse_page", "telegram:1001")

	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	key := adminSessionKey()
	ctx := authorityRunContextForPrincipal(t, store, key, actor)
	_, err = registry.ExecuteForSessionPrincipal(ctx, actor, key, "browse_page", json.RawMessage(`{"goal":"summarize"}`))
	if err == nil {
		t.Fatal("Execute() err = nil, want input-schema rejection")
	}
	if !strings.Contains(err.Error(), `missing required field "url"`) {
		t.Fatalf("err = %v, want input-schema rejection", err)
	}
}

func TestExternalProcessExecutorRejectsInvalidOutputAgainstSchema(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	script := filepath.Join(registry.workspace, "run.sh")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env bash\necho '{\"summary\":7}'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(run.sh) err = %v", err)
	}
	manifest := ExternalToolManifest{
		Name:      "browse_page",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
		IO:        ExternalToolManifestIO{OutputSchema: json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string"}},"required":["summary"]}`)},
	}
	_, err := registry.WithExternalToolManifests([]ExternalToolManifest{manifest})
	if err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	seedVerifiedExternalToolLifecycle(t, registry, store, manifest, sandbox.Scope{WorkingRoot: registry.workspace})
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: "browse_page", ImplementationRef: "external:browse_page", Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	grantToolInvoke(t, store, "browse_page", "telegram:1001")

	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	key := adminSessionKey()
	ctx := authorityRunContextForPrincipal(t, store, key, actor)
	_, err = registry.ExecuteForSessionPrincipal(ctx, actor, key, "browse_page", json.RawMessage(`{"url":"https://example.com"}`))
	if err == nil {
		t.Fatal("Execute() err = nil, want output-schema rejection")
	}
	if !strings.Contains(err.Error(), "output.summary must be a string") {
		t.Fatalf("err = %v, want output-schema rejection", err)
	}
	invocations, err := store.CapabilityInvocationsByGrant("grant:browse_page:telegram:1001", 10)
	if err != nil {
		t.Fatalf("CapabilityInvocationsByGrant() err = %v", err)
	}
	if len(invocations) != 1 {
		t.Fatalf("invocations = %#v, want one invocation row", invocations)
	}
	if invocations[0].Status != "allowed" || invocations[0].OutcomeStatus != "failed" || !strings.Contains(invocations[0].OutcomeErrorText, "output.summary must be a string") {
		t.Fatalf("invocation = %#v, want allowed decision with failed execution outcome", invocations[0])
	}
}

func TestExternalToolExecutionAccessMaterializesChildRuntimeTypesDistinctly(t *testing.T) {
	tmp := t.TempDir()
	readonlyPath := filepath.Join(tmp, "readonly")
	if err := os.MkdirAll(readonlyPath, 0o755); err != nil {
		t.Fatalf("MkdirAll(readonly) err = %v", err)
	}
	readonlyFile := filepath.Join(tmp, "config.json")
	if err := os.WriteFile(readonlyFile, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("WriteFile(readonlyFile) err = %v", err)
	}
	secretFile := filepath.Join(tmp, "x.env")
	if err := os.WriteFile(secretFile, []byte("X_BEARER_TOKEN=not-asserted\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(secretFile) err = %v", err)
	}
	t.Setenv("APHELION_E2_TEST_TOKEN", "present")

	access, err := externalToolExecutionAccessFromGrant(principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "child-public-feed"}, session.CapabilityGrant{
		GrantID: "capg-runtime-material",
		Contract: `{"child_runtime":{
			"readonly_paths":["` + readonlyPath + `"],
			"readonly_binds":[{"source":"` + readonlyFile + `","target":"/app/config.json"}],
			"secret_binds":[{"source":"` + secretFile + `","target":"/home/child/.aphelion/secrets/x.env"}],
			"env_from_parent":["APHELION_E2_TEST_TOKEN"]
		}}`,
	})
	if err != nil {
		t.Fatalf("externalToolExecutionAccessFromGrant() err = %v", err)
	}
	if len(access.ExtraReadonlyPaths) != 1 || access.ExtraReadonlyPaths[0] != readonlyPath {
		t.Fatalf("ExtraReadonlyPaths = %#v, want readonly path", access.ExtraReadonlyPaths)
	}
	if len(access.ExtraReadonlyBinds) != 2 {
		t.Fatalf("ExtraReadonlyBinds = %#v, want readonly bind + secret bind", access.ExtraReadonlyBinds)
	}
	if access.ExtraReadonlyBinds[0].Source != readonlyFile || access.ExtraReadonlyBinds[0].Target != "/app/config.json" {
		t.Fatalf("readonly bind = %#v, want typed readonly bind first", access.ExtraReadonlyBinds[0])
	}
	if access.ExtraReadonlyBinds[1].Source != secretFile || access.ExtraReadonlyBinds[1].Target != "/home/child/.aphelion/secrets/x.env" {
		t.Fatalf("secret bind = %#v, want typed secret file bind", access.ExtraReadonlyBinds[1])
	}
	if got := access.ExtraEnv["APHELION_E2_TEST_TOKEN"]; got != "present" {
		t.Fatalf("ExtraEnv = %#v, want env_from_parent materialized separately", access.ExtraEnv)
	}
}

func TestExternalToolExecutionAccessReportsExactMissingRuntimeMaterial(t *testing.T) {
	missingSecret := filepath.Join(t.TempDir(), "missing.env")
	_, err := externalToolExecutionAccessFromGrant(principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "child-public-feed"}, session.CapabilityGrant{
		GrantID:  "capg-missing-secret",
		Contract: `{"child_runtime":{"secret_binds":[{"source":"` + missingSecret + `","target":"/run/secrets/x.env"}]}}`,
	})
	if err == nil || !strings.Contains(err.Error(), "capg-missing-secret") || !strings.Contains(err.Error(), "secret_bind source") || !strings.Contains(err.Error(), missingSecret) {
		t.Fatalf("missing secret err = %v, want exact secret_bind source", err)
	}

	_, err = externalToolExecutionAccessFromGrant(principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "child-public-feed"}, session.CapabilityGrant{
		GrantID:  "capg-missing-env",
		Contract: `{"child_runtime":{"env_from_parent":["APHELION_E2_MISSING_ENV"]}}`,
	})
	if err == nil || !strings.Contains(err.Error(), "capg-missing-env") || !strings.Contains(err.Error(), `env_from_parent "APHELION_E2_MISSING_ENV" is not set`) {
		t.Fatalf("missing env err = %v, want exact env_from_parent name", err)
	}
}
