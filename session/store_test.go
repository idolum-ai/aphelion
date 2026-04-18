//go:build linux

package session

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	_ "github.com/mattn/go-sqlite3"
)

func TestSQLiteStoreCreatesReviewEventsTable(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	var count int
	err := store.db.QueryRow(`
		SELECT COUNT(1)
		FROM sqlite_master
		WHERE type = 'table' AND name = 'review_events'
	`).Scan(&count)
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	if count != 1 {
		t.Fatalf("review_events table count = %d, want 1", count)
	}
}

func TestReviewEventsPendingOrderingAndFiltering(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	target := int64(700)
	otherTarget := int64(701)
	for _, event := range []ReviewEvent{
		{
			SourceChatID:      10,
			SourceUserID:      0,
			SourceRole:        "approved_user",
			TargetAdminChatID: target,
			TurnFrom:          1,
			TurnTo:            3,
			Summary:           "first",
		},
		{
			SourceChatID:      11,
			SourceUserID:      0,
			SourceRole:        "approved_user",
			TargetAdminChatID: otherTarget,
			TurnFrom:          4,
			TurnTo:            5,
			Summary:           "wrong target",
		},
		{
			SourceChatID:      12,
			SourceUserID:      0,
			SourceRole:        "approved_user",
			TargetAdminChatID: target,
			TurnFrom:          6,
			TurnTo:            8,
			Summary:           "second",
		},
		{
			SourceChatID:      13,
			SourceUserID:      0,
			SourceRole:        "approved_user",
			TargetAdminChatID: target,
			TurnFrom:          9,
			TurnTo:            10,
			Summary:           "already delivered",
			Status:            "delivered",
		},
	} {
		if err := store.EnqueueReviewEvent(event); err != nil {
			t.Fatalf("EnqueueReviewEvent() err = %v", err)
		}
	}

	pending, err := store.PendingReviewEvents(target, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}

	if len(pending) != 2 {
		t.Fatalf("pending len = %d, want 2", len(pending))
	}
	if pending[0].Summary != "first" || pending[1].Summary != "second" {
		t.Fatalf("pending summaries = [%q, %q], want [first, second]", pending[0].Summary, pending[1].Summary)
	}
	if pending[0].ID >= pending[1].ID {
		t.Fatalf("pending IDs not ordered: first=%d second=%d", pending[0].ID, pending[1].ID)
	}
}

func TestReviewEventsLimitAndMarkDelivered(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	target := int64(800)
	for _, summary := range []string{"one", "two", "three"} {
		if err := store.EnqueueReviewEvent(ReviewEvent{
			SourceChatID:      20,
			SourceRole:        "approved_user",
			TargetAdminChatID: target,
			Summary:           summary,
		}); err != nil {
			t.Fatalf("EnqueueReviewEvent() err = %v", err)
		}
	}

	firstBatch, err := store.PendingReviewEvents(target, 2)
	if err != nil {
		t.Fatalf("PendingReviewEvents(limit=2) err = %v", err)
	}
	if len(firstBatch) != 2 {
		t.Fatalf("first batch len = %d, want 2", len(firstBatch))
	}

	if err := store.MarkReviewDelivered([]int64{firstBatch[0].ID}); err != nil {
		t.Fatalf("MarkReviewDelivered() err = %v", err)
	}

	var status string
	var deliveredAt string
	if err := store.db.QueryRow(`
		SELECT status, COALESCE(delivered_at, '')
		FROM review_events
		WHERE id = ?
	`, firstBatch[0].ID).Scan(&status, &deliveredAt); err != nil {
		t.Fatalf("query delivered review event: %v", err)
	}
	if status != "delivered" {
		t.Fatalf("status = %q, want delivered", status)
	}
	if deliveredAt == "" {
		t.Fatal("delivered_at is empty, want populated timestamp")
	}

	remaining, err := store.PendingReviewEvents(target, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(remaining) != 2 {
		t.Fatalf("remaining pending len = %d, want 2", len(remaining))
	}
	if remaining[0].Summary != "two" || remaining[1].Summary != "three" {
		t.Fatalf("remaining summaries = [%q, %q], want [two, three]", remaining[0].Summary, remaining[1].Summary)
	}
}

func TestReviewEventsPreserveScopeAndMetadata(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	event := ReviewEvent{
		SourceChatID:      0,
		SourceRole:        "durable_agent",
		TargetAdminChatID: 9001,
		SourceScope: ScopeRef{
			Kind:            ScopeKindDurableAgent,
			ID:              "family-group",
			DurableAgentID:  "family-group",
			ParentScopeKind: ScopeKindTelegramDM,
			ParentScopeID:   "1001",
		},
		TargetScope: ScopeRef{
			Kind: ScopeKindTelegramDM,
			ID:   "9001",
		},
		Summary:      "bounded child synthesis",
		MetadataJSON: `{"risk_flags":["tone drift"],"questions":["approve charter change?"]}`,
	}
	if err := store.EnqueueReviewEvent(event); err != nil {
		t.Fatalf("EnqueueReviewEvent() err = %v", err)
	}

	pending, err := store.PendingReviewEvents(9001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending len = %d, want 1", len(pending))
	}
	got := pending[0]
	if got.SourceScope.Kind != ScopeKindDurableAgent || got.SourceScope.ID != "family-group" {
		t.Fatalf("source scope = %#v, want durable_agent family-group", got.SourceScope)
	}
	if got.SourceScope.DurableAgentID != "family-group" {
		t.Fatalf("source durable agent id = %q, want family-group", got.SourceScope.DurableAgentID)
	}
	if got.TargetScope.Kind != ScopeKindTelegramDM || got.TargetScope.ID != "9001" {
		t.Fatalf("target scope = %#v, want telegram_dm 9001", got.TargetScope)
	}
	if got.MetadataJSON != event.MetadataJSON {
		t.Fatalf("MetadataJSON = %q, want %q", got.MetadataJSON, event.MetadataJSON)
	}
}

func TestSearchMessagesFiltersByScopeAndReturnsNewestFirst(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	for _, tc := range []struct {
		key      SessionKey
		turn     int
		userText string
		reply    string
	}{
		{SessionKey{ChatID: 1, UserID: 0}, 1, "alpha first", "reply one"},
		{SessionKey{ChatID: 1, UserID: 0}, 2, "alpha second", "reply two"},
		{SessionKey{ChatID: 2, UserID: 0}, 1, "beta alpha", "reply three"},
	} {
		sess, err := store.Load(tc.key)
		if err != nil {
			t.Fatalf("Load(%v) err = %v", tc.key, err)
		}
		sess.TurnCount = tc.turn
		if err := store.Save(sess, []Message{
			{Role: "user", Content: tc.userText, TurnIndex: tc.turn},
			{Role: "assistant", Content: tc.reply, FloorContent: tc.reply, TurnIndex: tc.turn},
		}, core.TokenUsage{}); err != nil {
			t.Fatalf("Save(%v) err = %v", tc.key, err)
		}
	}

	allHits, err := store.SearchMessages("alpha", 10, nil)
	if err != nil {
		t.Fatalf("SearchMessages(all) err = %v", err)
	}
	if len(allHits) != 3 {
		t.Fatalf("all hits len = %d, want 3", len(allHits))
	}
	if allHits[0].ChatID != 2 || allHits[1].TurnIndex != 2 {
		t.Fatalf("all hits ordering = %#v, want newest first", allHits)
	}

	scope := SessionKey{ChatID: 1, UserID: 0}
	scopedHits, err := store.SearchMessages("alpha", 10, &scope)
	if err != nil {
		t.Fatalf("SearchMessages(scoped) err = %v", err)
	}
	if len(scopedHits) != 2 {
		t.Fatalf("scoped hits len = %d, want 2", len(scopedHits))
	}
	for _, hit := range scopedHits {
		if hit.ChatID != 1 {
			t.Fatalf("scoped hit chat id = %d, want 1", hit.ChatID)
		}
	}
}

func TestPlanStateRoundTripAndUpdate(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 77, UserID: 0, Scope: ScopeRef{Kind: ScopeKindTelegramDM, ID: "77"}}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.PlanState = PlanState{
		Explanation: "Inspect before editing.",
		Steps: []PlanStep{
			{Step: "Inspect the relevant files.", Status: PlanStatusInProgress},
			{Step: "Patch the bug.", Status: PlanStatusPending},
		},
	}
	sess.TurnCount = 1
	if err := store.Save(sess, []Message{{Role: "assistant", Content: "planned", TurnIndex: 1}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	reloaded, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(reloaded) err = %v", err)
	}
	if reloaded.PlanState.Explanation != "Inspect before editing." {
		t.Fatalf("Explanation = %q, want persisted explanation", reloaded.PlanState.Explanation)
	}
	if len(reloaded.PlanState.Steps) != 2 {
		t.Fatalf("steps len = %d, want 2", len(reloaded.PlanState.Steps))
	}
	if reloaded.PlanState.Steps[0].Status != PlanStatusInProgress {
		t.Fatalf("first step status = %q, want in_progress", reloaded.PlanState.Steps[0].Status)
	}

	updated := PlanState{
		Explanation: "Execution complete.",
		Steps: []PlanStep{
			{Step: "Inspect the relevant files.", Status: PlanStatusCompleted},
			{Step: "Patch the bug.", Status: PlanStatusCompleted},
		},
	}
	if err := store.UpdatePlanState(key, updated); err != nil {
		t.Fatalf("UpdatePlanState() err = %v", err)
	}

	planState, err := store.PlanState(key)
	if err != nil {
		t.Fatalf("PlanState() err = %v", err)
	}
	if planState.Explanation != "Execution complete." {
		t.Fatalf("updated explanation = %q, want updated value", planState.Explanation)
	}
	if len(planState.Steps) != 2 || planState.Steps[1].Status != PlanStatusCompleted {
		t.Fatalf("updated steps = %#v, want completed steps", planState.Steps)
	}
}

func TestContinuationStateRoundTripAndUpdate(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	key := SessionKey{ChatID: 901, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.ContinuationState = ContinuationState{Status: ContinuationStatusPending, Objective: "implement continuation controls", StageSummary: "Attach approval UI", RemainingTurns: 1, ApprovedBy: 1002}
	if err := store.Save(sess, []Message{{Role: "assistant", Content: "ok", TurnIndex: 1}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}
	reloaded, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(reloaded) err = %v", err)
	}
	if reloaded.ContinuationState.Status != ContinuationStatusPending {
		t.Fatalf("status = %q, want pending", reloaded.ContinuationState.Status)
	}
	updated := ContinuationState{Status: ContinuationStatusApproved, Objective: "implement continuation controls", RemainingTurns: 1, ApprovedBy: 1002}
	if err := store.UpdateContinuationState(key, updated); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != ContinuationStatusApproved {
		t.Fatalf("status = %q, want approved", got.Status)
	}
	if got.ApprovedBy != 1002 {
		t.Fatalf("approved_by = %d, want 1002", got.ApprovedBy)
	}
}

func TestOperationStateRoundTripAndUpdate(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 80, UserID: 0, Scope: ScopeRef{Kind: ScopeKindTelegramDM, ID: "80"}}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.OperationState = OperationState{
		ID:        "op-1",
		Objective: "Investigate the current internet footprint.",
		Status:    OperationStatusActive,
		Stage:     "assessment",
		Summary:   "Collecting public traces before requesting external access.",
		Proposal: OperationProposal{
			ID:            "proposal-1",
			Kind:          "capability_acquisition",
			Summary:       "Acquire browser automation",
			WhyNow:        "A screenshot requires browser automation in this operation.",
			BoundedEffect: "Install Playwright locally and capture one screenshot.",
			Status:        ProposalStatusPending,
		},
		Findings: []OperationFinding{
			{Claim: "A browser is not currently available.", Confidence: FindingConfidenceHigh, Basis: "No browser tool is exposed in the active manifest."},
		},
		Artifacts: []OperationArtifact{
			{Label: "working-note", Ref: "tmp/notes.md"},
		},
	}
	sess.TurnCount = 1
	if err := store.Save(sess, []Message{{Role: "assistant", Content: "operating", TurnIndex: 1}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	reloaded, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(reloaded) err = %v", err)
	}
	if reloaded.OperationState.Objective != "Investigate the current internet footprint." {
		t.Fatalf("Objective = %q, want persisted objective", reloaded.OperationState.Objective)
	}
	if reloaded.OperationState.Proposal.Status != ProposalStatusPending {
		t.Fatalf("Proposal status = %q, want pending", reloaded.OperationState.Proposal.Status)
	}
	if len(reloaded.OperationState.Findings) != 1 {
		t.Fatalf("findings len = %d, want 1", len(reloaded.OperationState.Findings))
	}

	updated := OperationState{
		ID:        "op-1",
		Objective: "Investigate the current internet footprint.",
		Status:    OperationStatusActive,
		Stage:     "execution",
		Summary:   "Proposal approved and screenshot capture is underway.",
		Proposal: OperationProposal{
			ID:            "proposal-1",
			Kind:          "capability_acquisition",
			Summary:       "Acquire browser automation",
			WhyNow:        "A screenshot requires browser automation in this operation.",
			BoundedEffect: "Install Playwright locally and capture one screenshot.",
			Status:        ProposalStatusApproved,
		},
		Findings: []OperationFinding{
			{Claim: "Browser automation can be acquired locally.", Confidence: FindingConfidenceHigh, Basis: "Admin execution can install local dependencies."},
		},
		Artifacts: []OperationArtifact{
			{Label: "screenshot", Ref: "tmp/reddit.png"},
		},
	}
	if err := store.UpdateOperationState(key, updated); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	operationState, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if operationState.Stage != "execution" {
		t.Fatalf("updated stage = %q, want execution", operationState.Stage)
	}
	if operationState.Proposal.Status != ProposalStatusApproved {
		t.Fatalf("updated proposal status = %q, want approved", operationState.Proposal.Status)
	}
	if len(operationState.Artifacts) != 1 || operationState.Artifacts[0].Ref != "tmp/reddit.png" {
		t.Fatalf("artifacts = %#v, want updated screenshot artifact", operationState.Artifacts)
	}
}

func TestPlanEventsRoundTripAndRehydrateFromLatestEvent(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 78, UserID: 0, Scope: ScopeRef{Kind: ScopeKindTelegramDM, ID: "78"}}
	state := PlanState{
		Explanation: "Track long-running work durably.",
		Steps: []PlanStep{
			{Step: "Inspect the current runtime.", Status: PlanStatusInProgress},
			{Step: "Patch the missing event log.", Status: PlanStatusPending},
		},
	}

	if err := store.UpdatePlanStateWithEvent(key, state, PlanEventKindToolUpdated); err != nil {
		t.Fatalf("UpdatePlanStateWithEvent() err = %v", err)
	}

	events, err := store.PlanEvents(key, 10)
	if err != nil {
		t.Fatalf("PlanEvents() err = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("plan events len = %d, want 1", len(events))
	}
	if events[0].Kind != PlanEventKindToolUpdated {
		t.Fatalf("plan event kind = %q, want %q", events[0].Kind, PlanEventKindToolUpdated)
	}
	if events[0].PlanState.Explanation != state.Explanation {
		t.Fatalf("event explanation = %q, want %q", events[0].PlanState.Explanation, state.Explanation)
	}

	if _, err := store.db.Exec(`UPDATE sessions SET plan_state_json = '{}' WHERE session_id = ?`, SessionIDForKey(key)); err != nil {
		t.Fatalf("clear plan_state_json err = %v", err)
	}

	rehydrated, err := store.PlanState(key)
	if err != nil {
		t.Fatalf("PlanState(rehydrated) err = %v", err)
	}
	if rehydrated.Explanation != state.Explanation {
		t.Fatalf("rehydrated explanation = %q, want %q", rehydrated.Explanation, state.Explanation)
	}
	if len(rehydrated.Steps) != 2 || rehydrated.Steps[0].Status != PlanStatusInProgress {
		t.Fatalf("rehydrated steps = %#v, want original state", rehydrated.Steps)
	}

	events, err = store.PlanEvents(key, 10)
	if err != nil {
		t.Fatalf("PlanEvents(after rehydrate) err = %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("plan events len after rehydrate = %d, want >= 2", len(events))
	}
	if events[0].Kind != PlanEventKindRehydrated {
		t.Fatalf("latest plan event kind = %q, want %q", events[0].Kind, PlanEventKindRehydrated)
	}
}

func TestPlanStateRehydratesFromTranscriptWhenEventLogMissing(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 79, UserID: 0, Scope: ScopeRef{Kind: ScopeKindTelegramDM, ID: "79"}}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.TurnCount = 1
	if err := store.Save(sess, []Message{
		{Role: "user", Content: "work through this", TurnIndex: 1},
		{
			Role:     "tool",
			ToolName: "update_plan",
			Content: strings.Join([]string{
				"[PLAN_UPDATED]",
				"active: true",
				"explanation: Recover from transcript state.",
				"- [in_progress] Inspect the relevant files.",
				"- [pending] Patch the bug.",
			}, "\n"),
			TurnIndex: 1,
		},
	}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	if _, err := store.db.Exec(`UPDATE sessions SET plan_state_json = '{}' WHERE session_id = ?`, SessionIDForKey(key)); err != nil {
		t.Fatalf("clear plan_state_json err = %v", err)
	}

	rehydrated, err := store.PlanState(key)
	if err != nil {
		t.Fatalf("PlanState(rehydrated) err = %v", err)
	}
	if rehydrated.Explanation != "Recover from transcript state." {
		t.Fatalf("rehydrated explanation = %q, want transcript-derived explanation", rehydrated.Explanation)
	}
	if len(rehydrated.Steps) != 2 || rehydrated.Steps[1].Status != PlanStatusPending {
		t.Fatalf("rehydrated steps = %#v, want transcript-derived plan", rehydrated.Steps)
	}

	events, err := store.PlanEvents(key, 10)
	if err != nil {
		t.Fatalf("PlanEvents() err = %v", err)
	}
	if len(events) == 0 || events[0].Kind != PlanEventKindRehydrated {
		t.Fatalf("plan events = %#v, want transcript rehydration event", events)
	}
}

func TestSaveUpdatesCacheTotalsAndState(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 91, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	sess.TurnCount = 1
	if err := store.Save(sess, []Message{{Role: "assistant", Content: "first", TurnIndex: 1}}, core.TokenUsage{
		InputTokens:      10,
		OutputTokens:     2,
		CacheWriteTokens: 100,
	}); err != nil {
		t.Fatalf("Save(first) err = %v", err)
	}

	reloaded, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(reloaded) err = %v", err)
	}
	if reloaded.TotalCacheWrite != 100 {
		t.Fatalf("TotalCacheWrite = %d, want 100", reloaded.TotalCacheWrite)
	}
	if reloaded.CacheState.LastWriteBlock != 1 || reloaded.CacheState.BlocksSinceWrite != 0 {
		t.Fatalf("cache state after write = %#v", reloaded.CacheState)
	}

	reloaded.TurnCount = 2
	if err := store.Save(reloaded, []Message{{Role: "assistant", Content: "second", TurnIndex: 2}}, core.TokenUsage{
		InputTokens:     8,
		OutputTokens:    3,
		CacheReadTokens: 80,
	}); err != nil {
		t.Fatalf("Save(second) err = %v", err)
	}

	finalSession, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(final) err = %v", err)
	}
	if finalSession.TotalCacheRead != 80 {
		t.Fatalf("TotalCacheRead = %d, want 80", finalSession.TotalCacheRead)
	}
	if finalSession.CacheState.BlocksSinceWrite != 1 {
		t.Fatalf("BlocksSinceWrite = %d, want 1", finalSession.CacheState.BlocksSinceWrite)
	}
	if finalSession.CacheState.ConsecutiveMisses != 0 {
		t.Fatalf("ConsecutiveMisses = %d, want 0", finalSession.CacheState.ConsecutiveMisses)
	}
	if finalSession.CacheState.HitRate <= 0 {
		t.Fatalf("HitRate = %f, want positive", finalSession.CacheState.HitRate)
	}
}

func TestCompactMarksOldMessagesAndResetsCacheState(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 99, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.TurnCount = 3
	sess.CacheState.LastWriteBlock = 3
	sess.CacheState.BlocksSinceWrite = 2
	sess.CacheState.HitRate = 0.5
	sess.CacheState.ConsecutiveMisses = 2
	if err := store.Save(sess, []Message{
		{Role: "user", Content: "turn 1", TurnIndex: 1},
		{Role: "assistant", Content: "reply 1", TurnIndex: 1},
		{Role: "user", Content: "turn 2", TurnIndex: 2},
		{Role: "assistant", Content: "reply 2", TurnIndex: 2},
		{Role: "user", Content: "turn 3", TurnIndex: 3},
		{Role: "assistant", Content: "reply 3", TurnIndex: 3},
	}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	if err := store.Compact(key, "summary block", 3); err != nil {
		t.Fatalf("Compact() err = %v", err)
	}

	reloaded, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(reloaded) err = %v", err)
	}
	if len(reloaded.CompactionLog) != 1 {
		t.Fatalf("compaction log len = %d, want 1", len(reloaded.CompactionLog))
	}
	if reloaded.CompactionLog[0].Strategy != "summarize" {
		t.Fatalf("compaction strategy = %q, want summarize", reloaded.CompactionLog[0].Strategy)
	}
	if reloaded.CacheState.LastWriteBlock != 0 || reloaded.CacheState.BlocksSinceWrite != 0 || reloaded.CacheState.HitRate != 0 || reloaded.CacheState.ConsecutiveMisses != 0 {
		t.Fatalf("cache state after compact = %#v, want reset", reloaded.CacheState)
	}

	compacted := 0
	for _, msg := range reloaded.Messages {
		if msg.Compacted {
			compacted++
		}
	}
	if compacted == 0 {
		t.Fatal("compacted message count = 0, want old messages soft-deleted")
	}
}

func TestRhizomeEventRecordingAndProjectionEdges(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	if err := store.RecordRhizomeEvent("shared", "heartbeat", 1.0, []string{"governor", "memory", "reflection"}); err != nil {
		t.Fatalf("RecordRhizomeEvent(1) err = %v", err)
	}
	if err := store.RecordRhizomeEvent("shared", "heartbeat", 1.0, []string{"memory", "reflection"}); err != nil {
		t.Fatalf("RecordRhizomeEvent(2) err = %v", err)
	}

	edges, err := store.TopRhizomeEdges("shared", 10)
	if err != nil {
		t.Fatalf("TopRhizomeEdges() err = %v", err)
	}
	if len(edges) == 0 {
		t.Fatal("TopRhizomeEdges() returned no edges, want at least one")
	}
	if edges[0].LeftConcept != "memory" || edges[0].RightConcept != "reflection" {
		t.Fatalf("top edge = %#v, want memory/reflection strongest edge", edges[0])
	}
	if edges[0].RecurrenceCount != 2 {
		t.Fatalf("top edge recurrence = %d, want 2", edges[0].RecurrenceCount)
	}
}

func TestResetAllRhizomeClearsGraph(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	if err := store.RecordRhizomeEvent("shared", "heartbeat", 1.0, []string{"a", "b"}); err != nil {
		t.Fatalf("RecordRhizomeEvent() err = %v", err)
	}
	if err := store.ResetAllRhizome(); err != nil {
		t.Fatalf("ResetAllRhizome() err = %v", err)
	}

	edges, err := store.TopRhizomeEdges("shared", 10)
	if err != nil {
		t.Fatalf("TopRhizomeEdges() err = %v", err)
	}
	if len(edges) != 0 {
		t.Fatalf("edges len = %d, want 0 after reset", len(edges))
	}
}

func newTestSQLiteStore(t *testing.T) *SQLiteStore {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	return store
}

func TestSaveAndLoadFloorSidecar(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 1234, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	sess.TurnCount = 1
	sess.LastFloorText = "governor canonical"
	if err := store.Save(sess, []Message{
		{
			Role:      "user",
			Content:   "hello",
			TurnIndex: 1,
		},
		{
			Role:         "assistant",
			Content:      "idolum rendered",
			FloorContent: "governor canonical",
			TurnIndex:    1,
		},
	}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	got, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() after save err = %v", err)
	}
	if got.LastFloorText != "governor canonical" {
		t.Fatalf("LastFloorText = %q, want governor canonical", got.LastFloorText)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(got.Messages))
	}
	if got.Messages[1].Content != "idolum rendered" {
		t.Fatalf("assistant visible content = %q, want idolum rendered", got.Messages[1].Content)
	}
	if got.Messages[1].FloorContent != "governor canonical" {
		t.Fatalf("assistant floor content = %q, want governor canonical", got.Messages[1].FloorContent)
	}
}

func TestSavePersistsSessionScopeMetadata(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{
		ChatID: 5001,
		Scope: ScopeRef{
			Kind: ScopeKindHeartbeat,
			ID:   "admin-house",
		},
	}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	sess.Scope = key.Scope
	sess.TurnCount = 1
	if err := store.Save(sess, []Message{{Role: "assistant", Content: "ok", TurnIndex: 1}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	got, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(reloaded) err = %v", err)
	}
	if got.Scope.Kind != ScopeKindHeartbeat || got.Scope.ID != "admin-house" {
		t.Fatalf("Scope = %#v, want heartbeat admin-house", got.Scope)
	}
}

func TestDurableAgentRegistryAndStateRoundTrip(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	agent := core.DurableAgent{
		AgentID:            "family-group",
		ParentAgentID:      "house",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:            "help the family group without mutating the house",
			CapabilityEnvelope: []string{"read_channel", "draft_reply", "synthesize_review"},
			OutboundMode:       "draft_only",
			DriftPolicy:        "admin_ratified",
			PublicSurfaceMode:  "explicit_parent_relay_only",
		},
		BootstrapCeiling: core.DurableAgentBootstrapCeiling{
			CapabilityEnvelope:           []string{"read_channel", "draft_reply", "synthesize_review", "bounded_review_artifact"},
			AllowedOutboundModes:         []string{"draft_only", "read_only"},
			AllowedPublicSurfaceModes:    []string{"explicit_parent_relay_only", "none"},
			AllowedSharedInferenceReuse:  []string{"disabled"},
			AllowedSharedInferenceScopes: []string{"public_prefix_only"},
		},
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-group",
			BaseURL:        "https://openrouter.example.test",
			Model:          "openrouter/group-model",
			MaxTokens:      256,
		},
		ControlPlaneSecret: "group-control-secret",
		LocalStorageRoots:  []string{"/tmp/family-group"},
		NetworkPolicy:      "restricted",
		WakeupMode:         "event",
		SecretScopes:       []string{"telegram_bot"},
		Status:             "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	got, err := store.DurableAgent(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	if got.AgentID != agent.AgentID || got.ChannelKind != agent.ChannelKind {
		t.Fatalf("DurableAgent() = %#v, want agent %q kind %q", got, agent.AgentID, agent.ChannelKind)
	}
	if len(got.LivePolicy.CapabilityEnvelope) != 3 || got.LivePolicy.CapabilityEnvelope[2] != "synthesize_review" {
		t.Fatalf("CapabilityEnvelope = %#v, want preserved capabilities", got.LivePolicy.CapabilityEnvelope)
	}
	if len(got.SecretScopes) != 1 || got.SecretScopes[0] != "telegram_bot" {
		t.Fatalf("SecretScopes = %#v, want telegram_bot", got.SecretScopes)
	}
	if len(got.BootstrapCeiling.AllowedOutboundModes) != 2 || got.BootstrapCeiling.AllowedOutboundModes[0] != "draft_only" {
		t.Fatalf("BootstrapCeiling.AllowedOutboundModes = %#v, want preserved ceiling", got.BootstrapCeiling.AllowedOutboundModes)
	}
	if got.LivePolicy.OutboundMode != "draft_only" {
		t.Fatalf("OutboundMode = %q, want draft_only", got.LivePolicy.OutboundMode)
	}
	if got.BootstrapLLM.Backend != "native" {
		t.Fatalf("BootstrapLLM.Backend = %q, want native", got.BootstrapLLM.Backend)
	}
	if got.BootstrapLLM.NativeProvider != "openrouter" {
		t.Fatalf("BootstrapLLM.NativeProvider = %q, want openrouter", got.BootstrapLLM.NativeProvider)
	}
	if got.BootstrapLLM.APIKey != "sk-or-group" {
		t.Fatalf("BootstrapLLM.APIKey = %q, want sk-or-group", got.BootstrapLLM.APIKey)
	}
	if got.ControlPlaneSecret != "group-control-secret" {
		t.Fatalf("ControlPlaneSecret = %q, want group-control-secret", got.ControlPlaneSecret)
	}
	if got.PolicyVersion != 1 {
		t.Fatalf("PolicyVersion = %d, want 1", got.PolicyVersion)
	}
	if got.PolicyHash == "" {
		t.Fatal("PolicyHash is empty, want derived policy hash")
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("timestamps = created:%v updated:%v, want populated", got.CreatedAt, got.UpdatedAt)
	}

	if err := store.SetDurableAgentLivePolicy(agent.AgentID, core.DurableAgentLivePolicy{
		Charter:            "ratified updated charter",
		CapabilityEnvelope: []string{"read_channel", "bounded_review_artifact"},
		OutboundMode:       "read_only",
		DriftPolicy:        "admin_review",
	}); err != nil {
		t.Fatalf("SetDurableAgentLivePolicy() err = %v", err)
	}
	updated, err := store.DurableAgent(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgent(updated) err = %v", err)
	}
	if updated.LivePolicy.Charter != "ratified updated charter" {
		t.Fatalf("updated charter = %q, want ratified updated charter", updated.LivePolicy.Charter)
	}
	if updated.PolicyVersion != 2 {
		t.Fatalf("updated PolicyVersion = %d, want 2", updated.PolicyVersion)
	}
	if updated.PolicyHash == got.PolicyHash {
		t.Fatal("updated PolicyHash did not change after live policy update")
	}
	if updated.BootstrapCeiling.AllowedSharedInferenceReuse[0] != "disabled" {
		t.Fatalf("updated BootstrapCeiling.AllowedSharedInferenceReuse = %#v, want preserved disabled ceiling", updated.BootstrapCeiling.AllowedSharedInferenceReuse)
	}

	listed, err := store.ListDurableAgents()
	if err != nil {
		t.Fatalf("ListDurableAgents() err = %v", err)
	}
	if len(listed) != 1 || listed[0].AgentID != agent.AgentID {
		t.Fatalf("ListDurableAgents() = %#v, want single family-group agent", listed)
	}

	state := core.DurableAgentState{
		AgentID:      agent.AgentID,
		Cursor:       "msg-42",
		Status:       "dormant",
		StateJSON:    `{"last_sender":"alice"}`,
		LastWakeAt:   time.Now().UTC().Add(-5 * time.Minute).Round(0),
		LastReviewAt: time.Now().UTC().Round(0),
	}
	if err := store.SaveDurableAgentState(state); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}

	gotState, err := store.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	if gotState.Cursor != state.Cursor || gotState.Status != state.Status {
		t.Fatalf("DurableAgentState() = %#v, want cursor/status preserved", gotState)
	}
	if gotState.StateJSON != state.StateJSON {
		t.Fatalf("StateJSON = %q, want %q", gotState.StateJSON, state.StateJSON)
	}

	if err := store.DeleteDurableAgent(agent.AgentID); err != nil {
		t.Fatalf("DeleteDurableAgent() err = %v", err)
	}
	if _, err := store.DurableAgent(agent.AgentID); err == nil || !strings.Contains(err.Error(), "no rows") {
		t.Fatalf("DurableAgent() after delete err = %v, want no rows", err)
	}
	if _, err := store.DurableAgentState(agent.AgentID); err == nil || !strings.Contains(err.Error(), "no rows") {
		t.Fatalf("DurableAgentState() after delete err = %v, want no rows", err)
	}
}

func TestDurableAgentEmailChannelConfigRoundTrip(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	agent := core.DurableAgent{
		AgentID:            "idolum-email",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "email",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Review the inbox and surface important threads without sending mail.",
			CapabilityEnvelope: []string{"read_channel", "bounded_review_artifact", "summarize_pdf"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		ChannelConfig: core.DurableAgentChannelConfig{
			Email: &core.DurableAgentEmailChannelConfig{
				Address:          "idolum@example.com",
				Account:          "idolum@example.com",
				Adapter:          "gog_cli",
				Query:            "label:inbox newer_than:7d",
				PollInterval:     "5m",
				SurfaceRules:     []string{"job opportunity", "external inquiry"},
				SummarizePDFs:    true,
				SynthesisCadence: "4h",
				NeverRetain:      []string{"oauth_token", "password"},
			},
		},
		WakeupMode: "poll",
		Status:     "draft",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	got, err := store.DurableAgent(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	if got.ChannelConfig.Email == nil {
		t.Fatal("ChannelConfig.Email = nil, want persisted email channel config")
	}
	if got.ChannelConfig.Email.Address != "idolum@example.com" {
		t.Fatalf("ChannelConfig.Email.Address = %q, want idolum@example.com", got.ChannelConfig.Email.Address)
	}
	if got.ChannelConfig.Email.Adapter != "gog_cli" {
		t.Fatalf("ChannelConfig.Email.Adapter = %q, want gog_cli", got.ChannelConfig.Email.Adapter)
	}
	if got.ChannelConfig.Email.PollInterval != "5m" {
		t.Fatalf("ChannelConfig.Email.PollInterval = %q, want 5m", got.ChannelConfig.Email.PollInterval)
	}
	if !got.ChannelConfig.Email.SummarizePDFs {
		t.Fatal("ChannelConfig.Email.SummarizePDFs = false, want true")
	}
	if len(got.ChannelConfig.Email.SurfaceRules) != 2 || got.ChannelConfig.Email.SurfaceRules[0] != "job opportunity" {
		t.Fatalf("ChannelConfig.Email.SurfaceRules = %#v, want persisted surface rules", got.ChannelConfig.Email.SurfaceRules)
	}
	if len(got.ChannelConfig.Email.NeverRetain) != 2 || got.ChannelConfig.Email.NeverRetain[1] != "password" {
		t.Fatalf("ChannelConfig.Email.NeverRetain = %#v, want persisted never-retain classes", got.ChannelConfig.Email.NeverRetain)
	}
}

func TestApplyDurableAgentLivePolicyRejectsBootstrapCeilingWidening(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	agent := core.DurableAgent{
		AgentID:            "family-group",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:            "Observe and surface bounded family coordination.",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		},
		BootstrapCeiling: core.DurableAgentBootstrapCeiling{
			CapabilityEnvelope:           []string{"group_reply", "bounded_review_artifact"},
			AllowedOutboundModes:         []string{"read_only", "draft_only"},
			AllowedPublicSurfaceModes:    []string{"none"},
			AllowedSharedInferenceReuse:  []string{"disabled"},
			AllowedSharedInferenceScopes: []string{"public_prefix_only"},
		},
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-group",
			Model:          "openrouter/test-model",
		},
		Status: "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	_, _, err := store.ApplyDurableAgentLivePolicy(agent.AgentID, core.DurableAgentLivePolicy{
		Charter:            "Observe and surface bounded family coordination.",
		CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
		OutboundMode:       "reply_with_policy_authorization",
		DriftPolicy:        "admin_review",
	}, 0, "attempted widening")
	if err == nil {
		t.Fatal("ApplyDurableAgentLivePolicy() err = nil, want bootstrap ceiling violation")
	}
	if !strings.Contains(err.Error(), "bootstrap ceiling") {
		t.Fatalf("ApplyDurableAgentLivePolicy() err = %v, want bootstrap ceiling violation", err)
	}

	got, err := store.DurableAgent(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	if got.LivePolicy.OutboundMode != "read_only" {
		t.Fatalf("LivePolicy.OutboundMode = %q, want unchanged read_only", got.LivePolicy.OutboundMode)
	}
	if got.PolicyVersion != 1 {
		t.Fatalf("PolicyVersion = %d, want unchanged 1", got.PolicyVersion)
	}
}

func TestApplyDurableAgentLivePolicyTracksOfferedStateAndRatifiedOutcome(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	agent := core.DurableAgent{
		AgentID:            "family-group",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:            "Observe and surface bounded family coordination.",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		},
		BootstrapCeiling: core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DurableAgentLivePolicy{
			Charter:            "Observe and surface bounded family coordination.",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-group",
			Model:          "openrouter/test-model",
		},
		Status: "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	updated, update, err := store.ApplyDurableAgentLivePolicy(agent.AgentID, core.DurableAgentLivePolicy{
		Charter:            "Observe and surface family coordination, but allow reviewed drafting.",
		CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
		OutboundMode:       "draft_only",
		DriftPolicy:        "admin_review",
	}, 42, "ratified family-group narrowing")
	if err != nil {
		t.Fatalf("ApplyDurableAgentLivePolicy() err = %v", err)
	}
	if update == nil {
		t.Fatal("ApplyDurableAgentLivePolicy() update = nil, want policy update record")
	}
	if updated.PolicyVersion != 2 {
		t.Fatalf("updated.PolicyVersion = %d, want 2", updated.PolicyVersion)
	}

	state, err := store.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	if state.LastOfferedPolicyVersion != updated.PolicyVersion {
		t.Fatalf("LastOfferedPolicyVersion = %d, want %d", state.LastOfferedPolicyVersion, updated.PolicyVersion)
	}
	if state.LastOfferedPolicyHash != updated.PolicyHash {
		t.Fatalf("LastOfferedPolicyHash = %q, want %q", state.LastOfferedPolicyHash, updated.PolicyHash)
	}
	if state.LastApplyStatus != "pending" {
		t.Fatalf("LastApplyStatus = %q, want pending", state.LastApplyStatus)
	}
	if state.LastApplyError != "" {
		t.Fatalf("LastApplyError = %q, want empty", state.LastApplyError)
	}
	continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		t.Fatalf("ParseDurableAgentContinuityState() err = %v", err)
	}
	if len(continuity.RatifiedOutcomes) != 1 {
		t.Fatalf("RatifiedOutcomes len = %d, want 1", len(continuity.RatifiedOutcomes))
	}
	if continuity.RatifiedOutcomes[0].PolicyVersion != updated.PolicyVersion {
		t.Fatalf("RatifiedOutcomes[0].PolicyVersion = %d, want %d", continuity.RatifiedOutcomes[0].PolicyVersion, updated.PolicyVersion)
	}
	if continuity.RatifiedOutcomes[0].SourceReviewEventID != 42 {
		t.Fatalf("RatifiedOutcomes[0].SourceReviewEventID = %d, want 42", continuity.RatifiedOutcomes[0].SourceReviewEventID)
	}
	if !strings.Contains(continuity.RatifiedOutcomes[0].Summary, "ratified family-group narrowing") {
		t.Fatalf("RatifiedOutcomes[0].Summary = %q, want operator reason", continuity.RatifiedOutcomes[0].Summary)
	}
}

func TestInitMigratesLegacySessionsWithFloorColumn(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}

	legacyDDL := []string{
		`CREATE TABLE schema_version (
			version INTEGER NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO schema_version(version) VALUES (1)`,
		`CREATE TABLE sessions (
			chat_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL DEFAULT 0,
			system_prompt TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			turn_count INTEGER NOT NULL DEFAULT 0,
			chat_type TEXT NOT NULL DEFAULT 'dm',
			chat_title TEXT,
			user_name TEXT,
			cache_last_write_block INTEGER NOT NULL DEFAULT 0,
			cache_blocks_since INTEGER NOT NULL DEFAULT 0,
			cache_last_write_time TEXT,
			cache_hit_rate REAL NOT NULL DEFAULT 0.0,
			cache_consecutive_misses INTEGER NOT NULL DEFAULT 0,
			total_input_tokens INTEGER NOT NULL DEFAULT 0,
			total_output_tokens INTEGER NOT NULL DEFAULT 0,
			total_cache_read INTEGER NOT NULL DEFAULT 0,
			total_cache_write INTEGER NOT NULL DEFAULT 0,
			last_provider TEXT,
			last_model TEXT,
			active_tool_calls INTEGER NOT NULL DEFAULT 0,
			last_error TEXT,
			PRIMARY KEY (chat_id, user_id)
		)`,
	}
	for _, ddl := range legacyDDL {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("apply legacy ddl: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() migration err = %v", err)
	}
	defer store.Close()

	var hasColumn int
	err = store.db.QueryRow(`
			SELECT COUNT(1)
			FROM pragma_table_info('sessions')
			WHERE name = 'last_floor_text'
		`).Scan(&hasColumn)
	if err != nil {
		t.Fatalf("query pragma_table_info: %v", err)
	}
	if hasColumn != 1 {
		t.Fatalf("last_floor_text column count = %d, want 1", hasColumn)
	}

	err = store.db.QueryRow(`
			SELECT COUNT(1)
			FROM pragma_table_info('messages')
			WHERE name = 'floor_content'
		`).Scan(&hasColumn)
	if err != nil {
		t.Fatalf("query pragma_table_info(messages): %v", err)
	}
	if hasColumn != 1 {
		t.Fatalf("floor_content column count = %d, want 1", hasColumn)
	}

	err = store.db.QueryRow(`
			SELECT COUNT(1)
			FROM sqlite_master
			WHERE type = 'table' AND name = 'turn_runs'
		`).Scan(&hasColumn)
	if err != nil {
		t.Fatalf("query sqlite_master(turn_runs): %v", err)
	}
	if hasColumn != 1 {
		t.Fatalf("turn_runs table count = %d, want 1", hasColumn)
	}

	var maxVersion int
	if err := store.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&maxVersion); err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if maxVersion != schemaVersion {
		t.Fatalf("schema version max = %d, want %d", maxVersion, schemaVersion)
	}
}

func TestInitMigratesLegacySessionIdentityWithPlanEventsFKMismatch(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "legacy-plan-events.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}

	chatID := int64(321)
	sessionID := SessionIDFromParts(chatID, 0, ScopeRef{})

	legacyDDL := []string{
		`CREATE TABLE schema_version (
			version INTEGER NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO schema_version(version) VALUES (9)`,
		`CREATE TABLE sessions (
			chat_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL DEFAULT 0,
			session_id TEXT,
			system_prompt TEXT,
			last_floor_text TEXT,
			last_floor_metadata TEXT,
			plan_state_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			turn_count INTEGER NOT NULL DEFAULT 0,
			chat_type TEXT NOT NULL DEFAULT 'dm',
			chat_title TEXT,
			user_name TEXT,
			cache_last_write_block INTEGER NOT NULL DEFAULT 0,
			cache_blocks_since INTEGER NOT NULL DEFAULT 0,
			cache_last_write_time TEXT,
			cache_hit_rate REAL NOT NULL DEFAULT 0.0,
			cache_consecutive_misses INTEGER NOT NULL DEFAULT 0,
			total_input_tokens INTEGER NOT NULL DEFAULT 0,
			total_output_tokens INTEGER NOT NULL DEFAULT 0,
			total_cache_read INTEGER NOT NULL DEFAULT 0,
			total_cache_write INTEGER NOT NULL DEFAULT 0,
			last_provider TEXT,
			last_model TEXT,
			active_tool_calls INTEGER NOT NULL DEFAULT 0,
			last_error TEXT,
			PRIMARY KEY (chat_id, user_id)
		)`,
		`CREATE TABLE messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL DEFAULT 0,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			floor_content TEXT,
			floor_metadata TEXT,
			tool_calls TEXT,
			tool_id TEXT,
			tool_name TEXT,
			thinking TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			turn_index INTEGER NOT NULL,
			content_chars INTEGER NOT NULL DEFAULT 0,
			compacted INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE outbound_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL DEFAULT 0,
			turn_index INTEGER NOT NULL,
			telegram_msg_id INTEGER NOT NULL,
			msg_type TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE review_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_session_id TEXT,
			source_chat_id INTEGER NOT NULL DEFAULT 0,
			source_user_id INTEGER NOT NULL DEFAULT 0,
			source_role TEXT NOT NULL,
			source_scope_kind TEXT NOT NULL DEFAULT '',
			source_scope_id TEXT NOT NULL DEFAULT '',
			source_durable_agent_id TEXT NOT NULL DEFAULT '',
			target_session_id TEXT,
			target_chat_id INTEGER NOT NULL DEFAULT 0,
			target_scope_kind TEXT NOT NULL DEFAULT '',
			target_scope_id TEXT NOT NULL DEFAULT '',
			target_durable_agent_id TEXT NOT NULL DEFAULT '',
			turn_from INTEGER,
			turn_to INTEGER,
			summary TEXT NOT NULL,
			metadata_json TEXT,
			status TEXT NOT NULL DEFAULT 'pending',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			delivered_at TEXT
		)`,
		`CREATE TABLE turn_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			session_id TEXT,
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			durable_agent_id TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL,
			status TEXT NOT NULL,
			request_text TEXT NOT NULL,
			started_at TEXT NOT NULL DEFAULT (datetime('now')),
			completed_at TEXT,
			last_activity_at TEXT NOT NULL DEFAULT (datetime('now')),
			last_tool_name TEXT,
			last_tool_preview TEXT,
			tool_calls_started INTEGER NOT NULL DEFAULT 0,
			tool_calls_finished INTEGER NOT NULL DEFAULT 0,
			last_tool_result_preview TEXT,
			last_tool_error TEXT,
			progress_message_id INTEGER,
			error_text TEXT,
			recovery_summary TEXT,
			recovery_logged_at TEXT
		)`,
		`CREATE TABLE compaction_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL DEFAULT 0,
			session_id TEXT,
			timestamp TEXT NOT NULL DEFAULT (datetime('now')),
			turns_before INTEGER,
			turns_after INTEGER,
			tokens_before INTEGER,
			tokens_after INTEGER,
			summary TEXT,
			strategy TEXT NOT NULL DEFAULT 'summarize'
		)`,
		`CREATE TABLE plan_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			event_kind TEXT NOT NULL,
			plan_state_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (session_id) REFERENCES sessions(session_id) ON DELETE CASCADE
		)`,
	}
	for _, ddl := range legacyDDL {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("apply legacy ddl: %v", err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO sessions(session_id, chat_id, user_id, system_prompt)
		VALUES (?, ?, 0, 'legacy prompt')
	`, sessionID, chatID); err != nil {
		t.Fatalf("insert legacy session: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO plan_events(session_id, event_kind, plan_state_json)
		VALUES (?, 'update_plan', '{"steps":[{"step":"repair startup","status":"pending"}]}')
	`, sessionID); err != nil {
		t.Fatalf("insert legacy plan event: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() migration err = %v", err)
	}
	defer store.Close()

	var eventCount int
	if err := store.db.QueryRow(`
		SELECT COUNT(1)
		FROM plan_events
		WHERE session_id = ? AND event_kind = 'update_plan'
	`, sessionID).Scan(&eventCount); err != nil {
		t.Fatalf("query migrated plan_events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("migrated plan event count = %d, want 1", eventCount)
	}

	rows, err := store.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("PRAGMA foreign_key_check: %v", err)
	}
	defer rows.Close()
	if rows.Next() {
		var (
			table  string
			rowid  int64
			parent string
			fkid   int64
		)
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			t.Fatalf("scan foreign_key_check row: %v", err)
		}
		t.Fatalf("foreign_key_check reported violation table=%s rowid=%d parent=%s fkid=%d", table, rowid, parent, fkid)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate foreign_key_check rows: %v", err)
	}
}

func TestInitMigratesLegacyDurableAgentsToLivePolicy(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "legacy-durable.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}

	legacyDDL := []string{
		`CREATE TABLE schema_version (
			version INTEGER NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO schema_version(version) VALUES (10)`,
		`CREATE TABLE durable_agents (
			agent_id TEXT PRIMARY KEY,
			parent_agent_id TEXT,
			parent_scope_kind TEXT,
			parent_scope_id TEXT,
			review_target_chat_id INTEGER NOT NULL DEFAULT 0,
			channel_kind TEXT NOT NULL,
			charter TEXT NOT NULL DEFAULT '',
			capability_envelope_json TEXT NOT NULL DEFAULT '[]',
			local_storage_roots_json TEXT NOT NULL DEFAULT '[]',
			network_policy TEXT,
			wakeup_mode TEXT,
			outbound_mode TEXT,
			drift_policy TEXT,
			secret_scopes_json TEXT NOT NULL DEFAULT '[]',
			status TEXT NOT NULL DEFAULT 'active',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE durable_agent_state (
			agent_id TEXT PRIMARY KEY,
			cursor TEXT,
			status TEXT,
			state_json TEXT,
			last_wake_at TEXT,
			last_review_at TEXT,
			dormant_at TEXT,
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO durable_agents(
			agent_id, parent_scope_kind, parent_scope_id, review_target_chat_id, channel_kind, charter,
			capability_envelope_json, local_storage_roots_json, wakeup_mode, outbound_mode, drift_policy, secret_scopes_json, status,
			created_at, updated_at
		) VALUES (
			'family-group', 'heartbeat', 'admin-house', 1001, 'telegram_group', 'legacy charter',
			'["group_reply","bounded_review_artifact"]', '["/tmp/family-group"]', 'telegram_update', 'reply_within_charter', 'admin_review', '["telegram_bot"]', 'active',
			'2026-04-12T00:00:00Z', '2026-04-12T00:10:00Z'
		)`,
	}
	for _, stmt := range legacyDDL {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec legacy durable stmt %q: %v", stmt, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	got, err := store.DurableAgent("family-group")
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	if got.LivePolicy.Charter != "legacy charter" {
		t.Fatalf("LivePolicy.Charter = %q, want legacy charter", got.LivePolicy.Charter)
	}
	if got.LivePolicy.OutboundMode != "reply_with_policy_authorization" {
		t.Fatalf("LivePolicy.OutboundMode = %q, want migrated reply_with_policy_authorization", got.LivePolicy.OutboundMode)
	}
	if got.PolicyVersion != 1 {
		t.Fatalf("PolicyVersion = %d, want 1", got.PolicyVersion)
	}
	if got.PolicyHash == "" {
		t.Fatal("PolicyHash is empty after migration")
	}
	if got.BootstrapLLM.Configured() {
		t.Fatalf("BootstrapLLM = %#v, want empty bootstrap llm after legacy migration", got.BootstrapLLM)
	}
}

func TestRecordOutboundAndQueryAfterTurn(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 77, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.TurnCount = 2
	if err := store.Save(sess, nil, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	if err := store.RecordOutbound(key, 1, 100, "text"); err != nil {
		t.Fatalf("RecordOutbound(turn=1) err = %v", err)
	}
	if err := store.RecordOutbound(key, 3, 101, "voice"); err != nil {
		t.Fatalf("RecordOutbound(turn=3) err = %v", err)
	}

	got, err := store.OutboundAfterTurn(key, 1)
	if err != nil {
		t.Fatalf("OutboundAfterTurn() err = %v", err)
	}
	if len(got) != 1 || got[0] != 101 {
		t.Fatalf("OutboundAfterTurn() = %#v, want [101]", got)
	}
}

func TestTurnRunLifecycleAndRecovery(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 900, UserID: 0}
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	run, err := store.BeginTurnRun(key, TurnRunKindInteractive, "inspect repo")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if run.Status != TurnRunStatusRunning {
		t.Fatalf("begin status = %q, want running", run.Status)
	}

	if err := store.NoteTurnRunToolStart(run.ID, "exec", `{"command":"rg foo"}`); err != nil {
		t.Fatalf("NoteTurnRunToolStart() err = %v", err)
	}
	if err := store.NoteTurnRunToolFinish(run.ID, "stdout:\nmatch", ""); err != nil {
		t.Fatalf("NoteTurnRunToolFinish() err = %v", err)
	}
	if err := store.UpdateTurnRunProgressMessage(run.ID, 12345); err != nil {
		t.Fatalf("UpdateTurnRunProgressMessage() err = %v", err)
	}

	interrupted, err := store.InterruptRunningTurnRuns()
	if err != nil {
		t.Fatalf("InterruptRunningTurnRuns() err = %v", err)
	}
	if len(interrupted) != 1 {
		t.Fatalf("interrupted len = %d, want 1", len(interrupted))
	}
	if interrupted[0].ID != run.ID {
		t.Fatalf("interrupted run id = %d, want %d", interrupted[0].ID, run.ID)
	}
	if interrupted[0].ToolCallsStarted != 1 {
		t.Fatalf("tool_calls_started = %d, want 1", interrupted[0].ToolCallsStarted)
	}
	if interrupted[0].ToolCallsFinished != 1 {
		t.Fatalf("tool_calls_finished = %d, want 1", interrupted[0].ToolCallsFinished)
	}
	if interrupted[0].LastToolResultPreview != "stdout:\nmatch" {
		t.Fatalf("last_tool_result_preview = %q, want stdout match", interrupted[0].LastToolResultPreview)
	}
	if interrupted[0].ProgressMessageID != 12345 {
		t.Fatalf("progress_message_id = %d, want 12345", interrupted[0].ProgressMessageID)
	}

	pending, err := store.PendingRecoveryTurnRuns(10)
	if err != nil {
		t.Fatalf("PendingRecoveryTurnRuns() err = %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending len = %d, want 1", len(pending))
	}
	if pending[0].Status != TurnRunStatusInterrupted {
		t.Fatalf("pending status = %q, want interrupted", pending[0].Status)
	}

	if err := store.MarkTurnRunsRecovered([]int64{run.ID}, "check logs before retry"); err != nil {
		t.Fatalf("MarkTurnRunsRecovered() err = %v", err)
	}

	pending, err = store.PendingRecoveryTurnRuns(10)
	if err != nil {
		t.Fatalf("PendingRecoveryTurnRuns() after recovery err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending len after recovery = %d, want 0", len(pending))
	}
}

func TestCompleteTurnRun(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 901, UserID: 0}
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	run, err := store.BeginTurnRun(key, TurnRunKindCron, "cron work")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.CompleteTurnRun(run.ID, TurnRunStatusCompleted, ""); err != nil {
		t.Fatalf("CompleteTurnRun() err = %v", err)
	}

	rows, err := store.db.Query(`
		SELECT
			id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id, kind, status, request_text, started_at, completed_at,
			last_activity_at, last_tool_name, last_tool_preview, tool_calls_started, tool_calls_finished, last_tool_result_preview, last_tool_error,
			progress_message_id, error_text, recovery_summary, recovery_logged_at
		FROM turn_runs
		WHERE id = ?
	`, run.ID)
	if err != nil {
		t.Fatalf("query completed turn run: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("expected completed turn run row")
	}
	got, err := scanTurnRun(rows)
	if err != nil {
		t.Fatalf("scanTurnRun() err = %v", err)
	}
	if got.Status != TurnRunStatusCompleted {
		t.Fatalf("status = %q, want completed", got.Status)
	}
	if got.CompletedAt.IsZero() {
		t.Fatal("completed_at is zero, want populated timestamp")
	}
}

func TestArtifactIndexRoundTripAndSearch(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 55, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.TurnCount = 1
	sess.LastFloorMetadata = `{"artifacts":[{"artifact_id":"doc-1","kind":"document","source_type":"document","summary":"roadmap.txt","handling":"extract_text","retention":"child_local","fetch_state":"fetched_local","materialized_path":"/tmp/roadmap.txt"}]}`
	if err := store.Save(sess, []Message{{Role: "assistant", Content: "ok", TurnIndex: 1}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	hits, err := store.SearchArtifacts("roadmap", 10, nil)
	if err != nil {
		t.Fatalf("SearchArtifacts() err = %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("artifact hits len = %d, want 1", len(hits))
	}
	if hits[0].ArtifactID != "doc-1" {
		t.Fatalf("ArtifactID = %q, want doc-1", hits[0].ArtifactID)
	}
	if hits[0].Retention != "child_local" {
		t.Fatalf("Retention = %q, want child_local", hits[0].Retention)
	}
	if hits[0].MaterializedPath != "/tmp/roadmap.txt" {
		t.Fatalf("MaterializedPath = %q, want /tmp/roadmap.txt", hits[0].MaterializedPath)
	}

	scoped, err := store.SearchArtifacts("roadmap", 10, &key)
	if err != nil {
		t.Fatalf("SearchArtifacts(scoped) err = %v", err)
	}
	if len(scoped) != 1 || scoped[0].SessionID != SessionIDForKey(key) {
		t.Fatalf("scoped hits = %#v, want one hit in session", scoped)
	}
}

func TestInitBackfillsArtifactIndexFromExistingSessionFloorMetadata(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "legacy-artifact-index.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}

	legacyDDL := []string{
		`CREATE TABLE schema_version (
			version INTEGER NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO schema_version(version) VALUES (20)`,
		`CREATE TABLE sessions (
			session_id TEXT PRIMARY KEY,
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			last_floor_metadata TEXT,
			turn_count INTEGER NOT NULL DEFAULT 0
		)`,
		`INSERT INTO sessions(session_id, chat_id, user_id, last_floor_metadata, turn_count) VALUES (
			'telegram_dm:777', 777, 0,
			'{"artifacts":[{"artifact_id":"legacy-doc-1","kind":"document","source_type":"document","summary":"legacy-roadmap.pdf","handling":"extract_text","retention":"child_local","fetch_state":"fetched_local","materialized_path":"/tmp/legacy-roadmap.pdf"}]}'
			, 12
		)`,
	}
	for _, stmt := range legacyDDL {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec legacy artifact stmt %q: %v", stmt, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() migration err = %v", err)
	}
	defer store.Close()

	hits, err := store.SearchArtifacts("legacy-roadmap", 10, nil)
	if err != nil {
		t.Fatalf("SearchArtifacts() err = %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("artifact hits len = %d, want 1", len(hits))
	}
	if hits[0].ArtifactID != "legacy-doc-1" {
		t.Fatalf("ArtifactID = %q, want legacy-doc-1", hits[0].ArtifactID)
	}
	if hits[0].SessionID != "telegram_dm:777" {
		t.Fatalf("SessionID = %q, want telegram_dm:777", hits[0].SessionID)
	}
	if hits[0].TurnIndex != 12 {
		t.Fatalf("TurnIndex = %d, want 12", hits[0].TurnIndex)
	}
	if hits[0].MaterializedPath != "/tmp/legacy-roadmap.pdf" {
		t.Fatalf("MaterializedPath = %q, want /tmp/legacy-roadmap.pdf", hits[0].MaterializedPath)
	}
}

func TestArtifactIndexPreservesRepeatedArtifactIDsAcrossTurnsAndSessions(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	keyA := SessionKey{ChatID: 57, UserID: 0}
	sessA, err := store.Load(keyA)
	if err != nil {
		t.Fatalf("Load(keyA) err = %v", err)
	}
	sessA.TurnCount = 1
	sessA.LastFloorMetadata = `{"artifacts":[{"artifact_id":"telegram:location","kind":"structured","source_type":"location","summary":"first location","handling":"inspect_metadata","retention":"session_reference"}]}`
	if err := store.Save(sessA, []Message{{Role: "assistant", Content: "first", TurnIndex: 1}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save(sessA first) err = %v", err)
	}
	sessA.TurnCount = 2
	sessA.LastFloorMetadata = `{"artifacts":[{"artifact_id":"telegram:location","kind":"structured","source_type":"location","summary":"second location","handling":"inspect_metadata","retention":"session_reference"}]}`
	if err := store.Save(sessA, []Message{{Role: "assistant", Content: "second", TurnIndex: 2}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save(sessA second) err = %v", err)
	}

	keyB := SessionKey{ChatID: 58, UserID: 0}
	sessB, err := store.Load(keyB)
	if err != nil {
		t.Fatalf("Load(keyB) err = %v", err)
	}
	sessB.TurnCount = 1
	sessB.LastFloorMetadata = `{"artifacts":[{"artifact_id":"telegram:location","kind":"structured","source_type":"location","summary":"third location","handling":"inspect_metadata","retention":"session_reference"}]}`
	if err := store.Save(sessB, []Message{{Role: "assistant", Content: "third", TurnIndex: 1}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save(sessB) err = %v", err)
	}

	hits, err := store.SearchArtifacts("location", 10, nil)
	if err != nil {
		t.Fatalf("SearchArtifacts() err = %v", err)
	}
	if len(hits) != 3 {
		t.Fatalf("artifact hits len = %d, want 3", len(hits))
	}

	seen := map[string]bool{}
	for _, hit := range hits {
		seen[hit.SessionID+"#"+hit.Summary] = true
	}
	if !seen[SessionIDForKey(keyA)+"#first location"] {
		t.Fatalf("missing first occurrence in hits: %#v", hits)
	}
	if !seen[SessionIDForKey(keyA)+"#second location"] {
		t.Fatalf("missing second occurrence in hits: %#v", hits)
	}
	if !seen[SessionIDForKey(keyB)+"#third location"] {
		t.Fatalf("missing third occurrence in hits: %#v", hits)
	}
}

func TestInitRebuildsArtifactIndexFromMessageFloorMetadata(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "legacy-artifact-index-occurrence.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}

	legacyDDL := []string{
		`CREATE TABLE schema_version (
			version INTEGER NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO schema_version(version) VALUES (21)`,
		`CREATE TABLE sessions (
			session_id TEXT PRIMARY KEY,
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			last_floor_metadata TEXT,
			turn_count INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			floor_content TEXT,
			floor_metadata TEXT,
			tool_calls TEXT,
			tool_id TEXT,
			tool_name TEXT,
			thinking TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			turn_index INTEGER NOT NULL,
			content_chars INTEGER NOT NULL DEFAULT 0,
			compacted INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE artifact_index (
			artifact_id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			turn_index INTEGER NOT NULL DEFAULT 0,
			source_type TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			handling TEXT NOT NULL DEFAULT '',
			retention TEXT NOT NULL DEFAULT '',
			fetch_state TEXT NOT NULL DEFAULT '',
			materialized_path TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO sessions(session_id, chat_id, user_id, last_floor_metadata, turn_count) VALUES ('telegram_dm:777', 777, 0, '', 2)`,
		`INSERT INTO messages(session_id, chat_id, user_id, role, content, floor_metadata, turn_index, content_chars, compacted) VALUES
			('telegram_dm:777', 777, 0, 'assistant', 'one', '{"artifacts":[{"artifact_id":"telegram:location","kind":"structured","source_type":"location","summary":"legacy one","handling":"inspect_metadata","retention":"session_reference"}]}', 1, 3, 0),
			('telegram_dm:777', 777, 0, 'assistant', 'two', '{"artifacts":[{"artifact_id":"telegram:location","kind":"structured","source_type":"location","summary":"legacy two","handling":"inspect_metadata","retention":"session_reference"}]}', 2, 3, 0)`,
	}
	for _, stmt := range legacyDDL {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec legacy artifact stmt %q: %v", stmt, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() migration err = %v", err)
	}
	defer store.Close()

	hits, err := store.SearchArtifacts("legacy", 10, nil)
	if err != nil {
		t.Fatalf("SearchArtifacts() err = %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("artifact hits len = %d, want 2", len(hits))
	}
	if hits[0].TurnIndex == hits[1].TurnIndex {
		t.Fatalf("turn indexes = %d and %d, want distinct occurrences", hits[0].TurnIndex, hits[1].TurnIndex)
	}
}

func TestArtifactIndexIgnoresEphemeralArtifacts(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 56, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.TurnCount = 1
	sess.LastFloorMetadata = `{"artifacts":[{"artifact_id":"img-ephemeral","kind":"image","summary":"throwaway","retention":"ephemeral"}]}`
	if err := store.Save(sess, []Message{{Role: "assistant", Content: "ok", TurnIndex: 1}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	hits, err := store.SearchArtifacts("throwaway", 10, nil)
	if err != nil {
		t.Fatalf("SearchArtifacts() err = %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("artifact hits len = %d, want 0", len(hits))
	}
}
