//go:build linux

package githubapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

const (
	DefaultAPIBaseURL = "https://api.github.com"
	DefaultAPIVersion = "2026-03-10"
	DefaultUserAgent  = "aphelion"

	jwtBackdate = 60 * time.Second
	jwtTTL      = 9 * time.Minute
)

type App struct {
	Name                 string
	AppID                int64
	InstallationID       int64
	PrivateKeyFile       string
	Repositories         []string
	Permissions          []string
	AllowAllRepositories bool
	AllowAllPermissions  bool
}

type Client struct {
	HTTPClient  *http.Client
	APIBaseURL  string
	APIVersion  string
	UserAgent   string
	Now         func() time.Time
	ReadKeyFile func(string) ([]byte, error)
}

type InstallationToken struct {
	Token        string            `json:"token,omitempty"`
	ExpiresAt    time.Time         `json:"expires_at,omitempty"`
	Permissions  map[string]string `json:"permissions,omitempty"`
	Repositories []string          `json:"repositories,omitempty"`
}

type tokenResponse struct {
	Token        string            `json:"token"`
	ExpiresAt    time.Time         `json:"expires_at"`
	Permissions  map[string]string `json:"permissions"`
	Repositories []struct {
		FullName string `json:"full_name"`
		Name     string `json:"name"`
	} `json:"repositories"`
}

func NewClient(opts Client) *Client {
	client := opts
	if client.HTTPClient == nil {
		client.HTTPClient = http.DefaultClient
	}
	if strings.TrimSpace(client.APIBaseURL) == "" {
		client.APIBaseURL = DefaultAPIBaseURL
	}
	client.APIBaseURL = strings.TrimRight(strings.TrimSpace(client.APIBaseURL), "/")
	if strings.TrimSpace(client.APIVersion) == "" {
		client.APIVersion = DefaultAPIVersion
	}
	if strings.TrimSpace(client.UserAgent) == "" {
		client.UserAgent = DefaultUserAgent
	}
	if client.Now == nil {
		client.Now = func() time.Time { return time.Now().UTC() }
	}
	if client.ReadKeyFile == nil {
		client.ReadKeyFile = os.ReadFile
	}
	return &client
}

func (c *Client) MintInstallationToken(ctx context.Context, app App) (InstallationToken, error) {
	if c == nil {
		c = NewClient(Client{})
	}
	client := NewClient(*c)
	if app.AppID <= 0 {
		return InstallationToken{}, fmt.Errorf("github app id is required")
	}
	if app.InstallationID <= 0 {
		return InstallationToken{}, fmt.Errorf("github app installation id is required")
	}
	keyPath := strings.TrimSpace(app.PrivateKeyFile)
	if keyPath == "" {
		return InstallationToken{}, fmt.Errorf("github app private key file is required")
	}
	keyBytes, err := client.ReadKeyFile(keyPath)
	if err != nil {
		return InstallationToken{}, fmt.Errorf("read github app private key: %w", err)
	}
	key, err := ParsePrivateKeyPEM(keyBytes)
	if err != nil {
		return InstallationToken{}, err
	}
	jwt, err := GenerateJWT(app.AppID, key, client.Now())
	if err != nil {
		return InstallationToken{}, err
	}
	body, err := tokenRequestBody(app)
	if err != nil {
		return InstallationToken{}, err
	}
	endpoint, err := client.installationTokenURL(app.InstallationID)
	if err != nil {
		return InstallationToken{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return InstallationToken{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", client.UserAgent)
	req.Header.Set("X-GitHub-Api-Version", client.APIVersion)

	resp, err := client.HTTPClient.Do(req)
	if err != nil {
		return InstallationToken{}, fmt.Errorf("github app installation token request: %w", err)
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return InstallationToken{}, fmt.Errorf("read github app installation token response: %w", readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return InstallationToken{}, fmt.Errorf("github app installation token request failed: status=%d body=%s", resp.StatusCode, Redact(string(raw)))
	}
	var decoded tokenResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return InstallationToken{}, fmt.Errorf("decode github app installation token response: %w", err)
	}
	if strings.TrimSpace(decoded.Token) == "" {
		return InstallationToken{}, fmt.Errorf("github app installation token response did not include a token")
	}
	out := InstallationToken{
		Token:        decoded.Token,
		ExpiresAt:    decoded.ExpiresAt,
		Permissions:  decoded.Permissions,
		Repositories: make([]string, 0, len(decoded.Repositories)),
	}
	for _, repo := range decoded.Repositories {
		if fullName := strings.TrimSpace(repo.FullName); fullName != "" {
			out.Repositories = append(out.Repositories, fullName)
		} else if name := strings.TrimSpace(repo.Name); name != "" {
			out.Repositories = append(out.Repositories, name)
		}
	}
	return out, nil
}

func (c *Client) installationTokenURL(installationID int64) (string, error) {
	base, err := url.Parse(strings.TrimRight(firstNonEmpty(c.APIBaseURL, DefaultAPIBaseURL), "/"))
	if err != nil {
		return "", fmt.Errorf("parse github api base url: %w", err)
	}
	base.Path = path.Join(base.Path, "app", "installations", fmt.Sprintf("%d", installationID), "access_tokens")
	return base.String(), nil
}

func tokenRequestBody(app App) ([]byte, error) {
	payload := map[string]any{}
	if !app.AllowAllRepositories {
		if len(app.Repositories) == 0 {
			return nil, fmt.Errorf("github app repositories are required unless allow_all_repositories is true")
		}
		repositories := make([]string, 0, len(app.Repositories))
		for _, repo := range app.Repositories {
			if !validRepository(repo) {
				return nil, fmt.Errorf("invalid github app repository %q", repo)
			}
			// Local authorization keeps owner/repo full names; GitHub's
			// installation-token request expects repository names only.
			repositories = append(repositories, RepositoryName(repo))
		}
		payload["repositories"] = repositories
	}
	if !app.AllowAllPermissions {
		permissions, err := PermissionMap(app.Permissions)
		if err != nil {
			return nil, err
		}
		if len(permissions) == 0 {
			return nil, fmt.Errorf("github app permissions are required unless allow_all_permissions is true")
		}
		payload["permissions"] = permissions
	}
	return json.Marshal(payload)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
