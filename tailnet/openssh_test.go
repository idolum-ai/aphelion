//go:build linux

package tailnet

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestOpenSSHClientRunsCommandWithPortAndStdin(t *testing.T) {
	t.Parallel()

	runner := &fakeSSHCommandRunner{}
	client := NewOpenSSHClient(OpenSSHOptions{
		SSHPath:        "ssh-test",
		CommandTimeout: time.Minute,
		Runner:         runner,
	})
	result, err := client.RunOpenSSH(context.Background(), OpenSSHRequest{
		Host:  "mac-mini.example.ts.net",
		User:  "alice",
		Port:  2222,
		Args:  []string{"bash", "-lc", "cat >/tmp/in"},
		Stdin: []byte("payload"),
	})
	if err != nil {
		t.Fatalf("RunOpenSSH() err = %v", err)
	}
	if runner.name != "ssh-test" {
		t.Fatalf("runner name = %q, want ssh-test", runner.name)
	}
	wantArgs := []string{"-p", "2222", "--", "alice@mac-mini.example.ts.net", "'bash' '-lc' 'cat >/tmp/in'"}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("runner args = %#v, want %#v", runner.args, wantArgs)
	}
	if string(runner.stdin) != "payload" {
		t.Fatalf("stdin = %q, want payload", string(runner.stdin))
	}
	if result.Target != "alice@mac-mini.example.ts.net" || result.Output != "ok" || result.ExitCode != 0 {
		t.Fatalf("result = %#v, want target/output/exit", result)
	}
}

func TestOpenSSHClientQuotesRemoteCommandAsSingleSSHArgument(t *testing.T) {
	t.Parallel()

	runner := &fakeSSHCommandRunner{}
	client := NewOpenSSHClient(OpenSSHOptions{Runner: runner})
	_, err := client.RunOpenSSH(context.Background(), OpenSSHRequest{
		Host: "child",
		User: "alice",
		Args: []string{"bash", "-lc", "cd '/Users/alice/Code/app' && git status --short"},
	})
	if err != nil {
		t.Fatalf("RunOpenSSH() err = %v", err)
	}
	wantArgs := []string{"--", "alice@child", "'bash' '-lc' 'cd '\"'\"'/Users/alice/Code/app'\"'\"' && git status --short'"}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("runner args = %#v, want %#v", runner.args, wantArgs)
	}
}

func TestOpenSSHClientRejectsUnsafeInputs(t *testing.T) {
	t.Parallel()

	client := NewOpenSSHClient(OpenSSHOptions{Runner: &fakeSSHCommandRunner{}})
	for _, req := range []OpenSSHRequest{
		{Host: "", User: "alice", Args: []string{"true"}},
		{Host: "--bad", User: "alice", Args: []string{"true"}},
		{Host: "child", User: "", Args: []string{"true"}},
		{Host: "child", User: "--root", Args: []string{"true"}},
		{Host: "child", User: "alice", Port: 70000, Args: []string{"true"}},
		{Host: "child", User: "alice"},
	} {
		if _, err := client.RunOpenSSH(context.Background(), req); err == nil {
			t.Fatalf("RunOpenSSH(%#v) err = nil, want error", req)
		}
	}
}

func TestOpenSSHClientReturnsExitCodeOnFailure(t *testing.T) {
	t.Parallel()

	client := NewOpenSSHClient(OpenSSHOptions{Runner: &fakeSSHCommandRunner{err: errors.New("boom"), code: 17}})
	result, err := client.RunOpenSSH(context.Background(), OpenSSHRequest{Host: "child", User: "alice", Args: []string{"false"}})
	if err == nil {
		t.Fatal("RunOpenSSH() err = nil, want error")
	}
	if result.ExitCode != 17 || result.Output != "ok" {
		t.Fatalf("result = %#v, want exit 17 with output", result)
	}
}
