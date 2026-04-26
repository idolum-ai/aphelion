//go:build linux

package memory

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

type SemanticPromotionRequest struct {
	Root        string
	Scope       string
	PrincipalID string
	Limit       int
	MaxChars    int
	Now         time.Time
}

type SemanticPromotionCandidate struct {
	Document SemanticDocument
	Proposal MemoryProposal
}

func (e *SemanticEngine) ProposeApprovedImports(ctx context.Context, req SemanticPromotionRequest) ([]SemanticPromotionCandidate, error) {
	if e == nil || !e.opts.Enabled {
		return nil, fmt.Errorf("semantic retrieval is not enabled")
	}
	root := strings.TrimSpace(req.Root)
	if root == "" {
		return nil, fmt.Errorf("semantic promotion root is required")
	}
	scope := normalizeSemanticScope(req.Scope)
	principalID := normalizePrincipalID(req.PrincipalID)
	if scope == "principal" && principalID == "" {
		return nil, fmt.Errorf("semantic promotion principal_id is required for principal scope")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	maxChars := req.MaxChars
	if maxChars <= 0 {
		maxChars = 900
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	existingRefs, err := existingSemanticPromotionRefs(root)
	if err != nil {
		return nil, err
	}
	docs, err := e.ListImportAudit(ctx, SemanticAuditFilter{
		State:       SemanticImportStateApproved,
		Scope:       scope,
		PrincipalID: principalID,
		Limit:       limit,
	})
	if err != nil {
		return nil, err
	}

	out := make([]SemanticPromotionCandidate, 0, len(docs))
	for _, doc := range docs {
		sourceRef := semanticPromotionSourceRef(doc)
		if _, ok := existingRefs[sourceRef]; ok {
			continue
		}
		chunks, err := e.semanticDocumentChunks(ctx, doc.ID, maxChars)
		if err != nil {
			return nil, err
		}
		content := semanticPromotionContent(doc, chunks, maxChars)
		if strings.TrimSpace(content) == "" {
			continue
		}
		proposal, err := CreateProposal(ProposalRequest{
			Root:       root,
			Scope:      semanticPromotionScope(doc),
			Store:      semanticPromotionStore(doc),
			SourceKind: "semantic_import",
			SourceRef:  sourceRef,
			Reason:     "approved semantic import promotion",
			Content:    content,
			Now:        now,
		})
		if err != nil {
			return nil, err
		}
		existingRefs[sourceRef] = struct{}{}
		out = append(out, SemanticPromotionCandidate{Document: doc, Proposal: *proposal})
	}
	return out, nil
}

func existingSemanticPromotionRefs(root string) (map[string]struct{}, error) {
	refs := make(map[string]struct{})
	entries, err := os.ReadDir(proposalDir(root))
	if err != nil {
		if os.IsNotExist(err) {
			return refs, nil
		}
		return nil, fmt.Errorf("read memory proposal inbox: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		proposal, err := LoadProposal(root, strings.TrimSuffix(entry.Name(), ".md"))
		if err != nil {
			continue
		}
		if ref := strings.TrimSpace(proposal.SourceRef); ref != "" {
			refs[ref] = struct{}{}
		}
	}
	return refs, nil
}

func (e *SemanticEngine) semanticDocumentChunks(ctx context.Context, documentID int64, maxChars int) ([]string, error) {
	if documentID <= 0 {
		return nil, fmt.Errorf("semantic document id must be > 0")
	}
	db, err := e.ensureDB()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT text
		FROM semantic_chunks
		WHERE document_id = ?
		ORDER BY ordinal
	`, documentID)
	if err != nil {
		return nil, fmt.Errorf("load semantic document chunks: %w", err)
	}
	defer rows.Close()

	var out []string
	chars := 0
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return nil, fmt.Errorf("scan semantic document chunk: %w", err)
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		if maxChars > 0 && chars > 0 && chars+len(text) > maxChars {
			continue
		}
		if maxChars > 0 && len(text) > maxChars {
			text = truncate(text, maxChars)
		}
		out = append(out, text)
		chars += len(text)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate semantic document chunks: %w", err)
	}
	return out, nil
}

func semanticPromotionSourceRef(doc SemanticDocument) string {
	checksum := strings.TrimSpace(doc.Checksum)
	if checksum == "" {
		checksum = "unknown"
	}
	return fmt.Sprintf("semantic_document:%d:%s", doc.ID, checksum)
}

func semanticPromotionScope(doc SemanticDocument) string {
	if strings.TrimSpace(doc.Scope) == "principal" && strings.TrimSpace(doc.PrincipalID) != "" {
		return "principal:" + strings.TrimSpace(doc.PrincipalID)
	}
	return "shared"
}

func semanticPromotionStore(doc SemanticDocument) string {
	haystack := strings.ToLower(strings.TrimSpace(doc.SourceKind) + " " + strings.TrimSpace(doc.SourcePath))
	switch {
	case strings.Contains(haystack, "decision"):
		return StoreDecisions
	case strings.Contains(haystack, "question"):
		return StoreQuestions
	case strings.Contains(haystack, "rhizome"):
		return StoreRhizome
	case strings.Contains(haystack, "dream"):
		return StoreDreams
	default:
		return StoreKnowledge
	}
}

func semanticPromotionContent(doc SemanticDocument, chunks []string, maxChars int) string {
	_ = doc
	for _, chunk := range chunks {
		line := firstSemanticPromotionLine(chunk)
		if line == "" {
			continue
		}
		line = truncate(line, maxChars)
		if !strings.HasPrefix(line, "- ") {
			line = "- " + line
		}
		return line
	}
	return ""
}

func firstSemanticPromotionLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimPrefix(line, "* ")
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "<!--") {
			continue
		}
		return line
	}
	return ""
}
