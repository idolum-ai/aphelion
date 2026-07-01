//go:build linux

package session

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCompileAuthorityBundleContractRequiresBoundedShape(t *testing.T) {
	t.Parallel()

	base := AuthorityBundleContractInput{
		RequestInstanceID:             "bundle-request-1",
		SessionID:                     "telegram_dm:1001",
		Principal:                     "telegram:1001",
		Summary:                       "Finish one child setup/report cycle.",
		AllowedActions:                []string{"wake_named_child", "read_unread_messages"},
		ForbiddenActions:              []string{"credentials_or_tokens", "unbounded_retry_loop"},
		StopConditions:                []string{"stop after one report", "stop on typed blocker"},
		PrimaryContinuationContractID: "crec-child-wake",
		CreatedAt:                     time.Unix(100, 0).UTC(),
	}
	for _, tc := range []struct {
		name string
		edit func(*AuthorityBundleContractInput)
		want string
	}{
		{name: "request instance", edit: func(in *AuthorityBundleContractInput) { in.RequestInstanceID = "" }, want: "request_instance_id"},
		{name: "session", edit: func(in *AuthorityBundleContractInput) { in.SessionID = "" }, want: "session_id"},
		{name: "principal", edit: func(in *AuthorityBundleContractInput) { in.Principal = "" }, want: "principal"},
		{name: "summary", edit: func(in *AuthorityBundleContractInput) { in.Summary = "" }, want: "summary"},
		{name: "allowed", edit: func(in *AuthorityBundleContractInput) { in.AllowedActions = nil }, want: "allowed_actions"},
		{name: "forbidden", edit: func(in *AuthorityBundleContractInput) { in.ForbiddenActions = nil }, want: "forbidden_actions"},
		{name: "stops", edit: func(in *AuthorityBundleContractInput) { in.StopConditions = nil }, want: "stop_conditions"},
		{name: "component", edit: func(in *AuthorityBundleContractInput) { in.PrimaryContinuationContractID = "" }, want: "continuation contract or capability grant"},
		{name: "unregistered component", edit: func(in *AuthorityBundleContractInput) {
			in.Components = []AuthorityBundleComponent{{Kind: "mailbox_child_setup", RefID: "component-1"}}
		}, want: "component kind"},
		{name: "contradiction", edit: func(in *AuthorityBundleContractInput) { in.ForbiddenActions = []string{"wake_named_child"} }, want: "cannot be both allowed and forbidden"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			input := base
			tc.edit(&input)
			_, err := CompileAuthorityBundleContract(input)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("CompileAuthorityBundleContract() err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestAuthorityBundleContractStoreRoundTripsAndRejectsDivergentReplay(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Unix(200, 0).UTC()
	contract, err := CompileAuthorityBundleContract(AuthorityBundleContractInput{
		RequestInstanceID:             "bundle-request-store",
		SessionID:                     "telegram_dm:1001",
		Principal:                     "telegram:1001",
		Objective:                     "Finish child setup and report.",
		Summary:                       "One bounded child setup/report cycle.",
		SourceNextActionRecordIDs:     []string{"next-child-wake", "next-grant"},
		AllowedActions:                []string{"wake_named_child", "read_unread_messages"},
		ForbiddenActions:              []string{"credentials_or_tokens", "send_mail"},
		StopConditions:                []string{"stop after one report", "stop on typed blocker"},
		PrimaryContinuationContractID: "crec-child-wake",
		RequiredCapabilityGrants: []CapabilityGrantSpec{{
			RequestID:      "capreq-mail-read",
			GrantID:        "capg-mail-read",
			Kind:           CapabilityKindExternalAccount,
			TargetResource: "mailbox:host@example.test",
			GrantedTo:      "durable_agent:mail-child",
			AllowedActions: []string{"read", "archive"},
		}},
		Components: []AuthorityBundleComponent{{Kind: "next_action", RefID: "next-child-wake", Subject: "continuation_lease_request", SubjectRef: "child_wake/mail-child"}},
		ExpiresAt:  now.Add(time.Hour),
		CreatedAt:  now,
	})
	if err != nil {
		t.Fatalf("CompileAuthorityBundleContract() err = %v", err)
	}
	stored, err := store.UpsertAuthorityBundleContract(contract)
	if err != nil {
		t.Fatalf("UpsertAuthorityBundleContract() err = %v", err)
	}
	replayed, err := store.UpsertAuthorityBundleContract(contract)
	if err != nil {
		t.Fatalf("UpsertAuthorityBundleContract(replay) err = %v", err)
	}
	if replayed.BundleID != stored.BundleID || replayed.ContractHash != stored.ContractHash {
		t.Fatalf("replayed = %#v, want same identity as %#v", replayed, stored)
	}
	fetched, ok, err := store.AuthorityBundleContract(stored.BundleID)
	if err != nil {
		t.Fatalf("AuthorityBundleContract() err = %v", err)
	}
	if !ok {
		t.Fatal("AuthorityBundleContract() ok=false")
	}
	if fetched.Summary != contract.Summary || fetched.PrimaryContinuationContractID != "crec-child-wake" || len(fetched.RequiredCapabilityGrants) != 1 || len(fetched.Components) != 1 {
		t.Fatalf("fetched = %#v, want round-tripped contract", fetched)
	}

	conflict := contract
	conflict.Summary = "Changed after identity was issued."
	if _, err := store.UpsertAuthorityBundleContract(conflict); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("UpsertAuthorityBundleContract(conflict) err = %v, want canonical mismatch", err)
	}
}

func TestAuthorityBundleSessionIDIsContractIdentityTerm(t *testing.T) {
	t.Parallel()

	now := time.Unix(250, 0).UTC()
	base := AuthorityBundleContractInput{
		RequestInstanceID:             "bundle-session-contract",
		SessionID:                     "telegram_dm:1001",
		Principal:                     "telegram:1001",
		Objective:                     "Finish child setup and report.",
		Summary:                       "One bounded child setup/report cycle.",
		AllowedActions:                []string{"wake_named_child", "read_unread_messages"},
		ForbiddenActions:              []string{"credentials_or_tokens", "send_mail"},
		StopConditions:                []string{"stop after one report", "stop on typed blocker"},
		PrimaryContinuationContractID: "crec-child-wake",
		CreatedAt:                     now,
	}
	first, err := CompileAuthorityBundleContract(base)
	if err != nil {
		t.Fatalf("CompileAuthorityBundleContract(first) err = %v", err)
	}
	secondInput := base
	secondInput.SessionID = "telegram_dm:2002"
	second, err := CompileAuthorityBundleContract(secondInput)
	if err != nil {
		t.Fatalf("CompileAuthorityBundleContract(second) err = %v", err)
	}
	if first.ContractHash == second.ContractHash || first.BundleID == second.BundleID {
		t.Fatalf("session_id did not change contract identity: first=%s/%s second=%s/%s", first.BundleID, first.ContractHash, second.BundleID, second.ContractHash)
	}
}

func TestAuthorityBundleExpiryIsContractIdentityTerm(t *testing.T) {
	t.Parallel()

	now := time.Unix(300, 0).UTC()
	base := AuthorityBundleContractInput{
		RequestInstanceID:             "bundle-expiry-contract",
		SessionID:                     "telegram_dm:1001",
		Principal:                     "telegram:1001",
		Objective:                     "Finish child setup and report.",
		Summary:                       "One bounded child setup/report cycle.",
		AllowedActions:                []string{"wake_named_child", "read_unread_messages"},
		ForbiddenActions:              []string{"credentials_or_tokens", "send_mail"},
		StopConditions:                []string{"stop after one report", "stop on typed blocker"},
		PrimaryContinuationContractID: "crec-child-wake",
		ExpiresAt:                     now.Add(time.Hour),
		CreatedAt:                     now,
	}
	first, err := CompileAuthorityBundleContract(base)
	if err != nil {
		t.Fatalf("CompileAuthorityBundleContract(first) err = %v", err)
	}
	secondInput := base
	secondInput.ExpiresAt = now.Add(2 * time.Hour)
	second, err := CompileAuthorityBundleContract(secondInput)
	if err != nil {
		t.Fatalf("CompileAuthorityBundleContract(second) err = %v", err)
	}
	if first.ContractHash == second.ContractHash || first.BundleID == second.BundleID {
		t.Fatalf("expiry did not change contract identity: first=%s/%s second=%s/%s", first.BundleID, first.ContractHash, second.BundleID, second.ContractHash)
	}
}

func TestAuthorityBundleContractRejectsTamperedStoredSessionID(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Unix(350, 0).UTC()
	contract, err := CompileAuthorityBundleContract(AuthorityBundleContractInput{
		RequestInstanceID:             "bundle-session-tamper",
		SessionID:                     "telegram_dm:1001",
		Principal:                     "telegram:1001",
		Summary:                       "One bounded child setup/report cycle.",
		AllowedActions:                []string{"wake_named_child"},
		ForbiddenActions:              []string{"credentials_or_tokens"},
		StopConditions:                []string{"stop after one report"},
		PrimaryContinuationContractID: "crec-child-wake",
		ExpiresAt:                     now.Add(time.Hour),
		CreatedAt:                     now,
	})
	if err != nil {
		t.Fatalf("CompileAuthorityBundleContract() err = %v", err)
	}
	if _, err := store.UpsertAuthorityBundleContract(contract); err != nil {
		t.Fatalf("UpsertAuthorityBundleContract() err = %v", err)
	}
	if _, err := store.db.Exec(`UPDATE authority_bundle_contracts SET session_id = ? WHERE bundle_id = ?`, "telegram_dm:2002", contract.BundleID); err != nil {
		t.Fatalf("tamper bundle session_id: %v", err)
	}
	if _, _, err := store.AuthorityBundleContract(contract.BundleID); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("AuthorityBundleContract() err = %v, want session hash mismatch", err)
	}
}

func TestAuthorityBundleContractRejectsTamperedStoredExpiry(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Unix(400, 0).UTC()
	contract, err := CompileAuthorityBundleContract(AuthorityBundleContractInput{
		RequestInstanceID:             "bundle-expiry-tamper",
		SessionID:                     "telegram_dm:1001",
		Principal:                     "telegram:1001",
		Summary:                       "One bounded child setup/report cycle.",
		AllowedActions:                []string{"wake_named_child"},
		ForbiddenActions:              []string{"credentials_or_tokens"},
		StopConditions:                []string{"stop after one report"},
		PrimaryContinuationContractID: "crec-child-wake",
		ExpiresAt:                     now.Add(time.Hour),
		CreatedAt:                     now,
	})
	if err != nil {
		t.Fatalf("CompileAuthorityBundleContract() err = %v", err)
	}
	if _, err := store.UpsertAuthorityBundleContract(contract); err != nil {
		t.Fatalf("UpsertAuthorityBundleContract() err = %v", err)
	}
	if _, err := store.db.Exec(`UPDATE authority_bundle_contracts SET expires_at = ? WHERE bundle_id = ?`, now.Add(2*time.Hour).Format(time.RFC3339Nano), contract.BundleID); err != nil {
		t.Fatalf("tamper bundle expiry: %v", err)
	}
	if _, _, err := store.AuthorityBundleContract(contract.BundleID); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("AuthorityBundleContract() err = %v, want expiry hash mismatch", err)
	}
}
