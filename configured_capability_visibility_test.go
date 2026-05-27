//go:build linux

package main

import (
	"reflect"
	"testing"
)

func TestConfiguredSkillFilesDetectsRootRelativeSkillsDirectory(t *testing.T) {
	t.Parallel()

	got := configuredSkillFiles([]string{
		"skills/review.md",
		"relative/skills/scout.md",
		"notes/review.md",
	})
	want := []string{"skills/review.md", "relative/skills/scout.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("configuredSkillFiles() = %#v, want %#v", got, want)
	}
}
