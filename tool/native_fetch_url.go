//go:build linux

package tool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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
	return renderFetchURLDigest(parsed.String(), res.Status, res.Header.Get("Content-Type"), data, truncated), nil
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

func renderFetchURLDigest(rawURL, status, contentType string, data []byte, truncated bool) string {
	sum := sha256.Sum256(data)
	excerpt, excerptTruncated := fetchURLExcerpt(data, 2048)
	var b strings.Builder
	b.WriteString("[FETCH_URL]\n")
	fmt.Fprintf(&b, "url: %s\n", rawURL)
	fmt.Fprintf(&b, "status: %s\n", status)
	fmt.Fprintf(&b, "content_type: %s\n", contentType)
	fmt.Fprintf(&b, "bytes_read: %d\n", len(data))
	fmt.Fprintf(&b, "sha256: %s\n", hex.EncodeToString(sum[:]))
	fmt.Fprintf(&b, "truncated: %t\n", truncated)
	fmt.Fprintf(&b, "body_ref: fetch_url.raw sha256=%s bytes=%d truncated=%t max_bytes_param=re-run_with_higher_max_bytes_if_needed\n", hex.EncodeToString(sum[:]), len(data), truncated)
	fmt.Fprintf(&b, "excerpt_truncated: %t\n", excerptTruncated)
	b.WriteString("excerpt:\n")
	if excerpt != "" {
		b.WriteString(excerpt)
		if !strings.HasSuffix(excerpt, "\n") {
			b.WriteByte('\n')
		}
	}
	b.WriteString("[/FETCH_URL]")
	return b.String()
}

func fetchURLExcerpt(data []byte, limit int) (string, bool) {
	text := string(data)
	text = strings.ReplaceAll(text, "\x00", "�")
	text = strings.TrimSpace(text)
	if text == "" || limit <= 0 {
		return "", text != ""
	}
	if len(text) <= limit {
		return text, false
	}
	cut := limit
	for cut > 0 && !utf8.ValidString(text[:cut]) {
		cut--
	}
	if cut <= 0 {
		return "", true
	}
	return strings.TrimSpace(text[:cut]) + "…", true
}
