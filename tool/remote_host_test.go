//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tailnet"
)

type fakeOpenSSHRunner struct {
	requests []tailnet.OpenSSHRequest
	result   tailnet.SSHResult
	err      error
}

func (f *fakeOpenSSHRunner) RunOpenSSH(_ context.Context, req tailnet.OpenSSHRequest) (tailnet.SSHResult, error) {
	f.requests = append(f.requests, cloneOpenSSHRequest(req))
	result := f.result
	if strings.TrimSpace(result.Target) == "" {
		result.Target = req.User + "@" + req.Host
	}
	if result.Output == "" {
		result.Output = "ok"
	}
	if f.err != nil && result.ExitCode == 0 {
		result.ExitCode = 1
	}
	return result, f.err
}

func cloneOpenSSHRequest(req tailnet.OpenSSHRequest) tailnet.OpenSSHRequest {
	return tailnet.OpenSSHRequest{
		Host:  req.Host,
		User:  req.User,
		Port:  req.Port,
		Args:  append([]string(nil), req.Args...),
		Stdin: append([]byte(nil), req.Stdin...),
	}
}

func TestRemoteHostDefinitionRequiresDurableGrant(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	registry.WithRemoteHostRunner(&fakeOpenSSHRunner{})
	child := principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "child-alpha"}
	if toolDefExists(registry.DefinitionsForPrincipal(child), remoteHostToolName) {
		t.Fatal("DefinitionsForPrincipal without grant included remote_host")
	}
	grantRemoteHost(t, store, remoteHostGrantOptions{
		Principal: "durable_agent:child-alpha",
		Actions:   []string{"ssh_exec", "codex_exec"},
	})
	if !toolDefExists(registry.DefinitionsForPrincipal(child), remoteHostToolName) {
		t.Fatal("DefinitionsForPrincipal with grant missing remote_host")
	}
	admin := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	if toolDefExists(registry.DefinitionsForPrincipal(admin), remoteHostToolName) {
		t.Fatal("DefinitionsForPrincipal exposed remote_host to admin")
	}
}

func TestRemoteHostDeniesWithoutActiveGrant(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)
	registry.WithRemoteHostRunner(&fakeOpenSSHRunner{})
	child := principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "child-alpha"}
	_, err := registry.ExecuteForSessionPrincipal(context.Background(), child, adminSessionKey(), remoteHostToolName, json.RawMessage(`{"action":"ssh_exec","host":"mac-mini","user":"daniel","workdir":"/Users/daniel/Code/aphelion","command":"pwd"}`))
	if err == nil || !strings.Contains(err.Error(), "not granted") {
		t.Fatalf("remote_host without grant err = %v, want not granted", err)
	}
}

func TestRemoteHostCheckRunsHarmlessCommand(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	runner := &fakeOpenSSHRunner{}
	registry.WithRemoteHostRunner(runner)
	grantRemoteHost(t, store, remoteHostGrantOptions{
		Principal: "durable_agent:child-alpha",
		Actions:   []string{"check"},
	})
	child := principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "child-alpha"}
	ctx := authorityRunContextForPrincipal(t, store, adminSessionKey(), child)
	out, err := registry.ExecuteForSessionPrincipal(ctx, child, adminSessionKey(), remoteHostToolName, json.RawMessage(`{"action":"check","host":"mac-mini","user":"daniel","timeout_sec":30}`))
	if err != nil {
		t.Fatalf("remote_host check err = %v output=%s", err, out)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("runner requests = %d, want 1", len(runner.requests))
	}
	wantArgs := []string{"bash", "-lc", "printf 'user: '; whoami; printf '\\nhost: '; hostname; printf '\\nkernel: '; uname -a"}
	if !reflect.DeepEqual(runner.requests[0].Args, wantArgs) {
		t.Fatalf("check args = %#v, want %#v", runner.requests[0].Args, wantArgs)
	}
	if !strings.Contains(out, `"status": "completed"`) || !strings.Contains(out, `"action": "check"`) {
		t.Fatalf("check output = %s, want completed action", out)
	}
}

func TestRemoteHostSSHExecUsesOpenSSHAndRecordsEvidence(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	runner := &fakeOpenSSHRunner{result: tailnet.SSHResult{Output: " M file.go\n"}}
	registry.WithRemoteHostRunner(runner)
	grant := grantRemoteHost(t, store, remoteHostGrantOptions{
		Principal: "durable_agent:child-alpha",
		Actions:   []string{"ssh_exec", "codex_exec"},
	})
	child := principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "child-alpha"}
	ctx := authorityRunContextForPrincipal(t, store, adminSessionKey(), child)
	out, err := registry.ExecuteForSessionPrincipal(ctx, child, adminSessionKey(), remoteHostToolName, json.RawMessage(`{"action":"ssh_exec","host":"mac-mini","user":"daniel","workdir":"/Users/daniel/Code/aphelion","command":"git status --short","port":2222,"timeout_sec":60}`))
	if err != nil {
		t.Fatalf("remote_host ssh_exec err = %v output=%s", err, out)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("runner requests = %d, want 1", len(runner.requests))
	}
	req := runner.requests[0]
	if req.Host != "mac-mini" || req.User != "daniel" || req.Port != 2222 {
		t.Fatalf("request target = %#v, want mac-mini daniel port 2222", req)
	}
	wantArgs := []string{"bash", "-lc", "cd '/Users/daniel/Code/aphelion' && git status --short"}
	if !reflect.DeepEqual(req.Args, wantArgs) {
		t.Fatalf("ssh_exec args = %#v, want %#v", req.Args, wantArgs)
	}
	if !strings.Contains(out, `"stdout": "M file.go"`) {
		t.Fatalf("output = %s, want trimmed stdout", out)
	}
	invocations := capabilityInvocationsForGrant(t, store, grant.GrantID, 4)
	if len(invocations) != 1 || invocations[0].Status != "allowed" || invocations[0].OutcomeStatus != "completed" {
		t.Fatalf("invocations = %#v, want one allowed invocation with completed outcome", invocations)
	}
	if invocations[0].AuthoritySource != "continuation_lease" || invocations[0].SessionID == "" || invocations[0].TurnRunID <= 0 {
		t.Fatalf("invocation authority refs = %#v, want run-authority evidence", invocations[0])
	}
}

func TestRemoteHostCodexExecBuildsRemoteCodexCommand(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	runner := &fakeOpenSSHRunner{}
	registry.WithRemoteHostRunner(runner)
	grantRemoteHost(t, store, remoteHostGrantOptions{
		Principal: "durable_agent:child-alpha",
		Actions:   []string{"ssh_exec", "codex_exec"},
	})
	child := principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "child-alpha"}
	ctx := authorityRunContextForPrincipal(t, store, adminSessionKey(), child)
	out, err := registry.ExecuteForSessionPrincipal(ctx, child, adminSessionKey(), remoteHostToolName, json.RawMessage(`{"action":"codex_exec","host":"mac-mini","user":"daniel","workdir":"/Users/daniel/Code/project","prompt":"review the repo","sandbox":"workspace-write","codex_home":"/Users/daniel/.codex","model":"gpt-5.2"}`))
	if err != nil {
		t.Fatalf("remote_host codex_exec err = %v output=%s", err, out)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("runner requests = %d, want 1", len(runner.requests))
	}
	wantArgs := []string{"bash", "-lc", "cd '/Users/daniel/Code/project' && 'env' 'CODEX_HOME=/Users/daniel/.codex' 'codex' 'exec' '--json' '--ask-for-approval' 'never' '--sandbox' 'workspace-write' '--cd' '/Users/daniel/Code/project' '-m' 'gpt-5.2' '-'"}
	if !reflect.DeepEqual(runner.requests[0].Args, wantArgs) {
		t.Fatalf("codex_exec args = %#v, want %#v", runner.requests[0].Args, wantArgs)
	}
	if string(runner.requests[0].Stdin) != "review the repo\n" {
		t.Fatalf("codex_exec stdin = %q, want prompt newline", string(runner.requests[0].Stdin))
	}
}

func TestRemoteHostDeniesContractViolationsAndRecordsBlocked(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	runner := &fakeOpenSSHRunner{}
	registry.WithRemoteHostRunner(runner)
	grant := grantRemoteHost(t, store, remoteHostGrantOptions{
		Principal: "durable_agent:child-alpha",
		Actions:   []string{"ssh_exec", "codex_exec"},
	})
	child := principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "child-alpha"}
	ctx := authorityRunContextForPrincipal(t, store, adminSessionKey(), child)
	_, err := registry.ExecuteForSessionPrincipal(ctx, child, adminSessionKey(), remoteHostToolName, json.RawMessage(`{"action":"ssh_exec","host":"mac-mini","user":"root","workdir":"/Users/daniel/Code/aphelion","command":"pwd"}`))
	if err == nil || !strings.Contains(err.Error(), "user") {
		t.Fatalf("wrong user err = %v, want user block", err)
	}
	_, err = registry.ExecuteForSessionPrincipal(ctx, child, adminSessionKey(), remoteHostToolName, json.RawMessage(`{"action":"ssh_exec","host":"mac-mini","user":"daniel","workdir":"/Users/daniel/Secrets","command":"pwd"}`))
	if err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("wrong workdir err = %v, want outside block", err)
	}
	_, err = registry.ExecuteForSessionPrincipal(ctx, child, adminSessionKey(), remoteHostToolName, json.RawMessage(`{"action":"codex_exec","host":"mac-mini","user":"daniel","workdir":"/Users/daniel/Code/aphelion","prompt":"write","sandbox":"danger-full-access"}`))
	if err == nil || !strings.Contains(err.Error(), "sandbox") {
		t.Fatalf("wrong sandbox err = %v, want sandbox block", err)
	}
	if len(runner.requests) != 0 {
		t.Fatalf("runner requests = %d, want no remote execution", len(runner.requests))
	}
	invocations := capabilityInvocationsForGrant(t, store, grant.GrantID, 10)
	blocked := 0
	for _, invocation := range invocations {
		if invocation.Status == "blocked" {
			blocked++
		}
	}
	if blocked != 3 {
		t.Fatalf("invocations = %#v, want 3 blocked rows", invocations)
	}
}

func TestRemoteHostToolInvocationScopeConstrainsSelectors(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	runner := &fakeOpenSSHRunner{}
	registry.WithRemoteHostRunner(runner)
	grant := grantRemoteHost(t, store, remoteHostGrantOptions{
		Principal: "durable_agent:child-alpha",
		Actions:   []string{"codex_exec"},
		Constraints: `{
			"tool_invocation": {
				"actions": {
					"codex_exec": {
						"selectors": {
							"host": ["mac-mini"],
							"user": ["daniel"],
							"workdir": ["/Users/daniel/Code/aphelion"]
						},
						"allowed_fields": ["prompt", "sandbox", "codex_home", "timeout_sec", "model"]
					}
				}
			}
		}`,
	})
	child := principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "child-alpha"}
	ctx := authorityRunContextForPrincipal(t, store, adminSessionKey(), child)
	_, err := registry.ExecuteForSessionPrincipal(ctx, child, adminSessionKey(), remoteHostToolName, json.RawMessage(`{"action":"codex_exec","host":"mac-mini","user":"daniel","workdir":"/Users/daniel/Code/other","prompt":"review","sandbox":"read-only","codex_home":"/Users/daniel/.codex"}`))
	if err == nil || !strings.Contains(err.Error(), "selector") {
		t.Fatalf("wrong tool_invocation selector err = %v, want selector block", err)
	}
	out, err := registry.ExecuteForSessionPrincipal(ctx, child, adminSessionKey(), remoteHostToolName, json.RawMessage(`{"action":"codex_exec","host":"mac-mini","user":"daniel","workdir":"/Users/daniel/Code/aphelion","prompt":"review","sandbox":"read-only","codex_home":"/Users/daniel/.codex"}`))
	if err != nil {
		t.Fatalf("allowed selector err = %v output=%s", err, out)
	}
	invocations := capabilityInvocationsForGrant(t, store, grant.GrantID, 10)
	if len(invocations) != 2 || invocations[0].Status != "allowed" || invocations[0].OutcomeStatus != "completed" || invocations[1].Status != "blocked" {
		t.Fatalf("invocations = %#v, want allowed/completed then blocked", invocations)
	}
}

func TestRemoteHostGrantLifecycleFailsClosed(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		status session.CapabilityGrantStatus
		expiry time.Time
	}{
		{name: "expired", status: session.CapabilityGrantStatusActive, expiry: time.Now().Add(-time.Minute)},
		{name: "revoked", status: session.CapabilityGrantStatusRevoked},
		{name: "stale", status: session.CapabilityGrantStatusStale},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			registry, store := newDurableAgentToolRegistry(t)
			registry.WithRemoteHostRunner(&fakeOpenSSHRunner{})
			grantRemoteHost(t, store, remoteHostGrantOptions{
				Principal: "durable_agent:child-alpha",
				Actions:   []string{"ssh_exec"},
				Status:    tc.status,
				ExpiresAt: tc.expiry,
			})
			child := principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "child-alpha"}
			_, err := registry.ExecuteForSessionPrincipal(context.Background(), child, adminSessionKey(), remoteHostToolName, json.RawMessage(`{"action":"ssh_exec","host":"mac-mini","user":"daniel","workdir":"/Users/daniel/Code/aphelion","command":"pwd"}`))
			if err == nil || !strings.Contains(err.Error(), "not granted") {
				t.Fatalf("remote_host with %s grant err = %v, want not granted", tc.name, err)
			}
		})
	}
}

func TestRemoteHostRecordsFailedInvocation(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	runner := &fakeOpenSSHRunner{
		result: tailnet.SSHResult{Output: "Host key verification failed.", ExitCode: 255},
		err:    errors.New("exit status 255"),
	}
	registry.WithRemoteHostRunner(runner)
	grant := grantRemoteHost(t, store, remoteHostGrantOptions{
		Principal: "durable_agent:child-alpha",
		Actions:   []string{"ssh_exec"},
	})
	child := principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "child-alpha"}
	ctx := authorityRunContextForPrincipal(t, store, adminSessionKey(), child)
	out, err := registry.ExecuteForSessionPrincipal(ctx, child, adminSessionKey(), remoteHostToolName, json.RawMessage(`{"action":"ssh_exec","host":"mac-mini","user":"daniel","workdir":"/Users/daniel/Code/aphelion","command":"pwd"}`))
	if err == nil {
		t.Fatal("remote_host failed runner err = nil, want error")
	}
	if !strings.Contains(out, "SSH trust is not configured") {
		t.Fatalf("failed output = %s, want host trust setup blocker", out)
	}
	invocations := capabilityInvocationsForGrant(t, store, grant.GrantID, 4)
	if len(invocations) != 1 || invocations[0].Status != "allowed" || invocations[0].OutcomeStatus != "failed" {
		t.Fatalf("invocations = %#v, want one allowed invocation with failed outcome", invocations)
	}
}

type remoteHostGrantOptions struct {
	Principal   string
	Actions     []string
	Contract    string
	Constraints string
	Status      session.CapabilityGrantStatus
	ExpiresAt   time.Time
}

func grantRemoteHost(t *testing.T, store *session.SQLiteStore, opts remoteHostGrantOptions) session.CapabilityGrant {
	t.Helper()

	principalID := strings.TrimSpace(opts.Principal)
	if principalID == "" {
		principalID = "durable_agent:child-alpha"
	}
	actions := opts.Actions
	if len(actions) == 0 {
		actions = []string{"ssh_exec", "codex_exec"}
	}
	contract := strings.TrimSpace(opts.Contract)
	if contract == "" {
		contract = `{
			"remote_host": {
				"hosts": ["mac-mini"],
				"users": ["daniel"],
				"workdir_prefixes": ["/Users/daniel/Code"],
				"allowed_sandboxes": ["read-only", "workspace-write"],
				"codex_home": "/Users/daniel/.codex",
				"max_timeout_sec": 900
			}
		}`
	}
	constraints := strings.TrimSpace(opts.Constraints)
	if constraints == "" {
		constraints = "{}"
	}
	status := opts.Status
	if status == "" {
		status = session.CapabilityGrantStatusActive
	}
	grant, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "grant:remote-host:" + principalID + ":" + strings.Join(actions, "-"),
		GrantedBy:      "test",
		GrantedTo:      principalID,
		Kind:           session.CapabilityKindLocalDevice,
		TargetResource: "tailnet_host:mac-mini",
		AllowedActions: actions,
		Contract:       compactJSONForTest(t, contract),
		Constraints:    compactJSONForTest(t, constraints),
		Status:         status,
		ExpiresAt:      opts.ExpiresAt,
	})
	if err != nil {
		t.Fatalf("UpsertCapabilityGrant(remote_host) err = %v", err)
	}
	return grant
}

func compactJSONForTest(t *testing.T, raw string) string {
	t.Helper()

	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatalf("decode json fixture: %v", err)
	}
	out, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("compact json fixture: %v", err)
	}
	return string(out)
}

func capabilityInvocationsForGrant(t *testing.T, store *session.SQLiteStore, grantID string, limit int) []session.CapabilityInvocation {
	t.Helper()

	invocations, err := store.CapabilityInvocationsByGrant(grantID, limit)
	if err != nil {
		t.Fatalf("CapabilityInvocationsByGrant(%s) err = %v", grantID, err)
	}
	return invocations
}
