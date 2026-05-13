//go:build linux

package tailnet

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestSSHClientRunsTailscaleSSHWithStdin(t *testing.T) {
	t.Parallel()

	runner := &fakeSSHCommandRunner{}
	client := NewSSHClient(SSHOptions{
		CLIPath:        "tailscale-test",
		CommandTimeout: time.Minute,
		Runner:         runner,
	})
	result, err := client.Run(context.Background(), "alice@child", []string{"bash", "-c", "cat >/tmp/in"}, []byte("payload"))
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if runner.name != "tailscale-test" {
		t.Fatalf("runner name = %q, want tailscale-test", runner.name)
	}
	wantArgs := []string{"ssh", "--", "alice@child", "bash", "-c", "cat >/tmp/in"}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("runner args = %#v, want %#v", runner.args, wantArgs)
	}
	if string(runner.stdin) != "payload" {
		t.Fatalf("stdin = %q, want payload", string(runner.stdin))
	}
	if result.Output != "ok" || result.ExitCode != 0 {
		t.Fatalf("result = %#v, want ok exit 0", result)
	}
}

func TestSSHClientRejectsEmptyTargetAndCommand(t *testing.T) {
	t.Parallel()

	client := NewSSHClient(SSHOptions{Runner: &fakeSSHCommandRunner{}})
	if _, err := client.Run(context.Background(), "", []string{"true"}, nil); err == nil {
		t.Fatal("Run(empty target) err = nil, want error")
	}
	if _, err := client.Run(context.Background(), "--bad", []string{"true"}, nil); err == nil {
		t.Fatal("Run(option target) err = nil, want error")
	}
	if _, err := client.Run(context.Background(), "child", nil, nil); err == nil {
		t.Fatal("Run(empty command) err = nil, want error")
	}
}

func TestSSHClientReturnsExitCodeOnFailure(t *testing.T) {
	t.Parallel()

	client := NewSSHClient(SSHOptions{Runner: &fakeSSHCommandRunner{err: errors.New("boom"), code: 17}})
	result, err := client.Run(context.Background(), "child", []string{"false"}, nil)
	if err == nil {
		t.Fatal("Run() err = nil, want error")
	}
	if result.ExitCode != 17 || result.Output != "ok" {
		t.Fatalf("result = %#v, want exit 17 with output", result)
	}
}

type fakeSSHCommandRunner struct {
	name  string
	args  []string
	stdin []byte
	code  int
	err   error
}

func (r *fakeSSHCommandRunner) Run(_ context.Context, name string, args []string, stdin []byte) ([]byte, int, error) {
	r.name = name
	r.args = append([]string(nil), args...)
	r.stdin = append([]byte(nil), stdin...)
	return []byte("ok\n"), r.code, r.err
}
