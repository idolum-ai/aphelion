//go:build linux

package main

import (
	"os"
	"strings"
	"testing"
)

func TestDefaultAgentIdentityUsesCanonicalLayerNames(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"defaults/agent/SOUL.md",
		"defaults/agent/IDENTITY.md",
		"defaults/agent/AGENTS.md",
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) err = %v", path, err)
		}
		text := string(raw)
		for _, stale := range []string{
			"Aphelion is the governor",
			"Aphelion decides",
			"Final authority still belongs to Aphelion",
		} {
			if strings.Contains(text, stale) {
				t.Fatalf("%s contains stale identity claim %q:\n%s", path, stale, text)
			}
		}
	}

	identityRaw, err := os.ReadFile("defaults/agent/IDENTITY.md")
	if err != nil {
		t.Fatalf("ReadFile(IDENTITY.md) err = %v", err)
	}
	identity := string(identityRaw)
	for _, want := range []string{
		"Name: Idolum (System)",
		"Aphelion: repo/service/harness",
		"Idolum (System) decides.",
		"Idolum speaks.",
	} {
		if !strings.Contains(identity, want) {
			t.Fatalf("IDENTITY.md missing %q:\n%s", want, identity)
		}
	}
}

func TestDefaultAgentPromptFilesUseGPT55OutcomeStructure(t *testing.T) {
	t.Parallel()

	required := map[string][]string{
		"defaults/agent/SOUL.md": {
			"Role:",
			"## Goal",
			"## Success Criteria",
			"## Stop Rules",
		},
		"defaults/agent/IDOLUM.md": {
			"Role:",
			"## Goal",
			"## Success Criteria",
			"## Output",
			"## Stop Rules",
		},
		"defaults/agent/TOOLS.md": {
			"## Goal",
			"## Success Criteria",
			"## Validation",
			"## Stop Rules",
		},
		"defaults/agent/HEARTBEAT.md": {
			"## Goal",
			"## Success Criteria",
			"## Output",
			"## Stop Rules",
		},
		"defaults/agent/face/persona/telos.md": {
			"## Ends",
			"Represent Idolum",
			"Help the user achieve goals",
			"Authority preservation is a means",
		},
		"defaults/agent/face/contracts/semantic-memory-is-texture.md": {
			"Route beats retrieval",
			"semantic memory may add continuity only after that route is set",
		},
		"defaults/agent/face/scenes/approval-request.md": {
			"## Purpose",
			"bounded authority",
			"approval already exists",
		},
		"defaults/agent/face/models/overlays.md": {
			"Same ghost, different vessel",
			"evidence-gated compensation",
			"Do not create `persona-openai`",
		},
		"defaults/agent/face/models/openai-gpt-5.5.md": {
			"Apply this overlay only when the active face model route is `openai:gpt-5.5`",
			"4 of 5",
			"Do not change Idolum's shared telos",
		},
		"defaults/agent/face/models/anthropic-claude-sonnet-4-6.md": {
			"Apply this overlay only when the active face model route is",
			"3 of 3",
			"Do not replace the shared Idolum persona",
		},
	}

	for path, wants := range required {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) err = %v", path, err)
		}
		text := string(raw)
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Fatalf("%s missing %q:\n%s", path, want, text)
			}
		}
	}
}
