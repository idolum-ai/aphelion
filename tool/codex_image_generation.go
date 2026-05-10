//go:build linux

package tool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

const codexImageGenerationToolName = "codex_image_generation"

type codexImageGenerationInput struct {
	Prompt       string `json:"prompt,omitempty"`
	Brief        string `json:"brief,omitempty"`
	Instructions string `json:"instructions,omitempty"`
	OutputFormat string `json:"output_format,omitempty"`
}

func (r *Registry) codexImageGenerationToolDefinition() (agent.ToolDef, bool) {
	if r == nil || r.codexImageGenerationProvider == nil || r.store == nil {
		return agent.ToolDef{}, false
	}
	return agent.ToolDef{
		Name:        codexImageGenerationToolName,
		Description: "Generate one image through the authorized Codex built-in image_generation tool. Returns a local artifact path or an exact blocker. No public Images API fallback.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string","description":"Concrete image prompt or brief to generate."},"brief":{"type":"string","description":"Optional structured image brief; used when prompt is empty."},"instructions":{"type":"string","description":"Optional generation constraints."},"output_format":{"type":"string","enum":["png"],"description":"Output format; currently png."}},"required":["prompt"]}`),
	}, true
}

func (r *Registry) codexImageGenerationAccessAllowed(p principal.Principal) (bool, error) {
	if r == nil || r.store == nil || r.codexImageGenerationProvider == nil {
		return false, nil
	}
	_, ok, err := r.capabilityGrantAllowsAuthorityToolAccess(codexImageGenerationToolName, p)
	return ok, err
}

func (r *Registry) requireCodexImageGenerationAccess(p principal.Principal, key session.SessionKey) (session.CapabilityGrant, session.AuthorityUseRef, error) {
	if r == nil || r.store == nil {
		return session.CapabilityGrant{}, session.AuthorityUseRef{}, fmt.Errorf("%s requires transcript store", codexImageGenerationToolName)
	}
	if r.codexImageGenerationProvider == nil {
		return session.CapabilityGrant{}, session.AuthorityUseRef{}, fmt.Errorf("%s provider is not configured", codexImageGenerationToolName)
	}
	grant, ok, err := r.capabilityGrantAllowsAuthorityToolAccess(codexImageGenerationToolName, p)
	if err != nil {
		return session.CapabilityGrant{}, session.AuthorityUseRef{}, err
	}
	if !ok {
		return session.CapabilityGrant{}, session.AuthorityUseRef{}, fmt.Errorf("tool %q is not granted to principal %q", codexImageGenerationToolName, toolAuthorityPrincipalDisplay(p))
	}
	useRef, err := r.authorityUseRefForGrant(codexImageGenerationToolName, key)
	if err != nil {
		_ = r.recordCodexImageGenerationInvocation(grant, p, useRef, "blocked", err.Error())
		return grant, useRef, err
	}
	return grant, useRef, nil
}

func (r *Registry) codexImageGeneration(ctx context.Context, input json.RawMessage, scope sandbox.Scope, p principal.Principal, key session.SessionKey) (string, error) {
	grant, useRef, err := r.requireCodexImageGenerationAccess(p, key)
	if err != nil {
		return codexImageGenerationBlocker("blocked", err.Error(), grant.GrantID), err
	}
	var in codexImageGenerationInput
	if err := json.Unmarshal(input, &in); err != nil {
		return codexImageGenerationBlocker("blocked", "decode input: "+err.Error(), grant.GrantID), fmt.Errorf("decode codex_image_generation input: %w", err)
	}
	prompt := strings.TrimSpace(firstNonEmpty(in.Prompt, in.Brief))
	if prompt == "" {
		return codexImageGenerationBlocker("blocked", "prompt is required", grant.GrantID), fmt.Errorf("codex_image_generation prompt is required")
	}
	outputFormat := normalizeCodexImageOutputFormat(in.OutputFormat)
	messages := []agent.Message{
		{Role: "system", Content: strings.TrimSpace("You are a bounded image-generation adapter. Use the built-in image_generation tool to produce exactly one image. If generation is unavailable, explain the exact blocker. Do not use fallback image APIs.")},
		{Role: "user", Content: codexImageGenerationPrompt(prompt, in.Instructions)},
	}
	tools := []agent.ToolDef{{Name: "image_generation", Parameters: codexImageGenerationBuiltinParams(outputFormat)}}
	resp, err := r.codexImageGenerationProvider.Complete(ctx, messages, tools)
	if err != nil {
		_ = r.recordCodexImageGenerationInvocation(grant, p, useRef, "failed", err.Error())
		return codexImageGenerationBlocker("blocked", err.Error(), grant.GrantID), err
	}
	if len(resp.Media) == 0 {
		reason := "Codex returned no image_generation media artifact"
		if strings.TrimSpace(resp.Content) != "" {
			reason += ": " + strings.TrimSpace(resp.Content)
		}
		_ = r.recordCodexImageGenerationInvocation(grant, p, useRef, "failed", reason)
		return codexImageGenerationBlocker("blocked", reason, grant.GrantID), fmt.Errorf("%s", reason)
	}
	artifacts, err := materializeCodexImageGenerationMedia(scope, resp.Media)
	if err != nil {
		_ = r.recordCodexImageGenerationInvocation(grant, p, useRef, "failed", err.Error())
		return codexImageGenerationBlocker("blocked", err.Error(), grant.GrantID), err
	}
	_ = r.recordCodexImageGenerationInvocation(grant, p, useRef, "completed", "")
	return renderCodexImageGenerationResult(artifacts, grant.GrantID, resp.Content), nil
}

func codexImageGenerationPrompt(prompt string, instructions string) string {
	if strings.TrimSpace(instructions) == "" {
		return strings.TrimSpace(prompt)
	}
	return strings.TrimSpace(prompt) + "\n\nConstraints:\n" + strings.TrimSpace(instructions)
}

func codexImageGenerationBuiltinParams(outputFormat string) json.RawMessage {
	raw, _ := json.Marshal(map[string]string{"type": "builtin", "output_format": normalizeCodexImageOutputFormat(outputFormat)})
	return raw
}

func normalizeCodexImageOutputFormat(format string) string {
	// Keep the first adapter slice intentionally narrow; Codex source creates the tool with png.
	return "png"
}

type codexImageGenerationArtifact struct {
	ArtifactPath   string `json:"artifact_path"`
	MediaDirective string `json:"media_directive"`
	MimeType       string `json:"mime_type,omitempty"`
	Filename       string `json:"filename,omitempty"`
	SHA256         string `json:"sha256,omitempty"`
}

func materializeCodexImageGenerationMedia(scope sandbox.Scope, media []core.Media) ([]codexImageGenerationArtifact, error) {
	root := strings.TrimSpace(scope.WorkingRoot)
	if root == "" {
		return nil, fmt.Errorf("codex_image_generation requires working root")
	}
	out := make([]codexImageGenerationArtifact, 0, len(media))
	for i, item := range media {
		data := item.Data
		if len(data) == 0 && strings.TrimSpace(item.Path) != "" {
			loaded, err := os.ReadFile(item.Path)
			if err != nil {
				return nil, fmt.Errorf("read generated media %q: %w", item.Path, err)
			}
			data = loaded
		}
		if len(data) == 0 {
			continue
		}
		mimeType := strings.TrimSpace(item.MimeType)
		if mimeType == "" {
			mimeType = http.DetectContentType(data)
		}
		filename := sanitizeCodexImageGenerationFilename(item.Filename)
		if filename == "" {
			filename = fmt.Sprintf("codex-image-generation-%d%s", i+1, codexImageExtension(mimeType))
		}
		if filepath.Ext(filename) == "" {
			filename += codexImageExtension(mimeType)
		}
		path := filepath.Join(root, "generated", "image-generation", filename)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("create image generation artifact dir: %w", err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return nil, fmt.Errorf("write image generation artifact: %w", err)
		}
		sum := sha256.Sum256(data)
		out = append(out, codexImageGenerationArtifact{
			ArtifactPath:   path,
			MediaDirective: fmt.Sprintf(`MEDIA: {"path":%q}`, path),
			MimeType:       mimeType,
			Filename:       filepath.Base(path),
			SHA256:         "sha256:" + hex.EncodeToString(sum[:]),
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("Codex returned no non-empty image media")
	}
	return out, nil
}

func codexImageExtension(mimeType string) string {
	if exts, err := mime.ExtensionsByType(strings.TrimSpace(mimeType)); err == nil && len(exts) > 0 {
		return exts[0]
	}
	return ".png"
}

func sanitizeCodexImageGenerationFilename(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == string(filepath.Separator) {
		return ""
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		}
	}
	return b.String()
}

func renderCodexImageGenerationResult(artifacts []codexImageGenerationArtifact, grantID string, providerText string) string {
	payload := struct {
		Status       string                         `json:"status"`
		GrantID      string                         `json:"grant_id,omitempty"`
		Artifacts    []codexImageGenerationArtifact `json:"artifacts"`
		ProviderText string                         `json:"provider_text,omitempty"`
	}{Status: "completed", GrantID: strings.TrimSpace(grantID), Artifacts: artifacts, ProviderText: strings.TrimSpace(providerText)}
	raw, _ := json.MarshalIndent(payload, "", "  ")
	return string(raw)
}

func codexImageGenerationBlocker(status string, reason string, grantID string) string {
	payload := map[string]string{"status": status, "blocker": strings.TrimSpace(reason)}
	if strings.TrimSpace(grantID) != "" {
		payload["grant_id"] = strings.TrimSpace(grantID)
	}
	raw, _ := json.MarshalIndent(payload, "", "  ")
	return string(raw)
}

func (r *Registry) recordCodexImageGenerationInvocation(grant session.CapabilityGrant, p principal.Principal, ref session.AuthorityUseRef, status string, errText string) error {
	if r == nil || r.store == nil || strings.TrimSpace(grant.GrantID) == "" {
		return nil
	}
	_, err := r.store.RecordCapabilityInvocation(capabilityInvocationWithAuthorityUseRef(session.CapabilityInvocation{
		GrantID:   grant.GrantID,
		Principal: toolAuthorityPrincipalDisplay(p),
		Action:    "invoke",
		Status:    strings.TrimSpace(status),
		ErrorText: strings.TrimSpace(errText),
	}, ref))
	return err
}
