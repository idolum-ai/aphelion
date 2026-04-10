//go:build linux

package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

const defaultMaxOutputBytes = 32 * 1024

type Registry struct {
	workspace      string
	timeout        time.Duration
	maxOutputBytes int
	sandbox        *sandbox.Resolver
	runner         *sandbox.Runner
	store          *session.SQLiteStore
}

type execInput struct {
	Command    string `json:"command"`
	Workdir    string `json:"workdir,omitempty"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`
}

type memoryInput struct {
	Action     string   `json:"action"`
	Scope      string   `json:"scope,omitempty"`
	Store      string   `json:"store"`
	Content    string   `json:"content,omitempty"`
	Match      string   `json:"match,omitempty"`
	SourceTag  string   `json:"source_tag,omitempty"`
	Confidence *float64 `json:"confidence,omitempty"`
}

type sessionSearchInput struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
	Scope string `json:"scope,omitempty"`
}

func NewRegistry(workspace string, timeout time.Duration) *Registry {
	return &Registry{
		workspace:      workspace,
		timeout:        timeout,
		maxOutputBytes: defaultMaxOutputBytes,
	}
}

func NewRegistryWithSandbox(workspace string, timeout time.Duration, resolver *sandbox.Resolver) *Registry {
	registry := NewRegistry(workspace, timeout)
	registry.sandbox = resolver
	registry.runner = sandbox.NewRunner()
	return registry
}

func (r *Registry) WithSessionStore(store *session.SQLiteStore) *Registry {
	r.store = store
	return r
}

func (r *Registry) SupportsPrincipal(p principal.Principal) bool {
	if r == nil || r.sandbox == nil {
		return false
	}

	scope, err := r.sandbox.Resolve(p)
	if err != nil {
		return false
	}
	if r.runner == nil {
		return p.Role == principal.RoleAdmin
	}
	return r.runner.Supports(scope)
}

func (r *Registry) Definitions() []agent.ToolDef {
	return []agent.ToolDef{
		{
			Name:        "exec",
			Description: "Run a shell command in the configured workspace. Use this for git, file inspection, builds, tests, and repository edits.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"command": {"type": "string", "description": "Shell command to run with bash -lc"},
					"workdir": {"type": "string", "description": "Optional subdirectory within the workspace"},
					"timeout_sec": {"type": "integer", "minimum": 1, "description": "Optional per-command timeout in seconds"}
				},
				"required": ["command"]
			}`),
		},
		{
			Name:        "memory",
			Description: "Write curated memory for the current principal. Use this for compact durable notes, knowledge, decisions, questions, or rhizome associations.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"action": {"type": "string", "enum": ["add", "replace", "remove"], "description": "Memory write operation"},
					"scope": {"type": "string", "enum": ["shared", "principal"], "description": "Shared memory for admin, or principal-local memory for isolated users"},
					"store": {"type": "string", "enum": ["memory", "knowledge", "decisions", "questions", "rhizome"], "description": "Curated memory store to edit"},
					"content": {"type": "string", "description": "Content to add or replacement content"},
					"match": {"type": "string", "description": "Exact existing text to replace or remove"},
					"source_tag": {"type": "string", "enum": ["direct", "observed", "inferred", "hypothesized", "shared"], "description": "Optional provenance tag for added or replaced entries"},
					"confidence": {"type": "number", "minimum": 0, "maximum": 1, "description": "Optional confidence for added or replaced entries"}
				},
				"required": ["action", "store"]
			}`),
		},
		{
			Name:        "session_search",
			Description: "Search prior transcript messages explicitly. Use this to recall earlier conversations without silently flattening history into memory.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search text"},
					"limit": {"type": "integer", "minimum": 1, "maximum": 20, "description": "Maximum number of hits"},
					"scope": {"type": "string", "enum": ["session", "all"], "description": "Search only the current session or all visible sessions"}
				},
				"required": ["query"]
			}`),
		},
	}
}

func (r *Registry) Execute(ctx context.Context, name string, input json.RawMessage) (string, error) {
	return r.executeWithRoot(ctx, name, input, r.workspace)
}

func (r *Registry) ExecuteForPrincipal(ctx context.Context, p principal.Principal, name string, input json.RawMessage) (string, error) {
	return r.ExecuteForSessionPrincipal(ctx, p, session.SessionKey{}, name, input)
}

func (r *Registry) ExecuteForSessionPrincipal(ctx context.Context, p principal.Principal, key session.SessionKey, name string, input json.RawMessage) (string, error) {
	if r.sandbox == nil {
		return "", fmt.Errorf("principal-aware execution requires sandbox resolver")
	}

	scope, err := r.sandbox.Resolve(p)
	if err != nil {
		return "", err
	}
	if err := ensureScopeReady(scope); err != nil {
		return "", err
	}
	if r.runner == nil {
		return "", fmt.Errorf("principal-aware execution requires sandbox runner")
	}
	if !r.runner.Supports(scope) {
		return "", fmt.Errorf("no supported sandbox backend for principal role %q", p.Role)
	}
	return r.executeWithScopeAndPrincipal(ctx, name, input, scope, p, key)
}

func (r *Registry) executeWithRoot(ctx context.Context, name string, input json.RawMessage, root string) (string, error) {
	return r.executeWithScopeAndPrincipal(ctx, name, input, sandbox.Scope{
		WorkingRoot:      root,
		SharedMemoryRoot: root,
	}, principal.Principal{}, session.SessionKey{})
}

func (r *Registry) executeWithScope(ctx context.Context, name string, input json.RawMessage, scope sandbox.Scope) (string, error) {
	return r.executeWithScopeAndPrincipal(ctx, name, input, scope, scope.Principal, session.SessionKey{})
}

func (r *Registry) executeWithScopeAndPrincipal(ctx context.Context, name string, input json.RawMessage, scope sandbox.Scope, p principal.Principal, key session.SessionKey) (string, error) {
	switch name {
	case "exec":
		return r.exec(ctx, input, scope)
	case "memory":
		return r.memory(ctx, input, scope)
	case "session_search":
		return r.sessionSearch(ctx, input, p, key)
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}

func (r *Registry) exec(ctx context.Context, input json.RawMessage, scope sandbox.Scope) (string, error) {
	var in execInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("decode exec input: %w", err)
	}
	if strings.TrimSpace(in.Command) == "" {
		return "", fmt.Errorf("exec command is required")
	}

	workdir, err := resolveWorkdir(scope.WorkingRoot, in.Workdir)
	if err != nil {
		return "", err
	}

	timeout := r.timeout
	if in.TimeoutSec > 0 {
		timeout = time.Duration(in.TimeoutSec) * time.Second
	}
	timeout = defaultTimeout(timeout)

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stdout, stderr, err := r.runCommand(runCtx, scope, in.Command, workdir)
	out := renderOutput(stdout, stderr, r.maxOutputBytes)
	if err == nil {
		return out, nil
	}

	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return out, fmt.Errorf("command timed out after %s", timeout)
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return out, fmt.Errorf("command failed with exit code %d", exitErr.ExitCode())
	}

	return out, fmt.Errorf("run command: %w", err)
}

func (r *Registry) memory(_ context.Context, input json.RawMessage, scope sandbox.Scope) (string, error) {
	var in memoryInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("decode memory input: %w", err)
	}

	root, effectiveScope, err := resolveMemoryRoot(scope, in.Scope)
	if err != nil {
		return "", err
	}

	result, err := memstore.ApplyWrite(memstore.WriteRequest{
		Root:       root,
		Store:      in.Store,
		Action:     in.Action,
		Content:    in.Content,
		Match:      in.Match,
		SourceTag:  in.SourceTag,
		Confidence: in.Confidence,
	})
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("memory_%s_ok scope=%s store=%s path=%s", result.Action, effectiveScope, result.Store, result.Path), nil
}

func (r *Registry) runCommand(ctx context.Context, scope sandbox.Scope, command string, workdir string) (string, string, error) {
	if r.runner != nil && strings.TrimSpace(string(scope.Principal.Role)) != "" {
		res, err := r.runner.Run(ctx, sandbox.ExecRequest{
			Scope:   scope,
			Command: command,
			Workdir: workdir,
		})
		return res.Stdout, res.Stderr, err
	}

	cmd := exec.CommandContext(ctx, "bash", "-lc", command)
	cmd.Dir = workdir

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func defaultTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return 60 * time.Second
	}
	return timeout
}

func ensureScopeReady(scope sandbox.Scope) error {
	if err := os.MkdirAll(scope.WorkingRoot, 0o755); err != nil {
		return fmt.Errorf("prepare working root %q: %w", scope.WorkingRoot, err)
	}
	if strings.TrimSpace(scope.UserMemory) != "" {
		if err := os.MkdirAll(scope.UserMemory, 0o755); err != nil {
			return fmt.Errorf("prepare user memory root %q: %w", scope.UserMemory, err)
		}
	}
	return nil
}

func resolveWorkdir(root, raw string) (string, error) {
	base, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}

	target := base
	if strings.TrimSpace(raw) != "" {
		if filepath.IsAbs(raw) {
			target = filepath.Clean(raw)
		} else {
			target = filepath.Join(base, raw)
		}
	}

	target, err = filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("resolve workdir: %w", err)
	}

	rel, err := filepath.Rel(base, target)
	if err != nil {
		return "", fmt.Errorf("check workdir: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workdir %q escapes workspace %q", raw, base)
	}

	return target, nil
}

func renderOutput(stdout, stderr string, limit int) string {
	parts := make([]string, 0, 2)
	if strings.TrimSpace(stdout) != "" {
		parts = append(parts, "stdout:\n"+truncate(stdout, limit))
	}
	if strings.TrimSpace(stderr) != "" {
		parts = append(parts, "stderr:\n"+truncate(stderr, limit))
	}
	if len(parts) == 0 {
		return "(no output)"
	}
	return strings.Join(parts, "\n\n")
}

func truncate(raw string, limit int) string {
	if len(raw) <= limit || limit <= 0 {
		return raw
	}
	if limit <= 64 {
		return raw[:limit]
	}
	head := limit / 2
	tail := limit / 2
	return raw[:head] + "\n...[truncated]...\n" + raw[len(raw)-tail:]
}

func resolveMemoryRoot(scope sandbox.Scope, requested string) (string, string, error) {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested == "" {
		if scope.Principal.Role == principal.RoleApprovedUser && strings.TrimSpace(scope.UserMemory) != "" {
			requested = "principal"
		} else {
			requested = "shared"
		}
	}

	switch requested {
	case "shared":
		if scope.Principal.Role == principal.RoleApprovedUser {
			return "", "", fmt.Errorf("approved users may not write shared memory")
		}
		root := strings.TrimSpace(scope.SharedMemoryRoot)
		if root == "" {
			root = strings.TrimSpace(scope.WorkingRoot)
		}
		if root == "" {
			return "", "", fmt.Errorf("shared memory root is not configured")
		}
		return root, requested, nil
	case "principal":
		root := strings.TrimSpace(scope.UserMemory)
		if root == "" {
			return "", "", fmt.Errorf("principal memory root is not available for this principal")
		}
		return root, requested, nil
	default:
		return "", "", fmt.Errorf("memory scope must be shared or principal")
	}
}

func (r *Registry) sessionSearch(_ context.Context, input json.RawMessage, p principal.Principal, key session.SessionKey) (string, error) {
	if r.store == nil {
		return "", fmt.Errorf("session search requires transcript store")
	}

	var in sessionSearchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("decode session_search input: %w", err)
	}
	if strings.TrimSpace(in.Query) == "" {
		return "", fmt.Errorf("session_search query is required")
	}

	scope := strings.ToLower(strings.TrimSpace(in.Scope))
	var filter *session.SessionKey
	switch {
	case p.Role == principal.RoleApprovedUser:
		filter = &key
		scope = "session"
	case scope == "", scope == "all":
		filter = nil
		scope = "all"
	case scope == "session":
		filter = &key
	default:
		return "", fmt.Errorf("session_search scope must be session or all")
	}

	hits, err := r.store.SearchMessages(in.Query, in.Limit, filter)
	if err != nil {
		return "", err
	}
	return renderSessionSearchResults(scope, in.Query, hits), nil
}

func renderSessionSearchResults(scope string, query string, hits []session.SearchHit) string {
	var b strings.Builder
	b.WriteString("[SESSION_RECALL]\n")
	b.WriteString("scope: ")
	b.WriteString(scope)
	b.WriteString("\nquery: ")
	b.WriteString(strings.TrimSpace(query))
	b.WriteString("\n")
	if len(hits) == 0 {
		b.WriteString("no_hits\n[/SESSION_RECALL]")
		return b.String()
	}
	for i, hit := range hits {
		fmt.Fprintf(&b, "\n%d. chat=%d turn=%d role=%s\n", i+1, hit.ChatID, hit.TurnIndex, hit.Role)
		b.WriteString("content: ")
		b.WriteString(truncate(strings.TrimSpace(hit.Content), 600))
		b.WriteString("\n")
	}
	b.WriteString("[/SESSION_RECALL]")
	return b.String()
}
