//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func (r *Registry) memory(_ context.Context, input json.RawMessage, scope sandbox.Scope) (string, error) {
	var in memoryInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("decode memory input: %w", err)
	}

	root, effectiveScope, err := resolveMemoryRoot(scope, in.Scope)
	if err != nil {
		return "", err
	}
	action := strings.ToLower(strings.TrimSpace(in.Action))
	switch action {
	case "proposal_list":
		proposals, err := memstore.ListProposals(memstore.ProposalListOptions{Root: root, Status: in.Status, Limit: in.Limit})
		if err != nil {
			return "", err
		}
		return renderMemoryProposalList(effectiveScope, proposals), nil
	case "proposal_show":
		if strings.TrimSpace(in.ProposalID) == "" {
			return "", fmt.Errorf("memory proposal_show requires proposal_id")
		}
		proposal, err := memstore.LoadProposal(root, in.ProposalID)
		if err != nil {
			return "", err
		}
		return renderMemoryProposal(*proposal), nil
	case "proposal_approve":
		if strings.TrimSpace(in.ProposalID) == "" {
			return "", fmt.Errorf("memory proposal_approve requires proposal_id")
		}
		result, err := memstore.ApproveProposal(root, in.ProposalID, in.SourceTag, in.Confidence)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("memory_proposal_approved scope=%s proposal_id=%s store=%s path=%s", effectiveScope, strings.TrimSpace(in.ProposalID), result.Store, result.Path), nil
	case "proposal_reject":
		if strings.TrimSpace(in.ProposalID) == "" {
			return "", fmt.Errorf("memory proposal_reject requires proposal_id")
		}
		proposal, err := memstore.RejectProposal(root, in.ProposalID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("memory_proposal_rejected scope=%s proposal_id=%s store=%s", effectiveScope, proposal.ID, proposal.Store), nil
	case "add", "replace", "remove", "":
	default:
		return "", fmt.Errorf("unsupported memory action %q", in.Action)
	}

	result, err := memstore.ApplyWrite(memstore.WriteRequest{
		Root:       root,
		Store:      in.Store,
		Action:     action,
		Content:    in.Content,
		Match:      in.Match,
		SourceTag:  in.SourceTag,
		Scope:      effectiveScope,
		Confidence: in.Confidence,
	})
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("memory_%s_ok scope=%s store=%s path=%s", result.Action, effectiveScope, result.Store, result.Path), nil
}

func renderMemoryProposalList(scope string, proposals []memstore.MemoryProposal) string {
	var b strings.Builder
	fmt.Fprintf(&b, "scope: %s\n", firstNonEmpty(strings.TrimSpace(scope), "-"))
	if len(proposals) == 0 {
		b.WriteString("proposals: none")
		return b.String()
	}
	b.WriteString("proposals:\n")
	for _, proposal := range proposals {
		fmt.Fprintf(&b, "- id=%s status=%s store=%s source=%s created_at=%s sha=%s\n",
			proposal.ID,
			firstNonEmpty(proposal.Status, "-"),
			firstNonEmpty(proposal.Store, "-"),
			firstNonEmpty(proposal.SourceKind, "-"),
			proposal.CreatedAt.UTC().Format(time.RFC3339),
			firstNonEmpty(proposal.ContentSHA256, "-"),
		)
	}
	return strings.TrimSpace(b.String())
}

func renderMemoryProposal(proposal memstore.MemoryProposal) string {
	var b strings.Builder
	fmt.Fprintf(&b, "proposal_id: %s\n", proposal.ID)
	fmt.Fprintf(&b, "status: %s\n", firstNonEmpty(proposal.Status, "-"))
	fmt.Fprintf(&b, "scope: %s\n", firstNonEmpty(proposal.Scope, "-"))
	fmt.Fprintf(&b, "store: %s\n", firstNonEmpty(proposal.Store, "-"))
	fmt.Fprintf(&b, "source_kind: %s\n", firstNonEmpty(proposal.SourceKind, "-"))
	fmt.Fprintf(&b, "source_ref: %s\n", firstNonEmpty(proposal.SourceRef, "-"))
	fmt.Fprintf(&b, "reason: %s\n", firstNonEmpty(proposal.Reason, "-"))
	fmt.Fprintf(&b, "content_sha256: %s\n", firstNonEmpty(proposal.ContentSHA256, "-"))
	b.WriteString("content:\n")
	b.WriteString(strings.TrimSpace(proposal.Content))
	return b.String()
}

func resolveMemoryRoot(scope sandbox.Scope, requested string) (string, string, error) {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested == "" {
		if scope.Principal.Role == principal.RoleApprovedUser && strings.TrimSpace(scope.UserMemory) != "" {
			requested = "principal"
		} else {
			requested = "shared"
		}
	}

	switch requested {
	case "shared":
		if scope.Principal.Role == principal.RoleApprovedUser {
			return "", "", fmt.Errorf("approved users may not write shared memory")
		}
		root := strings.TrimSpace(scope.SharedMemoryRoot)
		if root == "" {
			root = strings.TrimSpace(scope.WorkingRoot)
		}
		if root == "" {
			return "", "", fmt.Errorf("shared memory root is not configured")
		}
		return root, requested, nil
	case "principal":
		root := strings.TrimSpace(scope.UserMemory)
		if root == "" {
			if scope.Principal.Role == principal.RoleAdmin {
				sharedRoot := strings.TrimSpace(scope.SharedMemoryRoot)
				if sharedRoot == "" {
					sharedRoot = strings.TrimSpace(scope.WorkingRoot)
				}
				if sharedRoot == "" {
					return "", "", fmt.Errorf("shared memory root is not configured")
				}
				return sharedRoot, "shared", nil
			}
			return "", "", fmt.Errorf("principal memory root is not available for this principal")
		}
		return root, requested, nil
	default:
		return "", "", fmt.Errorf("memory scope must be shared or principal")
	}
}

func (r *Registry) sessionSearch(_ context.Context, input json.RawMessage, p principal.Principal, key session.SessionKey) (string, error) {
	if r.store == nil {
		return "", fmt.Errorf("session search requires transcript store")
	}

	var in sessionSearchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("decode session_search input: %w", err)
	}
	if strings.TrimSpace(in.Query) == "" {
		return "", fmt.Errorf("session_search query is required")
	}

	scope := strings.ToLower(strings.TrimSpace(in.Scope))
	var filter *session.SessionKey
	switch {
	case p.Role == principal.RoleApprovedUser:
		filter = &key
		scope = "session"
	case scope == "", scope == "all":
		filter = nil
		scope = "all"
	case scope == "session":
		filter = &key
	default:
		return "", fmt.Errorf("session_search scope must be session or all")
	}

	hits, err := r.store.SearchMessages(in.Query, in.Limit, filter)
	if err != nil {
		return "", err
	}
	return renderSessionSearchResults(scope, in.Query, hits), nil
}

func (r *Registry) semanticSearch(ctx context.Context, input json.RawMessage, scope sandbox.Scope) (string, error) {
	if r.semantic == nil || !r.semantic.Enabled() {
		return "", fmt.Errorf("semantic search is not configured")
	}

	var in semanticSearchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("decode semantic_search input: %w", err)
	}
	if strings.TrimSpace(in.Query) == "" {
		return "", fmt.Errorf("semantic_search query is required")
	}

	root, effectiveScope, err := resolveMemoryRoot(scope, in.Scope)
	if err != nil {
		return "", err
	}

	principalID := ""
	if effectiveScope == "principal" && scope.Principal.TelegramUserID > 0 {
		principalID = strconv.FormatInt(scope.Principal.TelegramUserID, 10)
	}
	hits, err := r.semantic.Search(ctx, memstore.SemanticSearchRequest{
		Root:        root,
		Scope:       effectiveScope,
		PrincipalID: principalID,
		Query:       in.Query,
		Mode:        memstore.SemanticModeInteractive,
		Limit:       in.Limit,
		Now:         time.Now(),
	})
	if err != nil {
		return "", err
	}
	return renderSemanticSearchResults(effectiveScope, in.Query, hits), nil
}

func renderSessionSearchResults(scope string, query string, hits []session.SearchHit) string {
	var b strings.Builder
	b.WriteString("[SESSION_RECALL]\n")
	b.WriteString("scope: ")
	b.WriteString(scope)
	b.WriteString("\nquery: ")
	b.WriteString(strings.TrimSpace(query))
	b.WriteString("\n")
	if len(hits) == 0 {
		b.WriteString("no_hits\n[/SESSION_RECALL]")
		return b.String()
	}
	for i, hit := range hits {
		fmt.Fprintf(&b, "\n%d. chat=%d turn=%d role=%s\n", i+1, hit.ChatID, hit.TurnIndex, hit.Role)
		b.WriteString("content: ")
		b.WriteString(truncate(strings.TrimSpace(hit.Content), 600))
		b.WriteString("\n")
	}
	b.WriteString("[/SESSION_RECALL]")
	return b.String()
}

func renderSemanticSearchResults(scope string, query string, hits []memstore.SemanticHit) string {
	var b strings.Builder
	b.WriteString("[SEMANTIC_RECALL]\n")
	b.WriteString("scope: ")
	b.WriteString(scope)
	b.WriteString("\nquery: ")
	b.WriteString(strings.TrimSpace(query))
	b.WriteString("\n")
	if len(hits) == 0 {
		b.WriteString("no_hits\n[/SEMANTIC_RECALL]")
		return b.String()
	}
	for i, hit := range hits {
		fmt.Fprintf(&b, "\n%d. source=%s scope=%s", i+1, hit.Source, hit.Scope)
		if strings.TrimSpace(hit.PrincipalID) != "" {
			fmt.Fprintf(&b, " principal=%s", hit.PrincipalID)
		}
		fmt.Fprintf(&b, " kind=%s provenance=%s score=%.2f\n", hit.Kind, firstNonEmpty(strings.TrimSpace(hit.Provenance), "native"), hit.Score)
		b.WriteString("excerpt: ")
		b.WriteString(truncate(strings.TrimSpace(hit.Excerpt), 600))
		b.WriteString("\n")
	}
	b.WriteString("[/SEMANTIC_RECALL]")
	return b.String()
}
