//go:build linux

package core

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDurableExternalRuntimeSpecValidationAndHashStable(t *testing.T) {
	t.Parallel()

	left := NormalizeDurableExternalRuntimeSpec(DurableExternalRuntimeSpec{
		Kind:          "OpenClaw",
		Mode:          "gateway-presence",
		InstallRoot:   "/var/lib/aphelion/children/a/openclaw",
		StateRoot:     "/var/lib/aphelion/children/a/state",
		WorkspaceRoot: "/var/lib/aphelion/children/a/work",
		Source: RuntimeSourceRef{
			Kind: "git",
			Repo: "https://github.com/openclaw/openclaw",
			Ref:  "c7295e417d5daec76c18fb452d117f7b8eadc4d6",
			Integrity: map[string]string{
				"commit": "c7295e417d5daec76c18fb452d117f7b8eadc4d6",
				"tree":   "sha256:tree",
			},
		},
		Env: map[string]string{"OPENCLAW_STATE_DIR": "/var/lib/aphelion/children/a/state", "OPENCLAW_CONFIG_PATH": "/var/lib/aphelion/children/a/config.toml"},
	})
	right := NormalizeDurableExternalRuntimeSpec(left)
	right.Source.Integrity = map[string]string{"tree": "sha256:tree", "commit": "c7295e417d5daec76c18fb452d117f7b8eadc4d6"}
	right.Env = map[string]string{"OPENCLAW_CONFIG_PATH": "/var/lib/aphelion/children/a/config.toml", "OPENCLAW_STATE_DIR": "/var/lib/aphelion/children/a/state"}

	if err := ValidateDurableExternalRuntimeSpec(left); err != nil {
		t.Fatalf("ValidateDurableExternalRuntimeSpec(left) err = %v", err)
	}
	if err := ValidateDurableExternalRuntimeSpec(right); err != nil {
		t.Fatalf("ValidateDurableExternalRuntimeSpec(right) err = %v", err)
	}
	leftHash, err := StableExternalRuntimeContractHash(left)
	if err != nil {
		t.Fatalf("StableExternalRuntimeContractHash(left) err = %v", err)
	}
	rightHash, err := StableExternalRuntimeContractHash(right)
	if err != nil {
		t.Fatalf("StableExternalRuntimeContractHash(right) err = %v", err)
	}
	if leftHash != rightHash {
		t.Fatalf("hash mismatch for equivalent specs: left=%s right=%s", leftHash, rightHash)
	}
}

func TestGatewayDialogueAdmitsAllowedSenderAndAllowsSameConversationReply(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 7, 17, 0, 0, 0, time.UTC)
	contract := GatewayPresenceContract{
		RuntimeKind:                 "openclaw",
		Channel:                     "whatsapp",
		Account:                     "audience-line",
		DialogueMode:                GatewayDialogueModeDirectChildPersona,
		SameConversationReplyPolicy: GatewaySameConversationReplyAllowAdmitted,
		EffectBoundary:              GatewayEffectBoundaryAphelionBrokered,
		AllowedSenderIDs:            []string{"+15551234567"},
		UnknownSenderBehavior:       "pairing_only_no_memory",
		MemoryAdmission:             "after_review_principal",
		StateRoot:                   "/var/lib/aphelion/children/audience/openclaw",
	}
	turn, err := DialogueTurnFromGatewayEvent(contract, GatewayEvent{
		RuntimeKind:        "openclaw",
		Channel:            "whatsapp",
		Account:            "audience-line",
		SenderID:           "+15551234567",
		TransportMessageID: "wamid.1",
		Text:               "Can you draft today's update?",
	}, now)
	if err != nil {
		t.Fatalf("DialogueTurnFromGatewayEvent() err = %v", err)
	}
	if !turn.Admitted || turn.AdmissionDecision != "admitted" || turn.MemoryAdmission != "after_review_principal" {
		t.Fatalf("turn = %#v, want admitted direct dialogue", turn)
	}
	reply, err := SameConversationReplyFromDialogue(contract, turn, "Yes, I can draft it here.", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("SameConversationReplyFromDialogue() err = %v", err)
	}
	if reply.Channel != "whatsapp" || reply.SenderID != "+15551234567" || reply.GatewayContractHash != turn.GatewayContractHash {
		t.Fatalf("reply = %#v, want same-conversation reply bound to turn contract", reply)
	}
}

func TestGatewayDialogueRejectsUnknownSenderWithoutParentAuthority(t *testing.T) {
	t.Parallel()

	contract := GatewayPresenceContract{
		RuntimeKind:                 "hermes",
		Channel:                     "telegram",
		Account:                     "support-bot",
		DialogueMode:                GatewayDialogueModeDirectChildPersona,
		SameConversationReplyPolicy: GatewaySameConversationReplyAllowAdmitted,
		EffectBoundary:              GatewayEffectBoundaryAphelionBrokered,
		AllowedSenderIDs:            []string{"telegram:123"},
		UnknownSenderBehavior:       "pairing_only_no_memory",
	}
	turn, err := DialogueTurnFromGatewayEvent(contract, GatewayEvent{
		RuntimeKind:        "hermes",
		Channel:            "telegram",
		Account:            "support-bot",
		SenderID:           "telegram:999",
		TransportMessageID: "msg-1",
		Text:               "hello",
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("DialogueTurnFromGatewayEvent() err = %v", err)
	}
	if turn.Admitted || turn.AdmissionDecision != "pairing_only_no_memory" {
		t.Fatalf("turn = %#v, want unknown sender not admitted", turn)
	}
	if _, err := SameConversationReplyFromDialogue(contract, turn, "hello", time.Now().UTC()); err == nil {
		t.Fatal("SameConversationReplyFromDialogue() err = nil, want rejected non-admitted turn")
	}
}

func TestEffectRequestCompilesToDiscoveredEffectContract(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 7, 17, 0, 0, 0, time.UTC)
	turn := DialogueTurn{
		TurnID:    "dialogue_turn_01J",
		SenderID:  "+15551234567",
		Channel:   "whatsapp",
		Account:   "audience-line",
		Admitted:  true,
		CreatedAt: now,
	}
	req, err := EffectRequestFromDialogue(turn, EffectRequestInput{
		AgentID:  "audience-child",
		Action:   "gmail.search",
		Provider: "gog",
		Purpose:  "Find today's update messages.",
		Constraints: map[string]json.RawMessage{
			"accounts":          json.RawMessage(`["updates@example.com"]`),
			"max_messages":      json.RawMessage(`50`),
			"forbidden_actions": json.RawMessage(`["gmail.send","gmail.delete"]`),
		},
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("EffectRequestFromDialogue() err = %v", err)
	}
	if req.Source != ExternalRuntimeEffectSourceGatewayDialogue || req.RequestedBy != "sender:+15551234567" {
		t.Fatalf("effect request = %#v, want child-local request without authority", req)
	}
	contract, err := DiscoveredEffectContractFromRequest(req, DiscoveredEffectContractOptions{
		ReviewRoute: "resource_owner_principal",
		Materializes: GrantMaterialization{
			LeaseKind:  ExternalRuntimeLeaseKindToolInvocation,
			TTLSeconds: 900,
			SingleUse:  true,
		},
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("DiscoveredEffectContractFromRequest() err = %v", err)
	}
	if contract.ContractKind != ExternalRuntimeContractKindExternalEffect || contract.Provider != "gog" || contract.Action != "gmail.search" || !contract.Materializes.SingleUse {
		t.Fatalf("contract = %#v, want exact discovered effect contract", contract)
	}
}

func TestMaterializeWorkAgreementLeasesOnlyMatchingGrants(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 7, 17, 0, 0, 0, time.UTC)
	materialization, err := MaterializeWorkAgreementLeases(WorkAgreement{
		ID:                  "wa_daily_audience_update",
		Version:             3,
		AgentID:             "audience-child",
		Status:              "active",
		ConditionalGrantIDs: []string{"grant_gmail_read", "grant_whatsapp_draft"},
		Principals: WorkAgreementPrincipals{
			AuthorityPrincipal: "aphelion_admin:ops",
			ReviewPrincipal:    "customer:acme:comms-owner",
		},
	}, []ConditionalGrant{
		{
			ID:                   "grant_gmail_read",
			WorkAgreementID:      "wa_daily_audience_update",
			WorkAgreementVersion: 3,
			Capability:           "gmail_read",
			Actions:              []string{"gmail.search", "gmail.read"},
			Conditions:           ConditionalGrantConditions{Triggers: []string{"schedule:wa_daily_audience_update"}},
			Materializes:         GrantMaterialization{LeaseKind: ExternalRuntimeLeaseKindToolInvocation, TTLSeconds: 900, ReviewRoute: "resource_owner_principal", SingleUse: true},
			Status:               "active",
		},
		{
			ID:                   "grant_whatsapp_draft",
			WorkAgreementID:      "wa_daily_audience_update",
			WorkAgreementVersion: 2,
			Capability:           "channel_draft",
			Actions:              []string{"channel.draft"},
			Conditions:           ConditionalGrantConditions{Triggers: []string{"schedule:wa_daily_audience_update"}},
			Materializes:         GrantMaterialization{LeaseKind: ExternalRuntimeLeaseKindRuntimeTask, TTLSeconds: 900, ReviewRoute: "review_principal"},
			Status:               "active",
		},
	}, "schedule:wa_daily_audience_update", "sha256:runtime", now)
	if err != nil {
		t.Fatalf("MaterializeWorkAgreementLeases() err = %v", err)
	}
	if len(materialization.IssuedLeases) != 1 {
		t.Fatalf("issued leases = %#v, want only matching work agreement version", materialization.IssuedLeases)
	}
	lease := materialization.IssuedLeases[0]
	if lease.ConditionalGrantID != "grant_gmail_read" || !lease.SingleUse || !lease.ExpiresAt.Equal(now.Add(15*time.Minute)) {
		t.Fatalf("lease = %#v, want gmail_read single-use 15m lease", lease)
	}
}

func TestDiscoveredEffectContractRejectsInvalidDynamicConstraints(t *testing.T) {
	t.Parallel()

	req := EffectRequest{
		ID:             "effect_req_bad",
		AgentID:        "audience-child",
		Source:         ExternalRuntimeEffectSourceGatewayDialogue,
		DialogueTurnID: "dialogue_turn_01J",
		RequestedBy:    "sender:+15551234567",
		Action:         "gmail.search",
		Provider:       "gog",
		Constraints:    map[string]json.RawMessage{"query": json.RawMessage(`{"broken"`)},
	}
	_, err := DiscoveredEffectContractFromRequest(req, DiscoveredEffectContractOptions{ReviewRoute: "resource_owner_principal"})
	if err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("DiscoveredEffectContractFromRequest() err = %v, want invalid JSON constraint rejection", err)
	}
}

func TestExternalRuntimeTaskPacketPayloadAndParentMemoryAdmissionValidate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 7, 17, 0, 0, 0, time.UTC)
	effect := DiscoveredEffectContract{
		ID:                    "effect_contract_01J",
		AgentID:               "audience-child",
		SourceEffectRequestID: "effect_req_01J",
		ContractKind:          ExternalRuntimeContractKindExternalEffect,
		Provider:              "gog",
		Action:                "gmail.search",
		ReviewRoute:           "resource_owner_principal",
		Constraints:           map[string]json.RawMessage{"query": json.RawMessage(`"newer_than:1d"`)},
		Materializes:          GrantMaterialization{LeaseKind: ExternalRuntimeLeaseKindToolInvocation, TTLSeconds: 900, SingleUse: true},
		ExpectedResult:        ExpectedEffectResult{Kind: "effect_result", ArtifactPolicy: "bounded_redacted_summary"},
		CreatedAt:             now,
	}
	materialization := LeaseMaterialization{
		ID:                   "lm_01J",
		AgentID:              "audience-child",
		WorkAgreementID:      "wa_daily_audience_update",
		WorkAgreementVersion: 3,
		RuntimeSpecHash:      "sha256:runtime",
		IssuedLeases: []MaterializedLease{{
			LeaseID:                 "lease_01J",
			ConditionalGrantID:      "grant_gmail_read",
			ConditionalGrantVersion: 3,
			Capability:              "gmail_read",
			LeaseKind:               ExternalRuntimeLeaseKindToolInvocation,
			SingleUse:               true,
			ExpiresAt:               now.Add(15 * time.Minute),
		}},
		CreatedAt: now,
	}
	payload, err := ExternalRuntimeTaskPacketPayloadFromDiscoveredEffect(effect, materialization)
	if err != nil {
		t.Fatalf("ExternalRuntimeTaskPacketPayloadFromDiscoveredEffect() err = %v", err)
	}
	if err := ValidateExternalRuntimeTaskPacketPayload(payload); err != nil {
		t.Fatalf("ValidateExternalRuntimeTaskPacketPayload() err = %v", err)
	}
	if ExternalRuntimeTaskPacketSchemaV1 != "aphelion.child_task_packet.v1" {
		t.Fatalf("ExternalRuntimeTaskPacketSchemaV1 = %q, want child task packet schema", ExternalRuntimeTaskPacketSchemaV1)
	}
	if payload.Schema != ExternalRuntimeTaskPacketSchemaV1 {
		t.Fatalf("payload schema = %q, want child task packet schema", payload.Schema)
	}
	mismatchedPayload := payload
	mismatchedPayload.Authority[0].LeaseID = "lease_wrong"
	if err := ValidateExternalRuntimeTaskPacketPayload(mismatchedPayload); err == nil || !strings.Contains(err.Error(), "authority lease") {
		t.Fatalf("ValidateExternalRuntimeTaskPacketPayload(mismatch) err = %v, want authority lease mismatch", err)
	}
	resultPayload := ExternalRuntimeTaskResultPayload{
		Schema:           ExternalRuntimeTaskResultSchemaV1,
		AgentID:          "audience-child",
		TaskPacketID:     "packet_01J",
		EffectContractID: effect.ID,
		Status:           "completed",
		Summary:          "Fetched matching messages.",
		EffectResult: EffectResult{
			ResultID:         "effect_result_01J",
			EffectContractID: effect.ID,
			Status:           "completed",
			Summary:          "Fetched matching messages.",
			CreatedAt:        now,
		},
	}
	if err := ValidateExternalRuntimeTaskResultPayload(resultPayload); err != nil {
		t.Fatalf("ValidateExternalRuntimeTaskResultPayload() err = %v", err)
	}
	if ExternalRuntimeTaskResultSchemaV1 != "aphelion.child_task_result.v1" {
		t.Fatalf("ExternalRuntimeTaskResultSchemaV1 = %q, want child task result schema", ExternalRuntimeTaskResultSchemaV1)
	}
	if resultPayload.Schema != ExternalRuntimeTaskResultSchemaV1 {
		t.Fatalf("result schema = %q, want child task result schema", resultPayload.Schema)
	}
	mismatchedResult := resultPayload
	mismatchedResult.EffectResult.EffectContractID = "effect_contract_other"
	if err := ValidateExternalRuntimeTaskResultPayload(mismatchedResult); err == nil || !strings.Contains(err.Error(), "effect contract mismatch") {
		t.Fatalf("ValidateExternalRuntimeTaskResultPayload(mismatch) err = %v, want effect contract mismatch", err)
	}
	admission := ParentMemoryAdmission{
		AdmissionID: "mem_01J",
		AgentID:     "audience-child",
		SourceKind:  "dialogue_turn",
		SourceID:    "dialogue_turn_01J",
		Summary:     "Customer asked for a daily update cadence.",
		ReviewRoute: "review_principal",
		RequestedAt: now,
	}
	if err := ValidateParentMemoryAdmission(admission); err != nil {
		t.Fatalf("ValidateParentMemoryAdmission() err = %v", err)
	}
	normalized := NormalizeParentMemoryAdmission(admission)
	if normalized.Status != ExternalRuntimeMemoryAdmissionStatusPending {
		t.Fatalf("memory admission status = %q, want pending", normalized.Status)
	}
}

func TestChildRuntimeAdapterWakeOperationFromTaskPacketBindsRuntimeSpec(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 7, 17, 0, 0, 0, time.UTC)
	spec := NormalizeDurableExternalRuntimeSpec(DurableExternalRuntimeSpec{
		Kind:      "openclaw",
		Mode:      ExternalRuntimeModeOneshot,
		StateRoot: "/var/lib/aphelion/children/audience/openclaw",
		Source: RuntimeSourceRef{
			Kind: "git",
			Repo: "https://github.com/openclaw/openclaw",
			Ref:  "c7295e417d5daec76c18fb452d117f7b8eadc4d6",
		},
	})
	specHash, err := StableExternalRuntimeContractHash(spec)
	if err != nil {
		t.Fatalf("StableExternalRuntimeContractHash() err = %v", err)
	}
	packet, err := ExternalRuntimeTaskPacketPayloadFromDiscoveredEffect(DiscoveredEffectContract{
		ID:                    "effect_contract_01J",
		AgentID:               "audience-child",
		SourceEffectRequestID: "effect_req_01J",
		ContractKind:          ExternalRuntimeContractKindExternalEffect,
		Provider:              "gog",
		Action:                "gmail.search",
		ReviewRoute:           "resource_owner_principal",
		Constraints:           map[string]json.RawMessage{"query": json.RawMessage(`"newer_than:1d"`)},
		Materializes:          GrantMaterialization{LeaseKind: ExternalRuntimeLeaseKindToolInvocation, TTLSeconds: 900, SingleUse: true},
		ExpectedResult:        ExpectedEffectResult{Kind: "effect_result", ArtifactPolicy: "bounded_redacted_summary"},
		CreatedAt:             now,
	}, LeaseMaterialization{
		ID:                   "lm_01J",
		AgentID:              "audience-child",
		WorkAgreementID:      "wa_daily_audience_update",
		WorkAgreementVersion: 3,
		RuntimeSpecHash:      specHash,
		IssuedLeases: []MaterializedLease{{
			LeaseID:                 "lease_01J",
			ConditionalGrantID:      "grant_gmail_read",
			ConditionalGrantVersion: 3,
			Capability:              "gmail_read",
			LeaseKind:               ExternalRuntimeLeaseKindToolInvocation,
			SingleUse:               true,
			ExpiresAt:               now.Add(15 * time.Minute),
		}},
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("ExternalRuntimeTaskPacketPayloadFromDiscoveredEffect() err = %v", err)
	}
	op, err := ChildRuntimeAdapterWakeOperationFromTaskPacket(spec, packet, "artifact:child-task-packet/packet_01J", "continuation_lease:lease_01J", now.Add(15*time.Minute))
	if err != nil {
		t.Fatalf("ChildRuntimeAdapterWakeOperationFromTaskPacket() err = %v", err)
	}
	if op.Operation != ChildRuntimeAdapterOperationWake || op.RuntimeKind != "openclaw" || op.SpecHash != specHash || op.InputRef == "" || op.AuthorityRef == "" {
		t.Fatalf("operation = %#v, want bounded wake operation", op)
	}
	changedSpec := spec
	changedSpec.Source.Ref = "different-ref"
	if _, err := ChildRuntimeAdapterWakeOperationFromTaskPacket(changedSpec, packet, "artifact:child-task-packet/packet_01J", "continuation_lease:lease_01J", now.Add(15*time.Minute)); err == nil || !strings.Contains(err.Error(), "runtime spec hash mismatch") {
		t.Fatalf("ChildRuntimeAdapterWakeOperationFromTaskPacket(changed spec) err = %v, want runtime spec hash mismatch", err)
	}
}
