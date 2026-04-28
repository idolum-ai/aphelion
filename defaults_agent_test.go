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
