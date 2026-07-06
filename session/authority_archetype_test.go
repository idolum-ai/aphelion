//go:build linux

package session

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRedactedContinuationRecoveryArchetypeDoesNotExposeLiveValues(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)
	subjectRef := ContinuationRecoverySubjectRef(ContinuationLeaseClassChildWake, "private-email-agent", "grant-secret", "durable_agent", "wake_once", "")
	contract, err := CompileContinuationRecoveryContract(ContinuationRecoveryContractInput{
		RequestInstanceID:   "archetype-redaction",
		SessionID:           "telegram_dm:123456",
		SubjectKind:         "continuation_lease_request",
		SubjectRef:          subjectRef,
		Principal:           "telegram:123456",
		LeaseClass:          ContinuationLeaseClassChildWake,
		AllowedActions:      []string{"wake_named_child"},
		Constraints:         map[string]string{"agent_id": "private-email-agent", "mailbox": "host@example.com", "token_hint": "sk-live-canary"},
		Tool:                "durable_agent",
		ToolAction:          "wake_once",
		AgentID:             "private-email-agent",
		GrantID:             "grant-secret",
		GrantTargetResource: "durable_agent:private-email-agent:wake_once",
		RetryOperation: ContinuationRetryOperation{
			Contract:          ContinuationRecoveryRetryVersion,
			RequestInstanceID: "archetype-redaction",
			OperationKind:     "durable_agent_wake_once",
			Tool:              "durable_agent",
			InputJSON:         `{"action":"wake_once","agent_id":"private-email-agent","secret":"sk-live-canary","email":"host@example.com"}`,
			SubjectKind:       "continuation_lease_request",
			SubjectRef:        subjectRef,
		},
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("CompileContinuationRecoveryContract() err = %v", err)
	}

	archetype := RedactedContinuationRecoveryArchetype(contract)
	raw, err := json.Marshal(archetype)
	if err != nil {
		t.Fatalf("Marshal(archetype) err = %v", err)
	}
	text := string(raw)
	for _, forbidden := range []string{"private-email-agent", "host@example.com", "sk-live-canary", "grant-secret", "private-subject-ref", "telegram:123456"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("archetype leaked %q in %s", forbidden, text)
		}
	}
	for _, want := range []string{"child_wake", "continuation_lease_request", "durable_agent", "wake_once", "agent_id", "mailbox", "token_hint"} {
		if !strings.Contains(text, want) {
			t.Fatalf("archetype = %s, want class/key %q", text, want)
		}
	}
}

func TestRedactedAuthorityBundleArchetypeDoesNotExposeGrantValues(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)
	bundle, err := CompileAuthorityBundleContract(AuthorityBundleContractInput{
		RequestInstanceID: "bundle-redaction",
		SessionID:         "telegram_dm:123456",
		Principal:         "telegram:123456",
		Objective:         "Read private repo and mailbox names.",
		Summary:           "Private summary mentioning host@example.com and idolum-ai/private-repo.",
		AllowedActions:    []string{"github_issue_create", "mail_read"},
		ForbiddenActions:  []string{"publish_secret"},
		StopConditions:    []string{"stop before contacting host@example.com"},
		RequiredCapabilityGrants: []CapabilityGrantSpec{{
			RequestID:      "cap-secret",
			GrantID:        "grant-secret",
			Kind:           CapabilityKindExternalAccount,
			TargetResource: "github:idolum-ai/private-repo",
			GrantedTo:      "telegram:123456",
			AllowedActions: []string{"github_issue_create"},
			Contract:       `{"repo":"idolum-ai/private-repo"}`,
			Constraints:    `{"email":"host@example.com","token":"sk-live-canary"}`,
		}},
		Components: []AuthorityBundleComponent{{Kind: AuthorityBundleComponentKindOperationPhase, RefID: "phase-private", Subject: "operation", SubjectRef: "op-private"}},
		ExpiresAt:  now.Add(time.Hour),
		CreatedAt:  now,
	})
	if err != nil {
		t.Fatalf("CompileAuthorityBundleContract() err = %v", err)
	}

	archetype := RedactedAuthorityBundleArchetype(bundle)
	raw, err := json.Marshal(archetype)
	if err != nil {
		t.Fatalf("Marshal(archetype) err = %v", err)
	}
	text := string(raw)
	for _, forbidden := range []string{"host@example.com", "idolum-ai/private-repo", "sk-live-canary", "telegram:123456", "cap-secret", "grant-secret", "phase-private", "op-private"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("archetype leaked %q in %s", forbidden, text)
		}
	}
	for _, want := range []string{"authority_bundle", "external_account", "github", "github_issue_create", "operation_phase"} {
		if !strings.Contains(text, want) {
			t.Fatalf("archetype = %s, want class token %q", text, want)
		}
	}
}
