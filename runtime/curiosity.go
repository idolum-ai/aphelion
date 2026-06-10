//go:build linux

package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

const curiositySessionID = "admin-curiosity"

func (r *Runtime) StartCuriosityLoop(ctx context.Context, logger func(string, ...any)) {
	if r == nil || r.store == nil || r.provider == nil || !r.cfg.Curiosity.Enabled {
		return
	}
	if logger == nil {
		logger = log.Printf
	}
	cadence, err := time.ParseDuration(strings.TrimSpace(r.cfg.Curiosity.Every))
	if err != nil || cadence <= 0 {
		logger("WARN curiosity disabled due to invalid cadence: %q err=%v", r.cfg.Curiosity.Every, err)
		if err != nil {
			r.reportOperationalIssue(ctx, "curiosity", fmt.Errorf("invalid curiosity cadence %q: %w", r.cfg.Curiosity.Every, err))
		} else {
			r.reportOperationalIssue(ctx, "curiosity", fmt.Errorf("invalid curiosity cadence %q", r.cfg.Curiosity.Every))
		}
		return
	}
	r.startBackgroundLoop("curiosity", func() {
		runPeriodic(ctx, cadence, func(runCtx context.Context) {
			if err := r.runCuriosityOnce(runCtx, time.Now().UTC()); err != nil {
				logger("WARN curiosity failed: %v", err)
				r.reportOperationalIssue(runCtx, "curiosity", err)
			}
		})
	})
}

func (r *Runtime) runCuriosityOnce(ctx context.Context, now time.Time) (err error) {
	if r == nil || r.store == nil || r.provider == nil || !r.cfg.Curiosity.Enabled {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	key := curiositySessionKey()
	unlock := r.lockSession(key)
	defer unlock()

	actor := r.curiosityPrincipal()
	scope, err := r.scopeForPrincipal(actor)
	if err != nil {
		return fmt.Errorf("resolve curiosity scope: %w", err)
	}
	baseTools := r.toolsForPrincipal(actor, key)
	baseTools = toolRegistryForRunKind(baseTools, session.TurnRunKindCuriosity)
	if baseTools == nil {
		r.recordExecutionEvent(key, core.ExecutionEventCuriositySkipped, "curiosity", "skipped", map[string]any{"reason": "tools_unavailable"}, now)
		return nil
	}

	lease, err := r.ensureConfiguredCuriosityLease(now)
	if err != nil {
		return err
	}
	candidates, err := r.curiosityCandidates(baseTools, scope.SharedMemoryRoot, now)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		r.recordExecutionEvent(key, core.ExecutionEventCuriositySkipped, "curiosity", "skipped", map[string]any{"reason": "no_candidate"}, now)
		return nil
	}
	candidate := candidates[0]
	r.recordExecutionEvent(key, core.ExecutionEventCuriositySelected, "curiosity", "selected", curiosityCandidatePayload(candidate), now)

	lease, ok, err := r.store.ConsumeCuriosityLeaseTurn(lease.ID, now)
	if err != nil {
		return fmt.Errorf("consume curiosity lease: %w", err)
	}
	if !ok {
		r.recordExecutionEvent(key, core.ExecutionEventCuriositySkipped, "curiosity", "skipped", map[string]any{
			"reason":     "lease_unavailable",
			"lease_id":   lease.ID,
			"status":     lease.Status,
			"turns_used": lease.TurnsUsed,
			"budget":     lease.DailyTurnBudget,
		}, now)
		return nil
	}

	requestText := renderCuriosityRequest(candidate)
	monitor, err := r.startTurnMonitor(ctx, key, session.TurnRunKindCuriosity, requestText, nil, nil, core.InboundMessage{})
	if err != nil {
		return err
	}
	var finishErr error
	defer func() { monitor.Finish(ctx, finishErr) }()

	tools := &curiosityToolRegistry{base: monitor.observeTools(baseTools), candidate: candidate}
	opts := r.reasoningOptionsForRun(session.TurnRunKindCuriosity)
	if opts != nil {
		copy := *opts
		copy.Observer = monitor
		opts = &copy
	}
	r.recordExecutionEvent(key, core.ExecutionEventCuriosityStarted, "curiosity", "started", map[string]any{
		"lease_id":     lease.ID,
		"candidate_id": candidate.ID,
		"source_kind":  candidate.SourceKind,
		"source_ref":   candidate.SourceRef,
	}, now)
	result, _, runErr := agent.RunTurn(monitor.Context(), r.provider, tools, curiosityBudget(r.cfg.Curiosity), opts, []agent.Message{
		{Role: "system", Content: curiositySystemPrompt()},
		{Role: "user", Content: requestText},
	})
	if runErr != nil {
		finishErr = runErr
		r.recordExecutionEvent(key, core.ExecutionEventCuriosityFailed, "curiosity", "failed", map[string]any{
			"lease_id":     lease.ID,
			"candidate_id": candidate.ID,
			"error":        trimError(runErr.Error()),
		}, time.Now().UTC())
		return runErr
	}
	if result == nil {
		finishErr = fmt.Errorf("curiosity turn returned no result")
		return finishErr
	}
	if result.ProviderFailure != "" || result.Recovery != nil {
		r.recordExecutionEvent(key, core.ExecutionEventCuriosityFailed, "curiosity", "failed", map[string]any{
			"lease_id":          lease.ID,
			"candidate_id":      candidate.ID,
			"provider_failure":  trimError(result.ProviderFailure),
			"recovery_required": result.Recovery != nil,
		}, time.Now().UTC())
		return nil
	}
	used, outputHash, _ := tools.Used()
	if !used {
		r.recordExecutionEvent(key, core.ExecutionEventCuriositySkipped, "curiosity", "skipped", map[string]any{
			"reason":       "candidate_tool_not_used",
			"lease_id":     lease.ID,
			"candidate_id": candidate.ID,
		}, time.Now().UTC())
		return nil
	}
	parsed, err := parseCuriosityObservation(result.Text)
	if err != nil {
		r.recordExecutionEvent(key, core.ExecutionEventCuriosityFailed, "curiosity", "malformed", map[string]any{
			"lease_id":     lease.ID,
			"candidate_id": candidate.ID,
			"error":        trimError(err.Error()),
		}, time.Now().UTC())
		return nil
	}
	obs, err := r.store.RecordCuriosityObservation(key, session.CuriosityObservationInput{
		LeaseID:     lease.ID,
		CandidateID: candidate.ID,
		SourceKind:  candidate.SourceKind,
		SourceRef:   candidate.SourceRef,
		SubjectKey:  firstNonEmpty(candidate.SubjectKey, parsed.SubjectKey),
		Summary:     parsed.Summary,
		ContentHash: firstNonEmpty(outputHash, parsed.ContentHash),
		Confidence:  parsed.Confidence,
		ObservedAt:  now,
		Evidence:    curiosityEvidence(candidate, outputHash, append([]session.RecordReference(nil), parsed.Evidence...)),
	}, time.Now().UTC())
	if err != nil {
		finishErr = err
		return fmt.Errorf("record curiosity observation: %w", err)
	}
	if err := r.recordCuriosityInteriorSignal(obs, candidate, now); err != nil {
		log.Printf("WARN curiosity interior signal write failed: %v", err)
	}
	r.recordExecutionEvent(key, core.ExecutionEventCuriosityObservationRecorded, "curiosity", "recorded", map[string]any{
		"lease_id":       lease.ID,
		"observation_id": obs.ID,
		"candidate_id":   candidate.ID,
		"source_kind":    candidate.SourceKind,
		"source_ref":     candidate.SourceRef,
		"subject_key":    obs.SubjectKey,
		"confidence":     obs.Confidence,
	}, time.Now().UTC())
	return nil
}

func curiosityScopeRef() session.ScopeRef {
	return session.ScopeRef{Kind: session.ScopeKindCuriosity, ID: curiositySessionID}
}

func curiositySessionKey() session.SessionKey {
	return session.SessionKey{ChatID: cronSessionChatID(curiositySessionID), UserID: 0, Scope: curiosityScopeRef()}
}

func (r *Runtime) curiosityPrincipal() principal.Principal {
	for _, id := range r.cfg.Principals.Telegram.ApprovedUserIDs {
		if id > 0 {
			return principal.Principal{Role: principal.RoleApprovedUser, TelegramUserID: id}
		}
	}
	for _, id := range r.cfg.Principals.Telegram.AdminUserIDs {
		if id > 0 {
			return principal.Principal{Role: principal.RoleApprovedUser, TelegramUserID: id}
		}
	}
	return principal.Principal{Role: principal.RoleApprovedUser}
}

func (r *Runtime) ensureConfiguredCuriosityLease(now time.Time) (session.CuriosityLease, error) {
	cfg := r.cfg.Curiosity
	kinds := normalizedCuriositySourceClasses(cfg)
	refs := curiosityAllowedSourceRefs(cfg, kinds)
	ttl, err := time.ParseDuration(strings.TrimSpace(cfg.LeaseTTL))
	if err != nil || ttl <= 0 {
		return session.CuriosityLease{}, fmt.Errorf("invalid curiosity lease ttl %q", cfg.LeaseTTL)
	}
	periodStart := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	period := periodStart.Format("2006-01-02")
	return r.store.EnsureCuriosityLease(session.CuriosityLease{
		ID:                 session.CuriosityLeaseID(period, kinds, refs),
		Status:             session.CuriosityLeaseStatusActive,
		Scope:              curiosityScopeRef(),
		LeaseClass:         session.CuriosityLeaseClassReadOnly,
		WorkAction:         session.CuriosityWorkActionLook,
		AllowedSourceKinds: kinds,
		AllowedSourceRefs:  refs,
		DailyTurnBudget:    cfg.DailyTurnBudget,
		MaxLooksPerTurn:    cfg.MaxLooksPerTurn,
		PeriodStart:        period,
		ApprovedBy:         "config:curiosity",
		CreatedAt:          now,
		ExpiresAt:          periodStart.Add(ttl),
		UpdatedAt:          now,
	}, now)
}

func (r *Runtime) curiosityCandidates(tools agent.ToolRegistry, sharedMemoryRoot string, now time.Time) ([]curiosityCandidate, error) {
	states, err := r.store.InteriorSignalStates(session.SessionKey{ChatID: heartbeatSessionChatID, UserID: 0, Scope: heartbeatScopeRef()}, now)
	if err != nil {
		return nil, fmt.Errorf("load interior signal states: %w", err)
	}
	allowed := curiositySourceClassSet(r.cfg.Curiosity)
	defs := toolDefinitionSet(tools)
	out := make([]curiosityCandidate, 0, len(states)*2)
	for _, state := range states {
		if !isInteriorSignalCategory(state.Category) || state.Intensity < r.cfg.Curiosity.MinSignalIntensity {
			continue
		}
		supported, err := r.curiositySignalHasIndependentSupport(state, now)
		if err != nil {
			return nil, err
		}
		if !supported {
			continue
		}
		query := firstNonEmpty(state.Summary, state.SubjectKey)
		if allowed[session.CuriositySourceSession] && defs["session_search"] {
			out = append(out, newCuriosityCandidate(state, session.CuriositySourceSession, "session_search:"+state.SubjectKey, "session_search", map[string]any{
				"query": query,
				"scope": "all",
				"limit": 3,
			}))
		}
		if allowed[session.CuriositySourceMemory] && defs["read_file"] {
			for _, path := range r.cfg.Curiosity.MemoryPaths {
				path = strings.TrimSpace(path)
				if path == "" {
					continue
				}
				toolPath := curiosityMemoryToolPath(sharedMemoryRoot, path)
				out = append(out, newCuriosityCandidate(state, session.CuriositySourceMemory, path, "read_file", map[string]any{
					"path":      toolPath,
					"full":      true,
					"max_bytes": 32768,
				}))
			}
		}
		if allowed[session.CuriositySourceWorkspace] && defs["read_file"] {
			for _, path := range r.cfg.Curiosity.WorkspacePaths {
				path = strings.TrimSpace(path)
				if path == "" {
					continue
				}
				out = append(out, newCuriosityCandidate(state, session.CuriositySourceWorkspace, path, "read_file", map[string]any{
					"path":      path,
					"full":      true,
					"max_bytes": 32768,
				}))
			}
		}
		if allowed[session.CuriositySourceURL] && defs["fetch_url"] {
			for _, rawURL := range r.cfg.Curiosity.AllowlistedURLs {
				rawURL = strings.TrimSpace(rawURL)
				if rawURL == "" {
					continue
				}
				out = append(out, newCuriosityCandidate(state, session.CuriositySourceURL, rawURL, "fetch_url", map[string]any{
					"url":           rawURL,
					"max_bytes":     131072,
					"excerpt_bytes": 4096,
				}))
			}
		}
		if allowed[session.CuriositySourceMemory] && defs["semantic_search"] {
			out = append(out, newCuriosityCandidate(state, session.CuriositySourceMemory, "semantic_search:"+state.SubjectKey, "semantic_search", map[string]any{
				"query": query,
				"scope": "shared",
				"limit": 3,
			}))
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SignalIntensity != out[j].SignalIntensity {
			return out[i].SignalIntensity > out[j].SignalIntensity
		}
		if out[i].SourceKind != out[j].SourceKind {
			return out[i].SourceKind < out[j].SourceKind
		}
		return out[i].SourceRef < out[j].SourceRef
	})
	if len(out) > 12 {
		out = out[:12]
	}
	return out, nil
}

func curiosityMemoryToolPath(sharedMemoryRoot string, rel string) string {
	rel = strings.TrimSpace(rel)
	root := strings.TrimSpace(sharedMemoryRoot)
	if root == "" || filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(root, rel)
}

func newCuriosityCandidate(state session.InteriorSignalState, sourceKind string, sourceRef string, toolName string, input map[string]any) curiosityCandidate {
	raw, _ := json.Marshal(input)
	sum := sha256.Sum256([]byte(strings.Join([]string{state.Category, state.SubjectKey, sourceKind, sourceRef, toolName, string(raw)}, "\x00")))
	return curiosityCandidate{
		ID:              "curiosity-" + hex.EncodeToString(sum[:])[:12],
		SourceKind:      sourceKind,
		SourceRef:       strings.TrimSpace(sourceRef),
		SubjectKey:      strings.TrimSpace(state.SubjectKey),
		SignalCategory:  strings.TrimSpace(state.Category),
		SignalSummary:   strings.TrimSpace(state.Summary),
		SignalIntensity: state.Intensity,
		ToolName:        strings.TrimSpace(toolName),
		ToolInput:       raw,
	}
}

func curiositySystemPrompt() string {
	return `You are Aphelion's curiosity lane.

You have one read-only look. Use exactly the selected tool call with exactly the selected JSON input. Do not call any other tool. Do not ask for approval. Do not propose work. Do not send user-facing copy.

After the tool result, return exactly one JSON object:
{"summary":"one concrete observation from the source","subject_key":"stable-kebab-case-subject","confidence":0.0,"content_hash":"sha256:optional","evidence":[{"kind":"source","ref":"optional","label":"optional"}]}`
}

func renderCuriosityRequest(candidate curiosityCandidate) string {
	raw, _ := json.MarshalIndent(candidate, "", "  ")
	return "Selected curiosity candidate:\n" + string(raw)
}

type curiosityObservationOutput struct {
	Summary     string                    `json:"summary"`
	SubjectKey  string                    `json:"subject_key"`
	Confidence  float64                   `json:"confidence"`
	ContentHash string                    `json:"content_hash"`
	Evidence    []session.RecordReference `json:"evidence"`
}

func parseCuriosityObservation(raw string) (curiosityObservationOutput, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return curiosityObservationOutput{}, fmt.Errorf("curiosity observation response is empty")
	}
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end < start {
		return curiosityObservationOutput{}, fmt.Errorf("curiosity observation response did not contain a JSON object")
	}
	var out curiosityObservationOutput
	if err := json.Unmarshal([]byte(raw[start:end+1]), &out); err != nil {
		return curiosityObservationOutput{}, fmt.Errorf("decode curiosity observation JSON: %w", err)
	}
	out.Summary = strings.TrimSpace(out.Summary)
	out.SubjectKey = strings.TrimSpace(out.SubjectKey)
	out.ContentHash = strings.TrimSpace(out.ContentHash)
	out.Evidence = session.NormalizeRecordReferences(out.Evidence)
	if out.Confidence <= 0 || out.Confidence > 1 {
		out.Confidence = 0.55
	}
	if out.Summary == "" {
		return curiosityObservationOutput{}, fmt.Errorf("curiosity observation summary is required")
	}
	return out, nil
}

func (r *Runtime) recordCuriosityInteriorSignal(obs session.CuriosityObservation, candidate curiosityCandidate, now time.Time) error {
	_, err := r.store.RecordInteriorSignalObservations(session.SessionKey{ChatID: heartbeatSessionChatID, UserID: 0, Scope: heartbeatScopeRef()}, []session.InteriorSignalObservationInput{{
		Category:          firstNonEmpty(candidate.SignalCategory, hiddenInputSemanticRecurrence),
		SubjectKey:        firstNonEmpty(candidate.SubjectKey, obs.SubjectKey),
		Summary:           obs.Summary,
		Source:            "curiosity",
		Evidence:          obs.Evidence,
		SourceFingerprint: shortRuntimeHash("curiosity", obs.LeaseID, obs.CandidateID, obs.ContentHash),
		Weight:            0.20,
		Confidence:        obs.Confidence,
		ObservedAt:        now,
	}}, now)
	return err
}

func curiosityEvidence(candidate curiosityCandidate, outputHash string, refs []session.RecordReference) []session.RecordReference {
	refs = append(refs,
		session.RecordReference{Kind: "curiosity_source", Ref: candidate.SourceKind + ":" + candidate.SourceRef, Label: "selected curiosity source"},
		session.RecordReference{Kind: "curiosity_candidate", Ref: candidate.ID, Label: candidate.ToolName},
	)
	if candidate.SourceKind == session.CuriositySourceURL {
		refs = append(refs, session.RecordReference{Kind: "untrusted_external_source", Ref: candidate.SourceRef, Label: "third-party fetched text"})
	}
	if strings.TrimSpace(outputHash) != "" {
		refs = append(refs, session.RecordReference{Kind: "tool_output", Ref: outputHash, Label: "read-only look"})
	}
	return session.NormalizeRecordReferences(refs)
}

func (r *Runtime) curiositySignalHasIndependentSupport(state session.InteriorSignalState, now time.Time) (bool, error) {
	weight, err := r.store.InteriorSignalAppliedWeightSinceExcludingSource(
		session.SessionKey{ChatID: heartbeatSessionChatID, UserID: 0, Scope: heartbeatScopeRef()},
		session.InteriorSignalRef{Category: state.Category, SubjectKey: state.SubjectKey},
		now.Add(-session.InteriorSignalAppliedObservationRetention),
		"curiosity",
	)
	if err != nil {
		return false, fmt.Errorf("load curiosity independent signal support: %w", err)
	}
	return weight > 0, nil
}

func curiosityCandidatePayload(candidate curiosityCandidate) map[string]any {
	return map[string]any{
		"candidate_id":     candidate.ID,
		"source_kind":      candidate.SourceKind,
		"source_ref":       candidate.SourceRef,
		"subject_key":      candidate.SubjectKey,
		"signal_category":  candidate.SignalCategory,
		"signal_intensity": candidate.SignalIntensity,
		"tool":             candidate.ToolName,
	}
}

func curiosityBudget(cfg config.CuriosityConfig) *agent.Budget {
	looks := cfg.MaxLooksPerTurn
	if looks <= 0 {
		looks = 1
	}
	return &agent.Budget{
		Max:               looks + 2,
		Caution:           0.7,
		Warning:           0.9,
		ToolCallSoftLimit: looks,
		ToolCallHardLimit: looks,
	}
}

func normalizedCuriositySourceClasses(cfg config.CuriosityConfig) []string {
	if len(cfg.SourceClasses) == 0 {
		return []string{session.CuriositySourceSession, session.CuriositySourceMemory}
	}
	out := make([]string, 0, len(cfg.SourceClasses))
	seen := make(map[string]struct{}, len(cfg.SourceClasses))
	for _, source := range cfg.SourceClasses {
		source = strings.ToLower(strings.TrimSpace(source))
		if source == "" {
			continue
		}
		if _, ok := seen[source]; ok {
			continue
		}
		seen[source] = struct{}{}
		out = append(out, source)
	}
	return out
}

func curiositySourceClassSet(cfg config.CuriosityConfig) map[string]bool {
	out := make(map[string]bool)
	for _, source := range normalizedCuriositySourceClasses(cfg) {
		out[source] = true
	}
	return out
}

func curiosityAllowedSourceRefs(cfg config.CuriosityConfig, kinds []string) []string {
	allowed := make(map[string]bool, len(kinds))
	for _, kind := range kinds {
		allowed[strings.TrimSpace(kind)] = true
	}
	refs := make([]string, 0, len(cfg.WorkspacePaths)+len(cfg.MemoryPaths)+len(cfg.AllowlistedURLs)+1)
	if allowed[session.CuriositySourceSession] {
		refs = append(refs, "session_search")
	}
	if allowed[session.CuriositySourceMemory] {
		refs = append(refs, cfg.MemoryPaths...)
		refs = append(refs, "semantic_search:shared")
	}
	if allowed[session.CuriositySourceWorkspace] {
		refs = append(refs, cfg.WorkspacePaths...)
	}
	if allowed[session.CuriositySourceURL] {
		refs = append(refs, cfg.AllowlistedURLs...)
	}
	sort.Strings(refs)
	return refs
}

func toolDefinitionSet(tools agent.ToolRegistry) map[string]bool {
	out := make(map[string]bool)
	if tools == nil {
		return out
	}
	for _, def := range tools.Definitions() {
		if name := strings.TrimSpace(def.Name); name != "" {
			out[name] = true
		}
	}
	return out
}
