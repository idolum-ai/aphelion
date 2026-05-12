//go:build linux

package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDurableAgentChannelConfigNormalizesExternalJSON(t *testing.T) {
	t.Parallel()

	var cfg DurableAgentChannelConfig
	if err := json.Unmarshal([]byte(`{"external":{"address":" child-endpoint ","adapter":"Child_Adapter","poll_interval":"5m"}}`), &cfg); err != nil {
		t.Fatalf("Unmarshal channel config err = %v", err)
	}
	cfg = NormalizeDurableAgentChannelConfig(cfg)
	external := cfg.ExternalConfig()
	if external == nil {
		t.Fatal("ExternalConfig() = nil, want config")
	}
	if external.Address != "child-endpoint" || external.Adapter != "child_adapter" || external.PollInterval != "5m" {
		t.Fatalf("ExternalConfig() = %#v, want normalized config", external)
	}

	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal channel config err = %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, `"external"`) {
		t.Fatalf("marshaled config = %s, want external key", text)
	}
	if strings.Contains(text, `"email"`) {
		t.Fatalf("marshaled config = %s, should not emit removed email key", text)
	}
}
