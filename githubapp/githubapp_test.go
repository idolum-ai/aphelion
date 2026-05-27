//go:build linux

package githubapp

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestParsePrivateKeyPEMAcceptsPKCS1AndPKCS8RSA(t *testing.T) {
	t.Parallel()
	key := testRSAKey(t)
	for _, tc := range []struct {
		name string
		pem  []byte
	}{
		{name: "pkcs1", pem: pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})},
		{name: "pkcs8", pem: testPKCS8PEM(t, key)},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParsePrivateKeyPEM(tc.pem)
			if err != nil {
				t.Fatalf("ParsePrivateKeyPEM() err = %v", err)
			}
			if got.N.Cmp(key.N) != 0 {
				t.Fatal("parsed key modulus mismatch")
			}
		})
	}
}

func TestGenerateJWTUsesRS256ClaimsAndSignature(t *testing.T) {
	t.Parallel()
	key := testRSAKey(t)
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	token, err := GenerateJWT(12345, key, now)
	if err != nil {
		t.Fatalf("GenerateJWT() err = %v", err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT parts = %d, want 3", len(parts))
	}
	var header map[string]string
	decodeJWTPart(t, parts[0], &header)
	if header["alg"] != "RS256" || header["typ"] != "JWT" {
		t.Fatalf("header = %#v, want RS256 JWT", header)
	}
	var claims map[string]float64
	decodeJWTPart(t, parts[1], &claims)
	if int64(claims["iss"]) != 12345 {
		t.Fatalf("iss = %#v, want 12345", claims["iss"])
	}
	if int64(claims["iat"]) != now.Add(-jwtBackdate).Unix() {
		t.Fatalf("iat = %#v", claims["iat"])
	}
	if int64(claims["exp"]) != now.Add(jwtTTL).Unix() {
		t.Fatalf("exp = %#v", claims["exp"])
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], signature); err != nil {
		t.Fatalf("VerifyPKCS1v15() err = %v", err)
	}
}

func TestMintInstallationTokenPostsScopedRequest(t *testing.T) {
	t.Parallel()
	key := testRSAKey(t)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/app/installations/42/access_tokens" {
			t.Fatalf("request = %s %s, want POST installation token endpoint", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Fatalf("Authorization = %q, want bearer JWT", got)
		}
		if got := r.Header.Get("X-GitHub-Api-Version"); got != "2026-03-10" {
			t.Fatalf("api version = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		repos, ok := body["repositories"].([]any)
		if !ok || len(repos) != 1 || repos[0] != "aphelion" {
			t.Fatalf("repositories = %#v, want GitHub API repository name only", body["repositories"])
		}
		perms, ok := body["permissions"].(map[string]any)
		if !ok || perms["contents"] != "read" || perms["metadata"] != "read" {
			t.Fatalf("permissions = %#v", body["permissions"])
		}
		return &http.Response{
			StatusCode: http.StatusCreated,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"token":"token-for-test","expires_at":"2026-05-23T13:00:00Z","permissions":{"contents":"read"},"repositories":[{"full_name":"idolum-ai/aphelion"}]}`)),
		}, nil
	})}

	client := NewClient(Client{
		HTTPClient:  httpClient,
		APIBaseURL:  "https://api.github.test",
		APIVersion:  DefaultAPIVersion,
		ReadKeyFile: func(string) ([]byte, error) { return keyPEM, nil },
		Now:         func() time.Time { return time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC) },
	})
	token, err := client.MintInstallationToken(context.Background(), App{
		AppID:          11,
		InstallationID: 42,
		PrivateKeyFile: "key.pem",
		Repositories:   []string{"idolum-ai/aphelion"},
		Permissions:    []string{"contents:read", "metadata:read"},
	})
	if err != nil {
		t.Fatalf("MintInstallationToken() err = %v", err)
	}
	if token.Token != "token-for-test" || len(token.Repositories) != 1 || token.Repositories[0] != "idolum-ai/aphelion" {
		t.Fatalf("token = %#v", token)
	}
	if strings.Contains(token.GoString(), "token-for-test") {
		t.Fatalf("GoString leaked token: %s", token.GoString())
	}
}

func TestTokenRequestBodyUsesRepositoryNamesOnly(t *testing.T) {
	t.Parallel()
	body, err := tokenRequestBody(App{
		Repositories: []string{"idolum-ai/aphelion"},
		Permissions:  []string{"contents:read"},
	})
	if err != nil {
		t.Fatalf("tokenRequestBody() err = %v", err)
	}
	var got struct {
		Repositories []string          `json:"repositories"`
		Permissions  map[string]string `json:"permissions"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("Unmarshal() err = %v", err)
	}
	if len(got.Repositories) != 1 || got.Repositories[0] != "aphelion" {
		t.Fatalf("repositories = %#v, want [aphelion]", got.Repositories)
	}
}

func TestSelectRepositoryNarrowsConfiguredScope(t *testing.T) {
	t.Parallel()
	app := App{Repositories: []string{"idolum-ai/aphelion", "idolum-ai/other"}}
	got, err := SelectRepository(app, "idolum-ai/aphelion")
	if err != nil {
		t.Fatalf("SelectRepository() err = %v", err)
	}
	if len(got.Repositories) != 1 || got.Repositories[0] != "idolum-ai/aphelion" {
		t.Fatalf("repositories = %#v, want narrowed repo", got.Repositories)
	}
	if _, err := SelectRepository(app, "elsewhere/repo"); err == nil {
		t.Fatal("SelectRepository(outside scope) err = nil, want error")
	}
	if _, err := SelectRepository(App{AllowAllRepositories: true}, "bad owner/repo"); err == nil {
		t.Fatal("SelectRepository(invalid broad repo) err = nil, want error")
	}
}

func TestRedactHidesGitHubTokens(t *testing.T) {
	t.Parallel()
	raw := "body includes " + "ghs_" + "12345.jwt-body.jwt_sig and github_pat_" + "TEST_REDACT_ME"
	if got := Redact(raw); strings.Contains(got, "jwt-body") || strings.Contains(got, "TEST_REDACT_ME") || !strings.Contains(got, "<redacted>") {
		t.Fatalf("Redact() = %q", got)
	}
}

func testRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() err = %v", err)
	}
	return key
}

func testPKCS8PEM(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	raw, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey() err = %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw})
}

func decodeJWTPart(t *testing.T, raw string, out any) {
	t.Helper()
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		t.Fatalf("decode jwt part: %v", err)
	}
	if err := json.Unmarshal(decoded, out); err != nil {
		t.Fatalf("unmarshal jwt part: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
