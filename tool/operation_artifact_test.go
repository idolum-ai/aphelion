//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func TestDefinitionsIncludeOperationArtifactToolWhenStoreConfigured(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(t.TempDir(), 0)
	names := make([]string, 0, len(registry.Definitions()))
	for _, def := range registry.Definitions() {
		names = append(names, def.Name)
	}
	if containsString(names, "operation_artifact") {
		t.Fatalf("definitions without store = %#v, do not want operation_artifact", names)
	}

	registry = NewRegistry(t.TempDir(), 0).WithSessionStore(newToolTestStore(t))
	names = names[:0]
	for _, def := range registry.Definitions() {
		names = append(names, def.Name)
	}
	if !containsString(names, "operation_artifact") {
		t.Fatalf("definitions with store = %#v, want operation_artifact", names)
	}
}

func TestOperationArtifactToolListsAndResolvesSendableArtifact(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	reportPath := filepath.Join(registry.workspace, "reports", "work-evidence.md")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() err = %v", err)
	}
	if err := os.WriteFile(reportPath, []byte("evidence"), 0o600); err != nil {
		t.Fatalf("WriteFile() err = %v", err)
	}
	if err := store.UpdateOperationState(key, session.OperationState{
		Status: session.OperationStatusActive,
		Artifacts: []session.OperationArtifact{
			{Label: "Work evidence", Ref: "reports/work-evidence.md"},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	listOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"operation_artifact",
		json.RawMessage(`{"action":"list"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(operation_artifact list) err = %v", err)
	}
	for _, want := range []string{"[OPERATION_ARTIFACTS]", "Work evidence", "sendable: true", reportPath} {
		if !strings.Contains(listOut, want) {
			t.Fatalf("list output = %q, want %q", listOut, want)
		}
	}

	resolveOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"operation_artifact",
		json.RawMessage(`{"action":"resolve_sendable","latest":true}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(operation_artifact resolve) err = %v", err)
	}
	for _, want := range []string{"[OPERATION_ARTIFACT]", "Work evidence", `media_directive: MEDIA: {"path":"` + reportPath + `"}`, "only if the user explicitly asked"} {
		if !strings.Contains(resolveOut, want) {
			t.Fatalf("resolve output = %q, want %q", resolveOut, want)
		}
	}
}

func TestOperationArtifactToolRejectsAmbiguousResolveAndOutOfScopeArtifact(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	if err := store.UpdateOperationState(key, session.OperationState{
		Status: session.OperationStatusActive,
		Artifacts: []session.OperationArtifact{
			{Label: "Secret", Ref: "../secret.md"},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"operation_artifact",
		json.RawMessage(`{"action":"resolve_sendable"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "requires ref, label, or latest=true") {
		t.Fatalf("ambiguous resolve err = %v, want selector validation", err)
	}

	_, err = registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"operation_artifact",
		json.RawMessage(`{"action":"resolve_sendable","latest":true}`),
	)
	if err == nil || !strings.Contains(err.Error(), "outside the read roots") {
		t.Fatalf("out-of-scope resolve err = %v, want sandbox rejection", err)
	}
}
