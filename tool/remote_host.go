//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tailnet"
)

const remoteHostToolName = "remote_host"

const remoteHostDefaultSandbox = "read-only"

var remoteHostActions = []string{"check", "ssh_exec", "codex_exec"}

type remoteHostContractWrapper struct {
	RemoteHost *remoteHostContract `json:"remote_host,omitempty"`
}

type remoteHostContract struct {
	Hosts            []string `json:"hosts,omitempty"`
	Users            []string `json:"users,omitempty"`
	WorkdirPrefixes  []string `json:"workdir_prefixes,omitempty"`
	AllowedSandboxes []string `json:"allowed_sandboxes,omitempty"`
	CodexHome        string   `json:"codex_home,omitempty"`
	MaxTimeoutSec    int      `json:"max_timeout_sec,omitempty"`
}

type remoteHostContractScope struct {
	Source           string
	Hosts            []string
	Users            []string
	WorkdirPrefixes  []string
	AllowedSandboxes []string
	CodexHome        string
	MaxTimeoutSec    int
}

type remoteHostGrantAccess struct {
	Grant  session.CapabilityGrant
	Ref    session.AuthorityUseRef
	Scopes []remoteHostContractScope
}

type remoteHostResult struct {
	Status   string `json:"status"`
	GrantID  string `json:"grant_id,omitempty"`
	Target   string `json:"target,omitempty"`
	Action   string `json:"action,omitempty"`
	Host     string `json:"host,omitempty"`
	User     string `json:"user,omitempty"`
	Workdir  string `json:"workdir,omitempty"`
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Blocker  string `json:"blocker,omitempty"`
}

func remoteHostToolDefinition() agent.ToolDef {
	return agent.ToolDef{
		Name:        remoteHostToolName,
		Description: "Use a grant-bound OpenSSH lane to inspect or operate on one Tailnet-reachable host. Durable agents only. Actions are check, ssh_exec, and codex_exec; all calls require an active local_device capability grant for tailnet_host:<host>.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"action": {"type": "string", "enum": ["check", "ssh_exec", "codex_exec"], "description": "Remote host action to perform."},
				"host": {"type": "string", "description": "Tailnet MagicDNS name or Tailnet IPv4 address. The grant target must be tailnet_host:<host>."},
				"user": {"type": "string", "description": "Remote SSH user allowed by the grant."},
				"workdir": {"type": "string", "description": "Absolute remote working directory for ssh_exec and codex_exec."},
				"command": {"type": "string", "description": "Remote shell command for ssh_exec. Interpreted by bash -lc on the remote host."},
				"prompt": {"type": "string", "description": "Prompt sent to remote codex exec stdin for codex_exec."},
				"port": {"type": "integer", "minimum": 1, "maximum": 65535, "description": "Optional SSH port."},
				"timeout_sec": {"type": "integer", "minimum": 1, "description": "Optional timeout; must not exceed the grant's remote_host max_timeout_sec."},
				"codex_home": {"type": "string", "description": "Optional remote CODEX_HOME. Allowed only when exactly bound by the grant."},
				"model": {"type": "string", "description": "Optional remote Codex model."},
				"sandbox": {"type": "string", "enum": ["read-only", "workspace-write"], "description": "Remote Codex sandbox mode. Defaults to read-only."}
			},
			"required": ["action", "host", "user"]
		}`),
	}
}

func (r *Registry) remoteHostAccessAllowed(p principal.Principal) (bool, error) {
	if r == nil || r.store == nil || p.Role != principal.RoleDurableAgent {
		return false, nil
	}
	for _, candidate := range remoteHostPrincipalCandidates(p) {
		grants, err := r.store.CapabilityGrants(500, session.CapabilityGrantStatusActive, session.CapabilityKindLocalDevice, candidate)
		if err != nil {
			return false, err
		}
		now := time.Now().UTC()
		for _, grant := range grants {
			grant = session.NormalizeCapabilityGrant(grant)
			if !remoteHostGrantActiveAt(grant, now) || !strings.HasPrefix(grant.TargetResource, "tailnet_host:") {
				continue
			}
			if !remoteHostGrantAllowsAnyAction(grant) {
				continue
			}
			if scopes, ok, err := remoteHostContractScopesFromGrant(grant); err == nil && ok && len(scopes) > 0 {
				return true, nil
			}
		}
	}
	return false, nil
}

func (r *Registry) remoteHost(ctx context.Context, input json.RawMessage, p principal.Principal, key session.SessionKey) (string, error) {
	if r == nil || r.store == nil {
		return remoteHostBlocker("", "", "", "", "remote_host requires transcript store"), fmt.Errorf("remote_host requires transcript store")
	}
	var in remoteHostInput
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return remoteHostBlocker("", "", "", "", "decode input: "+err.Error()), fmt.Errorf("decode remote_host input: %w", err)
		}
	}
	in.Action = normalizeRemoteHostAction(in.Action)
	in.Host = canonicalRemoteHost(in.Host)
	in.User = strings.TrimSpace(in.User)
	in.Workdir = strings.TrimSpace(in.Workdir)
	in.CodexHome = strings.TrimSpace(in.CodexHome)
	in.Model = strings.TrimSpace(in.Model)
	in.Sandbox = normalizeRemoteHostSandbox(in.Sandbox)

	access, err := r.requireRemoteHostAccess(ctx, p, key, in, input)
	if err != nil {
		grantID := ""
		if strings.TrimSpace(access.Grant.GrantID) != "" {
			grantID = access.Grant.GrantID
		}
		return remoteHostBlocker(grantID, in.Action, in.Host, in.User, err.Error()), err
	}
	if err := validateRemoteHostInputAgainstContract(access.Grant, access.Scopes, &in); err != nil {
		if _, recordErr := r.recordRemoteHostInvocation(access.Grant, p, access.Ref, in.Action, "blocked", err.Error()); recordErr != nil {
			return remoteHostBlocker(access.Grant.GrantID, in.Action, in.Host, in.User, err.Error()), errors.Join(err, recordErr)
		}
		return remoteHostBlocker(access.Grant.GrantID, in.Action, in.Host, in.User, err.Error()), err
	}
	invocation, err := r.recordRemoteHostInvocation(access.Grant, p, access.Ref, in.Action, "allowed", "")
	if err != nil {
		return remoteHostBlocker(access.Grant.GrantID, in.Action, in.Host, in.User, err.Error()), err
	}

	request, err := remoteHostOpenSSHRequest(in)
	if err != nil {
		if recordErr := r.completeRemoteHostInvocation(invocation, "not_executed", err.Error()); recordErr != nil {
			return remoteHostBlocker(access.Grant.GrantID, in.Action, in.Host, in.User, err.Error()), errors.Join(err, recordErr)
		}
		return remoteHostBlocker(access.Grant.GrantID, in.Action, in.Host, in.User, err.Error()), err
	}
	runner := r.remoteHostRunner
	if runner == nil {
		runner = tailnet.NewOpenSSHClient(tailnet.OpenSSHOptions{CommandTimeout: r.timeout})
	}
	runCtx, cancel := remoteHostContext(ctx, in.TimeoutSec, access.Scopes)
	defer cancel()
	result, runErr := runner.RunOpenSSH(runCtx, request)
	if strings.TrimSpace(result.Target) == "" {
		result.Target = in.User + "@" + in.Host
	}
	result.Output = strings.TrimSpace(result.Output)
	if runErr != nil {
		reason := remoteHostRunError(result, runErr)
		recordErr := r.completeRemoteHostInvocation(invocation, "failed", reason)
		return remoteHostRenderResult(remoteHostResult{
			Status:   "failed",
			GrantID:  access.Grant.GrantID,
			Target:   result.Target,
			Action:   in.Action,
			Host:     in.Host,
			User:     in.User,
			Workdir:  in.Workdir,
			ExitCode: result.ExitCode,
			Stdout:   truncate(result.Output, r.maxOutputBytes),
			Blocker:  reason,
		}), errors.Join(runErr, recordErr)
	}
	if err := r.completeRemoteHostInvocation(invocation, "completed", ""); err != nil {
		return remoteHostBlocker(access.Grant.GrantID, in.Action, in.Host, in.User, err.Error()), err
	}
	return remoteHostRenderResult(remoteHostResult{
		Status:   "completed",
		GrantID:  access.Grant.GrantID,
		Target:   result.Target,
		Action:   in.Action,
		Host:     in.Host,
		User:     in.User,
		Workdir:  in.Workdir,
		ExitCode: result.ExitCode,
		Stdout:   truncate(result.Output, r.maxOutputBytes),
	}), nil
}

func (r *Registry) requireRemoteHostAccess(ctx context.Context, p principal.Principal, key session.SessionKey, in remoteHostInput, raw json.RawMessage) (remoteHostGrantAccess, error) {
	access := remoteHostGrantAccess{}
	if p.Role != principal.RoleDurableAgent {
		return access, fmt.Errorf("remote_host is available only to durable agents")
	}
	if in.Action == "" || !remoteHostActionKnown(in.Action) {
		grant, ok, grantErr := r.remoteHostEvidenceGrant(p, in.Host)
		if grantErr != nil {
			return access, grantErr
		}
		if ok {
			access.Grant = grant
			ref, refErr := r.authorityUseRefForGrant(ctx, remoteHostToolName, key, p)
			if refErr != nil {
				if _, recordErr := r.recordRemoteHostInvocation(grant, p, session.AuthorityUseRef{}, firstNonEmpty(in.Action, "invoke"), "blocked", refErr.Error()); recordErr != nil {
					return access, errors.Join(refErr, recordErr)
				}
				return access, refErr
			}
			access.Ref = ref
			if _, recordErr := r.recordRemoteHostInvocation(grant, p, access.Ref, firstNonEmpty(in.Action, "invoke"), "blocked", "remote_host action must be one of check, ssh_exec, or codex_exec"); recordErr != nil {
				return access, errors.Join(fmt.Errorf("remote_host action must be one of check, ssh_exec, or codex_exec"), recordErr)
			}
		}
		return access, fmt.Errorf("remote_host action must be one of check, ssh_exec, or codex_exec")
	}
	if in.Host == "" || !tailnet.SafeSSHHost(in.Host) {
		return access, fmt.Errorf("remote_host host is required and must be a safe Tailnet host name or IPv4 address")
	}
	if in.User == "" || !tailnet.SafeSSHUser(in.User) {
		return access, fmt.Errorf("remote_host user is required and must be a safe SSH user")
	}
	grant, ok, err := r.activeRemoteHostGrant(p, in.Host, in.Action)
	if err != nil {
		return access, err
	}
	if !ok {
		if evidence, evidenceOK, evidenceErr := r.remoteHostEvidenceGrant(p, in.Host); evidenceErr != nil {
			return access, evidenceErr
		} else if evidenceOK {
			ref, refErr := r.authorityUseRefForGrant(ctx, remoteHostToolName, key, p)
			reason := fmt.Sprintf("remote_host action %q is not granted to principal %q for tailnet_host:%s", in.Action, toolAuthorityPrincipalDisplay(p), in.Host)
			if refErr != nil {
				reason = refErr.Error()
			}
			if _, recordErr := r.recordRemoteHostInvocation(evidence, p, ref, in.Action, "blocked", reason); recordErr != nil {
				return access, errors.Join(fmt.Errorf("%s", reason), recordErr)
			}
			if refErr != nil {
				return access, refErr
			}
		}
		return access, fmt.Errorf("remote_host action %q is not granted to principal %q for tailnet_host:%s", in.Action, toolAuthorityPrincipalDisplay(p), in.Host)
	}
	access.Grant = grant
	ref, refErr := r.authorityUseRefForGrant(ctx, remoteHostToolName, key, p)
	if refErr != nil {
		if _, recordErr := r.recordRemoteHostInvocation(grant, p, session.AuthorityUseRef{}, in.Action, "blocked", refErr.Error()); recordErr != nil {
			return access, errors.Join(refErr, recordErr)
		}
		return access, refErr
	}
	access.Ref = ref
	scopes, hasScope, err := remoteHostContractScopesFromGrant(grant)
	if err != nil {
		if _, recordErr := r.recordRemoteHostInvocation(grant, p, access.Ref, in.Action, "blocked", err.Error()); recordErr != nil {
			return access, errors.Join(err, recordErr)
		}
		return access, err
	}
	if !hasScope || len(scopes) == 0 {
		err := fmt.Errorf("remote_host grant %s must include a remote_host contract block", grant.GrantID)
		if _, recordErr := r.recordRemoteHostInvocation(grant, p, access.Ref, in.Action, "blocked", err.Error()); recordErr != nil {
			return access, errors.Join(err, recordErr)
		}
		return access, err
	}
	access.Scopes = scopes
	if err := validateCapabilityToolInvocationInput(grant, raw); err != nil {
		if _, recordErr := r.recordRemoteHostInvocation(grant, p, access.Ref, in.Action, "blocked", err.Error()); recordErr != nil {
			return access, errors.Join(err, recordErr)
		}
		return access, err
	}
	return access, nil
}

func (r *Registry) activeRemoteHostGrant(p principal.Principal, host string, action string) (session.CapabilityGrant, bool, error) {
	target := remoteHostTargetResource(host)
	for _, candidate := range remoteHostPrincipalCandidates(p) {
		grant, ok, err := r.store.ActiveCapabilityGrant(session.CapabilityKindLocalDevice, target, candidate, action)
		if err != nil {
			return session.CapabilityGrant{}, false, err
		}
		if ok {
			return grant, true, nil
		}
	}
	return session.CapabilityGrant{}, false, nil
}

func (r *Registry) remoteHostEvidenceGrant(p principal.Principal, host string) (session.CapabilityGrant, bool, error) {
	if host == "" || !tailnet.SafeSSHHost(host) {
		return session.CapabilityGrant{}, false, nil
	}
	for _, action := range remoteHostActions {
		if grant, ok, err := r.activeRemoteHostGrant(p, host, action); err != nil || ok {
			return grant, ok, err
		}
	}
	return session.CapabilityGrant{}, false, nil
}

func remoteHostPrincipalCandidates(p principal.Principal) []string {
	candidates := append([]string{}, toolAuthorityPrincipalKeys(p)...)
	candidates = append(candidates, toolAuthorityPrincipalDisplay(p))
	seen := make(map[string]struct{}, len(candidates))
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func remoteHostTargetResource(host string) string {
	return "tailnet_host:" + canonicalRemoteHost(host)
}

func canonicalRemoteHost(host string) string {
	return strings.Trim(strings.ToLower(strings.TrimSpace(host)), ".")
}

func normalizeRemoteHostAction(action string) string {
	return normalizeToolInvocationToken(action)
}

func remoteHostActionKnown(action string) bool {
	action = normalizeRemoteHostAction(action)
	for _, known := range remoteHostActions {
		if action == known {
			return true
		}
	}
	return false
}

func normalizeRemoteHostSandbox(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return remoteHostDefaultSandbox
	}
	return strings.ToLower(value)
}

func remoteHostSandboxKnown(value string) bool {
	switch normalizeRemoteHostSandbox(value) {
	case "read-only", "workspace-write":
		return true
	default:
		return false
	}
}

func remoteHostGrantActiveAt(grant session.CapabilityGrant, now time.Time) bool {
	if session.NormalizeCapabilityGrantStatus(grant.Status) != session.CapabilityGrantStatusActive {
		return false
	}
	if !grant.RevokedAt.IsZero() {
		return false
	}
	if !grant.ExpiresAt.IsZero() && !grant.ExpiresAt.After(now.UTC()) {
		return false
	}
	return true
}

func remoteHostGrantAllowsAnyAction(grant session.CapabilityGrant) bool {
	actions := session.NormalizeCapabilityActions(grant.AllowedActions)
	for _, grantAction := range actions {
		for _, known := range remoteHostActions {
			if grantAction == known {
				return true
			}
		}
	}
	return false
}

func validateCapabilityRemoteHostScopeJSON(contractJSON string, constraintsJSON string) error {
	_, _, err := remoteHostContractScopesFromJSON(contractJSON, constraintsJSON)
	return err
}

func remoteHostContractScopesFromGrant(grant session.CapabilityGrant) ([]remoteHostContractScope, bool, error) {
	return remoteHostContractScopesFromJSON(grant.Contract, grant.Constraints)
}

func remoteHostContractScopesFromJSON(contractJSON string, constraintsJSON string) ([]remoteHostContractScope, bool, error) {
	scopes := []remoteHostContractScope{}
	for _, source := range []struct {
		name string
		raw  string
	}{
		{name: "contract", raw: contractJSON},
		{name: "constraints", raw: constraintsJSON},
	} {
		raw := strings.TrimSpace(source.raw)
		if raw == "" || raw == "{}" {
			continue
		}
		var wrapper remoteHostContractWrapper
		if err := json.Unmarshal([]byte(raw), &wrapper); err != nil {
			return nil, false, fmt.Errorf("decode %s remote_host scope: %w", source.name, err)
		}
		if wrapper.RemoteHost == nil {
			continue
		}
		scope, err := normalizeRemoteHostContractScope(source.name, *wrapper.RemoteHost)
		if err != nil {
			return nil, false, fmt.Errorf("invalid %s remote_host scope: %w", source.name, err)
		}
		scopes = append(scopes, scope)
	}
	return scopes, len(scopes) > 0, nil
}

func normalizeRemoteHostContractScope(source string, contract remoteHostContract) (remoteHostContractScope, error) {
	scope := remoteHostContractScope{
		Source:        strings.TrimSpace(source),
		MaxTimeoutSec: contract.MaxTimeoutSec,
	}
	if scope.MaxTimeoutSec <= 0 {
		return remoteHostContractScope{}, fmt.Errorf("max_timeout_sec must be > 0")
	}
	for _, host := range contract.Hosts {
		host = canonicalRemoteHost(host)
		if host == "" || !tailnet.SafeSSHHost(host) {
			return remoteHostContractScope{}, fmt.Errorf("hosts must contain only safe Tailnet host names or IPv4 addresses")
		}
		scope.Hosts = appendUniqueString(scope.Hosts, host)
	}
	if len(scope.Hosts) == 0 {
		return remoteHostContractScope{}, fmt.Errorf("hosts must list at least one host")
	}
	for _, user := range contract.Users {
		user = strings.TrimSpace(user)
		if user == "" || !tailnet.SafeSSHUser(user) {
			return remoteHostContractScope{}, fmt.Errorf("users must contain only safe SSH users")
		}
		scope.Users = appendUniqueString(scope.Users, user)
	}
	if len(scope.Users) == 0 {
		return remoteHostContractScope{}, fmt.Errorf("users must list at least one user")
	}
	for _, prefix := range contract.WorkdirPrefixes {
		prefix, err := normalizeRemoteAbsolutePath(prefix, "workdir_prefixes")
		if err != nil {
			return remoteHostContractScope{}, err
		}
		scope.WorkdirPrefixes = appendUniqueString(scope.WorkdirPrefixes, prefix)
	}
	for _, sandbox := range contract.AllowedSandboxes {
		sandbox = normalizeRemoteHostSandbox(sandbox)
		if !remoteHostSandboxKnown(sandbox) {
			return remoteHostContractScope{}, fmt.Errorf("allowed_sandboxes must contain read-only or workspace-write")
		}
		scope.AllowedSandboxes = appendUniqueString(scope.AllowedSandboxes, sandbox)
	}
	if strings.TrimSpace(contract.CodexHome) != "" {
		codexHome, err := normalizeRemoteAbsolutePath(contract.CodexHome, "codex_home")
		if err != nil {
			return remoteHostContractScope{}, err
		}
		scope.CodexHome = codexHome
	}
	sort.Strings(scope.Hosts)
	sort.Strings(scope.Users)
	sort.Strings(scope.WorkdirPrefixes)
	sort.Strings(scope.AllowedSandboxes)
	return scope, nil
}

func validateRemoteHostInputAgainstContract(grant session.CapabilityGrant, scopes []remoteHostContractScope, in *remoteHostInput) error {
	if in == nil {
		return fmt.Errorf("remote_host input is required")
	}
	targetHost := strings.TrimPrefix(grant.TargetResource, "tailnet_host:")
	if targetHost != "" && canonicalRemoteHost(targetHost) != in.Host {
		return fmt.Errorf("remote_host grant %s is bound to host %q, not %q", grant.GrantID, targetHost, in.Host)
	}
	if in.Port < 0 || in.Port > 65535 {
		return fmt.Errorf("remote_host port must be between 1 and 65535")
	}
	if in.Port == 0 {
		in.Port = 0
	}
	if in.Action == "ssh_exec" || in.Action == "codex_exec" {
		workdir, err := normalizeRemoteAbsolutePath(in.Workdir, "workdir")
		if err != nil {
			return err
		}
		in.Workdir = workdir
	}
	if in.Action == "ssh_exec" && strings.TrimSpace(in.Command) == "" {
		return fmt.Errorf("remote_host ssh_exec requires command")
	}
	if in.Action == "codex_exec" {
		if strings.TrimSpace(in.Prompt) == "" {
			return fmt.Errorf("remote_host codex_exec requires prompt")
		}
		if !remoteHostSandboxKnown(in.Sandbox) {
			return fmt.Errorf("remote_host sandbox must be read-only or workspace-write")
		}
		if in.CodexHome != "" {
			codexHome, err := normalizeRemoteAbsolutePath(in.CodexHome, "codex_home")
			if err != nil {
				return err
			}
			in.CodexHome = codexHome
		}
		if in.Model != "" && !safeRemoteHostModel(in.Model) {
			return fmt.Errorf("remote_host model contains unsafe characters")
		}
		codexHome, err := remoteHostCodexHomeForScopes(scopes, in.CodexHome)
		if err != nil {
			return err
		}
		in.CodexHome = codexHome
	}
	for _, scope := range scopes {
		if !stringAllowed(in.Host, scope.Hosts) {
			return fmt.Errorf("remote_host host %q is not allowed by %s remote_host scope", in.Host, scope.Source)
		}
		if !stringAllowed(in.User, scope.Users) {
			return fmt.Errorf("remote_host user %q is not allowed by %s remote_host scope", in.User, scope.Source)
		}
		if in.TimeoutSec > 0 && in.TimeoutSec > scope.MaxTimeoutSec {
			return fmt.Errorf("remote_host timeout_sec %d exceeds %s remote_host max_timeout_sec %d", in.TimeoutSec, scope.Source, scope.MaxTimeoutSec)
		}
		if in.Action == "ssh_exec" || in.Action == "codex_exec" {
			if len(scope.WorkdirPrefixes) == 0 {
				return fmt.Errorf("%s remote_host scope must include workdir_prefixes for %s", scope.Source, in.Action)
			}
			if !remotePathUnderAnyPrefix(in.Workdir, scope.WorkdirPrefixes) {
				return fmt.Errorf("remote_host workdir %q is outside %s remote_host workdir_prefixes", in.Workdir, scope.Source)
			}
		}
		if in.Action == "codex_exec" {
			if len(scope.AllowedSandboxes) == 0 {
				return fmt.Errorf("%s remote_host scope must include allowed_sandboxes for codex_exec", scope.Source)
			}
			if !stringAllowed(in.Sandbox, scope.AllowedSandboxes) {
				return fmt.Errorf("remote_host sandbox %q is not allowed by %s remote_host scope", in.Sandbox, scope.Source)
			}
		}
	}
	return nil
}

func remoteHostCodexHomeForScopes(scopes []remoteHostContractScope, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	allowed := ""
	for _, scope := range scopes {
		if strings.TrimSpace(scope.CodexHome) == "" {
			continue
		}
		if allowed == "" {
			allowed = scope.CodexHome
			continue
		}
		if allowed != scope.CodexHome {
			return "", fmt.Errorf("remote_host codex_home scopes conflict")
		}
	}
	if allowed == "" {
		if requested != "" {
			return "", fmt.Errorf("remote_host codex_home is not allowed by remote_host scope")
		}
		return "", nil
	}
	if requested != "" && requested != allowed {
		return "", fmt.Errorf("remote_host codex_home %q is not allowed by remote_host scope", requested)
	}
	return allowed, nil
}

func remoteHostOpenSSHRequest(in remoteHostInput) (tailnet.OpenSSHRequest, error) {
	req := tailnet.OpenSSHRequest{
		Host: in.Host,
		User: in.User,
		Port: in.Port,
	}
	switch in.Action {
	case "check":
		req.Args = []string{"bash", "-lc", "printf 'user: '; whoami; printf '\\nhost: '; hostname; printf '\\nkernel: '; uname -a"}
	case "ssh_exec":
		req.Args = []string{"bash", "-lc", "cd " + shellQuoteArg(in.Workdir) + " && " + strings.TrimSpace(in.Command)}
	case "codex_exec":
		req.Args = []string{"bash", "-lc", remoteHostCodexCommand(in)}
		prompt := strings.TrimRight(in.Prompt, "\r\n") + "\n"
		req.Stdin = []byte(prompt)
	default:
		return tailnet.OpenSSHRequest{}, fmt.Errorf("remote_host action %q is not supported", in.Action)
	}
	return req, nil
}

func remoteHostCodexCommand(in remoteHostInput) string {
	args := []string{"codex", "exec", "--json", "--ask-for-approval", "never", "--sandbox", normalizeRemoteHostSandbox(in.Sandbox), "--cd", in.Workdir}
	if strings.TrimSpace(in.Model) != "" {
		args = append(args, "-m", strings.TrimSpace(in.Model))
	}
	args = append(args, "-")
	command := shellQuoteCommand(args)
	if strings.TrimSpace(in.CodexHome) != "" {
		command = shellQuoteCommand([]string{"env", "CODEX_HOME=" + strings.TrimSpace(in.CodexHome)}) + " " + command
	}
	return "cd " + shellQuoteArg(in.Workdir) + " && " + command
}

func remoteHostContext(ctx context.Context, inputTimeoutSec int, scopes []remoteHostContractScope) (context.Context, context.CancelFunc) {
	timeoutSec := 0
	for _, scope := range scopes {
		if scope.MaxTimeoutSec <= 0 {
			continue
		}
		if timeoutSec == 0 || scope.MaxTimeoutSec < timeoutSec {
			timeoutSec = scope.MaxTimeoutSec
		}
	}
	if inputTimeoutSec > 0 && (timeoutSec == 0 || inputTimeoutSec < timeoutSec) {
		timeoutSec = inputTimeoutSec
	}
	if timeoutSec <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
}

func remoteHostRunError(result tailnet.SSHResult, err error) string {
	raw := strings.TrimSpace(err.Error())
	evidence := strings.TrimSpace(result.Output + "\n" + raw)
	if strings.Contains(evidence, "Host key verification failed") ||
		strings.Contains(evidence, "REMOTE HOST IDENTIFICATION HAS CHANGED") ||
		strings.Contains(evidence, "known_hosts") {
		return "remote host SSH trust is not configured for " + strings.TrimSpace(result.Target) + "; configure known_hosts intentionally before retrying: " + raw
	}
	return raw
}

func (r *Registry) recordRemoteHostInvocation(grant session.CapabilityGrant, p principal.Principal, ref session.AuthorityUseRef, action string, status string, errText string) (session.CapabilityInvocation, error) {
	if r == nil || r.store == nil || strings.TrimSpace(grant.GrantID) == "" {
		return session.CapabilityInvocation{}, nil
	}
	invocation, err := r.store.RecordCapabilityInvocation(capabilityInvocationWithAuthorityUseRef(session.CapabilityInvocation{
		GrantID:   grant.GrantID,
		Principal: toolAuthorityPrincipalDisplay(p),
		Action:    firstNonEmpty(strings.TrimSpace(action), "invoke"),
		Status:    strings.TrimSpace(status),
		ErrorText: strings.TrimSpace(errText),
	}, ref))
	return invocation, err
}

func (r *Registry) completeRemoteHostInvocation(invocation session.CapabilityInvocation, status string, errText string) error {
	if r == nil || r.store == nil || invocation.InvocationID <= 0 {
		return nil
	}
	_, err := r.store.CompleteCapabilityInvocation(invocation.InvocationID, strings.TrimSpace(status), strings.TrimSpace(errText), time.Now().UTC())
	return err
}

func remoteHostRenderResult(payload remoteHostResult) string {
	raw, _ := json.MarshalIndent(payload, "", "  ")
	return string(raw)
}

func remoteHostBlocker(grantID string, action string, host string, user string, reason string) string {
	return remoteHostRenderResult(remoteHostResult{
		Status:  "blocked",
		GrantID: strings.TrimSpace(grantID),
		Action:  strings.TrimSpace(action),
		Host:    strings.TrimSpace(host),
		User:    strings.TrimSpace(user),
		Blocker: strings.TrimSpace(reason),
	})
}

func normalizeRemoteAbsolutePath(value string, field string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("remote_host %s is required", field)
	}
	if strings.ContainsAny(value, "\x00\r\n") {
		return "", fmt.Errorf("remote_host %s must not contain control characters", field)
	}
	if !strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("remote_host %s must be an absolute remote path", field)
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == "/" {
		return "", fmt.Errorf("remote_host %s must be more specific than /", field)
	}
	return cleaned, nil
}

func remotePathUnderAnyPrefix(workdir string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if workdir == prefix || strings.HasPrefix(workdir, strings.TrimRight(prefix, "/")+"/") {
			return true
		}
	}
	return false
}

func safeRemoteHostModel(value string) bool {
	if strings.TrimSpace(value) == "" || strings.HasPrefix(value, "-") {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.', r == '/', r == ':':
			continue
		default:
			return false
		}
	}
	return true
}

func appendUniqueString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
