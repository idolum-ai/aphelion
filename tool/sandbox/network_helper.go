//go:build linux

package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

const (
	DefaultNetworkHelperSocketPath = "/run/aphelion/sandbox-net.sock"
	networkHelperBackendName       = "linux_netns_nftables_helper"
)

type NetworkHelperBackend struct {
	socketPath string
}

type NetworkHelperServeOptions struct {
	SocketPath        string
	SocketGroup       string
	SocketMode        os.FileMode
	AllowedUID        int
	EnforceAllowedUID bool
	LookPath          func(string) (string, error)
}

type networkHelperRequest struct {
	Action       string              `json:"action"`
	Destinations []string            `json:"destinations,omitempty"`
	Rules        []networkHelperRule `json:"rules,omitempty"`
	Hosts        map[string][]string `json:"hosts,omitempty"`
	BwrapPath    string              `json:"bwrap_path,omitempty"`
	BwrapArgs    []string            `json:"bwrap_args,omitempty"`
	Stdin        []byte              `json:"stdin,omitempty"`
}

type networkHelperRule struct {
	Prefix string `json:"prefix"`
	Port   uint16 `json:"port"`
}

type networkHelperResponse struct {
	Status   NetworkBackendStatus     `json:"status,omitempty"`
	Evidence NetworkExecutionEvidence `json:"evidence,omitempty"`
	Stdout   []byte                   `json:"stdout,omitempty"`
	Stderr   []byte                   `json:"stderr,omitempty"`
	ExitCode int                      `json:"exit_code,omitempty"`
	Error    string                   `json:"error,omitempty"`
}

func NewNetworkHelperBackend(socketPath string) *NetworkHelperBackend {
	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" {
		socketPath = DefaultNetworkHelperSocketPath
	}
	return &NetworkHelperBackend{socketPath: socketPath}
}

func (b *NetworkHelperBackend) Status(ctx context.Context) NetworkBackendStatus {
	status := NetworkBackendStatus{
		Name:         networkHelperBackendName,
		Requirements: networkHelperRequirements(),
	}
	if b == nil {
		status.Reason = "network helper backend is not configured"
		return status
	}
	resp, err := b.roundTrip(ctx, networkHelperRequest{Action: "status"})
	if err != nil {
		status.Reason = "sandbox network helper unavailable: " + err.Error()
		return status
	}
	if resp.Status.Name == "" {
		resp.Status.Name = networkHelperBackendName
	}
	if len(resp.Status.Requirements) == 0 {
		resp.Status.Requirements = networkHelperRequirements()
	}
	return resp.Status
}

func (b *NetworkHelperBackend) Prepare(context.Context, CompiledNetworkPolicy) (*NetworkLease, error) {
	return nil, fmt.Errorf("sandbox network helper runs complete isolated commands; direct leases are not available")
}

func (b *NetworkHelperBackend) RunNetworkCommand(ctx context.Context, req NetworkCommandRequest) (ExecResult, error) {
	if b == nil {
		return ExecResult{}, fmt.Errorf("sandbox network helper backend is not configured")
	}
	helperReq, err := networkHelperRequestFromCommand(req)
	if err != nil {
		return ExecResult{}, err
	}
	resp, err := b.roundTrip(ctx, helperReq)
	if err != nil {
		return ExecResult{}, fmt.Errorf("sandbox network helper request failed: %w", err)
	}
	evidence := resp.Evidence
	result := ExecResult{
		Stage:   StageIsolatedBwrap,
		Stdout:  string(resp.Stdout),
		Stderr:  string(resp.Stderr),
		Network: &evidence,
	}
	if strings.TrimSpace(resp.Error) != "" {
		return result, fmt.Errorf("%s", strings.TrimSpace(resp.Error))
	}
	if resp.ExitCode != 0 {
		return result, fmt.Errorf("sandbox network command exited with status %d", resp.ExitCode)
	}
	return result, nil
}

func (b *NetworkHelperBackend) roundTrip(ctx context.Context, req networkHelperRequest) (networkHelperResponse, error) {
	path := DefaultNetworkHelperSocketPath
	if b != nil && strings.TrimSpace(b.socketPath) != "" {
		path = b.socketPath
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return networkHelperResponse{}, err
	}
	defer conn.Close()

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	defer close(done)

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return networkHelperResponse{}, err
	}
	if unixConn, ok := conn.(*net.UnixConn); ok {
		_ = unixConn.CloseWrite()
	}
	var resp networkHelperResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		if ctx.Err() != nil {
			return networkHelperResponse{}, ctx.Err()
		}
		return networkHelperResponse{}, err
	}
	if strings.TrimSpace(resp.Error) != "" && req.Action == "status" {
		return networkHelperResponse{}, fmt.Errorf("%s", strings.TrimSpace(resp.Error))
	}
	return resp, nil
}

func ServeNetworkHelper(ctx context.Context, opts NetworkHelperServeOptions) error {
	socketPath := strings.TrimSpace(opts.SocketPath)
	if socketPath == "" {
		socketPath = DefaultNetworkHelperSocketPath
	}
	mode := opts.SocketMode
	if mode == 0 {
		mode = 0o660
	}
	lookPath := opts.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return fmt.Errorf("create sandbox network helper runtime dir: %w", err)
	}
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale sandbox network helper socket: %w", err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return fmt.Errorf("listen on sandbox network helper socket: %w", err)
	}
	defer listener.Close()
	defer os.Remove(socketPath)

	if err := os.Chmod(socketPath, mode); err != nil {
		return fmt.Errorf("chmod sandbox network helper socket: %w", err)
	}
	if strings.TrimSpace(opts.SocketGroup) != "" && os.Geteuid() == 0 {
		gid, err := lookupGroupID(opts.SocketGroup)
		if err != nil {
			return err
		}
		if err := os.Chown(socketPath, 0, gid); err != nil {
			return fmt.Errorf("chown sandbox network helper socket group: %w", err)
		}
	}

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	for {
		conn, err := listener.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept sandbox network helper request: %w", err)
		}
		go handleNetworkHelperConn(ctx, conn, opts.AllowedUID, opts.EnforceAllowedUID, lookPath)
	}
}

func handleNetworkHelperConn(ctx context.Context, conn *net.UnixConn, allowedUID int, enforceAllowedUID bool, lookPath func(string) (string, error)) {
	defer conn.Close()

	cred, credErr := unixPeerCred(conn)
	var req networkHelperRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		writeNetworkHelperResponse(conn, networkHelperResponse{Error: "decode sandbox network helper request: " + err.Error()})
		return
	}
	switch strings.TrimSpace(req.Action) {
	case "status":
		writeNetworkHelperResponse(conn, networkHelperResponse{Status: networkHelperLocalStatus(ctx, lookPath)})
	case "run":
		if credErr != nil {
			writeNetworkHelperResponse(conn, networkHelperResponse{Error: "read sandbox network helper peer credentials: " + credErr.Error()})
			return
		}
		if enforceAllowedUID && int(cred.Uid) != allowedUID {
			writeNetworkHelperResponse(conn, networkHelperResponse{Error: fmt.Sprintf("sandbox network helper peer uid %d is not allowed", cred.Uid)})
			return
		}
		writeNetworkHelperResponse(conn, runNetworkHelperCommand(ctx, req, cred, lookPath))
	default:
		writeNetworkHelperResponse(conn, networkHelperResponse{Error: "unknown sandbox network helper action"})
	}
}

func runNetworkHelperCommand(ctx context.Context, req networkHelperRequest, cred *syscall.Ucred, lookPath func(string) (string, error)) networkHelperResponse {
	status := networkHelperLocalStatus(ctx, lookPath)
	if !status.Available {
		return networkHelperResponse{Status: status, Error: "sandbox network helper unavailable: " + strings.TrimSpace(status.Reason)}
	}
	policy, err := validateNetworkHelperRunRequest(req)
	if err != nil {
		return networkHelperResponse{Status: status, Error: err.Error()}
	}
	if err := validateNetworkHelperTrustedBwrap(req.BwrapPath, lookPath); err != nil {
		return networkHelperResponse{Status: status, Error: err.Error()}
	}
	lease, err := NewLinuxNetworkBackend(lookPath).Prepare(ctx, policy)
	if err != nil {
		return networkHelperResponse{Status: status, Error: err.Error()}
	}

	args, err := injectNetworkHelperBinds(req.BwrapArgs, lease.ExtraReadonlyBinds)
	if err != nil {
		_ = lease.Cleanup(context.Background())
		return networkHelperResponse{Status: status, Error: err.Error()}
	}
	setprivPath, err := lookPath("setpriv")
	if err != nil {
		_ = lease.Cleanup(context.Background())
		return networkHelperResponse{Status: status, Error: "setpriv command not found"}
	}
	if len(lease.CommandPrefix) == 0 {
		_ = lease.Cleanup(context.Background())
		return networkHelperResponse{Status: status, Error: "sandbox network helper lease missing command prefix"}
	}
	binary := lease.CommandPrefix[0]
	commandArgs := append([]string(nil), lease.CommandPrefix[1:]...)
	commandArgs = append(commandArgs,
		setprivPath,
		"--reuid", strconv.FormatUint(uint64(cred.Uid), 10),
		"--regid", strconv.FormatUint(uint64(cred.Gid), 10),
		"--clear-groups",
		req.BwrapPath,
	)
	commandArgs = append(commandArgs, args...)

	cmd := exec.CommandContext(ctx, binary, commandArgs...)
	cmd.Dir = "/"
	cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
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
	evidence.Backend = networkHelperBackendName
	resp := networkHelperResponse{
		Status:   status,
		Evidence: evidence,
		Stdout:   stdout.Bytes(),
		Stderr:   stderr.Bytes(),
	}
	if runErr == nil && cleanupErr == nil {
		return resp
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		resp.ExitCode = exitErr.ExitCode()
		if cleanupErr != nil {
			resp.Error = fmt.Sprintf("sandbox network command exited with status %d; cleanup sandbox network helper lease: %v", resp.ExitCode, cleanupErr)
		}
		return resp
	}
	if runErr != nil {
		resp.Error = runErr.Error()
	}
	if cleanupErr != nil {
		if resp.Error != "" {
			resp.Error += "; "
		}
		resp.Error += "cleanup sandbox network helper lease: " + cleanupErr.Error()
	}
	return resp
}

func networkHelperLocalStatus(ctx context.Context, lookPath func(string) (string, error)) NetworkBackendStatus {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	status := NewLinuxNetworkBackend(lookPath).Status(ctx)
	status.Name = networkHelperBackendName
	status.Requirements = networkHelperRequirements()
	if !status.Available {
		return status
	}
	if _, err := lookPath("setpriv"); err != nil {
		status.Available = false
		status.Reason = "setpriv command not found"
	}
	return status
}

func networkHelperRequirements() []string {
	return []string{
		"aphelion sandbox-net helper service",
		"Unix helper socket",
		"iproute2 ip",
		"nftables nft",
		"setpriv",
		"CAP_NET_ADMIN in helper",
		"CAP_SYS_ADMIN in helper",
		"IPv4 forwarding",
	}
}

func networkHelperRequestFromCommand(req NetworkCommandRequest) (networkHelperRequest, error) {
	policy := req.Policy.IPv4Only()
	if len(policy.Rules) == 0 {
		return networkHelperRequest{}, fmt.Errorf("sandbox network allowlist compiled no enforceable rules")
	}
	out := networkHelperRequest{
		Action:       "run",
		Destinations: policy.DestinationStrings(),
		Hosts:        make(map[string][]string, len(policy.Hosts)),
		BwrapPath:    req.BwrapPath,
		BwrapArgs:    append([]string(nil), req.BwrapArgs...),
		Stdin:        append([]byte(nil), req.Stdin...),
	}
	for _, rule := range policy.Rules {
		out.Rules = append(out.Rules, networkHelperRule{Prefix: rule.Prefix.String(), Port: rule.Port})
	}
	sort.Slice(out.Rules, func(i, j int) bool {
		if out.Rules[i].Prefix == out.Rules[j].Prefix {
			return out.Rules[i].Port < out.Rules[j].Port
		}
		return out.Rules[i].Prefix < out.Rules[j].Prefix
	})
	for host, addrs := range policy.Hosts {
		for _, addr := range addrs {
			if addr.Is4() {
				out.Hosts[host] = append(out.Hosts[host], addr.String())
			}
		}
		sort.Strings(out.Hosts[host])
	}
	if _, err := validateNetworkHelperRunRequest(out); err != nil {
		return networkHelperRequest{}, err
	}
	return out, nil
}

func validateNetworkHelperRunRequest(req networkHelperRequest) (CompiledNetworkPolicy, error) {
	if strings.TrimSpace(req.Action) != "run" {
		return CompiledNetworkPolicy{}, fmt.Errorf("sandbox network helper request action must be run")
	}
	if err := validateNetworkHelperBwrapCommand(req.BwrapPath, req.BwrapArgs); err != nil {
		return CompiledNetworkPolicy{}, err
	}
	return networkPolicyFromHelperRequest(req)
}

func validateNetworkHelperBwrapCommand(bwrapPath string, args []string) error {
	bwrapPath = strings.TrimSpace(bwrapPath)
	if bwrapPath == "" {
		return fmt.Errorf("sandbox network helper request missing bubblewrap path")
	}
	if !filepath.IsAbs(bwrapPath) {
		return fmt.Errorf("sandbox network helper bubblewrap path must be absolute")
	}
	if filepath.Base(bwrapPath) != "bwrap" {
		return fmt.Errorf("sandbox network helper may only run bubblewrap")
	}
	if strings.ContainsRune(bwrapPath, 0) {
		return fmt.Errorf("sandbox network helper bubblewrap path contains NUL")
	}
	if len(args) == 0 {
		return fmt.Errorf("sandbox network helper bubblewrap args are required")
	}
	hasCommandSeparator := false
	for _, arg := range args {
		if strings.ContainsRune(arg, 0) {
			return fmt.Errorf("sandbox network helper bubblewrap arg contains NUL")
		}
		if arg == "--" {
			hasCommandSeparator = true
		}
	}
	if !hasCommandSeparator {
		return fmt.Errorf("sandbox network helper bubblewrap args missing command separator")
	}
	return nil
}

func validateNetworkHelperTrustedBwrap(bwrapPath string, lookPath func(string) (string, error)) error {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	expected, err := lookPath("bwrap")
	if err != nil {
		return fmt.Errorf("find trusted bubblewrap path: %w", err)
	}
	expected, err = filepath.Abs(expected)
	if err != nil {
		return fmt.Errorf("resolve trusted bubblewrap path: %w", err)
	}
	requested, err := filepath.Abs(bwrapPath)
	if err != nil {
		return fmt.Errorf("resolve requested bubblewrap path: %w", err)
	}
	expectedReal, expectedErr := filepath.EvalSymlinks(expected)
	requestedReal, requestedErr := filepath.EvalSymlinks(requested)
	if expectedErr == nil && requestedErr == nil {
		expected = expectedReal
		requested = requestedReal
	}
	if filepath.Clean(requested) != filepath.Clean(expected) {
		return fmt.Errorf("sandbox network helper bubblewrap path %q does not match trusted path %q", bwrapPath, expected)
	}
	return nil
}

func networkPolicyFromHelperRequest(req networkHelperRequest) (CompiledNetworkPolicy, error) {
	destinations, err := ParseNetworkDestinations(req.Destinations)
	if err != nil {
		return CompiledNetworkPolicy{}, err
	}
	policy := CompiledNetworkPolicy{
		Destinations: destinations,
		Hosts:        make(map[string][]netip.Addr, len(req.Hosts)),
	}
	seenRules := map[string]struct{}{}
	for _, raw := range req.Rules {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw.Prefix))
		if err != nil {
			return CompiledNetworkPolicy{}, fmt.Errorf("sandbox network helper rule prefix %q is invalid: %w", raw.Prefix, err)
		}
		prefix = prefix.Masked()
		if !prefix.Addr().Is4() {
			return CompiledNetworkPolicy{}, fmt.Errorf("sandbox network helper rule %q is not IPv4", raw.Prefix)
		}
		if raw.Port == 0 {
			return CompiledNetworkPolicy{}, fmt.Errorf("sandbox network helper rule %q has empty port", raw.Prefix)
		}
		addNetworkRule(&policy, seenRules, NetworkRule{Prefix: prefix, Port: raw.Port})
	}
	if len(policy.Rules) == 0 {
		return CompiledNetworkPolicy{}, fmt.Errorf("sandbox network helper request has no enforceable IPv4 rules")
	}
	for host, rawAddrs := range req.Hosts {
		host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
		if err := validateNetworkHostname(host); err != nil {
			return CompiledNetworkPolicy{}, fmt.Errorf("sandbox network helper host %q is invalid: %w", host, err)
		}
		for _, rawAddr := range rawAddrs {
			addr, err := netip.ParseAddr(strings.TrimSpace(rawAddr))
			if err != nil {
				return CompiledNetworkPolicy{}, fmt.Errorf("sandbox network helper host %q address %q is invalid: %w", host, rawAddr, err)
			}
			addr = addr.Unmap()
			if !addr.Is4() {
				return CompiledNetworkPolicy{}, fmt.Errorf("sandbox network helper host %q address %q is not IPv4", host, rawAddr)
			}
			policy.Hosts[host] = append(policy.Hosts[host], addr)
		}
		policy.Hosts[host] = dedupeAddrs(policy.Hosts[host])
	}
	sort.Slice(policy.Rules, func(i, j int) bool { return policy.Rules[i].String() < policy.Rules[j].String() })
	return policy, nil
}

func injectNetworkHelperBinds(args []string, binds []BindPath) ([]string, error) {
	separator := -1
	for i, arg := range args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 {
		return nil, fmt.Errorf("sandbox network helper bubblewrap args missing command separator")
	}
	out := make([]string, 0, len(args)+len(binds)*3)
	out = append(out, args[:separator]...)
	for _, bind := range binds {
		if strings.TrimSpace(bind.Source) == "" || strings.TrimSpace(bind.Target) == "" {
			continue
		}
		out = append(out, "--ro-bind", bind.Source, bind.Target)
	}
	out = append(out, args[separator:]...)
	return out, nil
}

func writeNetworkHelperResponse(conn *net.UnixConn, resp networkHelperResponse) {
	_ = json.NewEncoder(conn).Encode(resp)
}

func unixPeerCred(conn *net.UnixConn) (*syscall.Ucred, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return nil, err
	}
	var cred *syscall.Ucred
	var controlErr error
	if err := raw.Control(func(fd uintptr) {
		cred, controlErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return nil, err
	}
	if controlErr != nil {
		return nil, controlErr
	}
	if cred == nil {
		return nil, fmt.Errorf("peer credentials unavailable")
	}
	return cred, nil
}

func lookupGroupID(name string) (int, error) {
	group, err := user.LookupGroup(strings.TrimSpace(name))
	if err != nil {
		return 0, fmt.Errorf("lookup sandbox network helper socket group %q: %w", name, err)
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		return 0, fmt.Errorf("parse sandbox network helper socket group %q gid: %w", name, err)
	}
	return gid, nil
}
