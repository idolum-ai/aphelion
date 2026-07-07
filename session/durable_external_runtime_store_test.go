//go:build linux

package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/idolum-ai/aphelion/core"
)

func TestMigratesSchemaV90ToV91DurableExternalRuntimeStorage(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sessions-v90.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_version(version INTEGER NOT NULL, applied_at TEXT NOT NULL DEFAULT (datetime('now'))) `); err != nil {
		t.Fatalf("create schema_version: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO schema_version(version) VALUES (90)`); err != nil {
		t.Fatalf("insert schema version: %v", err)
	}
	_ = db.Close()

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(v90) err = %v", err)
	}
	defer store.Close()
	assertSchemaVersion(t, store.db, schemaVersion)
	for _, table := range []string{
		"durable_child_runtime_specs",
		"durable_child_work_agreement_versions",
		"durable_child_conditional_grants",
		"durable_child_lease_materializations",
		"durable_child_work_agreement_amendments",
	} {
		assertSQLiteTable(t, store.db, table)
	}
	assertSQLiteIndex(t, store.db, "idx_durable_child_work_agreements_agent_status")
	assertSQLiteIndex(t, store.db, "idx_durable_child_lease_materializations_grant")
	if _, err := store.VerifyCriticalSchemaShape(); err != nil {
		t.Fatalf("VerifyCriticalSchemaShape() err = %v", err)
	}
}

func TestDurableExternalRuntimeDraftApprovalAndLeaseMaterialization(t *testing.T) {
	store := newTestSQLiteStore(t)
	now := time.Date(2026, 7, 7, 17, 0, 0, 0, time.UTC)
	spec, agreement, grants := durableExternalRuntimeFixture(now, 3)

	draft, err := store.CreateDurableExternalRuntimeWorkAgreementDraft(DurableExternalRuntimeWorkAgreementDraftInput{
		RuntimeSpec:         spec,
		WorkAgreement:       agreement,
		ConditionalGrants:   grants,
		SourceRequestID:     "cap-wa-daily",
		SourceReviewEventID: 42,
		CreatedAt:           now,
	})
	if err != nil {
		t.Fatalf("CreateDurableExternalRuntimeWorkAgreementDraft() err = %v", err)
	}
	if draft.WorkAgreement.Status != DurableChildWorkAgreementVersionStatusProposed {
		t.Fatalf("draft status = %q, want proposed", draft.WorkAgreement.Status)
	}
	if len(draft.ConditionalGrants) != 1 {
		t.Fatalf("draft grants len = %d, want 1", len(draft.ConditionalGrants))
	}
	if _, ok, err := store.ActiveDurableChildWorkAgreementVersion(agreement.AgentID, agreement.ID); err != nil || ok {
		t.Fatalf("ActiveDurableChildWorkAgreementVersion() ok=%t err=%v, want no active authority before approval", ok, err)
	}

	if _, err := store.UpsertCapabilityRequest(CapabilityRequest{
		RequestID:      "cap-wa-daily",
		RequestedBy:    "durable_agent:audience-child",
		RequestedFor:   "durable_agent:audience-child",
		Kind:           CapabilityKindGenericDelegation,
		TargetResource: "work_agreement:wa_daily_audience_update",
		Purpose:        "Approve daily audience work agreement version 3.",
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	if _, err := store.AppendCapabilityReview(CapabilityReview{
		ReviewID:     "review-wa-daily",
		RequestID:    "cap-wa-daily",
		Reviewer:     "admin:aphelion",
		ReviewerRole: "admin",
		Status:       CapabilityReviewStatusApproved,
		Rationale:    "Approve the bounded work agreement.",
		CreatedAt:    now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("AppendCapabilityReview() err = %v", err)
	}
	active, ok, err := store.ActiveDurableChildWorkAgreementVersion(agreement.AgentID, agreement.ID)
	if err != nil || !ok {
		t.Fatalf("ActiveDurableChildWorkAgreementVersion() ok=%t err=%v, want active", ok, err)
	}
	if active.AgreementHash != draft.WorkAgreement.AgreementHash {
		t.Fatalf("active hash = %q, want stable draft hash %q", active.AgreementHash, draft.WorkAgreement.AgreementHash)
	}
	if active.Agreement.Status != "" {
		t.Fatalf("stored agreement JSON status = %q, want status column to carry lifecycle", active.Agreement.Status)
	}

	materialized, err := store.MaterializeActiveWorkAgreementLeases(agreement.AgentID, agreement.ID, "schedule:"+agreement.ID, draft.RuntimeSpec.SpecHash, now.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("MaterializeActiveWorkAgreementLeases() err = %v", err)
	}
	if len(materialized) != 1 || materialized[0].AgreementVersion != 3 || materialized[0].ConditionalGrantVersion != 3 {
		t.Fatalf("materialized = %#v, want single version-fenced lease", materialized)
	}
	consumed, changed, err := store.ConsumeDurableChildLease(materialized[0].LeaseID, now.Add(6*time.Minute))
	if err != nil {
		t.Fatalf("ConsumeDurableChildLease() err = %v", err)
	}
	if !changed || consumed.Status != DurableChildLeaseMaterializationStatusConsumed || consumed.ConsumedAt.IsZero() {
		t.Fatalf("consumed = %#v changed=%t, want consumed lease", consumed, changed)
	}
	_, changed, err = store.ConsumeDurableChildLease(materialized[0].LeaseID, now.Add(7*time.Minute))
	if err != nil {
		t.Fatalf("ConsumeDurableChildLease(replay) err = %v", err)
	}
	if changed {
		t.Fatal("ConsumeDurableChildLease(replay) changed = true, want single-use completion")
	}
}

func TestWorkAgreementAmendmentApprovalActivatesNewVersionAndFencesOldLease(t *testing.T) {
	store := newTestSQLiteStore(t)
	now := time.Date(2026, 7, 7, 17, 0, 0, 0, time.UTC)
	spec, agreementV3, grantsV3 := durableExternalRuntimeFixture(now, 3)
	draftV3, err := store.CreateDurableExternalRuntimeWorkAgreementDraft(DurableExternalRuntimeWorkAgreementDraftInput{
		RuntimeSpec:       spec,
		WorkAgreement:     agreementV3,
		ConditionalGrants: grantsV3,
		SourceRequestID:   "cap-wa-v3",
		CreatedAt:         now,
	})
	if err != nil {
		t.Fatalf("draft v3 err = %v", err)
	}
	if err := store.UpdateDurableChildWorkAgreementStatusForRequest("cap-wa-v3", CapabilityReviewStatusApproved); err != nil {
		t.Fatalf("activate v3 err = %v", err)
	}
	leasedV3, err := store.MaterializeActiveWorkAgreementLeases(agreementV3.AgentID, agreementV3.ID, "schedule:"+agreementV3.ID, draftV3.RuntimeSpec.SpecHash, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("materialize v3 err = %v", err)
	}

	agreementV4 := agreementV3
	agreementV4.Version = 4
	agreementV4.ConditionalGrantIDs = []string{"grant_gmail_read_v4"}
	grantsV4 := []core.ConditionalGrant{grantsV3[0]}
	grantsV4[0].ID = "grant_gmail_read_v4"
	grantsV4[0].WorkAgreementVersion = 4
	grantsV4[0].Actions = append(grantsV4[0].Actions, "gmail.labels")
	if _, err := store.CreateDurableExternalRuntimeWorkAgreementDraft(DurableExternalRuntimeWorkAgreementDraftInput{
		RuntimeSpec:       spec,
		WorkAgreement:     agreementV4,
		ConditionalGrants: grantsV4,
		SourceRequestID:   "cap-wa-v4",
		CreatedAt:         now.Add(2 * time.Minute),
	}); err != nil {
		t.Fatalf("draft v4 err = %v", err)
	}
	_, err = store.UpsertDurableChildWorkAgreementAmendment(DurableChildWorkAgreementAmendmentRecord{
		Amendment: core.WorkAgreementAmendment{
			ID:              "amend-wa-v4",
			WorkAgreementID: agreementV3.ID,
			FromVersion:     3,
			ProposedVersion: 4,
			ProposedBy:      "durable_agent:audience-child",
			ChangeClass:     []string{"tool_action"},
			Diff:            map[string]json.RawMessage{"actions": json.RawMessage(`["gmail.labels"]`)},
			RiskDelta:       map[string]json.RawMessage{"risk": json.RawMessage(`"wider label access"`)},
		},
		SourceRequestID: "cap-wa-v4-amend",
		CreatedAt:       now.Add(2 * time.Minute),
		UpdatedAt:       now.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("UpsertDurableChildWorkAgreementAmendment() err = %v", err)
	}
	if err := store.UpdateDurableChildWorkAgreementAmendmentStatusForRequest("cap-wa-v4-amend", CapabilityReviewStatusApproved); err != nil {
		t.Fatalf("approve amendment err = %v", err)
	}
	v3, err := store.DurableChildWorkAgreementVersion(agreementV3.ID, 3)
	if err != nil {
		t.Fatalf("DurableChildWorkAgreementVersion(v3) err = %v", err)
	}
	v4, ok, err := store.ActiveDurableChildWorkAgreementVersion(agreementV3.AgentID, agreementV3.ID)
	if err != nil || !ok {
		t.Fatalf("ActiveDurableChildWorkAgreementVersion(v4) ok=%t err=%v", ok, err)
	}
	if v3.Status != DurableChildWorkAgreementVersionStatusSuperseded || v4.Version != 4 {
		t.Fatalf("v3=%#v v4=%#v, want v3 superseded and v4 active", v3, v4)
	}
	oldLease, ok, err := store.DurableChildLeaseMaterialization(leasedV3[0].LeaseID)
	if err != nil || !ok {
		t.Fatalf("DurableChildLeaseMaterialization(old) ok=%t err=%v", ok, err)
	}
	if oldLease.AgreementVersion != 3 || oldLease.ConditionalGrantVersion != 3 || oldLease.Status != DurableChildLeaseMaterializationStatusActive {
		t.Fatalf("old lease = %#v, want original version fence preserved", oldLease)
	}
}

func durableExternalRuntimeFixture(now time.Time, version int) (core.DurableExternalRuntimeSpec, core.WorkAgreement, []core.ConditionalGrant) {
	spec := core.DurableExternalRuntimeSpec{
		Kind:      "openclaw",
		Mode:      core.ExternalRuntimeModeOneshot,
		StateRoot: "/var/lib/aphelion/children/audience/openclaw",
		DependencyRoots: []core.DependencyRoot{
			{Kind: "node_modules", Path: "/var/lib/aphelion/children/audience/openclaw/deps/node_modules", Writable: true},
		},
		Source:         core.RuntimeSourceRef{Kind: "git", Repo: "https://github.com/openclaw/openclaw", Ref: "c7295e417d5daec76c18fb452d117f7b8eadc4d6"},
		NetworkClasses: []string{"provider_egress"},
	}
	agreement := core.WorkAgreement{
		ID:          "wa_daily_audience_update",
		Version:     version,
		AgentID:     "audience-child",
		Title:       "Daily audience update",
		RuntimeKind: "openclaw",
		Principals: core.WorkAgreementPrincipals{
			AuthorityPrincipal:      "admin:aphelion",
			ReviewPrincipal:         "customer:audience-owner",
			ResourceOwnerPrincipals: []string{"customer:audience-owner"},
		},
		Schedule:            core.ScheduleSpec{Kind: "cron", Expression: "0 13 * * *", Timezone: "America/New_York"},
		ReviewPolicy:        core.ReviewPolicy{DefaultOutbound: "draft_only", SendRequires: "review_principal"},
		ConditionalGrantIDs: []string{"grant_gmail_read"},
	}
	grant := core.ConditionalGrant{
		ID:                   "grant_gmail_read",
		WorkAgreementID:      agreement.ID,
		WorkAgreementVersion: version,
		Capability:           "gmail_read",
		Tool:                 "gog",
		Actions:              []string{"gmail.search", "gmail.read"},
		CredentialScope:      "gmail:audience",
		Conditions:           core.ConditionalGrantConditions{Triggers: []string{"schedule:" + agreement.ID}},
		Constraints:          map[string]json.RawMessage{"max_messages": json.RawMessage(`10`)},
		Materializes:         core.GrantMaterialization{LeaseKind: core.ExternalRuntimeLeaseKindToolInvocation, TTLSeconds: 900, ReviewRoute: "review_principal", SingleUse: true},
	}
	_ = now
	return spec, agreement, []core.ConditionalGrant{grant}
}

func TestDurableExternalRuntimeDraftRejectsInvalidRuntimeSpec(t *testing.T) {
	store := newTestSQLiteStore(t)
	spec, agreement, grants := durableExternalRuntimeFixture(time.Now().UTC(), 3)
	_, err := store.CreateDurableExternalRuntimeWorkAgreementDraft(DurableExternalRuntimeWorkAgreementDraftInput{
		RuntimeSpec: core.DurableExternalRuntimeSpec{
			Kind:      "openclaw",
			Mode:      core.ExternalRuntimeModeOneshot,
			StateRoot: "/var/lib/aphelion/children/audience/openclaw",
			Source:    core.RuntimeSourceRef{Kind: "git", Repo: "https://github.com/openclaw/openclaw", Ref: "c7295e417d5daec76c18fb452d117f7b8eadc4d6"},
		},
		WorkAgreement:     agreement,
		ConditionalGrants: grants,
		SourceRequestID:   "cap-invalid-runtime",
	})
	if err == nil || !strings.Contains(err.Error(), "dependency_roots") {
		t.Fatalf("CreateDurableExternalRuntimeWorkAgreementDraft(invalid runtime) err = %v, want dependency_roots rejection", err)
	}

	badGrants := []core.ConditionalGrant{grants[0]}
	badGrants[0].Actions = nil
	_, err = store.CreateDurableExternalRuntimeWorkAgreementDraft(DurableExternalRuntimeWorkAgreementDraftInput{
		RuntimeSpec:       spec,
		WorkAgreement:     agreement,
		ConditionalGrants: badGrants,
		SourceRequestID:   "cap-invalid-grant",
	})
	if err == nil || !strings.Contains(err.Error(), "at least one action") {
		t.Fatalf("CreateDurableExternalRuntimeWorkAgreementDraft(invalid grant) err = %v, want action rejection", err)
	}
	specHash, err := core.StableExternalRuntimeContractHash(core.NormalizeDurableExternalRuntimeSpec(spec))
	if err != nil {
		t.Fatalf("StableExternalRuntimeContractHash() err = %v", err)
	}
	if _, ok, err := store.DurableChildRuntimeSpecByHash(agreement.AgentID, specHash); err != nil || ok {
		t.Fatalf("DurableChildRuntimeSpecByHash(after invalid grant) ok=%t err=%v, want no partial runtime spec", ok, err)
	}
	if _, err := store.DurableChildWorkAgreementVersion(agreement.ID, agreement.Version); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("DurableChildWorkAgreementVersion(after invalid grant) err = %v, want sql.ErrNoRows", err)
	}
}
