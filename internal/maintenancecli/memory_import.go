//go:build linux

package maintenancecli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	memstore "github.com/idolum-ai/aphelion/memory"
)

func runImportAuditCommand(args []string) error {
	fs := flag.NewFlagSet("import-audit", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	scope := fs.String("scope", "", "filter by scope: shared|principal")
	principalID := fs.String("principal", "", "filter by principal key")
	state := fs.String("state", string(memstore.SemanticImportStateQuarantine), "filter by import state")
	id := fs.Int64("id", 0, "document id for review/approve/reject")
	limit := fs.Int("limit", 20, "max documents to list")
	chunks := fs.Int("chunks", 6, "max chunk excerpts to show during review")
	maxChars := fs.Int("max_chars", 4000, "max excerpt chars during review")
	if err := fs.Parse(args); err != nil {
		return err
	}

	action := "list"
	if fs.NArg() > 0 {
		action = strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	}

	cfg, _, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}
	engine, err := newSemanticEngineForConfig(cfg, true)
	if err != nil {
		return err
	}
	defer engine.Close()

	ctx := context.Background()
	switch action {
	case "", "list":
		docs, err := engine.ListImportAudit(ctx, memstore.SemanticAuditFilter{
			State:       memstore.SemanticImportState(strings.ToLower(strings.TrimSpace(*state))),
			Scope:       *scope,
			PrincipalID: *principalID,
			Limit:       *limit,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "action: list\n")
		if len(docs) == 0 {
			fmt.Fprintf(os.Stdout, "documents: 0\n")
			return nil
		}
		fmt.Fprintf(os.Stdout, "documents: %d\n", len(docs))
		for _, doc := range docs {
			fmt.Fprintf(os.Stdout, "- id=%d scope=%s", doc.ID, doc.Scope)
			if strings.TrimSpace(doc.PrincipalID) != "" {
				fmt.Fprintf(os.Stdout, " principal=%s", doc.PrincipalID)
			}
			fmt.Fprintf(os.Stdout, " state=%s kind=%s provenance=%s source=%s\n",
				doc.ImportState,
				doc.SourceKind,
				doc.ProvenanceSource,
				doc.SourcePath,
			)
		}
		return nil
	case "review":
		if *id <= 0 {
			return fmt.Errorf("import-audit review requires --id")
		}
		review, err := engine.ReviewImportDocument(ctx, *id, *chunks, *maxChars)
		if err != nil {
			return err
		}
		doc := review.Document
		fmt.Fprintf(os.Stdout, "action: review\n")
		fmt.Fprintf(os.Stdout, "id: %d\n", doc.ID)
		fmt.Fprintf(os.Stdout, "scope: %s\n", doc.Scope)
		if strings.TrimSpace(doc.PrincipalID) != "" {
			fmt.Fprintf(os.Stdout, "principal: %s\n", doc.PrincipalID)
		}
		fmt.Fprintf(os.Stdout, "state: %s\n", doc.ImportState)
		fmt.Fprintf(os.Stdout, "kind: %s\n", doc.SourceKind)
		fmt.Fprintf(os.Stdout, "provenance: %s\n", doc.ProvenanceSource)
		fmt.Fprintf(os.Stdout, "source: %s\n", doc.SourcePath)
		fmt.Fprintf(os.Stdout, "chunks: %d\n", review.ChunkCount)
		for i, excerpt := range review.Excerpts {
			fmt.Fprintf(os.Stdout, "\n[%d]\n%s\n", i+1, excerpt)
		}
		return nil
	case "approve":
		if *id <= 0 {
			return fmt.Errorf("import-audit approve requires --id")
		}
		if err := engine.SetImportState(ctx, *id, memstore.SemanticImportStateApproved); err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "action: approve\nid: %d\nstate: %s\n", *id, memstore.SemanticImportStateApproved)
		return nil
	case "reject":
		if *id <= 0 {
			return fmt.Errorf("import-audit reject requires --id")
		}
		if err := engine.SetImportState(ctx, *id, memstore.SemanticImportStateRejected); err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "action: reject\nid: %d\nstate: %s\n", *id, memstore.SemanticImportStateRejected)
		return nil
	default:
		return fmt.Errorf("import-audit action must be one of list|review|approve|reject")
	}
}

func runImportSemanticCommand(args []string) error {
	fs := flag.NewFlagSet("import-semantic", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	dbPath := fs.String("db", "", "path to foreign semantic sqlite db")
	scope := fs.String("scope", "shared", "target scope: shared|principal")
	principalID := fs.String("principal", "", "target principal key when scope=principal")
	provenance := fs.String("provenance", "", "provenance label override")
	state := fs.String("state", string(memstore.SemanticImportStateQuarantine), "initial import state")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("import-semantic requires a source type such as openclaw or host")
	}
	sourceType := strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	if strings.TrimSpace(*dbPath) == "" {
		return fmt.Errorf("import-semantic requires --db")
	}

	cfg, _, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}
	engine, err := newSemanticEngineForConfig(cfg, true)
	if err != nil {
		return err
	}
	defer engine.Close()

	importState := memstore.SemanticImportState(strings.ToLower(strings.TrimSpace(*state)))
	switch sourceType {
	case "openclaw", "host":
		prov := strings.TrimSpace(*provenance)
		if prov == "" {
			if sourceType == "host" {
				prov = "host_archive"
			} else {
				prov = "openclaw_import"
			}
		}
		summary, err := engine.ImportOpenClaw(context.Background(), memstore.SemanticOpenClawImportRequest{
			DBPath:           *dbPath,
			Scope:            *scope,
			PrincipalID:      *principalID,
			ProvenanceSource: prov,
			ImportState:      importState,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "action: import-semantic\n")
		fmt.Fprintf(os.Stdout, "source: %s\n", summary.Source)
		fmt.Fprintf(os.Stdout, "contract: %s\n", summary.Contract)
		fmt.Fprintf(os.Stdout, "provenance: %s\n", summary.Provenance)
		fmt.Fprintf(os.Stdout, "scope: %s\n", summary.Scope)
		if strings.TrimSpace(summary.PrincipalID) != "" {
			fmt.Fprintf(os.Stdout, "principal: %s\n", summary.PrincipalID)
		}
		fmt.Fprintf(os.Stdout, "documents: %d\n", summary.Documents)
		fmt.Fprintf(os.Stdout, "chunks: %d\n", summary.Chunks)
		fmt.Fprintf(os.Stdout, "embedding_chunks: %d\n", summary.EmbeddedChunkCount)
		fmt.Fprintf(os.Stdout, "embedding_use: %s\n", summary.EmbeddingUse)
		fmt.Fprintf(os.Stdout, "state: %s\n", importState)
		return nil
	default:
		return fmt.Errorf("import-semantic source must be one of openclaw|host")
	}
}

type codexSessionImportCommandOptions struct {
	CodexHome   string
	Lookback    time.Duration
	ActiveGrace time.Duration
	MaxSessions int
	Scope       string
	PrincipalID string
	ImportState memstore.SemanticImportState
}

func defaultCodexSessionImportCommandOptions(cfg *config.Config) codexSessionImportCommandOptions {
	opts := codexSessionImportCommandOptions{
		Lookback:    14 * 24 * time.Hour,
		ActiveGrace: 5 * time.Minute,
		MaxSessions: 50,
		Scope:       "shared",
		ImportState: memstore.SemanticImportStateQuarantine,
	}
	if cfg != nil {
		opts.CodexHome = strings.TrimSpace(cfg.Governor.Codex.CodexHome)
	}
	return opts
}

func runImportCodexSessionsCommand(args []string) error {
	fs := flag.NewFlagSet("import-codex-sessions", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	codexHome := fs.String("codex-home", "", "Codex home directory; defaults to governor.codex.codex_home, CODEX_HOME, or ~/.codex")
	lookback := fs.Duration("lookback", 14*24*time.Hour, "session mtime lookback window")
	activeGrace := fs.Duration("active-grace", 5*time.Minute, "skip sessions modified more recently than this")
	maxSessions := fs.Int("max", 50, "max newest sessions to import")
	scope := fs.String("scope", "shared", "target scope: shared|principal")
	principalID := fs.String("principal", "", "target principal key when scope=principal")
	state := fs.String("state", string(memstore.SemanticImportStateQuarantine), "initial import state: quarantine|approved|rejected")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, _, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}
	opts := defaultCodexSessionImportCommandOptions(cfg)
	if strings.TrimSpace(*codexHome) != "" {
		opts.CodexHome = strings.TrimSpace(*codexHome)
	}
	opts.Lookback = *lookback
	opts.ActiveGrace = *activeGrace
	opts.MaxSessions = *maxSessions
	opts.Scope = *scope
	opts.PrincipalID = *principalID
	opts.ImportState = memstore.SemanticImportState(strings.ToLower(strings.TrimSpace(*state)))
	if err := validateCodexSessionImportState(opts.ImportState); err != nil {
		return err
	}

	result, err := importCodexSessionsForConfig(context.Background(), cfg, opts)
	if err != nil {
		return err
	}
	printCodexSessionImportResult(os.Stdout, result, opts.ImportState)
	return nil
}

func validateCodexSessionImportState(state memstore.SemanticImportState) error {
	switch state {
	case memstore.SemanticImportStateQuarantine, memstore.SemanticImportStateApproved, memstore.SemanticImportStateRejected:
		return nil
	default:
		return fmt.Errorf("state must be one of quarantine|approved|rejected")
	}
}

func importCodexSessionsForConfig(ctx context.Context, cfg *config.Config, opts codexSessionImportCommandOptions) (*memstore.CodexSessionImportResult, error) {
	engine, err := newSemanticEngineForConfig(cfg, true)
	if err != nil {
		return nil, err
	}
	defer engine.Close()
	return engine.ImportCodexSessions(ctx, memstore.CodexSessionImportOptions{
		CodexHome:   opts.CodexHome,
		Lookback:    opts.Lookback,
		ActiveGrace: opts.ActiveGrace,
		MaxSessions: opts.MaxSessions,
		Scope:       opts.Scope,
		PrincipalID: opts.PrincipalID,
		ImportState: opts.ImportState,
	})
}

func printCodexSessionImportResult(w io.Writer, result *memstore.CodexSessionImportResult, state memstore.SemanticImportState) {
	if result == nil {
		return
	}
	fmt.Fprintf(w, "action: import-codex-sessions\n")
	fmt.Fprintf(w, "codex_home: %s\n", result.CodexHome)
	if strings.TrimSpace(result.SessionsDir) != "" {
		fmt.Fprintf(w, "sessions_dir: %s\n", result.SessionsDir)
	}
	fmt.Fprintf(w, "state: %s\n", state)
	fmt.Fprintf(w, "scanned: %d\n", result.Scanned)
	fmt.Fprintf(w, "eligible: %d\n", result.Eligible)
	fmt.Fprintf(w, "imported: %d\n", result.Imported)
	fmt.Fprintf(w, "updated: %d\n", result.Updated)
	fmt.Fprintf(w, "skipped_already_imported: %d\n", result.SkippedAlreadyImported)
	fmt.Fprintf(w, "skipped_old: %d\n", result.SkippedOld)
	fmt.Fprintf(w, "skipped_active: %d\n", result.SkippedActive)
	fmt.Fprintf(w, "skipped_empty: %d\n", result.SkippedEmpty)
	fmt.Fprintf(w, "failed: %d\n", result.Failed)
	for _, failure := range result.Failures {
		fmt.Fprintf(w, "  - path=%s error=%s\n", failure.Path, failure.Error)
	}
}
