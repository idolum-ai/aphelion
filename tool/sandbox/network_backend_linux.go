//go:build linux

package sandbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type NetworkBackendStatus struct {
	Name         string
	Available    bool
	Reason       string
	Requirements []string
}

type NetworkExecutionEvidence struct {
	Policy       string
	Backend      string
	Destinations []string
	Rules        []string
}

type NetworkLease struct {
	CommandPrefix      []string
	ExtraReadonlyBinds []BindPath
	Evidence           NetworkExecutionEvidence
	cleanup            func(context.Context) error
}

func (l *NetworkLease) Cleanup(ctx context.Context) error {
	if l == nil || l.cleanup == nil {
		return nil
	}
	return l.cleanup(ctx)
}

type NetworkBackend interface {
	Status(context.Context) NetworkBackendStatus
	Prepare(context.Context, CompiledNetworkPolicy) (*NetworkLease, error)
}

type NetworkCommandRequest struct {
	Policy    CompiledNetworkPolicy
	BwrapPath string
	BwrapArgs []string
	Stdin     []byte
}

type NetworkCommandBackend interface {
	RunNetworkCommand(context.Context, NetworkCommandRequest) (ExecResult, error)
}

type LinuxNetworkBackend struct {
	lookPath func(string) (string, error)
}

func NewLinuxNetworkBackend(lookPath func(string) (string, error)) *LinuxNetworkBackend {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	return &LinuxNetworkBackend{lookPath: lookPath}
}

func (b *LinuxNetworkBackend) Status(_ context.Context) NetworkBackendStatus {
	status := NetworkBackendStatus{
		Name: "linux_netns_nftables",
		Requirements: []string{
			"iproute2 ip",
			"nftables nft",
			"CAP_NET_ADMIN",
			"CAP_SYS_ADMIN",
			"IPv4 forwarding",
		},
	}
	if b == nil {
		status.Reason = "network backend is not configured"
		return status
	}
	if _, err := b.lookPath("ip"); err != nil {
		status.Reason = "ip command not found"
		return status
	}
	if _, err := b.lookPath("nft"); err != nil {
		status.Reason = "nft command not found"
		return status
	}
	if !processHasCapNetAdmin() {
		status.Reason = "CAP_NET_ADMIN is not available to this process"
		return status
	}
	if !processHasCapSysAdmin() {
		status.Reason = "CAP_SYS_ADMIN is not available to this process"
		return status
	}
	if !ipv4ForwardingEnabled() {
		status.Reason = "IPv4 forwarding is disabled"
		return status
	}
	status.Available = true
	return status
}

func (b *LinuxNetworkBackend) Prepare(ctx context.Context, policy CompiledNetworkPolicy) (*NetworkLease, error) {
	status := b.Status(ctx)
	if !status.Available {
		return nil, fmt.Errorf("sandbox network allowlist backend unavailable: %s", status.Reason)
	}
	policy = policy.IPv4Only()
	if len(policy.Rules) == 0 {
		return nil, fmt.Errorf("sandbox network allowlist compiled no enforceable rules")
	}
	ipPath, err := b.lookPath("ip")
	if err != nil {
		return nil, fmt.Errorf("find ip command: %w", err)
	}
	nftPath, err := b.lookPath("nft")
	if err != nil {
		return nil, fmt.Errorf("find nft command: %w", err)
	}

	token, err := randomNetworkToken()
	if err != nil {
		return nil, err
	}
	nsName := "aph-" + token
	hostIf := "aph" + token[:8] + "h"
	nsIf := "aph" + token[:8] + "n"
	hostIP, nsIP, err := leaseIPv4Pair()
	if err != nil {
		return nil, err
	}
	tableName := "aphelion_" + strings.ReplaceAll(token, "-", "_")
	tempDir, err := os.MkdirTemp("", "aphelion-sandbox-net-*")
	if err != nil {
		return nil, fmt.Errorf("create sandbox network temp dir: %w", err)
	}
	if err := os.Chmod(tempDir, 0o711); err != nil {
		return nil, fmt.Errorf("chmod sandbox network temp dir: %w", err)
	}

	cleanup := &networkCleanup{
		ipPath:    ipPath,
		nftPath:   nftPath,
		nsName:    nsName,
		hostIf:    hostIf,
		tableName: tableName,
		tempDir:   tempDir,
	}
	ok := false
	defer func() {
		if !ok {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = cleanup.Run(cleanupCtx)
		}
	}()

	if err := runCommand(ctx, ipPath, "netns", "add", nsName); err != nil {
		return nil, fmt.Errorf("create sandbox network namespace: %w", err)
	}
	if err := runCommand(ctx, ipPath, "link", "add", hostIf, "type", "veth", "peer", "name", nsIf); err != nil {
		return nil, fmt.Errorf("create sandbox veth pair: %w", err)
	}
	if err := runCommand(ctx, ipPath, "link", "set", nsIf, "netns", nsName); err != nil {
		return nil, fmt.Errorf("move sandbox veth peer: %w", err)
	}
	if err := runCommand(ctx, ipPath, "addr", "add", hostIP.String()+"/30", "dev", hostIf); err != nil {
		return nil, fmt.Errorf("assign sandbox host veth address: %w", err)
	}
	if err := runCommand(ctx, ipPath, "link", "set", hostIf, "up"); err != nil {
		return nil, fmt.Errorf("raise sandbox host veth: %w", err)
	}
	if err := runCommand(ctx, ipPath, "netns", "exec", nsName, ipPath, "addr", "add", nsIP.String()+"/30", "dev", nsIf); err != nil {
		return nil, fmt.Errorf("assign sandbox namespace veth address: %w", err)
	}
	if err := runCommand(ctx, ipPath, "netns", "exec", nsName, ipPath, "link", "set", "lo", "up"); err != nil {
		return nil, fmt.Errorf("raise sandbox namespace loopback: %w", err)
	}
	if err := runCommand(ctx, ipPath, "netns", "exec", nsName, ipPath, "link", "set", nsIf, "up"); err != nil {
		return nil, fmt.Errorf("raise sandbox namespace veth: %w", err)
	}
	if err := runCommand(ctx, ipPath, "netns", "exec", nsName, ipPath, "route", "add", "default", "via", hostIP.String()); err != nil {
		return nil, fmt.Errorf("add sandbox namespace default route: %w", err)
	}
	if err := applyNftRules(ctx, nftPath, tableName, hostIf, nsIP, policy.Rules); err != nil {
		return nil, err
	}

	hostsPath := filepath.Join(tempDir, "hosts")
	if err := os.WriteFile(hostsPath, []byte(renderSandboxHosts(policy.Hosts)), 0o644); err != nil {
		return nil, fmt.Errorf("write sandbox hosts file: %w", err)
	}
	ok = true
	return &NetworkLease{
		CommandPrefix:      []string{ipPath, "netns", "exec", nsName},
		ExtraReadonlyBinds: []BindPath{{Source: hostsPath, Target: "/etc/hosts"}},
		Evidence: NetworkExecutionEvidence{
			Policy:       string(NetworkAllowlist),
			Backend:      status.Name,
			Destinations: policy.DestinationStrings(),
			Rules:        policy.RuleStrings(),
		},
		cleanup: cleanup.Run,
	}, nil
}

type networkCleanup struct {
	ipPath    string
	nftPath   string
	nsName    string
	hostIf    string
	tableName string
	tempDir   string
}

func (c *networkCleanup) Run(ctx context.Context) error {
	var errs []string
	if c.nftPath != "" && c.tableName != "" {
		if err := runCommand(ctx, c.nftPath, "delete", "table", "ip", c.tableName); err != nil {
			appendCleanupErr(&errs, err)
		}
	}
	if c.ipPath != "" && c.nsName != "" {
		if err := runCommand(ctx, c.ipPath, "netns", "delete", c.nsName); err != nil {
			appendCleanupErr(&errs, err)
		}
	}
	if c.ipPath != "" && c.hostIf != "" {
		if err := runCommand(ctx, c.ipPath, "link", "delete", c.hostIf); err != nil {
			appendCleanupErr(&errs, err)
		}
	}
	if c.tempDir != "" {
		if err := os.RemoveAll(c.tempDir); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("cleanup sandbox network backend: %s", strings.Join(errs, "; "))
	}
	return nil
}

func appendCleanupErr(errs *[]string, err error) {
	if err == nil || isMissingNetworkObjectError(err) {
		return
	}
	*errs = append(*errs, err.Error())
}

func isMissingNetworkObjectError(err error) bool {
	text := strings.ToLower(err.Error())
	for _, needle := range []string{
		"cannot find device",
		"does not exist",
		"no such file",
		"no such process",
		"no such table",
	} {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func applyNftRules(ctx context.Context, nftPath string, tableName string, hostIf string, nsIP netip.Addr, rules []NetworkRule) error {
	var b strings.Builder
	fmt.Fprintf(&b, "add table ip %s\n", tableName)
	fmt.Fprintf(&b, "add chain ip %s input { type filter hook input priority 0; policy accept; }\n", tableName)
	fmt.Fprintf(&b, "add chain ip %s forward { type filter hook forward priority 0; policy accept; }\n", tableName)
	fmt.Fprintf(&b, "add chain ip %s postrouting { type nat hook postrouting priority 100; policy accept; }\n", tableName)
	fmt.Fprintf(&b, "add rule ip %s input iifname %q ip saddr %s drop\n", tableName, hostIf, nsIP.String())
	fmt.Fprintf(&b, "add rule ip %s forward ct state established,related accept\n", tableName)
	for _, rule := range rules {
		if !rule.Prefix.Addr().Is4() {
			continue
		}
		fmt.Fprintf(&b, "add rule ip %s forward ip saddr %s ip daddr %s tcp dport %d accept\n", tableName, nsIP.String(), rule.Prefix.String(), rule.Port)
		fmt.Fprintf(&b, "add rule ip %s forward ip saddr %s ip daddr %s udp dport %d accept\n", tableName, nsIP.String(), rule.Prefix.String(), rule.Port)
	}
	fmt.Fprintf(&b, "add rule ip %s forward ip saddr %s drop\n", tableName, nsIP.String())
	fmt.Fprintf(&b, "add rule ip %s postrouting ip saddr %s masquerade\n", tableName, nsIP.String())

	cmd := exec.CommandContext(ctx, nftPath, "-f", "-")
	cmd.Stdin = strings.NewReader(b.String())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("apply sandbox network nftables rules: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func renderSandboxHosts(hosts map[string][]netip.Addr) string {
	var b strings.Builder
	b.WriteString("127.0.0.1 localhost\n")
	names := make([]string, 0, len(hosts))
	for name := range hosts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		addrs := append([]netip.Addr(nil), hosts[name]...)
		sort.Slice(addrs, func(i, j int) bool { return addrs[i].String() < addrs[j].String() })
		for _, addr := range addrs {
			fmt.Fprintf(&b, "%s %s\n", addr.String(), name)
		}
	}
	return b.String()
}

func runCommand(ctx context.Context, binary string, args ...string) error {
	cmd := exec.CommandContext(ctx, binary, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, detail)
	}
	return nil
}

func processHasCapNetAdmin() bool {
	return processHasCapability(12)
}

func processHasCapSysAdmin() bool {
	return processHasCapability(21)
}

func processHasCapability(bit uint) bool {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return os.Geteuid() == 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "CapEff:") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "CapEff:"))
		value, err := strconv.ParseUint(raw, 16, 64)
		if err != nil {
			return os.Geteuid() == 0
		}
		return value&(uint64(1)<<bit) != 0
	}
	return os.Geteuid() == 0
}

func ipv4ForwardingEnabled() bool {
	data, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == "1"
}

func randomNetworkToken() (string, error) {
	var data [6]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", fmt.Errorf("read sandbox network random token: %w", err)
	}
	return fmt.Sprintf("%x", data[:]), nil
}

func leaseIPv4Pair() (hostIP netip.Addr, nsIP netip.Addr, err error) {
	var data [4]byte
	if _, err := rand.Read(data[:]); err != nil {
		return netip.Addr{}, netip.Addr{}, fmt.Errorf("read sandbox network subnet token: %w", err)
	}
	seed := binary.BigEndian.Uint32(data[:])
	octet2 := byte(200 + seed%40)
	octet3 := byte((seed >> 8) % 255)
	base := byte(((seed >> 16) % 64) * 4)
	host := netip.AddrFrom4([4]byte{10, octet2, octet3, base + 1})
	ns := netip.AddrFrom4([4]byte{10, octet2, octet3, base + 2})
	return host, ns, nil
}
