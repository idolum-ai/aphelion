//go:build linux

package maintenancecli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func runEvidenceCommand(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printCommandGroupHelp("evidence", []string{"inspect", "hydrate"})
		return nil
	}
	switch args[0] {
	case "inspect":
		return runEvidenceInspectCommand(args[1:])
	case "hydrate":
		return runEvidenceHydrateCommand(args[1:])
	default:
		return fmt.Errorf("unknown evidence command %q (known: inspect|hydrate)", args[0])
	}
}

func runEvidenceInspectCommand(args []string) error {
	fs := flag.NewFlagSet("evidence inspect", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	idFlag := fs.String("id", "", "evidence object id")
	formatFlag := fs.String("format", commandOutputHuman, "output format: human|json|kv")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if extra, ok := firstPositionalArg(fs.Args()); ok {
		return fmt.Errorf("unknown argument %q for evidence inspect", extra)
	}
	format, err := normalizeCommandOutputFormat(*formatFlag, false)
	if err != nil {
		return err
	}
	id := strings.TrimSpace(*idFlag)
	if id == "" {
		return fmt.Errorf("evidence inspect requires --id")
	}
	store, closeStore, err := evidenceStoreForCommand(*configFlag)
	if err != nil {
		return err
	}
	if closeStore != nil {
		defer closeStore()
	}
	if store == nil {
		return fmt.Errorf("evidence inspect requires an existing sessions database")
	}
	object, ok, err := store.EvidenceObject(id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("evidence object %q not found", id)
	}
	return printEvidenceInspectResult(object, format)
}

func runEvidenceHydrateCommand(args []string) error {
	fs := flag.NewFlagSet("evidence hydrate", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	chatIDFlag := fs.Int64("chat-id", 0, "chat id")
	userIDFlag := fs.Int64("user-id", 0, "user id")
	scopeKindFlag := fs.String("scope-kind", "", "optional scope kind")
	scopeIDFlag := fs.String("scope-id", "", "optional scope id")
	durableAgentIDFlag := fs.String("durable-agent-id", "", "optional durable agent id")
	operationIDFlag := fs.String("operation-id", "", "optional operation id")
	queryFlag := fs.String("query", "", "hydration query")
	requiredFlag := fs.String("required", "", "comma-separated required evidence ids")
	limitFlag := fs.Int("limit", 16, "maximum evidence objects")
	formatFlag := fs.String("format", commandOutputHuman, "output format: human|json|kv")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if extra, ok := firstPositionalArg(fs.Args()); ok {
		return fmt.Errorf("unknown argument %q for evidence hydrate", extra)
	}
	format, err := normalizeCommandOutputFormat(*formatFlag, false)
	if err != nil {
		return err
	}
	if *chatIDFlag == 0 {
		return fmt.Errorf("evidence hydrate requires --chat-id")
	}
	store, closeStore, err := evidenceStoreForCommand(*configFlag)
	if err != nil {
		return err
	}
	if closeStore != nil {
		defer closeStore()
	}
	if store == nil {
		return fmt.Errorf("evidence hydrate requires an existing sessions database")
	}
	key := session.SessionKey{
		ChatID: *chatIDFlag,
		UserID: *userIDFlag,
		Scope: session.ScopeRef{
			Kind:           session.ScopeKind(strings.TrimSpace(*scopeKindFlag)),
			ID:             strings.TrimSpace(*scopeIDFlag),
			DurableAgentID: strings.TrimSpace(*durableAgentIDFlag),
		},
	}
	result, err := store.HydrateEvidence(session.EvidenceHydrationQuery{
		Key:                 key,
		OperationID:         strings.TrimSpace(*operationIDFlag),
		Query:               strings.TrimSpace(*queryFlag),
		RequiredEvidenceIDs: parseCSVValues(*requiredFlag),
		Limit:               *limitFlag,
		Now:                 time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	return printEvidenceHydrateResult(result, format)
}

func evidenceStoreForCommand(configPathFlag string) (*session.SQLiteStore, func(), error) {
	cfg, _, err := loadConfigForCommand(configPathFlag)
	if err != nil {
		return nil, nil, err
	}
	store, err := openStoreIfExists(cfg.Sessions.DBPath)
	if err != nil {
		return nil, nil, err
	}
	closeFn := func() {}
	if store != nil {
		closeFn = func() { _ = store.Close() }
	}
	return store, closeFn, nil
}

func printEvidenceInspectResult(object session.EvidenceObject, format string) error {
	switch format {
	case commandOutputJSON:
		return json.NewEncoder(os.Stdout).Encode(object)
	case commandOutputKV:
		fmt.Fprintf(os.Stdout, "evidence_id: %s\n", object.ID)
		fmt.Fprintf(os.Stdout, "source_kind: %s\n", object.SourceKind)
		fmt.Fprintf(os.Stdout, "source_ref: %s\n", object.SourceRef)
		fmt.Fprintf(os.Stdout, "session_id: %s\n", object.SessionID)
		fmt.Fprintf(os.Stdout, "epistemic_status: %s\n", object.EpistemicStatus)
		fmt.Fprintf(os.Stdout, "payload_hash: %s\n", object.PayloadHash)
	default:
		fmt.Fprintf(os.Stdout, "Evidence %s\n", object.ID)
		fmt.Fprintf(os.Stdout, "Source: %s %s\n", object.SourceKind, object.SourceRef)
		fmt.Fprintf(os.Stdout, "Status: %s\n", object.EpistemicStatus)
		if object.Summary != "" {
			fmt.Fprintf(os.Stdout, "Summary: %s\n", object.Summary)
		}
		if object.Digest != "" {
			fmt.Fprintf(os.Stdout, "Digest: %s\n", object.Digest)
		}
		fmt.Fprintf(os.Stdout, "Payload hash: %s\n", object.PayloadHash)
	}
	return nil
}

func printEvidenceHydrateResult(result session.EvidenceHydrationResult, format string) error {
	switch format {
	case commandOutputJSON:
		return json.NewEncoder(os.Stdout).Encode(result)
	case commandOutputKV:
		fmt.Fprintf(os.Stdout, "run_id: %s\n", result.RunID)
		fmt.Fprintf(os.Stdout, "session_id: %s\n", result.SessionID)
		fmt.Fprintf(os.Stdout, "selected: %d\n", len(result.Selected))
		fmt.Fprintf(os.Stdout, "missing: %d\n", len(result.MissingEvidenceIDs))
		fmt.Fprintf(os.Stdout, "fallback_used: %t\n", result.FallbackUsed)
	default:
		fmt.Fprintf(os.Stdout, "Evidence hydration %s\n", result.RunID)
		fmt.Fprintf(os.Stdout, "Selected: %d\n", len(result.Selected))
		if len(result.MissingEvidenceIDs) > 0 {
			fmt.Fprintf(os.Stdout, "Missing required: %s\n", strings.Join(result.MissingEvidenceIDs, ", "))
		}
		if result.FallbackUsed {
			fmt.Fprintf(os.Stdout, "Fallback: %s\n", result.FallbackReason)
		}
		for i, object := range result.Selected {
			fmt.Fprintf(os.Stdout, "%d. %s [%s] %s\n", i+1, object.ID, object.SourceKind, object.Summary)
		}
	}
	return nil
}
