//go:build linux

package main

import (
	"os"
	"strings"
	"testing"
)

func TestDefaultAgentSkillsLoadDocumentAndPDFPractices(t *testing.T) {
	t.Parallel()

	skillsRaw, err := os.ReadFile("defaults/agent/SKILLS.md")
	if err != nil {
		t.Fatalf("ReadFile(SKILLS.md) err = %v", err)
	}
	skills := string(skillsRaw)
	for _, needle := range []string{
		"practices/document-intake.md",
		"practices/pdf-generation.md",
	} {
		if !strings.Contains(skills, needle) {
			t.Fatalf("SKILLS.md = %q, want %q", skills, needle)
		}
	}

	documentRaw, err := os.ReadFile("defaults/agent/practices/document-intake.md")
	if err != nil {
		t.Fatalf("ReadFile(document-intake.md) err = %v", err)
	}
	if !strings.Contains(string(documentRaw), "Link-only documents are documents") ||
		!strings.Contains(string(documentRaw), "If tools disagree") {
		t.Fatalf("document-intake.md = %q, want link-only and disagreement guidance", documentRaw)
	}

	pdfRaw, err := os.ReadFile("defaults/agent/practices/pdf-generation.md")
	if err != nil {
		t.Fatalf("ReadFile(pdf-generation.md) err = %v", err)
	}
	if !strings.Contains(string(pdfRaw), "self-contained output directory") ||
		!strings.Contains(string(pdfRaw), "pdfinfo") ||
		!strings.Contains(string(pdfRaw), "pdftotext") {
		t.Fatalf("pdf-generation.md = %q, want validation guidance", pdfRaw)
	}
}

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
		"defaults/agent/practices/document-intake.md": {
			"## Goal",
			"## Success Criteria",
			"## Operational Rules",
			"## Stop Rules",
		},
		"defaults/agent/practices/pdf-generation.md": {
			"## Goal",
			"## Success Criteria",
			"## Operational Rules",
			"## Stop Rules",
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
