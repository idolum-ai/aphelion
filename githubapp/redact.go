//go:build linux

package githubapp

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var tokenPattern = regexp.MustCompile(`\b(gh[psuor]_[A-Za-z0-9_.-]+|github_pat_[A-Za-z0-9_]+)\b`)

func Redact(raw string) string {
	return tokenPattern.ReplaceAllString(raw, "<redacted>")
}

func (t InstallationToken) Redacted() InstallationToken {
	t.Token = ""
	return t
}

func (t InstallationToken) String() string {
	return fmt.Sprintf("InstallationToken{Token:<redacted> ExpiresAt:%s Permissions:%v Repositories:%v}", t.ExpiresAt.Format(time.RFC3339), t.Permissions, t.Repositories)
}

func (t InstallationToken) GoString() string {
	return t.String()
}

func GitCredentialHost(apiBaseURL string) string {
	host := "github.com"
	if parsed, err := url.Parse(strings.TrimSpace(apiBaseURL)); err == nil && parsed.Host != "" {
		host = parsed.Host
	}
	if host == "api.github.com" {
		return "github.com"
	}
	return host
}
