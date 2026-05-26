//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func (r *Registry) fetchURL(ctx context.Context, input json.RawMessage, scope sandbox.Scope, p principal.Principal) (string, error) {
	var in fetchURLInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("decode fetch_url input: %w", err)
	}
	raw := strings.TrimSpace(in.URL)
	if raw == "" {
		return "", fmt.Errorf("fetch_url url is required")
	}
	if scope.Profile.Network == sandbox.NetworkDeny {
		return "", fmt.Errorf("fetch_url denied by sandbox network policy")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("fetch_url parse url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("fetch_url only supports http and https")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("fetch_url rejects URLs with embedded credentials")
	}
	if p.Role != principal.RoleAdmin && hostLooksLocal(parsed.Hostname()) {
		return "", fmt.Errorf("fetch_url rejects local/private hosts for non-admin principals")
	}
	transport := http.DefaultTransport
	var fetchPolicy *nativeFetchNetworkPolicy
	if scope.Profile.Mode == sandbox.ModeIsolated && scope.Profile.Network == sandbox.NetworkAllowlist {
		allowlistTransport, policy, err := r.fetchURLAllowlistTransport(ctx, scope.Profile, p.Role != principal.RoleAdmin)
		if err != nil {
			return "", err
		}
		transport = allowlistTransport
		fetchPolicy = policy
	}
	maxBytes := clampNativeLimit(in.MaxBytes, defaultNativeFetchMaxBytes, maxNativeFetchBytes)
	client := &http.Client{Timeout: 20 * time.Second, Transport: transport}
	if fetchPolicy != nil {
		client.CheckRedirect = func(req *http.Request, _ []*http.Request) error {
			return fetchPolicy.authorizeURL(req.Context(), req.URL)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", fmt.Errorf("fetch_url create request: %w", err)
	}
	if userAgent := strings.TrimSpace(r.nativeFetchUserAgent); userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	if fetchPolicy != nil {
		if err := fetchPolicy.authorizeURL(ctx, parsed); err != nil {
			return "", err
		}
	}
	res, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch_url request: %w", err)
	}
	defer res.Body.Close()
	data, truncated, err := readBounded(res.Body, maxBytes)
	if err != nil {
		return "", fmt.Errorf("fetch_url read response: %w", err)
	}
	var b strings.Builder
	b.WriteString("[FETCH_URL]\n")
	fmt.Fprintf(&b, "url: %s\nstatus: %s\ncontent_type: %s\nbytes: %d\ntruncated: %t\nbody:\n",
		parsed.String(),
		res.Status,
		res.Header.Get("Content-Type"),
		len(data),
		truncated,
	)
	b.Write(data)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		b.WriteByte('\n')
	}
	b.WriteString("[/FETCH_URL]")
	return b.String(), nil
}

func fetchURLPort(parsed *url.URL) (uint16, error) {
	if parsed == nil {
		return 0, fmt.Errorf("fetch_url url is required")
	}
	if raw := strings.TrimSpace(parsed.Port()); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil || port <= 0 || port > 65535 {
			return 0, fmt.Errorf("fetch_url port must be between 1 and 65535")
		}
		return uint16(port), nil
	}
	switch parsed.Scheme {
	case "http":
		return 80, nil
	case "https":
		return 443, nil
	default:
		return 0, fmt.Errorf("fetch_url only supports http and https")
	}
}

func hostLooksLocal(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" {
		return false
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}
