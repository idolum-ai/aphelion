//go:build linux

package runtime

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/session"
)

func TestDoctorWebSearchStatusProjectsConfigAndGrants(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "grant:web-search:test",
		GrantedBy:      "test",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindTool,
		TargetResource: "web_search",
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}
	cfg := config.Default()
	cfg.Tools.WebSearch.Enabled = true
	cfg.Tools.WebSearch.Brave.Enabled = true
	cfg.Tools.WebSearch.Brave.APIKeyEnv = "BRAVE_API_KEY"
	rt := &Runtime{cfg: &cfg, store: store}
	var b strings.Builder
	rt.writeDoctorWebSearchStatus(&b)
	out := b.String()
	for _, want := range []string{"web_search: configured", "web_search_enabled=\"true\"", "web_search_brave=\"enabled:api_key_env\"", "web_search_active_grants=\"1\""} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor web_search output missing %q:\n%s", want, out)
		}
	}
}
