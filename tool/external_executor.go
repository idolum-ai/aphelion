//go:build linux

package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type ExternalToolExecutor interface {
	Supports(manifest ExternalToolManifest) bool
	Execute(ctx context.Context, manifest ExternalToolManifest, input json.RawMessage, scope sandbox.Scope, runner *sandbox.Runner, maxOutputBytes int, access ExternalToolExecutionAccess) (string, error)
}

type ExternalToolExecutionAccess struct {
	ExtraReadonlyPaths []string
	ExtraReadonlyBinds []sandbox.BindPath
	ExtraEnv           map[string]string
}

type defaultExternalToolExecutor struct{}

func (defaultExternalToolExecutor) Supports(manifest ExternalToolManifest) bool {
	manifest = NormalizeExternalToolManifest(manifest)
	if manifest.Execution.Mode != "process" && manifest.Execution.Mode != "subprocess" {
		return false
	}
	return validateExternalProcessPolicy(manifest) == nil
}

func (defaultExternalToolExecutor) Execute(ctx context.Context, manifest ExternalToolManifest, input json.RawMessage, scope sandbox.Scope, runner *sandbox.Runner, maxOutputBytes int, access ExternalToolExecutionAccess) (string, error) {
	manifest = NormalizeExternalToolManifest(manifest)
	if err := validateExternalToolManifest(manifest); err != nil {
		return "", err
	}
	if err := validateExternalProcessPolicy(manifest); err != nil {
		return "", err
	}
	if manifest.Execution.Mode != "process" && manifest.Execution.Mode != "subprocess" {
		return "", fmt.Errorf("external tool %q execution constraints are not supported by the process executor", manifest.Name)
	}
	if err := validateExternalToolSchema(manifest.IO.InputSchema, input, "input"); err != nil {
		return "", err
	}
	scope, err := scopeForExternalProcessNetwork(manifest, scope)
	if err != nil {
		return "", err
	}
	workdir, err := resolveWorkdir(scope.WorkingRoot, manifest.Execution.Workdir)
	if err != nil {
		return "", err
	}
	timeout := defaultTimeout(30 * time.Second)
	if manifest.Execution.TimeoutSeconds > 0 {
		timeout = time.Duration(manifest.Execution.TimeoutSeconds) * time.Second
	}
	if manifest.Constraints.MaxRuntimeSeconds > 0 {
		constraintTimeout := time.Duration(manifest.Constraints.MaxRuntimeSeconds) * time.Second
		if timeout <= 0 || constraintTimeout < timeout {
			timeout = constraintTimeout
		}
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stdout, stderr, err := runExternalProcessCommand(runCtx, scope, runner, manifest.Execution.Entry, workdir, input, access)
	if err != nil {
		return "", fmt.Errorf("external tool %q execution failed: %s", manifest.Name, renderOutput(stdout, stderr, maxOutputBytes))
	}
	output := []byte(strings.TrimSpace(stdout))
	if len(output) == 0 {
		return "", fmt.Errorf("external tool %q returned empty stdout; expected json output", manifest.Name)
	}
	if !json.Valid(output) {
		return "", fmt.Errorf("external tool %q returned invalid json stdout: %s", manifest.Name, truncate(string(output), maxOutputBytes))
	}
	if err := validateExternalToolSchema(manifest.IO.OutputSchema, json.RawMessage(output), "output"); err != nil {
		return "", err
	}
	return string(output), nil
}

func scopeForExternalProcessNetwork(manifest ExternalToolManifest, scope sandbox.Scope) (sandbox.Scope, error) {
	if manifest.Constraints.Network != "allowlist" {
		return scope, nil
	}
	if scope.Profile.Mode != sandbox.ModeIsolated || scope.Profile.Network != sandbox.NetworkAllowlist {
		return sandbox.Scope{}, externalPolicyViolationError{Reason: "process-mode network=\"allowlist\" requires an isolated sandbox profile configured with network=allowlist"}
	}
	requested, err := sandbox.ParseNetworkDestinations(manifest.Constraints.NetworkTargets)
	if err != nil {
		return sandbox.Scope{}, externalPolicyViolationError{Reason: fmt.Sprintf("process-mode network target is invalid: %v", err)}
	}
	if !sandbox.NetworkDestinationsContainAll(scope.Profile.NetworkAllow, requested) {
		return sandbox.Scope{}, externalPolicyViolationError{Reason: "process-mode network targets exceed the sandbox profile network_allow ceiling"}
	}
	scope.Profile.NetworkAllow = requested
	return scope, nil
}

func runExternalProcessCommand(ctx context.Context, scope sandbox.Scope, runner *sandbox.Runner, command string, workdir string, stdin []byte, access ExternalToolExecutionAccess) (string, string, error) {
	if runner != nil && strings.TrimSpace(string(scope.Principal.Role)) != "" {
		res, err := runner.Run(ctx, sandbox.ExecRequest{
			Scope:              scope,
			Command:            command,
			Workdir:            workdir,
			Stdin:              stdin,
			ExtraReadonlyPaths: access.ExtraReadonlyPaths,
			ExtraReadonlyBinds: access.ExtraReadonlyBinds,
			ExtraEnv:           access.ExtraEnv,
		})
		return res.Stdout, res.Stderr, err
	}

	cmd := exec.CommandContext(ctx, "bash", "-lc", command)
	cmd.Dir = workdir
	if len(access.ExtraEnv) > 0 {
		cmd.Env = append([]string(nil), cmd.Environ()...)
		for key, value := range access.ExtraEnv {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	if len(stdin) > 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

type simpleJSONSchema struct {
	Type       string                      `json:"type"`
	Properties map[string]simpleJSONSchema `json:"properties,omitempty"`
	Required   []string                    `json:"required,omitempty"`
	Items      *simpleJSONSchema           `json:"items,omitempty"`
}

func validateExternalToolSchema(schemaRaw json.RawMessage, valueRaw json.RawMessage, label string) error {
	if len(schemaRaw) == 0 {
		return nil
	}
	var schema simpleJSONSchema
	if err := json.Unmarshal(schemaRaw, &schema); err != nil {
		return fmt.Errorf("external tool %s schema decode failed: %w", label, err)
	}
	if strings.TrimSpace(schema.Type) == "" {
		return nil
	}
	var value any
	if err := json.Unmarshal(valueRaw, &value); err != nil {
		return fmt.Errorf("external tool %s payload must be valid json: %w", label, err)
	}
	if err := validateSimpleJSONSchema(schema, value, label); err != nil {
		return err
	}
	return nil
}

func validateSimpleJSONSchema(schema simpleJSONSchema, value any, path string) error {
	switch strings.TrimSpace(schema.Type) {
	case "object":
		obj, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("external tool %s must be an object", path)
		}
		required := make(map[string]struct{}, len(schema.Required))
		for _, name := range schema.Required {
			required[strings.TrimSpace(name)] = struct{}{}
		}
		for name := range required {
			if _, exists := obj[name]; !exists {
				return fmt.Errorf("external tool %s is missing required field %q", path, name)
			}
		}
		for name, child := range schema.Properties {
			v, exists := obj[name]
			if !exists {
				continue
			}
			if err := validateSimpleJSONSchema(child, v, path+"."+name); err != nil {
				return err
			}
		}
		return nil
	case "array":
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("external tool %s must be an array", path)
		}
		if schema.Items == nil {
			return nil
		}
		for i, item := range items {
			if err := validateSimpleJSONSchema(*schema.Items, item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
		return nil
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("external tool %s must be a string", path)
		}
		return nil
	case "integer":
		num, ok := value.(float64)
		if !ok || float64(int64(num)) != num {
			return fmt.Errorf("external tool %s must be an integer", path)
		}
		return nil
	case "number":
		if _, ok := value.(float64); !ok {
			return fmt.Errorf("external tool %s must be a number", path)
		}
		return nil
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("external tool %s must be a boolean", path)
		}
		return nil
	case "null":
		if value != nil {
			return fmt.Errorf("external tool %s must be null", path)
		}
		return nil
	default:
		return nil
	}
}
