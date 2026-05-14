//go:build linux

package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type Stage string

const (
	StageTrustedHost   Stage = "trusted_host"
	StageIsolatedBwrap Stage = "isolated_bwrap"
	StageUnavailable   Stage = "unavailable"
)

type ExecRequest struct {
	Scope              Scope
	Command            string
	Workdir            string
	Stdin              []byte
	ExtraWritablePaths []string
	ExtraReadonlyPaths []string
	ExtraReadonlyBinds []BindPath
	ExtraEnv           map[string]string
}

type BindPath struct {
	Source string
	Target string
}

type ExecResult struct {
	Stage   Stage
	Stdout  string
	Stderr  string
	Network *NetworkExecutionEvidence
}

type ExecutionPlan struct {
	Stage   Stage
	Binary  string
	Args    []string
	Dir     string
	Env     []string
	Network *NetworkExecutionEvidence
}

type Runner struct {
	lookPath        func(string) (string, error)
	networkBackend  NetworkBackend
	networkResolver NetworkResolver

	once      sync.Once
	bwrapPath string
}

func NewRunner() *Runner {
	return NewRunnerWithLookPath(exec.LookPath)
}

func NewRunnerWithLookPath(lookPath func(string) (string, error)) *Runner {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	return &Runner{lookPath: lookPath}
}

func (r *Runner) WithNetworkBackend(backend NetworkBackend) *Runner {
	if r == nil {
		return r
	}
	r.networkBackend = backend
	return r
}

func (r *Runner) WithNetworkResolver(resolver NetworkResolver) *Runner {
	if r == nil {
		return r
	}
	r.networkResolver = resolver
	return r
}

func (r *Runner) NetworkBackendStatus(ctx context.Context) NetworkBackendStatus {
	return r.networkBackendOrDefault().Status(ctx)
}

func (r *Runner) Supports(scope Scope) bool {
	return r.Stage(scope) != StageUnavailable
}

func (r *Runner) Stage(scope Scope) Stage {
	switch scope.Profile.Mode {
	case ModeTrusted:
		return StageTrustedHost
	case ModeIsolated:
		if r.bwrapBinary() == "" {
			return StageUnavailable
		}
		return StageIsolatedBwrap
	default:
		return StageUnavailable
	}
}

func (r *Runner) Run(ctx context.Context, req ExecRequest) (ExecResult, error) {
	if req.Scope.Profile.Mode == ModeIsolated && req.Scope.Profile.Network == NetworkAllowlist {
		return r.runIsolatedAllowlist(ctx, req)
	}
	plan, err := r.Plan(req)
	if err != nil {
		return ExecResult{}, err
	}

	cmd := exec.CommandContext(ctx, plan.Binary, plan.Args...)
	cmd.Dir = plan.Dir
	cmd.Env = plan.Env
	if len(req.Stdin) > 0 {
		cmd.Stdin = bytes.NewReader(req.Stdin)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	return ExecResult{
		Stage:   plan.Stage,
		Stdout:  stdout.String(),
		Stderr:  stderr.String(),
		Network: plan.Network,
	}, err
}

func (r *Runner) Plan(req ExecRequest) (ExecutionPlan, error) {
	command := strings.TrimSpace(req.Command)
	if command == "" {
		return ExecutionPlan{}, fmt.Errorf("command is required")
	}

	workdir := strings.TrimSpace(req.Workdir)
	if workdir == "" {
		return ExecutionPlan{}, fmt.Errorf("workdir is required")
	}

	stage := r.Stage(req.Scope)
	switch stage {
	case StageTrustedHost:
		return ExecutionPlan{
			Stage:  stage,
			Binary: "bash",
			Args:   []string{"-lc", command},
			Dir:    workdir,
			Env:    os.Environ(),
		}, nil
	case StageIsolatedBwrap:
		bwrapPath := r.bwrapBinary()
		if bwrapPath == "" {
			return ExecutionPlan{}, fmt.Errorf("bubblewrap is required for isolated execution")
		}
		if req.Scope.Profile.Network == NetworkAllowlist {
			status := r.NetworkBackendStatus(context.Background())
			if !status.Available {
				return ExecutionPlan{}, fmt.Errorf("sandbox network allowlist backend unavailable: %s", status.Reason)
			}
			policy, err := r.compileNetworkPolicy(context.Background(), req.Scope.Profile.NetworkAllow)
			if err != nil {
				return ExecutionPlan{}, err
			}
			args, err := buildBwrapArgs(req.Scope, workdir, command, req.ExtraWritablePaths, req.ExtraReadonlyPaths, req.ExtraReadonlyBinds, req.ExtraEnv)
			if err != nil {
				return ExecutionPlan{}, err
			}
			network := NetworkExecutionEvidence{
				Policy:       string(NetworkAllowlist),
				Backend:      status.Name,
				Destinations: policy.DestinationStrings(),
				Rules:        policy.RuleStrings(),
			}
			return ExecutionPlan{
				Stage:   stage,
				Binary:  bwrapPath,
				Args:    args,
				Dir:     "/",
				Env:     nil,
				Network: &network,
			}, nil
		}
		args, err := buildBwrapArgs(req.Scope, workdir, command, req.ExtraWritablePaths, req.ExtraReadonlyPaths, req.ExtraReadonlyBinds, req.ExtraEnv)
		if err != nil {
			return ExecutionPlan{}, err
		}
		return ExecutionPlan{
			Stage:  stage,
			Binary: bwrapPath,
			Args:   args,
			Dir:    "/",
			Env:    nil,
		}, nil
	default:
		return ExecutionPlan{}, fmt.Errorf("no supported execution backend for sandbox mode %q", req.Scope.Profile.Mode)
	}
}

func (r *Runner) runIsolatedAllowlist(ctx context.Context, req ExecRequest) (ExecResult, error) {
	command := strings.TrimSpace(req.Command)
	if command == "" {
		return ExecResult{}, fmt.Errorf("command is required")
	}
	workdir := strings.TrimSpace(req.Workdir)
	if workdir == "" {
		return ExecResult{}, fmt.Errorf("workdir is required")
	}
	bwrapPath := r.bwrapBinary()
	if bwrapPath == "" {
		return ExecResult{}, fmt.Errorf("bubblewrap is required for isolated execution")
	}
	policy, err := r.compileNetworkPolicy(ctx, req.Scope.Profile.NetworkAllow)
	if err != nil {
		return ExecResult{}, err
	}
	backend := r.networkBackendOrDefault()
	args, err := buildBwrapArgs(req.Scope, workdir, command, req.ExtraWritablePaths, req.ExtraReadonlyPaths, req.ExtraReadonlyBinds, req.ExtraEnv)
	if err != nil {
		return ExecResult{}, err
	}
	if commandBackend, ok := backend.(NetworkCommandBackend); ok {
		return commandBackend.RunNetworkCommand(ctx, NetworkCommandRequest{
			Policy:    policy,
			BwrapPath: bwrapPath,
			BwrapArgs: args,
			Stdin:     req.Stdin,
		})
	}

	lease, err := backend.Prepare(ctx, policy)
	if err != nil {
		return ExecResult{}, err
	}
	extraReadonlyBinds := append([]BindPath(nil), req.ExtraReadonlyBinds...)
	extraReadonlyBinds = append(extraReadonlyBinds, lease.ExtraReadonlyBinds...)
	args, err = buildBwrapArgs(req.Scope, workdir, command, req.ExtraWritablePaths, req.ExtraReadonlyPaths, extraReadonlyBinds, req.ExtraEnv)
	if err != nil {
		cleanupErr := lease.Cleanup(context.Background())
		if cleanupErr != nil {
			return ExecResult{}, fmt.Errorf("%w; cleanup failed: %v", err, cleanupErr)
		}
		return ExecResult{}, err
	}
	binary := bwrapPath
	if len(lease.CommandPrefix) > 0 {
		binary = lease.CommandPrefix[0]
		args = append(append([]string(nil), lease.CommandPrefix[1:]...), append([]string{bwrapPath}, args...)...)
	}

	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = "/"
	cmd.Env = nil
	if len(req.Stdin) > 0 {
		cmd.Stdin = bytes.NewReader(req.Stdin)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	cleanupErr := lease.Cleanup(context.Background())
	evidence := lease.Evidence
	result := ExecResult{
		Stage:   StageIsolatedBwrap,
		Stdout:  stdout.String(),
		Stderr:  stderr.String(),
		Network: &evidence,
	}
	if runErr != nil && cleanupErr != nil {
		return result, fmt.Errorf("%w; cleanup failed: %v", runErr, cleanupErr)
	}
	if runErr != nil {
		return result, runErr
	}
	if cleanupErr != nil {
		return result, cleanupErr
	}
	return result, nil
}

func (r *Runner) compileNetworkPolicy(ctx context.Context, destinations []NetworkDestination) (CompiledNetworkPolicy, error) {
	return CompileNetworkPolicy(ctx, destinations, r.networkResolver)
}

func buildBwrapArgs(scope Scope, workdir, command string, extraWritablePaths []string, extraReadonlyPaths []string, extraReadonlyBinds []BindPath, extraEnv map[string]string) ([]string, error) {
	runtimeRO := existingRoots("/bin", "/usr", "/lib", "/lib64", "/etc")
	runtimeRO = dedupePaths(append(runtimeRO, resolverReadonlyRoots()...))

	writablePaths, err := resolveScopedPaths(scope.Profile.WritablePaths, scope)
	if err != nil {
		return nil, err
	}
	resolvedExtraWritable, err := resolveHostPaths(extraWritablePaths)
	if err != nil {
		return nil, err
	}
	writablePaths = append(writablePaths, resolvedExtraWritable...)
	readonlyPaths, err := resolveScopedPaths(scope.Profile.ReadonlyPaths, scope)
	if err != nil {
		return nil, err
	}
	resolvedExtraReadonly, err := resolveHostPaths(extraReadonlyPaths)
	if err != nil {
		return nil, err
	}
	readonlyPaths = append(readonlyPaths, resolvedExtraReadonly...)
	resolvedExtraReadonlyBinds, err := resolveHostBinds(extraReadonlyBinds)
	if err != nil {
		return nil, err
	}
	hiddenPaths, err := resolveScopedPaths(scope.Profile.HiddenPaths, scope)
	if err != nil {
		return nil, err
	}

	if !containsPath(writablePaths, scope.WorkingRoot) && !containsPath(readonlyPaths, scope.WorkingRoot) {
		writablePaths = append(writablePaths, scope.WorkingRoot)
	}

	args := []string{
		"--die-with-parent",
		"--new-session",
		"--unshare-user",
		"--uid", "65534",
		"--gid", "65534",
		"--unshare-pid",
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
		"--cap-drop", "ALL",
		"--clearenv",
		"--setenv", "HOME", scope.UserWorkspace,
		"--setenv", "PATH", "/usr/local/bin:/usr/bin:/bin",
		"--setenv", "TMPDIR", "/tmp",
	}
	if len(extraEnv) > 0 {
		keys := make([]string, 0, len(extraEnv))
		for key := range extraEnv {
			key = strings.TrimSpace(key)
			if key != "" {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			args = append(args, "--setenv", key, extraEnv[key])
		}
	}
	if scope.Profile.Network == NetworkDeny {
		args = append(args, "--unshare-net")
	}

	exposedRoots := make([]string, 0, len(runtimeRO)+len(writablePaths)+len(readonlyPaths)+len(resolvedExtraReadonlyBinds))
	for _, p := range runtimeRO {
		args = append(args, "--ro-bind", p, p)
		exposedRoots = append(exposedRoots, p)
	}
	for _, p := range readonlyPaths {
		if p == "/tmp" {
			continue
		}
		args = append(args, "--ro-bind", p, p)
		exposedRoots = append(exposedRoots, p)
	}
	for _, bind := range resolvedExtraReadonlyBinds {
		if bind.Source == "/tmp" || bind.Target == "/tmp" {
			continue
		}
		args = append(args, "--ro-bind", bind.Source, bind.Target)
		exposedRoots = append(exposedRoots, bind.Target)
	}
	for _, p := range writablePaths {
		if p == "/tmp" {
			continue
		}
		args = append(args, "--bind", p, p)
		exposedRoots = append(exposedRoots, p)
	}

	maskArgs, err := buildHiddenMaskArgs(hiddenPaths, exposedRoots)
	if err != nil {
		return nil, err
	}
	args = append(args, maskArgs...)

	args = append(args,
		"--chdir", workdir,
		"--", "bash", "-lc", command,
	)
	return args, nil
}

func buildHiddenMaskArgs(hiddenPaths []string, exposedRoots []string) ([]string, error) {
	args := make([]string, 0)
	for _, hidden := range hiddenPaths {
		if hidden == "" || strings.ContainsAny(hidden, "*?[") {
			continue
		}

		for _, exposed := range exposedRoots {
			if samePath(hidden, exposed) || pathWithin(exposed, hidden) {
				return nil, fmt.Errorf("hidden path %q conflicts with exposed root %q", hidden, exposed)
			}
		}

		if !pathWithinAny(hidden, exposedRoots) {
			continue
		}

		info, err := os.Stat(hidden)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("stat hidden path %q: %w", hidden, err)
		}

		if info.IsDir() {
			args = append(args, "--tmpfs", hidden)
			continue
		}
		args = append(args, "--ro-bind", "/dev/null", hidden)
	}
	return args, nil
}

func resolveHostBinds(raw []BindPath) ([]BindPath, error) {
	out := make([]BindPath, 0, len(raw))
	for _, value := range raw {
		source := strings.TrimSpace(value.Source)
		target := strings.TrimSpace(value.Target)
		if source == "" || target == "" {
			continue
		}
		resolvedSource, err := resolveRootPath("sandbox_host_bind_source", source)
		if err != nil {
			return nil, err
		}
		resolvedTarget, err := filepath.Abs(target)
		if err != nil {
			return nil, fmt.Errorf("resolve sandbox bind target %q: %w", target, err)
		}
		if !filepath.IsAbs(resolvedTarget) {
			return nil, fmt.Errorf("sandbox bind target %q resolved to non-absolute %q", target, resolvedTarget)
		}
		out = append(out, BindPath{Source: resolvedSource, Target: filepath.Clean(resolvedTarget)})
	}
	return out, nil
}

func resolveScopedPaths(raw []string, scope Scope) ([]string, error) {
	out := make([]string, 0, len(raw))
	for _, value := range raw {
		resolved, err := resolveScopedPath(value, scope)
		if err != nil {
			return nil, err
		}
		if resolved == "" {
			continue
		}
		out = append(out, resolved)
	}
	return dedupePaths(out), nil
}

func resolveScopedPath(value string, scope Scope) (string, error) {
	p := strings.TrimSpace(value)
	if p == "" {
		return "", nil
	}

	replacements := map[string]string{
		"{global_root}":        scope.GlobalRoot,
		"{shared_memory_root}": scope.SharedMemoryRoot,
		"{user_workspace}":     scope.UserWorkspace,
		"{user_memory}":        scope.UserMemory,
		"{working_root}":       scope.WorkingRoot,
	}
	for token, target := range replacements {
		p = strings.ReplaceAll(p, token, target)
	}

	if p == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve ~: %w", err)
		}
		p = home
	}
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve ~/ path %q: %w", value, err)
		}
		p = filepath.Join(home, p[2:])
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("sandbox path %q resolved to non-absolute %q", value, p)
	}

	abs, err := filepath.Abs(filepath.Clean(p))
	if err != nil {
		return "", fmt.Errorf("resolve sandbox path %q: %w", value, err)
	}
	return abs, nil
}

func resolveHostPaths(raw []string) ([]string, error) {
	out := make([]string, 0, len(raw))
	for _, value := range raw {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		resolved, err := resolveRootPath("sandbox_host_path", value)
		if err != nil {
			return nil, err
		}
		out = append(out, resolved)
	}
	return dedupePaths(out), nil
}

func resolverReadonlyRoots() []string {
	target, err := filepath.EvalSymlinks("/etc/resolv.conf")
	if err != nil {
		return nil
	}
	target = filepath.Clean(target)
	if !filepath.IsAbs(target) {
		return nil
	}
	parent := filepath.Dir(target)
	if parent == "/" || parent == "/etc" {
		return nil
	}
	if _, err := os.Stat(parent); err != nil {
		return nil
	}
	return []string{parent}
}

func existingRoots(paths ...string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func containsPath(paths []string, want string) bool {
	for _, p := range paths {
		if samePath(p, want) {
			return true
		}
	}
	return false
}

func dedupePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

func pathWithin(path string, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func pathWithinAny(path string, roots []string) bool {
	for _, root := range roots {
		if pathWithin(path, root) {
			return true
		}
	}
	return false
}

func (r *Runner) bwrapBinary() string {
	if r == nil {
		return ""
	}
	r.once.Do(func() {
		path, err := r.lookPath("bwrap")
		if err == nil {
			r.bwrapPath = path
		}
	})
	return r.bwrapPath
}

func (r *Runner) networkBackendOrDefault() NetworkBackend {
	if r != nil && r.networkBackend != nil {
		return r.networkBackend
	}
	if r == nil {
		return NewNetworkHelperBackend("")
	}
	socketPath := strings.TrimSpace(os.Getenv("APHELION_SANDBOX_NET_SOCKET"))
	return NewNetworkHelperBackend(socketPath)
}
