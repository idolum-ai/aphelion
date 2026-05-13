//go:build linux

package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (e *SemanticEngine) syncNativeCorpus(ctx context.Context, root string, scope string, principalID string, now time.Time) error {
	db, err := e.ensureDB()
	if err != nil {
		return err
	}
	sources, err := e.collectNativeSources(root, now)
	if err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin semantic sync tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := loadIndexedDocumentsTx(tx, scope, principalID, "native")
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(sources))
	for _, src := range sources {
		seen[src.path] = struct{}{}
		doc, ok := existing[src.path]
		if ok && doc.Checksum == src.checksum && sameInstant(doc.MTime, src.mtime) && doc.ImportState == SemanticImportStateApproved {
			continue
		}
		docID, err := upsertSemanticDocumentTx(tx, SemanticDocument{
			ID:               doc.ID,
			Scope:            scope,
			PrincipalID:      principalID,
			SourcePath:       src.path,
			SourceKind:       src.kind,
			SourceClass:      src.class,
			ProvenanceSource: src.provenance,
			ImportState:      src.importState,
			Checksum:         src.checksum,
			MTime:            src.mtime,
		})
		if err != nil {
			return err
		}
		if err := replaceSemanticChunksTx(tx, docID, chunkText(src.path, src.kind, src.content)); err != nil {
			return err
		}
	}

	for path, doc := range existing {
		if _, ok := seen[path]; ok {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM semantic_documents WHERE id = ?`, doc.ID); err != nil {
			return fmt.Errorf("delete removed semantic document %s: %w", path, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit semantic sync tx: %w", err)
	}
	return nil
}

func (e *SemanticEngine) collectNativeSources(root string, now time.Time) ([]semanticSource, error) {
	var out []semanticSource
	for _, rel := range e.semanticSourceList() {
		path := filepath.Join(root, filepath.FromSlash(rel))
		raw, info, err := readSemanticFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read semantic source %s: %w", path, err)
		}
		content := StripInstrumentation(string(raw))
		out = append(out, semanticSource{
			path:        filepath.ToSlash(rel),
			kind:        detectSemanticKind(rel),
			class:       classifySemanticSource(rel, detectSemanticKind(rel)),
			content:     content,
			checksum:    checksumText(content),
			mtime:       info.ModTime().UTC(),
			provenance:  "native",
			importState: SemanticImportStateApproved,
		})
	}

	if e.opts.IncludeDailyNotes {
		dir := strings.TrimSpace(e.opts.DailyNotesDir)
		if dir == "" {
			dir = "memory/daily"
		}
		noteRoot := filepath.Join(root, filepath.FromSlash(dir))
		entries, err := collectMarkdownFiles(noteRoot)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("collect semantic daily notes %s: %w", noteRoot, err)
			}
		} else {
			for _, entry := range entries {
				rel, err := filepath.Rel(root, entry.Path)
				if err != nil {
					return nil, fmt.Errorf("relative daily note path: %w", err)
				}
				content := StripInstrumentation(string(entry.Raw))
				out = append(out, semanticSource{
					path:        filepath.ToSlash(rel),
					kind:        "daily_note",
					class:       "daily_note",
					content:     content,
					checksum:    checksumText(content),
					mtime:       entry.ModTime.UTC(),
					provenance:  "native",
					importState: SemanticImportStateApproved,
				})
			}
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, nil
}

func (e *SemanticEngine) semanticSourceList() []string {
	sources := append([]string(nil), e.opts.Sources...)
	if e.opts.IncludeQuestions {
		sources = append(sources, "memory/questions.md")
	}
	if e.opts.IncludeRhizome {
		sources = append(sources, "memory/rhizome.md")
	}
	return uniqueStrings(sources)
}

func readSemanticFile(path string) ([]byte, os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	return raw, info, nil
}

type markdownFile struct {
	Path    string
	Raw     []byte
	ModTime time.Time
}

func collectMarkdownFiles(root string) ([]markdownFile, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}
	var out []markdownFile
	err = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		if strings.ToLower(filepath.Ext(path)) != ".md" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out = append(out, markdownFile{
			Path:    path,
			Raw:     raw,
			ModTime: info.ModTime(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func withinDailyWindow(mode SemanticMode, now time.Time, source string, mtime time.Time) bool {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	threshold := 48 * time.Hour
	if mode == SemanticModeHeartbeat {
		threshold = 7 * 24 * time.Hour
	}
	ts := dailyNoteTime(source, mtime)
	if ts.IsZero() {
		return true
	}
	return ts.After(now.Add(-threshold))
}

func dailyNoteTime(source string, fallback time.Time) time.Time {
	base := strings.TrimSuffix(filepath.Base(filepath.ToSlash(source)), filepath.Ext(source))
	if t, err := time.Parse("2006-01-02", base); err == nil {
		return t.UTC()
	}
	return fallback.UTC()
}

func detectSemanticKind(source string) string {
	switch strings.ToLower(filepath.ToSlash(strings.TrimSpace(source))) {
	case "memory.md":
		return "memory"
	case "memory/knowledge.md":
		return "knowledge"
	case "memory/decisions.md":
		return "decision"
	case "memory/questions.md":
		return "question"
	case "memory/rhizome.md":
		return "rhizome"
	case "memory/dreams.md":
		return "dream"
	default:
		if strings.Contains(source, "daily/") || strings.Contains(source, "daily\\") {
			return "daily_note"
		}
		return "memory"
	}
}
