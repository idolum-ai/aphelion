//go:build linux

package durableagent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/tailnet"
)

func TestBuildProvisionPlanDefaultsFromTailnetPolicy(t *testing.T) {
	t.Parallel()

	binary := writeProvisionBinary(t)
	agent := provisionTestAgent()
	plan, err := BuildProvisionPlan(ProvisionOptions{
		Agent:      agent,
		Bootstrap:  provisionTestBootstrap(agent),
		BinaryPath: binary,
		SSHUser:    "alice",
	})
	if err != nil {
		t.Fatalf("BuildProvisionPlan() err = %v", err)
	}
	if plan.SSHTarget != "alice@family-child" {
		t.Fatalf("SSHTarget = %q, want alice@family-child", plan.SSHTarget)
	}
	if plan.ChildRoot != "~/.aphelion/children/family-child" {
		t.Fatalf("ChildRoot = %q, want default child root", plan.ChildRoot)
	}
	if plan.ServiceName != "aphelion-child-family-child" {
		t.Fatalf("ServiceName = %q, want default service", plan.ServiceName)
	}
	if plan.ParentControlURL != "http://aphelion.example.ts.net:8765/control" {
		t.Fatalf("ParentControlURL = %q", plan.ParentControlURL)
	}
}

func TestBuildProvisionPlanRejectsMissingTailnetPolicy(t *testing.T) {
	t.Parallel()

	binary := writeProvisionBinary(t)
	agent := provisionTestAgent()
	agent.LivePolicy.TailnetMode = ""
	agent.LivePolicy.TailnetHostname = ""
	_, err := BuildProvisionPlan(ProvisionOptions{
		Agent:      agent,
		Bootstrap:  provisionTestBootstrap(agent),
		BinaryPath: binary,
	})
	if err == nil || !strings.Contains(err.Error(), "tailnet policy") {
		t.Fatalf("BuildProvisionPlan() err = %v, want tailnet policy error", err)
	}
}

func TestBuildProvisionPlanRejectsUnsafeTailnetSSHInputs(t *testing.T) {
	t.Parallel()

	binary := writeProvisionBinary(t)
	agent := provisionTestAgent()
	for _, tc := range []struct {
		name string
		opts ProvisionOptions
	}{
		{
			name: "option host",
			opts: ProvisionOptions{SSHHost: "--proxy"},
		},
		{
			name: "empty label",
			opts: ProvisionOptions{SSHHost: "family..child"},
		},
		{
			name: "underscore host",
			opts: ProvisionOptions{SSHHost: "family_child"},
		},
		{
			name: "option user",
			opts: ProvisionOptions{SSHUser: "-root"},
		},
		{
			name: "systemd specifier path",
			opts: ProvisionOptions{ChildRoot: "~/.aphelion/%i"},
		},
		{
			name: "relative path",
			opts: ProvisionOptions{ChildRoot: ".aphelion/child"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts := tc.opts
			opts.Agent = agent
			opts.Bootstrap = provisionTestBootstrap(agent)
			opts.BinaryPath = binary
			if _, err := BuildProvisionPlan(opts); err == nil {
				t.Fatal("BuildProvisionPlan() err = nil, want unsafe input rejection")
			}
		})
	}
}

func TestProvisionRemoteChildDryRunDoesNotRequireRunner(t *testing.T) {
	t.Parallel()

	binary := writeProvisionBinary(t)
	agent := provisionTestAgent()
	result, err := ProvisionRemoteChild(context.Background(), ProvisionOptions{
		Agent:      agent,
		Bootstrap:  provisionTestBootstrap(agent),
		BinaryPath: binary,
		SSHUser:    "alice",
	})
	if err != nil {
		t.Fatalf("ProvisionRemoteChild(dry-run) err = %v", err)
	}
	if result.Output != "" || result.Plan.AgentID != "family-child" {
		t.Fatalf("result = %#v, want dry-run plan only", result)
	}
}

func TestProvisionRemoteChildApplyStreamsArchiveOverTailnetSSH(t *testing.T) {
	t.Parallel()

	binary := writeProvisionBinary(t)
	agent := provisionTestAgent()
	runner := &fakeProvisionSSHRunner{}
	result, err := ProvisionRemoteChild(context.Background(), ProvisionOptions{
		Agent:      agent,
		Bootstrap:  provisionTestBootstrap(agent),
		BinaryPath: binary,
		SSHUser:    "alice",
		Apply:      true,
		Runner:     runner,
	})
	if err != nil {
		t.Fatalf("ProvisionRemoteChild(apply) err = %v", err)
	}
	if runner.target != "alice@family-child" {
		t.Fatalf("target = %q, want alice@family-child", runner.target)
	}
	if len(runner.args) < 4 || runner.args[0] != "bash" || runner.args[1] != "-c" || runner.args[3] != "--" {
		t.Fatalf("args = %#v, want bash -c script -- ...", runner.args)
	}
	files := readProvisionArchive(t, runner.stdin)
	if string(files["aphelion"]) != "binary" {
		t.Fatalf("archive aphelion = %q, want binary", string(files["aphelion"]))
	}
	if !strings.Contains(string(files["remote-bootstrap.json"]), `"AgentID": "family-child"`) {
		t.Fatalf("bootstrap archive = %q, want agent id", string(files["remote-bootstrap.json"]))
	}
	if result.Output != "remote ok" {
		t.Fatalf("Output = %q, want remote ok", result.Output)
	}
}

func provisionTestAgent() core.DurableAgent {
	return core.DurableAgent{
		AgentID:       "family-child",
		ParentAgentID: "house",
		ChannelKind:   "telegram_group",
		BootstrapLLM:  core.NodeLLMBootstrap{Backend: "codex", CodexHome: "/home/alice/.codex"},
		BootstrapCeiling: core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DurableAgentLivePolicy{
			OutboundMode: "read_only",
		}),
		LivePolicy: core.DurableAgentLivePolicy{
			OutboundMode:         "read_only",
			TailnetMode:          "tsnet",
			TailnetHostname:      "family-child",
			TailnetSurfacePolicy: "private_status",
			CapabilityEnvelope:   []string{"bounded_review_artifact"},
			DriftPolicy:          "admin_review",
			PublicSurfaceMode:    "none",
			SharedInferenceReuse: "disabled",
			TailnetTags:          []string{"tag:aphelion-child"},
		},
		Status: "active",
	}
}

func provisionTestBootstrap(agent core.DurableAgent) core.DurableAgentRemoteBootstrap {
	return core.DurableAgentRemoteBootstrap{
		AgentID:          agent.AgentID,
		ParentAgentID:    agent.ParentAgentID,
		ChannelKind:      agent.ChannelKind,
		ParentControlURL: "http://aphelion.example.ts.net:8765/control",
		EnrollmentToken:  "enroll-token",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
		BootstrapLLM:     agent.BootstrapLLM,
		BootstrapCeiling: agent.BootstrapCeiling,
	}
}

func writeProvisionBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "aphelion")
	if err := os.WriteFile(path, []byte("binary"), 0o755); err != nil {
		t.Fatalf("WriteFile(binary) err = %v", err)
	}
	return path
}

func readProvisionArchive(t *testing.T, raw []byte) map[string][]byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("gzip.NewReader() err = %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	files := map[string][]byte{}
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next() err = %v", err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("ReadAll(%s) err = %v", header.Name, err)
		}
		files[header.Name] = data
	}
	return files
}

type fakeProvisionSSHRunner struct {
	target string
	args   []string
	stdin  []byte
}

func (r *fakeProvisionSSHRunner) Run(_ context.Context, target string, args []string, stdin []byte) (tailnet.SSHResult, error) {
	r.target = target
	r.args = append([]string(nil), args...)
	r.stdin = append([]byte(nil), stdin...)
	return tailnet.SSHResult{Target: target, Args: args, Output: "remote ok"}, nil
}
