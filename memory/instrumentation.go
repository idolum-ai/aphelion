//go:build linux

package memory

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	memoryEntryMarkerPrefix    = "<!-- aphelion-memory-entry:v1"
	memoryProposalMarkerPrefix = "<!-- aphelion-memory-proposal:v1"
	memoryMarkerEnd            = "-->"

	EntryStatusActive     = "active"
	EntryStatusStale      = "stale"
	EntryStatusSuperseded = "superseded"
	EntryStatusRejected   = "rejected"

	ProposalStatusProposed = "proposed"
	ProposalStatusApproved = "approved"
	ProposalStatusRejected = "rejected"
)

// MemoryEntry is the structured view of one filesystem-backed memory item.
// Markdown remains the source of truth; this shape exists for indexing and
// audit instrumentation.
type MemoryEntry struct {
	ID            string
	Scope         string
	Store         string
	Kind          string
	Status        string
	SourceKind    string
	SourceRef     string
	Confidence    string
	CreatedAt     string
	MigratedAt    string
	ContentSHA256 string
	Path          string
	Ordinal       int
	Content       string
}

type MemoryEvent struct {
	EventID       string            `json:"event_id"`
	Type          string            `json:"type"`
	Scope         string            `json:"scope,omitempty"`
	Store         string            `json:"store,omitempty"`
	Path          string            `json:"path,omitempty"`
	ProposalID    string            `json:"proposal_id,omitempty"`
	MemoryID      string            `json:"memory_id,omitempty"`
	Status        string            `json:"status,omitempty"`
	Action        string            `json:"action,omitempty"`
	ContentSHA256 string            `json:"content_sha256,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
}

type ProposalRequest struct {
	Root       string
	Scope      string
	Store      string
	SourceKind string
	SourceRef  string
	Reason     string
	Content    string
	Now        time.Time
}

type MemoryProposal struct {
	ID            string
	Scope         string
	Store         string
	Status        string
	SourceKind    string
	SourceRef     string
	Reason        string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	ContentSHA256 string
	Path          string
	Content       string
}

type ProposalListOptions struct {
	Root   string
	Status string
	Limit  int
}

func KnownStoreNames() []string {
	return []string{StoreMemory, StoreKnowledge, StoreDecisions, StoreQuestions, StoreRhizome, StoreDreams}
}

func StripInstrumentation(raw string) string {
	if !strings.Contains(raw, "aphelion-memory-") {
		return raw
	}
	text := strings.ReplaceAll(raw, "\r\n", "\n")
	for {
		idx := strings.Index(text, "<!-- aphelion-memory-")
		if idx < 0 {
			break
		}
		end := strings.Index(text[idx:], memoryMarkerEnd)
		if end < 0 {
			break
		}
		endIdx := idx + end + len(memoryMarkerEnd)
		text = text[:idx] + text[endIdx:]
	}
	return normalizeSpacing(text)
}

func EntryID(scope string, store string, path string, ordinal int, content string) string {
	seed := strings.Join([]string{
		strings.TrimSpace(scope),
		normalizeStore(store),
		filepath.ToSlash(strings.TrimSpace(path)),
		strconv.Itoa(ordinal),
		normalizeEntryContent(content),
	}, "\x00")
	return "mem_" + shortHash(seed, 16)
}

func NewMemoryEntry(scope string, store string, path string, ordinal int, content string, sourceKind string, sourceRef string, confidence string, now time.Time) MemoryEntry {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	store = normalizeStore(store)
	content = strings.TrimSpace(content)
	if confidence == "" {
		confidence = "0.70"
	}
	sourceKind = firstNonEmpty(strings.TrimSpace(sourceKind), "direct")
	createdAt := utcString(now)
	if sourceKind == "untagged" {
		createdAt = "unknown"
	}
	return MemoryEntry{
		ID:            EntryID(scope, store, path, ordinal, content),
		Scope:         firstNonEmpty(strings.TrimSpace(scope), "shared"),
		Store:         store,
		Kind:          storeKind(store),
		Status:        EntryStatusActive,
		SourceKind:    sourceKind,
		SourceRef:     strings.TrimSpace(sourceRef),
		Confidence:    confidence,
		CreatedAt:     createdAt,
		ContentSHA256: checksumText(content),
		Path:          filepath.ToSlash(strings.TrimSpace(path)),
		Ordinal:       ordinal,
		Content:       content,
	}
}

func RenderEntry(entry MemoryEntry) string {
	entry.Store = normalizeStore(entry.Store)
	lines := []string{memoryEntryMarkerPrefix}
	fields := []struct{ key, value string }{
		{"id", entry.ID},
		{"scope", firstNonEmpty(entry.Scope, "shared")},
		{"store", entry.Store},
		{"kind", firstNonEmpty(entry.Kind, storeKind(entry.Store))},
		{"status", firstNonEmpty(entry.Status, EntryStatusActive)},
		{"source_kind", firstNonEmpty(entry.SourceKind, "direct")},
		{"source_ref", entry.SourceRef},
		{"confidence", entry.Confidence},
		{"created_at", firstNonEmpty(entry.CreatedAt, "unknown")},
		{"migrated_at", entry.MigratedAt},
		{"content_sha256", firstNonEmpty(entry.ContentSHA256, checksumText(entry.Content))},
	}
	for _, field := range fields {
		if strings.TrimSpace(field.value) == "" {
			continue
		}
		lines = append(lines, field.key+": "+strings.TrimSpace(field.value))
	}
	lines = append(lines, memoryMarkerEnd)
	return strings.Join(lines, "\n") + "\n\n" + strings.TrimSpace(entry.Content)
}

func ParseEntries(path string, store string, scope string, raw string) []MemoryEntry {
	store = normalizeStore(store)
	blocks := splitEntryBlocks(raw)
	entries := make([]MemoryEntry, 0, len(blocks))
	for i := 0; i < len(blocks); i++ {
		block := strings.TrimSpace(blocks[i])
		if strings.HasPrefix(block, memoryEntryMarkerPrefix) {
			meta, content := splitMetadataBlock(block)
			if strings.TrimSpace(content) == "" && i+1 < len(blocks) {
				content = strings.TrimSpace(blocks[i+1])
				i++
			}
			entry := entryFromMetadata(meta, content)
			entry.Path = filepath.ToSlash(path)
			entry.Store = firstNonEmpty(normalizeStore(entry.Store), store)
			entry.Scope = firstNonEmpty(entry.Scope, scope)
			entry.Kind = firstNonEmpty(entry.Kind, storeKind(entry.Store))
			entry.Ordinal = len(entries) + 1
			entry.Content = strings.TrimSpace(content)
			if entry.ID == "" {
				entry.ID = EntryID(entry.Scope, entry.Store, path, entry.Ordinal, entry.Content)
			}
			if entry.ContentSHA256 == "" {
				entry.ContentSHA256 = checksumText(entry.Content)
			}
			entries = append(entries, entry)
			continue
		}
		if shouldTagMemoryBlock(block) {
			entries = append(entries, NewMemoryEntry(scope, store, path, len(entries)+1, block, "untagged", "", "0.60", time.Time{}))
		}
	}
	return entries
}

func AppendEvent(root string, event MemoryEvent) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return fmt.Errorf("memory event root is required")
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if event.EventID == "" {
		event.EventID = "mev_" + event.CreatedAt.UTC().Format("20060102T150405.000000000Z") + "_" + shortHash(event.Type+event.Path+event.ProposalID+event.MemoryID+event.ContentSHA256, 8)
	}
	dir := instrumentationDir(root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create memory instrumentation dir: %w", err)
	}
	path := filepath.Join(dir, "events.jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open memory event log: %w", err)
	}
	defer file.Close()
	enc := json.NewEncoder(file)
	return enc.Encode(event)
}

func CreateProposal(req ProposalRequest) (*MemoryProposal, error) {
	root := strings.TrimSpace(req.Root)
	if root == "" {
		return nil, fmt.Errorf("memory proposal root is required")
	}
	store := normalizeStore(req.Store)
	if _, _, err := ResolveStorePath(root, store); err != nil {
		return nil, err
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return nil, fmt.Errorf("memory proposal content is required")
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	scope := firstNonEmpty(req.Scope, "shared")
	proposal := &MemoryProposal{
		Scope:         scope,
		Store:         store,
		Status:        ProposalStatusProposed,
		SourceKind:    firstNonEmpty(req.SourceKind, "reflection"),
		SourceRef:     strings.TrimSpace(req.SourceRef),
		Reason:        strings.TrimSpace(req.Reason),
		CreatedAt:     now.UTC(),
		UpdatedAt:     now.UTC(),
		ContentSHA256: checksumText(content),
		Content:       content,
	}
	proposal.ID = "mp_" + now.UTC().Format("20060102T150405.000000000Z") + "_" + shortHash(scope+store+proposal.SourceKind+proposal.ContentSHA256, 10)
	proposal.Path = proposalPath(root, proposal.ID)
	if err := os.MkdirAll(filepath.Dir(proposal.Path), 0o700); err != nil {
		return nil, fmt.Errorf("create memory proposal inbox: %w", err)
	}
	if err := os.WriteFile(proposal.Path, []byte(renderProposal(*proposal)), 0o600); err != nil {
		return nil, fmt.Errorf("write memory proposal: %w", err)
	}
	if err := AppendEvent(root, MemoryEvent{
		Type:          "memory.proposal.created",
		Scope:         scope,
		Store:         store,
		Path:          proposal.Path,
		ProposalID:    proposal.ID,
		Status:        proposal.Status,
		ContentSHA256: proposal.ContentSHA256,
		Metadata: map[string]string{
			"source_kind": proposal.SourceKind,
			"source_ref":  proposal.SourceRef,
			"reason":      proposal.Reason,
		},
		CreatedAt: now,
	}); err != nil {
		return nil, err
	}
	return proposal, nil
}

func ListProposals(opts ProposalListOptions) ([]MemoryProposal, error) {
	root := strings.TrimSpace(opts.Root)
	if root == "" {
		return nil, fmt.Errorf("memory proposal root is required")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	entries, err := os.ReadDir(proposalDir(root))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read memory proposal inbox: %w", err)
	}
	status := strings.ToLower(strings.TrimSpace(opts.Status))
	out := make([]MemoryProposal, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		proposal, err := LoadProposal(root, strings.TrimSuffix(entry.Name(), ".md"))
		if err != nil {
			continue
		}
		if status != "" && strings.ToLower(proposal.Status) != status {
			continue
		}
		out = append(out, *proposal)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func LoadProposal(root string, proposalID string) (*MemoryProposal, error) {
	proposalID = strings.TrimSpace(proposalID)
	if proposalID == "" {
		return nil, fmt.Errorf("memory proposal id is required")
	}
	path := proposalPath(root, proposalID)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read memory proposal %s: %w", proposalID, err)
	}
	proposal, err := parseProposal(path, string(raw))
	if err != nil {
		return nil, err
	}
	return proposal, nil
}

func ApproveProposal(root string, proposalID string, sourceTag string, confidence *float64) (*WriteResult, error) {
	proposal, err := LoadProposal(root, proposalID)
	if err != nil {
		return nil, err
	}
	if proposal.Status != ProposalStatusProposed {
		return nil, fmt.Errorf("memory proposal %s is %s, want proposed", proposal.ID, proposal.Status)
	}
	if strings.TrimSpace(sourceTag) == "" {
		sourceTag = proposal.SourceKind
	}
	res, err := ApplyWrite(WriteRequest{
		Root:       root,
		Store:      proposal.Store,
		Action:     "add",
		Content:    proposal.Content,
		SourceTag:  sourceTag,
		Confidence: confidence,
		SourceRef:  "proposal:" + proposal.ID,
		Scope:      proposal.Scope,
	})
	if err != nil {
		return nil, err
	}
	proposal.Status = ProposalStatusApproved
	proposal.UpdatedAt = time.Now().UTC()
	if err := os.WriteFile(proposal.Path, []byte(renderProposal(*proposal)), 0o600); err != nil {
		return nil, fmt.Errorf("update approved proposal: %w", err)
	}
	if err := AppendEvent(root, MemoryEvent{
		Type:          "memory.proposal.approved",
		Scope:         proposal.Scope,
		Store:         proposal.Store,
		Path:          proposal.Path,
		ProposalID:    proposal.ID,
		Status:        proposal.Status,
		ContentSHA256: proposal.ContentSHA256,
		CreatedAt:     proposal.UpdatedAt,
	}); err != nil {
		return nil, err
	}
	return res, nil
}

func RejectProposal(root string, proposalID string) (*MemoryProposal, error) {
	proposal, err := LoadProposal(root, proposalID)
	if err != nil {
		return nil, err
	}
	if proposal.Status != ProposalStatusProposed {
		return nil, fmt.Errorf("memory proposal %s is %s, want proposed", proposal.ID, proposal.Status)
	}
	proposal.Status = ProposalStatusRejected
	proposal.UpdatedAt = time.Now().UTC()
	if err := os.WriteFile(proposal.Path, []byte(renderProposal(*proposal)), 0o600); err != nil {
		return nil, fmt.Errorf("update rejected proposal: %w", err)
	}
	if err := AppendEvent(root, MemoryEvent{
		Type:          "memory.proposal.rejected",
		Scope:         proposal.Scope,
		Store:         proposal.Store,
		Path:          proposal.Path,
		ProposalID:    proposal.ID,
		Status:        proposal.Status,
		ContentSHA256: proposal.ContentSHA256,
		CreatedAt:     proposal.UpdatedAt,
	}); err != nil {
		return nil, err
	}
	return proposal, nil
}

func renderProposal(proposal MemoryProposal) string {
	lines := []string{memoryProposalMarkerPrefix}
	fields := []struct{ key, value string }{
		{"id", proposal.ID},
		{"scope", proposal.Scope},
		{"store", proposal.Store},
		{"status", proposal.Status},
		{"source_kind", proposal.SourceKind},
		{"source_ref", proposal.SourceRef},
		{"reason", proposal.Reason},
		{"created_at", utcString(proposal.CreatedAt)},
		{"updated_at", utcString(proposal.UpdatedAt)},
		{"content_sha256", proposal.ContentSHA256},
	}
	for _, field := range fields {
		if strings.TrimSpace(field.value) == "" {
			continue
		}
		lines = append(lines, field.key+": "+strings.TrimSpace(field.value))
	}
	lines = append(lines, memoryMarkerEnd)
	return strings.Join(lines, "\n") + "\n\n" + strings.TrimSpace(proposal.Content) + "\n"
}

func parseProposal(path string, raw string) (*MemoryProposal, error) {
	blocks := splitEntryBlocks(raw)
	if len(blocks) == 0 || !strings.HasPrefix(strings.TrimSpace(blocks[0]), memoryProposalMarkerPrefix) {
		return nil, fmt.Errorf("memory proposal %s missing metadata", path)
	}
	meta, content := splitMetadataBlock(blocks[0])
	if strings.TrimSpace(content) == "" && len(blocks) > 1 {
		content = strings.TrimSpace(strings.Join(blocks[1:], "\n\n"))
	}
	created, _ := time.Parse(time.RFC3339, meta["created_at"])
	updated, _ := time.Parse(time.RFC3339, meta["updated_at"])
	proposal := &MemoryProposal{
		ID:            meta["id"],
		Scope:         firstNonEmpty(meta["scope"], "shared"),
		Store:         normalizeStore(meta["store"]),
		Status:        firstNonEmpty(meta["status"], ProposalStatusProposed),
		SourceKind:    meta["source_kind"],
		SourceRef:     meta["source_ref"],
		Reason:        meta["reason"],
		CreatedAt:     created,
		UpdatedAt:     updated,
		ContentSHA256: meta["content_sha256"],
		Path:          path,
		Content:       strings.TrimSpace(content),
	}
	if proposal.ID == "" {
		proposal.ID = strings.TrimSuffix(filepath.Base(path), ".md")
	}
	if proposal.ContentSHA256 == "" {
		proposal.ContentSHA256 = checksumText(proposal.Content)
	}
	return proposal, nil
}

func entryFromMetadata(meta map[string]string, content string) MemoryEntry {
	return MemoryEntry{
		ID:            meta["id"],
		Scope:         meta["scope"],
		Store:         normalizeStore(meta["store"]),
		Kind:          meta["kind"],
		Status:        firstNonEmpty(meta["status"], EntryStatusActive),
		SourceKind:    meta["source_kind"],
		SourceRef:     meta["source_ref"],
		Confidence:    meta["confidence"],
		CreatedAt:     meta["created_at"],
		MigratedAt:    meta["migrated_at"],
		ContentSHA256: meta["content_sha256"],
		Content:       strings.TrimSpace(content),
	}
}

func splitMetadataBlock(block string) (map[string]string, string) {
	meta := map[string]string{}
	trimmed := strings.TrimSpace(block)
	end := strings.Index(trimmed, memoryMarkerEnd)
	if end < 0 {
		return meta, trimmed
	}
	metadataRaw := trimmed[:end]
	content := strings.TrimSpace(trimmed[end+len(memoryMarkerEnd):])
	scanner := bufio.NewScanner(strings.NewReader(metadataRaw))
	first := true
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if first {
			first = false
			continue
		}
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		meta[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
	}
	return meta, content
}

func splitEntryBlocks(raw string) []string {
	text := strings.TrimSpace(strings.ReplaceAll(raw, "\r\n", "\n"))
	if text == "" {
		return nil
	}
	parts := strings.Split(text, "\n\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func shouldTagMemoryBlock(block string) bool {
	block = strings.TrimSpace(block)
	if block == "" {
		return false
	}
	if strings.HasPrefix(block, "<!--") && strings.HasSuffix(block, "-->") {
		return false
	}
	if strings.HasPrefix(block, memoryEntryMarkerPrefix) || strings.HasPrefix(block, memoryProposalMarkerPrefix) {
		return false
	}
	lines := strings.Split(block, "\n")
	nonEmpty := 0
	headings := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		nonEmpty++
		if strings.HasPrefix(line, "#") {
			headings++
		}
	}
	return nonEmpty > 0 && headings != nonEmpty
}

func instrumentationDir(root string) string {
	return filepath.Join(strings.TrimSpace(root), "memory", ".aphelion")
}

func proposalDir(root string) string {
	return filepath.Join(strings.TrimSpace(root), "memory", "inbox")
}

func proposalPath(root string, proposalID string) string {
	return filepath.Join(proposalDir(root), strings.TrimSpace(proposalID)+".md")
}

func storeKind(store string) string {
	switch normalizeStore(store) {
	case StoreDecisions:
		return "decision"
	case StoreQuestions:
		return "question"
	case StoreKnowledge:
		return "knowledge"
	case StoreRhizome:
		return "rhizome"
	case StoreDreams:
		return "dream"
	default:
		return "memory"
	}
}

func shortHash(raw string, n int) string {
	if n <= 0 {
		n = 12
	}
	full := checksumText(raw)
	if n > len(full) {
		n = len(full)
	}
	return full[:n]
}

func normalizeEntryContent(content string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(content)), " ")
}

func ensureTrailingNewline(raw string) string {
	return strings.TrimSpace(raw) + "\n"
}

func utcString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
