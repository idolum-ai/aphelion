//go:build linux

package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/media"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func (r *Runtime) prepareInboundTurn(ctx context.Context, scope sandbox.Scope, msg core.InboundMessage) (pipeline.TurnPrepareContract, error) {
	prepared := pipeline.TurnPrepareContract{
		UserText:      strings.TrimSpace(msg.Text),
		LedgerText:    strings.TrimSpace(msg.Text),
		MediaAttached: len(msg.Artifacts) > 0,
	}

	for _, raw := range msg.Artifacts {
		artifact := core.NormalizeArtifact(raw)
		if hydrated, err := r.materializeInboundArtifact(ctx, scope, artifact); err != nil {
			return prepared, err
		} else {
			artifact = hydrated
		}
		if strings.TrimSpace(artifact.Scope) == "" {
			artifact.Scope = scopeRootLabel(scope)
		}
		if strings.TrimSpace(artifact.PrincipalID) == "" {
			artifact.PrincipalID = scopePrincipalID(scope)
		}

		ref := core.ArtifactReference{
			ArtifactID:       artifact.ID,
			Kind:             artifact.Kind,
			SourceType:       artifact.SourceType,
			Summary:          summarizeArtifactForFloor(artifact),
			Retention:        firstNonEmpty(strings.TrimSpace(artifact.DefaultRetention), "ephemeral"),
			ProvenanceScope:  artifact.Scope,
			FetchState:       inboundArtifactFetchState(artifact),
			MaterializedPath: strings.TrimSpace(artifact.Path),
		}

		switch artifactHandling(artifact) {
		case "attach_for_vision":
			ref.Handling = "attach_for_vision"
			media, ok := artifactVisionMedia(artifact)
			if ok {
				prepared.AgentMedia = append(prepared.AgentMedia, media)
				prepared.MediaAttached = true
				prepared.MediaMode = "vision"
			}
		case "attach_for_media_analysis":
			ref.Handling = "attach_for_media_analysis"
			media, ok := artifactAnalysisMedia(artifact)
			if ok {
				prepared.AgentMedia = append(prepared.AgentMedia, media)
				prepared.MediaAttached = true
				prepared.MediaMode = mediaAnalysisMode(artifact)
				prepared.UserText = appendTextSection(prepared.UserText, fmt.Sprintf("[%s attached for analysis: %s]", artifactHumanLabel(artifact), firstNonEmpty(strings.TrimSpace(artifact.Filename), strings.TrimSpace(artifact.RemoteID), "media")))
			} else {
				prepared.UserText = appendTextSection(prepared.UserText, metadataNoteForArtifact(artifact))
			}
		case "transcribe":
			prepared.InboundWasVoice = true
			ref.Handling = "transcribe"
			ref.DerivedOutput = "transcript"
			setIfEmpty(&prepared.MediaMode, "transcript")
			transcript, err := r.transcribeAudioArtifact(ctx, scope, artifact)
			if err != nil {
				prepared.UserText = appendTextSection(prepared.UserText, fmt.Sprintf("[%s attached: transcription unavailable: %v]", artifactHumanLabel(artifact), err))
				prepared.LedgerText = appendTextSection(prepared.LedgerText, fmt.Sprintf("[%s attached: transcription unavailable]", artifactHumanLabel(artifact)))
			} else if strings.TrimSpace(transcript) != "" {
				prepared.UserText = appendTextSection(prepared.UserText, strings.TrimSpace(transcript))
				prepared.LedgerText = appendTextSection(prepared.LedgerText, strings.TrimSpace(transcript))
			} else {
				prepared.UserText = appendTextSection(prepared.UserText, fmt.Sprintf("[%s attached: empty transcript]", artifactHumanLabel(artifact)))
				prepared.LedgerText = appendTextSection(prepared.LedgerText, fmt.Sprintf("[%s attached: empty transcript]", artifactHumanLabel(artifact)))
			}
		case "extract_text":
			ref.Handling = "extract_text"
			ref.DerivedOutput = "extracted_text"
			prepared.MediaAttached = true
			setIfEmpty(&prepared.MediaMode, "document_text")
			extracted, err := r.extractArtifactText(ctx, scope, artifact)
			if err != nil {
				prepared.UserText = appendTextSection(prepared.UserText, fmt.Sprintf("[%s attached: text extraction unavailable: %v]", artifactHumanLabel(artifact), err))
			} else if strings.TrimSpace(extracted) != "" {
				prepared.UserText = appendTextSection(prepared.UserText, documentTextSectionForArtifact(artifact, extracted))
			} else {
				prepared.UserText = appendTextSection(prepared.UserText, fmt.Sprintf("[%s attached: no extractable text found]", artifactHumanLabel(artifact)))
			}
		case "inspect_metadata":
			ref.Handling = "inspect_metadata"
			ref.DerivedOutput = "metadata_note"
			setIfEmpty(&prepared.MediaMode, "artifact_reference")
			prepared.UserText = appendTextSection(prepared.UserText, metadataNoteForArtifact(artifact))
		default:
			ref.Handling = "store_reference_only"
			setIfEmpty(&prepared.MediaMode, "artifact_reference")
		}

		ref.DecisionSummary = summarizeArtifactDecision(ref, artifact)
		if decisionInput := artifactDecisionHiddenInput(ref, artifact); decisionInput != nil {
			prepared.ArtifactDecisionInputs = append(prepared.ArtifactDecisionInputs, *decisionInput)
		}
		if artifact.Kind == "audio" {
			prepared.InboundWasVoice = true
		}
		prepared.ArtifactRefs = append(prepared.ArtifactRefs, ref)
	}

	prepared.UserText = strings.TrimSpace(prepared.UserText)
	prepared.LedgerText = summarizeInboundForLedger(prepared.LedgerText, msg.Artifacts)
	currentClaims := r.interpretCurrentTurnClaims(ctx, interpretationRequest{
		Surface:  "inbound_media_instruction",
		Text:     msg.Text,
		HasAudio: inboundMessageHasAudio(msg) || prepared.InboundWasVoice,
		HasMedia: len(msg.Artifacts) > 0,
	})
	applyMediaIntentPolicy("", msg, &prepared, currentClaims)
	return prepared, nil
}

func (r *Runtime) materializeInboundArtifact(ctx context.Context, scope sandbox.Scope, artifact core.Artifact) (core.Artifact, error) {
	artifact = core.NormalizeArtifact(artifact)
	if len(artifact.Data) > 0 || strings.TrimSpace(artifact.Channel) != "telegram" || strings.TrimSpace(artifact.RemoteID) == "" {
		return artifact, nil
	}
	if !r.shouldFetchInboundArtifactNow(artifact) {
		return artifact, nil
	}
	if r == nil || r.inbound == nil {
		return artifact, nil
	}
	maxBytes, err := config.ParseByteSize(r.cfg.Telegram.Media.DownloadMaxSize)
	if err != nil {
		return artifact, err
	}
	data, err := r.inbound.DownloadFileChecked(ctx, artifact.RemoteID, maxBytes)
	if err != nil {
		return artifact, fmt.Errorf("download telegram artifact %s: %w", artifact.RemoteID, err)
	}
	artifact.Data = data
	if artifact.SizeBytes == 0 {
		artifact.SizeBytes = int64(len(data))
	}
	if shouldPersistInboundArtifactLocally(artifact) {
		if path, err := r.persistInboundArtifactBytes(scope, artifact); err != nil {
			return artifact, err
		} else if strings.TrimSpace(path) != "" {
			artifact.Path = path
		}
	}
	return core.NormalizeArtifact(artifact), nil
}

func (r *Runtime) shouldFetchInboundArtifactNow(artifact core.Artifact) bool {
	artifact = core.NormalizeArtifact(artifact)
	switch artifactMediaProcessingChoice(artifact) {
	case "skip":
		return false
	case "analyze":
		if artifact.Kind == "audio" || artifact.Kind == "video" {
			return true
		}
	}
	if hasExplicitArtifactRetentionChoice(artifact) && shouldPersistInboundArtifactLocally(artifact) {
		return true
	}
	switch {
	case artifact.Kind == "audio":
		return true
	case artifact.Kind == "image" && artifact.SourceType == "photo":
		return r != nil && r.cfg.Telegram.Media.AutoVisionPhotos
	case artifact.Kind == "image" && artifact.SourceType == "document":
		return r != nil && r.cfg.Telegram.Media.AutoVisionDocs
	case artifact.Kind == "sticker" && artifact.Subtype == "static_sticker":
		return true
	case artifact.Kind == "document" && artifact.Subtype == "pdf":
		return r != nil && r.cfg.Telegram.Media.ExtractPDFText
	case artifact.Kind == "document" && artifact.Subtype == "text":
		return true
	default:
		return false
	}
}

var inboundArtifactFilenameSanitizer = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func (r *Runtime) persistInboundArtifactBytes(scope sandbox.Scope, artifact core.Artifact) (string, error) {
	if len(artifact.Data) == 0 {
		return "", nil
	}
	root := inboundArtifactRootForArtifact(scope, r.cfg.Agent, artifact)
	if temporaryAudioArtifact(artifact) {
		_, _ = cleanupArtifactFilesOlderThan(root, r.idleExpiry, time.Now().UTC())
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create inbound artifact root: %w", err)
	}
	filename := safeInboundArtifactFilename(artifact)
	path := filepath.Join(root, filename)
	if err := os.WriteFile(path, artifact.Data, 0o600); err != nil {
		return "", fmt.Errorf("write inbound artifact: %w", err)
	}
	return path, nil
}

func inboundArtifactRoot(scope sandbox.Scope, cfg config.AgentConfig) string {
	base := strings.TrimSpace(scope.WorkingRoot)
	if scope.Principal.Role == principal.RoleApprovedUser && strings.TrimSpace(scope.UserMemory) != "" {
		base = scope.UserMemory
	}
	if base == "" {
		base = strings.TrimSpace(cfg.ExecRoot)
	}
	return filepath.Join(base, ".aphelion", "inbound")
}

func inboundArtifactRootForArtifact(scope sandbox.Scope, cfg config.AgentConfig, artifact core.Artifact) string {
	root := inboundArtifactRoot(scope, cfg)
	if temporaryAudioArtifact(core.NormalizeArtifact(artifact)) {
		return filepath.Join(root, "audio-session")
	}
	return root
}

func temporaryAudioArtifact(artifact core.Artifact) bool {
	artifact = core.NormalizeArtifact(artifact)
	return artifact.Kind == "audio" && strings.TrimSpace(artifact.DefaultRetention) == "session_reference"
}

func (r *Runtime) cleanupTemporaryAudioArtifacts(now time.Time) (int, error) {
	if r == nil {
		return 0, nil
	}
	root := filepath.Join(strings.TrimSpace(r.cfg.Agent.ExecRoot), ".aphelion", "inbound", "audio-session")
	return cleanupArtifactFilesOlderThan(root, r.idleExpiry, now)
}

func cleanupArtifactFilesOlderThan(root string, maxAge time.Duration, now time.Time) (int, error) {
	root = strings.TrimSpace(root)
	if root == "" || maxAge <= 0 {
		return 0, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	removed := 0
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if now.Sub(info.ModTime()) <= maxAge {
			return nil
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		removed++
		return nil
	})
	return removed, err
}

func safeInboundArtifactFilename(artifact core.Artifact) string {
	base := firstNonEmpty(strings.TrimSpace(artifact.RemoteID), strings.TrimSpace(artifact.ID), "artifact")
	base = strings.ReplaceAll(base, "/", "-")
	base = strings.ReplaceAll(base, ":", "-")
	base = inboundArtifactFilenameSanitizer.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-.")
	if base == "" {
		base = "artifact"
	}
	name := inboundArtifactFilenameSanitizer.ReplaceAllString(strings.TrimSpace(artifact.Filename), "-")
	name = strings.Trim(name, "-.")
	if name == "" {
		name = defaultInboundArtifactName(artifact)
	}
	return base + "--" + name
}

func defaultInboundArtifactName(artifact core.Artifact) string {
	switch {
	case strings.TrimSpace(artifact.Filename) != "":
		return strings.TrimSpace(artifact.Filename)
	case artifact.Kind == "audio" && artifact.Subtype == "voice_note":
		return "voice.ogg"
	case artifact.Kind == "image":
		return "image"
	case artifact.Kind == "document" && artifact.Subtype == "pdf":
		return "document.pdf"
	case artifact.Kind == "document":
		return "document"
	case artifact.Kind == "sticker":
		return "sticker.webp"
	default:
		return "artifact"
	}
}

func inboundArtifactMaterializeMode(artifact core.Artifact) string {
	if artifact.Metadata == nil {
		return ""
	}
	return strings.TrimSpace(artifact.Metadata["aphelion_materialize"])
}

func shouldPersistInboundArtifactLocally(artifact core.Artifact) bool {
	return !strings.EqualFold(inboundArtifactMaterializeMode(artifact), "memory_only")
}

func hasExplicitArtifactRetentionChoice(artifact core.Artifact) bool {
	if artifact.Metadata == nil {
		return false
	}
	return strings.TrimSpace(artifact.Metadata["aphelion_retention_choice"]) != ""
}

func inboundArtifactFetchState(artifact core.Artifact) string {
	if len(artifact.Data) > 0 && strings.TrimSpace(artifact.Path) != "" {
		return "fetched_local"
	}
	if len(artifact.Data) > 0 {
		return "fetched_memory"
	}
	if strings.TrimSpace(artifact.RemoteID) != "" {
		return "remote_metadata_only"
	}
	return "metadata_only"
}

func summarizeArtifactDecision(ref core.ArtifactReference, artifact core.Artifact) string {
	parts := []string{firstNonEmpty(strings.TrimSpace(ref.Handling), "store_reference_only")}
	if choice := strings.TrimSpace(artifact.Metadata["aphelion_retention_choice"]); choice != "" {
		parts = append(parts, "operator_"+choice)
	}
	if choice := artifactMediaProcessingChoice(artifact); choice != "" {
		parts = append(parts, "media_"+choice)
	}
	if fetch := strings.TrimSpace(ref.FetchState); fetch != "" {
		parts = append(parts, fetch)
	}
	if retention := strings.TrimSpace(ref.Retention); retention != "" {
		parts = append(parts, retention)
	}
	if strings.TrimSpace(artifact.Path) != "" {
		parts = append(parts, "materialized_locally")
	}
	return strings.Join(parts, "; ")
}

func artifactDecisionHiddenInput(ref core.ArtifactReference, artifact core.Artifact) *core.HiddenInput {
	fetchState := strings.TrimSpace(ref.FetchState)
	if fetchState == "" || (fetchState != "fetched_local" && fetchState != "remote_metadata_only") {
		return nil
	}
	summary := firstNonEmpty(strings.TrimSpace(artifact.Filename), strings.TrimSpace(ref.Summary), strings.TrimSpace(ref.ArtifactID), "artifact")
	decision := strings.TrimSpace(ref.DecisionSummary)
	if decision == "" {
		decision = summarizeArtifactDecision(ref, artifact)
	}
	return &core.HiddenInput{
		Category: "artifact_retention_decision",
		Summary:  fmt.Sprintf("inbound artifact %s handled as %s", summary, decision),
	}
}

func (r *Runtime) executionForTurn(prepared pipeline.TurnPrepareContract) pipeline.TurnExecutionContract {
	exec := pipeline.TurnExecutionContract{
		Provider:      r.provider,
		Backend:       strings.TrimSpace(r.governorBackend),
		ProviderName:  r.governorProviderName(),
		ModelName:     r.governorModelName(),
		ProviderPath:  r.configuredGovernorProviderPath(),
		MediaAttached: prepared.MediaAttached,
		MediaMode:     prepared.MediaMode,
	}
	r.applyModelSlotExecution(&exec, core.ModelSlotGovernor)
	if (prepared.MediaMode == "vision" || prepared.MediaMode == "audio_analysis" || prepared.MediaMode == "video_analysis") && r.native != nil {
		exec.Provider = r.native
		exec.Backend = "native"
		exec.ProviderName = r.nativeProviderName()
		exec.ModelName = r.nativeModelName()
		exec.ProviderPath = r.configuredNativeProviderPath()
	}
	return exec
}

func artifactHandling(artifact core.Artifact) string {
	switch {
	case artifactMediaProcessingChoice(artifact) == "skip":
		return "inspect_metadata"
	case artifactMediaProcessingChoice(artifact) == "analyze" && (artifact.Kind == "audio" || artifact.Kind == "video") && len(artifact.Data) > 0:
		return "attach_for_media_analysis"
	case artifact.HasCapability("vision") && len(artifact.Data) > 0:
		return "attach_for_vision"
	case artifact.HasCapability("extract_text") && len(artifact.Data) > 0:
		return "extract_text"
	case artifact.HasCapability("transcribe") && len(artifact.Data) > 0:
		return "transcribe"
	case artifact.HasCapability("inspect_metadata"):
		return "inspect_metadata"
	default:
		return "store_reference_only"
	}
}

func artifactMediaProcessingChoice(artifact core.Artifact) string {
	if artifact.Metadata == nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(artifact.Metadata[core.ArtifactMetadataMediaProcessingChoice])) {
	case "transcribe", "analyze", "agent", "skip":
		return strings.ToLower(strings.TrimSpace(artifact.Metadata[core.ArtifactMetadataMediaProcessingChoice]))
	default:
		return ""
	}
}

func artifactVisionMedia(artifact core.Artifact) (core.Media, bool) {
	if len(artifact.Data) == 0 {
		return core.Media{}, false
	}
	switch artifact.Kind {
	case "image":
		return core.Media{
			Type:     firstNonEmpty(strings.TrimSpace(artifact.SourceType), "image"),
			Data:     artifact.Data,
			MimeType: artifact.MimeType,
			Filename: artifact.Filename,
		}, true
	case "sticker":
		if artifact.Subtype == "static_sticker" {
			return core.Media{
				Type:     "sticker",
				Data:     artifact.Data,
				MimeType: artifact.MimeType,
				Filename: firstNonEmpty(artifact.Filename, "sticker.webp"),
			}, true
		}
	}
	return core.Media{}, false
}

func artifactAnalysisMedia(artifact core.Artifact) (core.Media, bool) {
	if len(artifact.Data) == 0 {
		return core.Media{}, false
	}
	switch artifact.Kind {
	case "audio", "video":
		return core.Media{
			Type:     artifact.Kind,
			Data:     artifact.Data,
			MimeType: artifact.MimeType,
			Filename: artifact.Filename,
		}, true
	default:
		return core.Media{}, false
	}
}

func mediaAnalysisMode(artifact core.Artifact) string {
	switch artifact.Kind {
	case "video":
		return "video_analysis"
	default:
		return "audio_analysis"
	}
}

var newPDFTextExtractor = media.NewPDFTextExtractor

func (r *Runtime) extractArtifactText(ctx context.Context, scope sandbox.Scope, artifact core.Artifact) (string, error) {
	if artifact.Subtype == "pdf" || strings.EqualFold(strings.TrimSpace(artifact.MimeType), "application/pdf") {
		return r.extractPDFText(ctx, scope, core.Media{
			Type:     firstNonEmpty(strings.TrimSpace(artifact.SourceType), "document"),
			Data:     artifact.Data,
			MimeType: artifact.MimeType,
			Filename: artifact.Filename,
		})
	}
	if len(artifact.Data) == 0 {
		return "", fmt.Errorf("document bytes unavailable")
	}
	if !utf8.Valid(artifact.Data) {
		return "", fmt.Errorf("document is not valid utf-8 text")
	}
	return strings.TrimSpace(string(artifact.Data)), nil
}

func (r *Runtime) extractPDFText(ctx context.Context, scope sandbox.Scope, item core.Media) (string, error) {
	if !r.cfg.Telegram.Media.ExtractPDFText {
		return "", fmt.Errorf("pdf extraction disabled")
	}
	maxBytes, err := config.ParseByteSize(r.cfg.Telegram.Media.MaxPDFBytes)
	if err != nil {
		return "", err
	}
	extractor := newPDFTextExtractor()
	extraction, err := extractor.ExtractDocumentText(ctx, &media.DocumentTextExtractionRequest{
		Data:     item.Data,
		MimeType: item.MimeType,
		Filename: item.Filename,
		TempDir:  voiceTempRoot(scope, r.cfg.Agent),
		MaxBytes: maxBytes,
	})
	if err != nil {
		return "", err
	}
	return extraction.Text, nil
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

func summarizeInboundForLedger(text string, artifacts []core.Artifact) string {
	text = strings.TrimSpace(text)
	notes := make([]string, 0, len(artifacts))
	seen := map[string]struct{}{}
	for _, raw := range artifacts {
		marker := ledgerMarkerForArtifact(core.NormalizeArtifact(raw))
		if marker == "" {
			continue
		}
		if _, ok := seen[marker]; ok {
			continue
		}
		seen[marker] = struct{}{}
		notes = append(notes, marker)
	}
	if len(notes) == 0 {
		return text
	}
	sort.Strings(notes)
	return appendTextSection(text, strings.Join(notes, "\n"))
}

func ledgerMarkerForArtifact(artifact core.Artifact) string {
	switch {
	case artifact.Kind == "audio":
		return "[voice attached]"
	case artifact.Kind == "image":
		return "[image attached]"
	case artifact.Kind == "sticker":
		return "[sticker attached]"
	case artifact.Kind == "video":
		return "[video attached]"
	case artifact.Kind == "structured":
		return "[" + firstNonEmpty(artifact.Subtype, artifact.SourceType, "structured") + " attached]"
	case artifact.Kind == "document" && artifact.Subtype == "pdf":
		return "[pdf attached]"
	case artifact.Kind == "document":
		return "[document attached]"
	default:
		return ""
	}
}

func summarizeArtifactForFloor(artifact core.Artifact) string {
	name := firstNonEmpty(strings.TrimSpace(artifact.Filename), artifact.SourceType, artifact.Subtype, artifact.Kind, "artifact")
	switch artifact.Kind {
	case "structured":
		return metadataNoteForArtifact(artifact)
	case "sticker":
		return artifactHumanLabel(artifact) + " " + name
	default:
		return name
	}
}

func documentTextSectionForArtifact(artifact core.Artifact, extracted string) string {
	extracted = strings.TrimSpace(extracted)
	if extracted == "" {
		return ""
	}
	if artifact.Subtype == "pdf" {
		return "[PDF attached]\n\n[DOCUMENT_TEXT]\n" + extracted + "\n[/DOCUMENT_TEXT]"
	}
	name := firstNonEmpty(strings.TrimSpace(artifact.Filename), "document")
	return fmt.Sprintf("[Document attached: %s]\n\n[DOCUMENT_TEXT]\n%s\n[/DOCUMENT_TEXT]", name, extracted)
}

func metadataNoteForArtifact(artifact core.Artifact) string {
	name := firstNonEmpty(strings.TrimSpace(artifact.Filename), artifact.SourceType, artifact.Subtype, artifact.Kind, "artifact")
	if artifactMediaProcessingChoice(artifact) == "skip" {
		return fmt.Sprintf("[%s attached: skipped]", artifactHumanLabel(artifact))
	}
	switch artifact.Kind {
	case "video":
		return fmt.Sprintf("[video attached: %s]", name)
	case "structured":
		switch artifact.Subtype {
		case "location":
			return fmt.Sprintf("[location attached: latitude=%s longitude=%s]", artifact.Metadata["latitude"], artifact.Metadata["longitude"])
		case "venue":
			return fmt.Sprintf("[venue attached: %s, %s]", artifact.Metadata["title"], artifact.Metadata["address"])
		case "contact":
			return fmt.Sprintf("[contact attached: %s %s]", artifact.Metadata["first_name"], artifact.Metadata["last_name"])
		case "poll":
			return fmt.Sprintf("[poll attached: %s]", artifact.Metadata["question"])
		default:
			return fmt.Sprintf("[%s attached]", firstNonEmpty(artifact.Subtype, "structured artifact"))
		}
	case "document":
		return fmt.Sprintf("[document attached: %s]", name)
	case "sticker":
		return fmt.Sprintf("[sticker attached: %s]", name)
	default:
		return fmt.Sprintf("[%s attached: %s]", artifactHumanLabel(artifact), name)
	}
}

func artifactHumanLabel(artifact core.Artifact) string {
	switch {
	case artifact.Kind == "audio" && artifact.Subtype == "voice_note":
		return "voice"
	case artifact.Kind == "audio":
		return "audio"
	case artifact.Kind == "image":
		return "image"
	case artifact.Kind == "video":
		return "video"
	case artifact.Kind == "document" && artifact.Subtype == "pdf":
		return "PDF"
	case artifact.Kind == "document":
		return "document"
	case artifact.Kind == "sticker":
		return "sticker"
	case artifact.Kind == "structured":
		return firstNonEmpty(artifact.Subtype, "structured artifact")
	default:
		return "artifact"
	}
}

func scopeRootLabel(scope sandbox.Scope) string {
	if strings.TrimSpace(scope.UserWorkspace) != "" {
		return "principal"
	}
	return "shared"
}

func scopePrincipalID(scope sandbox.Scope) string {
	if strings.TrimSpace(scope.UserWorkspace) == "" || scope.Principal.TelegramUserID == 0 {
		return ""
	}
	return strconv.FormatInt(scope.Principal.TelegramUserID, 10)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func setIfEmpty(current *string, value string) {
	if current == nil {
		return
	}
	if strings.TrimSpace(*current) != "" || strings.TrimSpace(value) == "" {
		return
	}
	*current = strings.TrimSpace(value)
}
