//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const autonomyAuthorityBehavior = "approvals require an open auto-mode window"

type operatorAutoModeGate struct {
	Mode      string
	Scope     string
	Source    string
	Actor     int64
	ExpiresAt time.Time
}

func (r *Runtime) AutonomyStatusSnapshot() core.AutonomyStatusSnapshot {
	return r.autonomyStatusSnapshot(0, 0, time.Now().UTC())
}

func (r *Runtime) ChatAutonomyStatusSnapshot(chatID int64, adminUserID int64) (core.AutonomyStatusSnapshot, error) {
	return r.autonomyStatusSnapshot(chatID, adminUserID, time.Now().UTC()), nil
}

func (r *Runtime) ChatAutonomyStatusSnapshotForKey(key session.SessionKey, adminUserID int64) (core.AutonomyStatusSnapshot, error) {
	scopeKind, scopeID := operatorAutoTargetScopeForKey(key)
	return r.autonomyStatusSnapshotForScope(key.ChatID, scopeKind, scopeID, adminUserID, time.Now().UTC())
}

func (r *Runtime) ConfigureAutonomy(ctx context.Context, chatID int64, adminUserID int64, args string) (string, error) {
	scopeKind, scopeID := operatorAutoDefaultScope(chatID)
	return r.configureAutonomyForScope(ctx, chatID, scopeKind, scopeID, adminUserID, args)
}

func (r *Runtime) ConfigureAutonomyForKey(ctx context.Context, key session.SessionKey, adminUserID int64, args string) (string, error) {
	scopeKind, scopeID := operatorAutoTargetScopeForKey(key)
	return r.configureAutonomyForScope(ctx, key.ChatID, scopeKind, scopeID, adminUserID, args)
}

func (r *Runtime) configureAutonomyForScope(ctx context.Context, chatID int64, scopeKind string, scopeID string, adminUserID int64, args string) (string, error) {
	if r == nil || r.store == nil {
		return "Auto mode is unavailable.", nil
	}
	if !r.IsTelegramAdmin(adminUserID) {
		return "Auto mode is admin only.", nil
	}
	action, spec, err := parseOperatorAutonomyCommand(args)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	scopeKind = strings.TrimSpace(scopeKind)
	scopeID = strings.TrimSpace(scopeID)
	switch action {
	case "status":
		snapshot, err := r.autonomyStatusSnapshotForScope(chatID, scopeKind, scopeID, adminUserID, now)
		if err != nil {
			return "", err
		}
		return r.renderAutonomyCommandStatus(snapshot, now), nil
	case "off":
		revoked, err := r.store.RevokeOperatorAutonomyOverridesForScope(chatID, adminUserID, scopeKind, scopeID, now)
		if err != nil {
			return "", err
		}
		r.recordOperatorAutoModeEvent(chatID, core.ExecutionEventAutoModeRevoked, "revoked", operatorAutoModePrimaryOverrideForScope(revoked, chatID, scopeKind, scopeID, adminUserID), operatorAutoModeRevokedEventPayload(revoked, now))
		return renderOperatorAutonomyRevoked(revoked, now), nil
	case "double":
		return r.doubleOperatorAutonomyOverrideForScope(ctx, chatID, scopeKind, scopeID, adminUserID, now)
	case "leased":
		if err := r.validateAutonomyLiveOverride(spec.Mode, spec.Duration); err != nil {
			return "", err
		}
		reason := strings.TrimSpace(spec.Reason)
		if reason == "" {
			reason = "bounded auto mode"
		}
		override := session.OperatorAutonomyOverride{
			ID:          newOperatorAutonomyOverrideID(chatID, adminUserID, now),
			AdminUserID: adminUserID,
			ChatID:      chatID,
			ScopeKind:   scopeKind,
			ScopeID:     scopeID,
			Mode:        spec.Mode,
			Scope:       spec.Scope,
			Reason:      reason,
			CreatedAt:   now,
			ExpiresAt:   now.Add(spec.Duration),
			UpdatedAt:   now,
		}
		if _, err := r.store.RevokeOperatorAutonomyOverridesForScope(chatID, adminUserID, scopeKind, scopeID, now); err != nil {
			return "", err
		}
		created, err := r.store.CreateOperatorAutonomyOverride(override)
		if err != nil {
			return "", err
		}
		r.recordOperatorAutoModeEvent(chatID, core.ExecutionEventAutoModeEnabled, "active", created, nil)
		_ = ctx
		return r.renderOperatorAutonomyEnabled(created, now), nil
	case "mission":
		if err := r.validateAutonomyLiveOverride(spec.Mode, 0); err != nil {
			return "", err
		}
		return "", fmt.Errorf("mission autonomy is not active in this build; use leased mode for bounded approval cycling")
	default:
		return "", fmt.Errorf("unknown auto mode action %q", action)
	}
}

func (r *Runtime) autonomyStatusSnapshot(chatID int64, adminUserID int64, now time.Time) core.AutonomyStatusSnapshot {
	scopeKind, scopeID := operatorAutoDefaultScope(chatID)
	snapshot, _ := r.autonomyStatusSnapshotForScope(chatID, scopeKind, scopeID, adminUserID, now)
	return snapshot
}

func (r *Runtime) autonomyStatusSnapshotForScope(chatID int64, scopeKind string, scopeID string, adminUserID int64, now time.Time) (core.AutonomyStatusSnapshot, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	policy := config.EffectiveAutonomyPolicy(nil)
	source := "default"
	if r != nil && r.cfg != nil {
		policy = config.EffectiveAutonomyPolicy(r.cfg)
		source = "config"
	}
	snapshot := core.AutonomyStatusSnapshot{
		GeneratedAt:         now,
		DefaultMode:         policy.DefaultMode,
		Ceiling:             policy.Ceiling,
		AllowLiveOverrides:  policy.AllowLiveOverrides,
		MaxOverrideDuration: policy.MaxOverrideDuration,
		Source:              source,
		AuthorityBehavior:   autonomyAuthorityBehavior,
	}
	if chatID == 0 || r == nil || r.store == nil {
		return snapshot, nil
	}
	override, ok, err := r.activeAutonomyOverrideForScope(chatID, scopeKind, scopeID, adminUserID, now)
	if err != nil || !ok {
		return snapshot, err
	}
	snapshot.ActiveOverrideMode = override.Mode
	snapshot.ActiveOverrideActor = strconv.FormatInt(override.AdminUserID, 10)
	snapshot.ActiveOverrideScope = strings.TrimSpace(override.Scope)
	snapshot.ActiveOverrideExpiry = override.ExpiresAt
	snapshot.AuthorityBehavior = "approval grants may be spent only for prompts allowed by the active auto mode"
	return snapshot, nil
}

func (r *Runtime) activeAutonomyOverride(chatID int64, adminUserID int64, now time.Time) (session.OperatorAutonomyOverride, bool, error) {
	scopeKind, scopeID := operatorAutoDefaultScope(chatID)
	return r.activeAutonomyOverrideForScope(chatID, scopeKind, scopeID, adminUserID, now)
}

func (r *Runtime) activeAutonomyOverrideForScope(chatID int64, scopeKind string, scopeID string, adminUserID int64, now time.Time) (session.OperatorAutonomyOverride, bool, error) {
	if r == nil || r.store == nil || chatID == 0 {
		return session.OperatorAutonomyOverride{}, false, nil
	}
	if err := r.validateAutonomyLiveOverride("leased", 0); err != nil {
		return session.OperatorAutonomyOverride{}, false, nil
	}
	overrides, err := r.store.ActiveOperatorAutonomyOverridesForScope(chatID, scopeKind, scopeID, now)
	if err != nil {
		return session.OperatorAutonomyOverride{}, false, err
	}
	var selected session.OperatorAutonomyOverride
	for _, override := range overrides {
		override = session.NormalizeOperatorAutonomyOverride(override)
		if !override.ActiveAt(now) {
			continue
		}
		if adminUserID > 0 && override.AdminUserID != adminUserID {
			continue
		}
		if selected.ID == "" || override.ExpiresAt.After(selected.ExpiresAt) {
			selected = override
		}
	}
	if selected.ID == "" && adminUserID > 0 {
		for _, override := range overrides {
			override = session.NormalizeOperatorAutonomyOverride(override)
			if !override.ActiveAt(now) {
				continue
			}
			if selected.ID == "" || override.ExpiresAt.After(selected.ExpiresAt) {
				selected = override
			}
		}
	}
	if selected.ID == "" {
		return session.OperatorAutonomyOverride{}, false, nil
	}
	return selected, true, nil
}

func (r *Runtime) operatorAutoModeGate(chatID int64, adminUserID int64, now time.Time) (operatorAutoModeGate, bool, error) {
	scopeKind, scopeID := operatorAutoDefaultScope(chatID)
	return r.operatorAutoModeGateForScope(chatID, scopeKind, scopeID, adminUserID, now)
}

func (r *Runtime) operatorAutoModeGateForScope(chatID int64, scopeKind string, scopeID string, adminUserID int64, now time.Time) (operatorAutoModeGate, bool, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if override, ok, err := r.activeAutonomyOverrideForScope(chatID, scopeKind, scopeID, adminUserID, now); err != nil || ok {
		if err != nil {
			return operatorAutoModeGate{}, false, err
		}
		return operatorAutoModeGate{
			Mode:      override.Mode,
			Scope:     override.Scope,
			Source:    "override",
			Actor:     override.AdminUserID,
			ExpiresAt: override.ExpiresAt,
		}, true, nil
	}
	policy := config.EffectiveAutonomyPolicy(nil)
	if r != nil && r.cfg != nil {
		policy = config.EffectiveAutonomyPolicy(r.cfg)
	}
	if config.NormalizeAutonomyMode(policy.DefaultMode) != "leased" {
		return operatorAutoModeGate{}, false, nil
	}
	return operatorAutoModeGate{
		Mode:   "leased",
		Scope:  session.OperatorAutoApprovalScopeAll,
		Source: "config",
	}, true, nil
}

func (r *Runtime) validateAutonomyLiveOverride(mode string, duration time.Duration) error {
	policy := config.EffectiveAutonomyPolicy(nil)
	if r != nil && r.cfg != nil {
		policy = config.EffectiveAutonomyPolicy(r.cfg)
	}
	if !policy.AllowLiveOverrides {
		return fmt.Errorf("autonomy live overrides are disabled by config")
	}
	ceilingRank, ok := config.AutonomyModeRank(policy.Ceiling)
	if !ok {
		return fmt.Errorf("autonomy ceiling is invalid")
	}
	modeRank, ok := config.AutonomyModeRank(mode)
	if !ok {
		return fmt.Errorf("autonomy mode must be one of off|review_only|ask_first|leased|mission")
	}
	if modeRank > ceilingRank {
		return fmt.Errorf("autonomy mode %s exceeds configured ceiling %s", config.NormalizeAutonomyMode(mode), policy.Ceiling)
	}
	if duration > 0 && duration > policy.MaxOverrideDuration {
		return fmt.Errorf("autonomy live override duration is capped at %s", policy.MaxOverrideDuration)
	}
	return nil
}

type operatorAutonomyCommandSpec struct {
	Mode     string
	Duration time.Duration
	Scope    string
	Reason   string
}

func parseOperatorAutonomyCommand(raw string) (string, operatorAutonomyCommandSpec, error) {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) == 0 {
		return "status", operatorAutonomyCommandSpec{}, nil
	}
	first := strings.ToLower(strings.TrimSpace(fields[0]))
	switch first {
	case "status":
		return "status", operatorAutonomyCommandSpec{}, nil
	case "off", "disable", "revoke", "stop", "review", "review-only", "review_only", "ask", "ask-first", "ask_first":
		return "off", operatorAutonomyCommandSpec{Mode: config.NormalizeAutonomyMode(first)}, nil
	case "double", "2x", "2×", "extend":
		return "double", operatorAutonomyCommandSpec{Mode: "leased"}, nil
	case "lease", "leased":
		spec, err := parseOperatorAutoModeLeaseSpec(strings.Join(fields[1:], " "))
		if err != nil {
			return "", operatorAutonomyCommandSpec{}, err
		}
		return "leased", spec, nil
	case "mission":
		return "mission", operatorAutonomyCommandSpec{Mode: "mission"}, nil
	default:
		return "", operatorAutonomyCommandSpec{}, fmt.Errorf("auto mode action must be status, off, leased, or double")
	}
}

func parseOperatorAutoModeLeaseSpec(raw string) (operatorAutonomyCommandSpec, error) {
	fields := strings.Fields(strings.TrimSpace(raw))
	spec := operatorAutonomyCommandSpec{Mode: "leased", Scope: session.OperatorAutoApprovalScopeAll}
	durationSet := false
	reason := make([]string, 0)
	for _, field := range fields {
		token := strings.TrimSpace(field)
		lower := strings.ToLower(token)
		if token == "" {
			continue
		}
		if strings.HasPrefix(lower, "uses=") || strings.HasPrefix(lower, "max_uses=") || strings.HasPrefix(lower, "max=") {
			return spec, fmt.Errorf("auto mode does not accept a use budget")
		}
		if isOperatorAutoApprovalScope(lower) {
			spec.Scope = session.NormalizeOperatorAutoApprovalScope(lower)
			continue
		}
		if !durationSet {
			if parsed, err := time.ParseDuration(lower); err == nil {
				spec.Duration = parsed
				durationSet = true
				continue
			}
		}
		reason = append(reason, token)
	}
	if !durationSet {
		return spec, fmt.Errorf("auto mode duration, scope, and optional reason are required")
	}
	if spec.Duration < operatorAutoApprovalMinDuration {
		return spec, fmt.Errorf("auto mode duration must be at least %s", operatorAutoApprovalMinDuration)
	}
	spec.Reason = strings.TrimSpace(strings.Join(reason, " "))
	return spec, nil
}

func (r *Runtime) renderAutonomyCommandStatus(snapshot core.AutonomyStatusSnapshot, now time.Time) string {
	if strings.TrimSpace(snapshot.ActiveOverrideMode) == "" {
		return "Auto mode has no live override. Default: " + autonomyModeRuntimeLabel(snapshot.DefaultMode) + "; ceiling: " + autonomyModeRuntimeLabel(snapshot.Ceiling) + ". Approval grants need the configured default or a bounded approval window before they can be spent."
	}
	scope := strings.TrimSuffix(operatorAutoApprovalScopeLabel(snapshot.ActiveOverrideScope), ".")
	if strings.TrimSpace(scope) == "" {
		scope = "matching prompts"
	}
	expires := ""
	if !snapshot.ActiveOverrideExpiry.IsZero() {
		expires = " until " + formatApprovalWindowExpiry(snapshot.ActiveOverrideExpiry, now, r.approvalWindowDisplayLocation())
	}
	return "Auto mode is live in " + strings.ToLower(autonomyModeRuntimeLabel(snapshot.ActiveOverrideMode)) + " mode for " + scope + expires + ". Approval grants may be spent only for prompts allowed by this mode."
}

func (r *Runtime) doubleOperatorAutonomyOverride(ctx context.Context, chatID int64, adminUserID int64, now time.Time) (string, error) {
	scopeKind, scopeID := operatorAutoDefaultScope(chatID)
	return r.doubleOperatorAutonomyOverrideForScope(ctx, chatID, scopeKind, scopeID, adminUserID, now)
}

func (r *Runtime) doubleOperatorAutonomyOverrideForScope(ctx context.Context, chatID int64, scopeKind string, scopeID string, adminUserID int64, now time.Time) (string, error) {
	_ = ctx
	override, ok, err := r.activeOperatorAutonomyOverrideForAdminAndScope(chatID, scopeKind, scopeID, adminUserID, now)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("no active auto mode to double")
	}
	override = session.NormalizeOperatorAutonomyOverride(override)
	policy := config.EffectiveAutonomyPolicy(nil)
	if r != nil && r.cfg != nil {
		policy = config.EffectiveAutonomyPolicy(r.cfg)
	}
	previousDuration, doubledDuration := doubledOperatorWindowDuration(override.CreatedAt, override.ExpiresAt, now, operatorAutoApprovalMinDuration, policy.MaxOverrideDuration)
	if err := r.validateAutonomyLiveOverride(override.Mode, doubledDuration); err != nil {
		return "", err
	}
	createdOverride := session.OperatorAutonomyOverride{
		ID:          newOperatorAutonomyOverrideID(chatID, adminUserID, now),
		AdminUserID: adminUserID,
		ChatID:      chatID,
		Mode:        override.Mode,
		ScopeKind:   scopeKind,
		ScopeID:     scopeID,
		Scope:       override.Scope,
		Reason:      override.Reason,
		CreatedAt:   now,
		ExpiresAt:   now.Add(doubledDuration),
		UpdatedAt:   now,
	}
	if _, err := r.store.RevokeOperatorAutonomyOverridesForScope(chatID, adminUserID, scopeKind, scopeID, now); err != nil {
		return "", err
	}
	created, err := r.store.CreateOperatorAutonomyOverride(createdOverride)
	if err != nil {
		return "", err
	}
	r.recordOperatorAutoModeEvent(chatID, core.ExecutionEventAutoModeEnabled, "active", created, map[string]any{
		"doubled_from_override_id":  strings.TrimSpace(override.ID),
		"previous_duration_seconds": int64(previousDuration / time.Second),
		"new_duration_seconds":      int64(doubledDuration / time.Second),
	})
	return r.renderOperatorAutonomyDoubled(created, now, previousDuration, doubledDuration), nil
}

func (r *Runtime) activeOperatorAutonomyOverrideForAdmin(chatID int64, adminUserID int64, now time.Time) (session.OperatorAutonomyOverride, bool, error) {
	scopeKind, scopeID := operatorAutoDefaultScope(chatID)
	return r.activeOperatorAutonomyOverrideForAdminAndScope(chatID, scopeKind, scopeID, adminUserID, now)
}

func (r *Runtime) activeOperatorAutonomyOverrideForAdminAndScope(chatID int64, scopeKind string, scopeID string, adminUserID int64, now time.Time) (session.OperatorAutonomyOverride, bool, error) {
	if r == nil || r.store == nil || chatID == 0 || adminUserID <= 0 {
		return session.OperatorAutonomyOverride{}, false, nil
	}
	if err := r.validateAutonomyLiveOverride("leased", 0); err != nil {
		return session.OperatorAutonomyOverride{}, false, nil
	}
	overrides, err := r.store.ActiveOperatorAutonomyOverridesForScope(chatID, scopeKind, scopeID, now)
	if err != nil {
		return session.OperatorAutonomyOverride{}, false, err
	}
	for _, override := range overrides {
		override = session.NormalizeOperatorAutonomyOverride(override)
		if override.AdminUserID == adminUserID && override.ActiveAt(now) {
			return override, true, nil
		}
	}
	return session.OperatorAutonomyOverride{}, false, nil
}

func (r *Runtime) renderOperatorAutonomyDoubled(override session.OperatorAutonomyOverride, now time.Time, previousDuration time.Duration, doubledDuration time.Duration) string {
	override = session.NormalizeOperatorAutonomyOverride(override)
	scope := strings.TrimSuffix(operatorAutoApprovalScopeLabel(override.Scope), ".")
	return "Auto mode was extended for " + scope + " until " + formatApprovalWindowExpiry(override.ExpiresAt, now, r.approvalWindowDisplayLocation()) + " (extended from " + roundDuration(previousDuration) + " to " + roundDuration(doubledDuration) + ")."
}

func (r *Runtime) renderOperatorAutonomyEnabled(override session.OperatorAutonomyOverride, now time.Time) string {
	override = session.NormalizeOperatorAutonomyOverride(override)
	scope := strings.TrimSuffix(operatorAutoApprovalScopeLabel(override.Scope), ".")
	text := "Auto mode is live for " + scope + " until " + formatApprovalWindowExpiry(override.ExpiresAt, now, r.approvalWindowDisplayLocation()) + ". Matching approval grants may be spent until it expires."
	if reason := strings.TrimSpace(override.Reason); reason != "" {
		text += " Reason: " + reason + "."
	}
	return text
}

func renderOperatorAutonomyRevoked(overrides []session.OperatorAutonomyOverride, now time.Time) string {
	if len(overrides) == 0 {
		return "Auto mode is already off for this chat. Approve one request and open an approval window to use it again."
	}
	active := operatorAutoModeActiveOverrides(overrides, now)
	if len(active) > 0 {
		return "Auto mode is off; cleared " + operatorAutoModeOverrideSummary(active) + ". New approval grants need a live gate again."
	}
	if len(overrides) > 1 {
		return fmt.Sprintf("Auto mode is off; cleared %d stored mode records. New approval grants need a live gate again.", len(overrides))
	}
	return "Auto mode is off; cleared stored mode record. New approval grants need a live gate again."
}

func operatorAutoModeActiveOverrides(overrides []session.OperatorAutonomyOverride, now time.Time) []session.OperatorAutonomyOverride {
	out := make([]session.OperatorAutonomyOverride, 0, len(overrides))
	for _, override := range overrides {
		override = session.NormalizeOperatorAutonomyOverride(override)
		if override.ActiveAt(now) {
			out = append(out, override)
		}
	}
	return out
}

func operatorAutoModeOverrideSummary(overrides []session.OperatorAutonomyOverride) string {
	if len(overrides) == 0 {
		return "0 modes"
	}
	if len(overrides) > 1 {
		return fmt.Sprintf("%d modes", len(overrides))
	}
	override := session.NormalizeOperatorAutonomyOverride(overrides[0])
	return autonomyModeRuntimeLabel(override.Mode) + ", " + operatorAutoApprovalScopeLabel(override.Scope)
}

func operatorAutoModePrimaryOverride(overrides []session.OperatorAutonomyOverride, chatID int64, adminUserID int64) session.OperatorAutonomyOverride {
	scopeKind, scopeID := operatorAutoDefaultScope(chatID)
	return operatorAutoModePrimaryOverrideForScope(overrides, chatID, scopeKind, scopeID, adminUserID)
}

func operatorAutoModePrimaryOverrideForScope(overrides []session.OperatorAutonomyOverride, chatID int64, scopeKind string, scopeID string, adminUserID int64) session.OperatorAutonomyOverride {
	if len(overrides) > 0 {
		return session.NormalizeOperatorAutonomyOverride(overrides[0])
	}
	return session.OperatorAutonomyOverride{
		AdminUserID: adminUserID,
		ChatID:      chatID,
		ScopeKind:   strings.TrimSpace(scopeKind),
		ScopeID:     strings.TrimSpace(scopeID),
	}
}

func operatorAutoModeRevokedEventPayload(overrides []session.OperatorAutonomyOverride, now time.Time) map[string]any {
	ids := make([]string, 0, len(overrides))
	activeCount := 0
	for _, override := range overrides {
		override = session.NormalizeOperatorAutonomyOverride(override)
		if override.ID != "" {
			ids = append(ids, override.ID)
		}
		if override.ActiveAt(now) {
			activeCount++
		}
	}
	payload := map[string]any{
		"revoked_count":        len(overrides),
		"revoked_active_count": activeCount,
	}
	if len(ids) > 0 {
		payload["revoked_override_ids"] = ids
	}
	return payload
}

func newOperatorAutonomyOverrideID(chatID int64, adminUserID int64, now time.Time) string {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return fmt.Sprintf("mode-%d-%d-%d", adminUserID, chatID, now.UTC().UnixNano())
}

func operatorAutoModeScopeAllows(scope string, req operatorAutoApprovalRequest) bool {
	return operatorAutoApprovalScopeAllows(scope, req)
}

func operatorAutoModeScopeIntersects(modeScope string, approvalScope string) bool {
	modeScope = session.NormalizeOperatorAutoApprovalScope(modeScope)
	approvalScope = session.NormalizeOperatorAutoApprovalScope(approvalScope)
	return modeScope == session.OperatorAutoApprovalScopeAll ||
		approvalScope == session.OperatorAutoApprovalScopeAll ||
		modeScope == approvalScope
}

func autonomyModeRuntimeLabel(mode string) string {
	switch config.NormalizeAutonomyMode(mode) {
	case "off":
		return "Off"
	case "review_only":
		return "Review only"
	case "ask_first":
		return "Ask first"
	case "leased":
		return "Leased"
	case "mission":
		return "Mission"
	default:
		if strings.TrimSpace(mode) == "" {
			return "Ask first"
		}
		return strings.TrimSpace(mode)
	}
}

func (r *Runtime) recordOperatorAutoModeEvent(chatID int64, eventType string, status string, override session.OperatorAutonomyOverride, extra map[string]any) {
	if r == nil || r.store == nil || chatID == 0 {
		return
	}
	override = session.NormalizeOperatorAutonomyOverride(override)
	payload := map[string]any{
		"override_id":       strings.TrimSpace(override.ID),
		"admin_user_id":     override.AdminUserID,
		"mode":              strings.TrimSpace(override.Mode),
		"scope":             strings.TrimSpace(override.Scope),
		"target_scope_kind": strings.TrimSpace(override.ScopeKind),
		"target_scope_id":   strings.TrimSpace(override.ScopeID),
		"reason":            strings.TrimSpace(override.Reason),
	}
	if !override.ExpiresAt.IsZero() {
		payload["expires_at"] = override.ExpiresAt.UTC().Format(time.RFC3339)
	}
	for key, value := range extra {
		if strings.TrimSpace(key) != "" {
			payload[key] = value
		}
	}
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	r.recordExecutionEvent(key, eventType, "auto_mode", status, payload, time.Now().UTC())
}
