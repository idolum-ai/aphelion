//go:build linux

package runtime

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type preparedInboundTurn struct {
	UserText        string
	LedgerText      string
	AgentMedia      []core.Media
	InboundWasVoice bool
	MediaAttached   bool
	MediaMode       string
}

func (r *Runtime) prepareInboundTurn(ctx context.Context, scope sandbox.Scope, msg core.InboundMessage) (preparedInboundTurn, error) {
	userText, inboundWasVoice, err := r.transcribeVoiceIfNeeded(ctx, scope, msg)
	if err != nil {
		return preparedInboundTurn{}, err
	}

	prepared := preparedInboundTurn{
		UserText:        strings.TrimSpace(userText),
		LedgerText:      strings.TrimSpace(userText),
		InboundWasVoice: inboundWasVoice,
	}

	imageMedia := supportedImageMedia(msg.Media)
	if len(imageMedia) > 0 {
		prepared.AgentMedia = append(prepared.AgentMedia, imageMedia...)
		prepared.MediaAttached = true
		prepared.MediaMode = "vision"
	}

	if pdfMedia, ok := firstPDFMedia(msg.Media); ok {
		prepared.MediaAttached = true
		if prepared.MediaMode == "" {
			prepared.MediaMode = "document_text"
		}
		extracted, err := r.extractPDFText(ctx, scope, pdfMedia)
		if err != nil {
			prepared.UserText = appendTextSection(prepared.UserText, fmt.Sprintf("[PDF attached: text extraction unavailable: %v]", err))
		} else if strings.TrimSpace(extracted) != "" {
			prepared.UserText = appendTextSection(prepared.UserText, "[PDF attached]")
			prepared.UserText = appendTextSection(prepared.UserText, "[DOCUMENT_TEXT]\n"+strings.TrimSpace(extracted)+"\n[/DOCUMENT_TEXT]")
		} else {
			prepared.UserText = appendTextSection(prepared.UserText, "[PDF attached: no extractable text found]")
		}
	}

	prepared.UserText = strings.TrimSpace(prepared.UserText)
	prepared.LedgerText = summarizeInboundForLedger(prepared.LedgerText, msg.Media)
	return prepared, nil
}

func (r *Runtime) executionForTurn(prepared preparedInboundTurn) governorExecution {
	exec := governorExecution{
		Provider:      r.provider,
		Backend:       strings.TrimSpace(r.governorBackend),
		ProviderName:  r.governorProviderName(),
		ModelName:     r.governorModelName(),
		ProviderPath:  r.configuredGovernorProviderPath(),
		MediaAttached: prepared.MediaAttached,
		MediaMode:     prepared.MediaMode,
	}
	if prepared.MediaMode == "vision" && r.native != nil {
		exec.Provider = r.native
		exec.Backend = "native"
		exec.ProviderName = r.nativeProviderName()
		exec.ModelName = r.nativeModelName()
		exec.ProviderPath = r.configuredNativeProviderPath()
	}
	return exec
}

func supportedImageMedia(items []core.Media) []core.Media {
	out := make([]core.Media, 0, len(items))
	for _, item := range items {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(item.MimeType)), "image/") && len(item.Data) > 0 {
			out = append(out, item)
		}
	}
	return out
}

func firstPDFMedia(items []core.Media) (core.Media, bool) {
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item.MimeType), "application/pdf") && len(item.Data) > 0 {
			return item, true
		}
	}
	return core.Media{}, false
}

func (r *Runtime) extractPDFText(ctx context.Context, scope sandbox.Scope, media core.Media) (string, error) {
	if !r.cfg.Telegram.Media.ExtractPDFText {
		return "", fmt.Errorf("pdf extraction disabled")
	}
	maxBytes, err := config.ParseByteSize(r.cfg.Telegram.Media.MaxPDFBytes)
	if err != nil {
		return "", err
	}
	if maxBytes > 0 && int64(len(media.Data)) > maxBytes {
		return "", fmt.Errorf("pdf exceeds configured size limit")
	}

	tmpRoot := voiceTempRoot(scope, r.cfg.Agent)
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		return "", fmt.Errorf("create pdf temp root: %w", err)
	}
	tmp, err := os.CreateTemp(tmpRoot, "aphelion-doc-*.pdf")
	if err != nil {
		return "", fmt.Errorf("create temp pdf: %w", err)
	}
	path := filepath.Clean(tmp.Name())
	defer os.Remove(path)
	if _, err := tmp.Write(media.Data); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write temp pdf: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close temp pdf: %w", err)
	}

	cmd := exec.CommandContext(ctx, "pdftotext", "-layout", "-nopgbrk", path, "-")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errText := strings.TrimSpace(stderr.String())
		if errText != "" {
			return "", fmt.Errorf("%s", errText)
		}
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

func appendTextSection(base string, addition string) string {
	base = strings.TrimSpace(base)
	addition = strings.TrimSpace(addition)
	if addition == "" {
		return base
	}
	if base == "" {
		return addition
	}
	return base + "\n\n" + addition
}

func summarizeInboundForLedger(text string, media []core.Media) string {
	text = strings.TrimSpace(text)
	notes := make([]string, 0, len(media))
	for _, item := range media {
		switch {
		case item.Type == "voice":
			notes = append(notes, "[voice attached]")
		case strings.EqualFold(strings.TrimSpace(item.MimeType), "application/pdf"):
			notes = append(notes, "[pdf attached]")
		case strings.HasPrefix(strings.ToLower(strings.TrimSpace(item.MimeType)), "image/"):
			notes = append(notes, "[image attached]")
		}
	}
	if len(notes) == 0 {
		return text
	}
	return appendTextSection(text, strings.Join(notes, "\n"))
}
