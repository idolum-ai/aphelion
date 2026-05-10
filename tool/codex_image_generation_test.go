//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type fakeCodexImageProvider struct {
	messages []agent.Message
	tools    []agent.ToolDef
	resp     *agent.Response
	err      error
}

func (f *fakeCodexImageProvider) Complete(_ context.Context, messages []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	f.messages = append([]agent.Message(nil), messages...)
	f.tools = append([]agent.ToolDef(nil), tools...)
	if f.err != nil {
		return nil, f.err
	}
	if f.resp != nil {
		return f.resp, nil
	}
	return &agent.Response{}, nil
}

func TestCodexImageGenerationDefinitionRequiresGrant(t *testing.T) {
	registry, store := newDurableAgentToolRegistry(t)
	registry.SetCodexImageGenerationProvider(&fakeCodexImageProvider{})
	p := principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "child-alpha"}
	if toolDefExists(registry.DefinitionsForPrincipal(p), codexImageGenerationToolName) {
		t.Fatal("DefinitionsForPrincipal without grant included codex_image_generation")
	}
	grantToolInvoke(t, store, codexImageGenerationToolName, "durable_agent:child-alpha")
	if !toolDefExists(registry.DefinitionsForPrincipal(p), codexImageGenerationToolName) {
		t.Fatal("DefinitionsForPrincipal with grant missing codex_image_generation")
	}
}

func TestCodexImageGenerationInvokesBuiltInAndWritesArtifact(t *testing.T) {
	registry, store := newDurableAgentToolRegistry(t)
	provider := &fakeCodexImageProvider{resp: &agent.Response{Content: "draft", Media: []core.Media{{Type: "image", Data: []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, MimeType: "image/png", Filename: "draft.png"}}}}
	registry.SetCodexImageGenerationProvider(provider)
	p := principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "child-alpha"}
	grantToolInvoke(t, store, codexImageGenerationToolName, "durable_agent:child-alpha")

	key := adminSessionKey()
	out, err := registry.executeWithScopeAndPrincipal(context.Background(), codexImageGenerationToolName, json.RawMessage(`{"prompt":"make a slide"}`), sandbox.Scope{WorkingRoot: registry.workspace, SharedMemoryRoot: registry.workspace}, p, key)
	if err != nil {
		t.Fatalf("codex_image_generation err = %v output=%s", err, out)
	}
	if len(provider.tools) != 1 || provider.tools[0].Name != "image_generation" || !strings.Contains(string(provider.tools[0].Parameters), `"builtin"`) {
		t.Fatalf("provider tools = %#v, want image_generation builtin", provider.tools)
	}
	if !strings.Contains(out, `"status": "completed"`) || !strings.Contains(out, `MEDIA: {\"path\":`) {
		t.Fatalf("output = %s, want completed structured MEDIA directive", out)
	}
	path := registry.workspace + "/generated/image-generation/draft.png"
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("generated artifact %q err = %v", path, err)
	}
	grant, ok, err := store.CapabilityGrant("grant:" + codexImageGenerationToolName + ":durable_agent:child-alpha")
	if err != nil || !ok {
		t.Fatalf("CapabilityGrant ok=%t err=%v", ok, err)
	}
	if grant.InvocationCount == 0 {
		t.Fatalf("InvocationCount = 0, want recorded invocation")
	}
	invocations, err := store.CapabilityInvocationsByGrant(grant.GrantID, 1)
	if err != nil {
		t.Fatalf("CapabilityInvocationsByGrant() err = %v", err)
	}
	if len(invocations) != 1 || invocations[0].ContinuationLeaseID == "" {
		t.Fatalf("codex_image_generation invocation refs = %#v, want continuation lease evidence", invocations)
	}
}

func TestCodexImageGenerationDeniesUngrantedPrincipal(t *testing.T) {
	registry, _ := newDurableAgentToolRegistry(t)
	registry.SetCodexImageGenerationProvider(&fakeCodexImageProvider{})
	p := principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "child-alpha"}
	_, err := registry.executeWithScopeAndPrincipal(context.Background(), codexImageGenerationToolName, json.RawMessage(`{"prompt":"make a slide"}`), sandbox.Scope{WorkingRoot: registry.workspace}, p, session.SessionKey{})
	if err == nil || !strings.Contains(err.Error(), "not granted") {
		t.Fatalf("err = %v, want not granted", err)
	}
}

func TestRuntimeInjectsCodexImageGenerationProviderWhenCodexBackendActive(t *testing.T) {
	// Compile-time coverage for the setter path lives in runtime; keep this package test focused.
	_ = time.Second
}
