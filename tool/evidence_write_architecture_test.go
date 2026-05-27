//go:build linux

package tool

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestToolEvidenceWritesDoNotDropErrors(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	toolDir := filepath.Join(root, "tool")
	forbidden := []*regexp.Regexp{
		regexp.MustCompile(`(^|[^[:alnum:]_])_\s*=\s*r\.store\.Upsert[A-Za-z0-9_]+\s*\(`),
		regexp.MustCompile(`(^|[^[:alnum:]_])_,\s*_\s*=\s*r\.store\.Upsert[A-Za-z0-9_]+\s*\(`),
		regexp.MustCompile(`(^|[^[:alnum:]_])_\s*=\s*r\.append[A-Za-z0-9_]*Event\s*\(`),
		regexp.MustCompile(`(^|[^[:alnum:]_])_,\s*_\s*=\s*r\.append[A-Za-z0-9_]*Event\s*\(`),
	}

	var violations []string
	err := filepath.WalkDir(toolDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Base(path) == "evidence_write_architecture_test.go" || !strings.HasSuffix(path, ".go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(raw), "\n") {
			for _, pattern := range forbidden {
				if pattern.MatchString(line) {
					rel, _ := filepath.Rel(root, path)
					violations = append(violations, rel+":"+itoa(i+1)+": "+strings.TrimSpace(line))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) > 0 {
		t.Fatalf("tool evidence writes must propagate errors or log through warnDroppedEvidenceWrite; forbidden dropped-error patterns:\n%s", strings.Join(violations, "\n"))
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find repository root from %s", dir)
		}
		dir = parent
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
