//go:build linux

package turn

import (
	"os/exec"
	"strings"
	"testing"
)

func TestTurnPackageDoesNotDependOnRuntimePackage(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", "github.com/idolum-ai/aphelion/turn")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list deps failed: %v\n%s", err, string(out))
	}
	const runtimePkg = "github.com/idolum-ai/aphelion/runtime"
	for _, dep := range strings.Fields(string(out)) {
		if strings.TrimSpace(dep) == runtimePkg {
			t.Fatalf("turn package depends on runtime package: %s", runtimePkg)
		}
	}
}
