//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type operationArtifactInput struct {
	Action string `json:"action"`
	Ref    string `json:"ref,omitempty"`
	Label  string `json:"label,omitempty"`
	Latest bool   `json:"latest,omitempty"`
	Type   string `json:"type,omitempty"`
}

func (r *Registry) operationArtifact(_ context.Context, input json.RawMessage, scope sandbox.Scope, key session.SessionKey) (string, error) {
	if r.store == nil {
		return "", fmt.Errorf("operation_artifact requires transcript store")
	}
	if key.ChatID == 0 && key.UserID == 0 && key.Scope.IsZero() {
		return "", fmt.Errorf("operation_artifact requires session context")
	}

	var in operationArtifactInput
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return "", fmt.Errorf("decode operation_artifact input: %w", err)
		}
	}
	action := strings.TrimSpace(in.Action)
	if action == "" {
		action = "list"
	}

	state, err := r.store.OperationState(key)
	if err != nil {
		return "", err
	}
	state = session.NormalizeOperationState(state)

	switch action {
	case "list":
		return renderOperationArtifactList(scope, state.Artifacts), nil
	case "resolve_sendable":
		artifact, path, err := resolveSendableOperationArtifact(scope, state.Artifacts, in)
		if err != nil {
			return "", err
		}
		return renderResolvedOperationArtifact(artifact, path), nil
	default:
		return "", fmt.Errorf("operation_artifact action must be list or resolve_sendable")
	}
}

func renderOperationArtifactList(scope sandbox.Scope, artifacts []session.OperationArtifact) string {
	artifacts = session.NormalizeOperationState(session.OperationState{Artifacts: artifacts}).Artifacts
	var b strings.Builder
	b.WriteString("[OPERATION_ARTIFACTS]\n")
	if len(artifacts) == 0 {
		b.WriteString("artifacts: none")
		return b.String()
	}
	b.WriteString("artifacts:\n")
	for _, artifact := range artifacts {
		label := firstNonEmpty(strings.TrimSpace(artifact.Label), filepath.Base(strings.TrimSpace(artifact.Ref)))
		path, err := resolveOperationArtifactPath(scope, artifact)
		sendable := err == nil && path != ""
		fmt.Fprintf(&b, "- label: %s\n", label)
		fmt.Fprintf(&b, "  ref: %s\n", strings.TrimSpace(artifact.Ref))
		fmt.Fprintf(&b, "  sendable: %t\n", sendable)
		if sendable {
			fmt.Fprintf(&b, "  path: %s\n", path)
		} else if err != nil {
			fmt.Fprintf(&b, "  reason: %s\n", err.Error())
		}
	}
	return strings.TrimSpace(b.String())
}

func resolveSendableOperationArtifact(scope sandbox.Scope, artifacts []session.OperationArtifact, in operationArtifactInput) (session.OperationArtifact, string, error) {
	artifacts = session.NormalizeOperationState(session.OperationState{Artifacts: artifacts}).Artifacts
	if len(artifacts) == 0 {
		return session.OperationArtifact{}, "", fmt.Errorf("operation_artifact has no artifacts for this session")
	}
	wantPDF := strings.EqualFold(strings.TrimSpace(in.Type), "pdf")
	if artifactType := strings.ToLower(strings.TrimSpace(in.Type)); artifactType != "" && artifactType != "any" && artifactType != "pdf" {
		return session.OperationArtifact{}, "", fmt.Errorf("operation_artifact type must be any or pdf")
	}
	ref := strings.TrimSpace(in.Ref)
	label := strings.ToLower(strings.TrimSpace(in.Label))
	if ref == "" && label == "" && !in.Latest {
		return session.OperationArtifact{}, "", fmt.Errorf("operation_artifact resolve_sendable requires ref, label, or latest=true")
	}
	var lastErr error
	for i := len(artifacts) - 1; i >= 0; i-- {
		artifact := artifacts[i]
		if ref != "" && strings.TrimSpace(artifact.Ref) != ref {
			continue
		}
		if label != "" && !strings.Contains(strings.ToLower(strings.TrimSpace(artifact.Label)), label) {
			continue
		}
		if wantPDF && !operationArtifactToolLooksLikePDF(artifact) {
			continue
		}
		path, err := resolveOperationArtifactPath(scope, artifact)
		if err != nil {
			lastErr = err
			continue
		}
		return artifact, path, nil
	}
	if lastErr != nil {
		return session.OperationArtifact{}, "", lastErr
	}
	return session.OperationArtifact{}, "", fmt.Errorf("operation_artifact could not find a matching sendable artifact")
}

func resolveOperationArtifactPath(scope sandbox.Scope, artifact session.OperationArtifact) (string, error) {
	ref := strings.TrimSpace(artifact.Ref)
	if ref == "" {
		return "", fmt.Errorf("artifact ref is empty")
	}
	path, err := resolveNativeToolPath(scope, ref, nativePathRead)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat artifact %q: %w", ref, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("artifact %q is a directory", ref)
	}
	return path, nil
}

func renderResolvedOperationArtifact(artifact session.OperationArtifact, path string) string {
	mediaDirective, _ := json.Marshal(map[string]string{"path": path})
	label := firstNonEmpty(strings.TrimSpace(artifact.Label), filepath.Base(path))
	var b strings.Builder
	b.WriteString("[OPERATION_ARTIFACT]\n")
	fmt.Fprintf(&b, "label: %s\n", label)
	fmt.Fprintf(&b, "ref: %s\n", strings.TrimSpace(artifact.Ref))
	fmt.Fprintf(&b, "path: %s\n", path)
	fmt.Fprintf(&b, "media_directive: MEDIA: %s\n", mediaDirective)
	b.WriteString("delivery_note: Include the MEDIA directive in the final reply only if the user explicitly asked to receive this artifact.")
	return b.String()
}

func operationArtifactToolLooksLikePDF(artifact session.OperationArtifact) bool {
	joined := strings.ToLower(strings.TrimSpace(artifact.Label) + " " + strings.TrimSpace(artifact.Ref))
	return strings.Contains(joined, ".pdf") || strings.Contains(joined, "pdf")
}
