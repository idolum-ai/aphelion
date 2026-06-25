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
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func TestSystemLogReadRunsJournalctlWithBoundedLiteralFilters(t *testing.T) {
	orig := runSystemLogCommand
	defer func() { runSystemLogCommand = orig }()
	var gotArgs []string
	runSystemLogCommand = func(_ context.Context, args []string) (systemLogCommandResult, error) {
		gotArgs = append([]string(nil), args...)
		return systemLogCommandResult{Stdout: strings.Join([]string{
			"2026-06-25T00:00:00Z aphelion[1]: unrelated",
			"2026-06-25T00:00:01Z aphelion[1]: idolum-email Authorization: Bearer bearer-secret-value",
			"2026-06-25T00:00:02Z aphelion[1]: continuation ok",
		}, "\n")}, nil
	}
	registry := NewRegistry(t.TempDir(), time.Second)
	out, err := registry.executeWithScopeAndPrincipal(
		context.Background(),
		"system_log_read",
		json.RawMessage(`{"unit":"aphelion.service","since":"2 hours ago","include":["idolum-email","continuation"],"limit":10}`),
		sandbox.Scope{WorkingRoot: t.TempDir(), SharedMemoryRoot: t.TempDir()},
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		session.SessionKey{ChatID: 1001, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "1001"}},
	)
	if err != nil {
		t.Fatalf("system_log_read err = %v", err)
	}
	wantArgs := []string{"--no-pager", "--output=short-iso", "--user", "-u", "aphelion.service", "--since", "2 hours ago"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("journalctl args = %#v, want %#v", gotArgs, wantArgs)
	}
	if strings.Contains(out, "unrelated") {
		t.Fatalf("output = %q, want include filter applied", out)
	}
	if strings.Contains(out, "bearer-secret-value") || !strings.Contains(out, "<redacted:bearer:") {
		t.Fatalf("output = %q, want bearer redaction", out)
	}
	if !strings.Contains(out, "partial: false") || !strings.Contains(out, "status: ok") {
		t.Fatalf("output = %q, want bounded status metadata", out)
	}
}

func TestSystemLogReadRequiresAdminDiagnosticScope(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(t.TempDir(), time.Second)
	_, err := registry.executeWithScopeAndPrincipal(
		context.Background(),
		"system_log_read",
		json.RawMessage(`{"unit":"aphelion.service"}`),
		sandbox.Scope{WorkingRoot: t.TempDir(), SharedMemoryRoot: t.TempDir()},
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		session.SessionKey{ChatID: -200, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramGroup, ID: "-200"}},
	)
	if err == nil || !strings.Contains(err.Error(), "admin diagnostic scope") {
		t.Fatalf("group system_log_read err = %v, want admin diagnostic scope rejection", err)
	}
	_, err = registry.executeWithScopeAndPrincipal(
		context.Background(),
		"system_log_read",
		json.RawMessage(`{"unit":"aphelion.service"}`),
		sandbox.Scope{WorkingRoot: t.TempDir(), SharedMemoryRoot: t.TempDir()},
		principal.Principal{Role: principal.RoleApprovedUser, TelegramUserID: 1001},
		session.SessionKey{ChatID: 1001, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "1001"}},
	)
	if err == nil || !strings.Contains(err.Error(), "admin diagnostic scope") {
		t.Fatalf("approved user system_log_read err = %v, want admin diagnostic scope rejection", err)
	}
}

func TestSystemLogReadReturnsSafeFailureProjection(t *testing.T) {
	orig := runSystemLogCommand
	defer func() { runSystemLogCommand = orig }()
	runSystemLogCommand = func(_ context.Context, _ []string) (systemLogCommandResult, error) {
		return systemLogCommandResult{Stderr: "failed Authorization: Bearer bearer-secret-value"}, errors.New("journalctl failed with Authorization: Bearer bearer-secret-value")
	}
	registry := NewRegistry(t.TempDir(), time.Second)
	out, err := registry.executeWithScopeAndPrincipal(
		context.Background(),
		"system_log_read",
		json.RawMessage(`{"unit":"aphelion.service"}`),
		sandbox.Scope{WorkingRoot: t.TempDir(), SharedMemoryRoot: t.TempDir()},
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		session.SessionKey{ChatID: 1001, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "1001"}},
	)
	if err == nil {
		t.Fatal("system_log_read err = nil, want journalctl failure")
	}
	if strings.Contains(err.Error(), "bearer-secret-value") || !strings.Contains(err.Error(), "<redacted:bearer:") {
		t.Fatalf("failure err = %q, want redacted error object", err)
	}
	if strings.Contains(out, "bearer-secret-value") || !strings.Contains(out, "<redacted:bearer:") {
		t.Fatalf("failure output = %q, want redacted stderr/error metadata", out)
	}
}
