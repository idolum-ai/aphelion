//go:build linux

package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDurableAgentChannelConfigNormalizesLegacyEmailJSONToExternal(t *testing.T) {
	t.Parallel()

	var cfg DurableAgentChannelConfig
	if err := json.Unmarshal([]byte(`{"email":{"address":" child-endpoint ","adapter":"Child_Adapter","poll_interval":"5m"}}`), &cfg); err != nil {
		t.Fatalf("Unmarshal legacy channel config err = %v", err)
	}
	cfg = NormalizeDurableAgentChannelConfig(cfg)
	external := cfg.ExternalConfig()
	if external == nil {
		t.Fatal("ExternalConfig() = nil, want migrated legacy config")
	}
	if external.Address != "child-endpoint" || external.Adapter != "child_adapter" || external.PollInterval != "5m" {
		t.Fatalf("ExternalConfig() = %#v, want normalized migrated config", external)
	}

	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal migrated channel config err = %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, `"external"`) {
		t.Fatalf("marshaled config = %s, want external key", text)
	}
	if strings.Contains(text, `"email"`) {
		t.Fatalf("marshaled config = %s, should not emit legacy email key", text)
	}
}
