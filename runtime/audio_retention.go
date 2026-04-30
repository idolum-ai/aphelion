//go:build linux

package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

// KeepAudioArtifactsPermanently materializes audio artifacts from an already
// accepted inbound Telegram message into the shared artifact root without
// re-running the conversational turn.
func (r *Runtime) KeepAudioArtifactsPermanently(ctx context.Context, msg core.InboundMessage) error {
	if r == nil {
		return fmt.Errorf("runtime is nil")
	}
	if r.store == nil {
		return fmt.Errorf("session store is nil")
	}
	actor := principal.Principal{}
	if r.resolver != nil && msg.SenderID != 0 {
		if resolved, ok := r.resolver.ResolveTelegramUser(msg.SenderID); ok {
			actor = resolved
		}
	}
	if actor.TelegramUserID == 0 && strings.TrimSpace(actor.DurableAgentID) == "" {
		return ErrPrincipalDenied
	}
	scope, err := r.scopeForPrincipal(actor)
	if err != nil {
		return fmt.Errorf("resolve principal scope: %w", err)
	}

	refs := make([]core.ArtifactReference, 0, len(msg.Artifacts))
	for _, raw := range msg.Artifacts {
		artifact := core.NormalizeArtifact(raw)
		if artifact.Kind != "audio" {
			continue
		}
		path, hydrated, err := r.persistPermanentAudioArtifact(ctx, scope, msg, artifact)
		if err != nil {
			return err
		}
		refs = append(refs, core.ArtifactReference{
			ArtifactID:       hydrated.ID,
			Kind:             hydrated.Kind,
			SourceType:       hydrated.SourceType,
			Summary:          summarizeArtifactForFloor(hydrated),
			Handling:         "store_reference_only",
			Retention:        "child_local",
			ProvenanceScope:  firstNonEmpty(strings.TrimSpace(hydrated.Scope), scopeRootLabel(scope)),
			FetchState:       "fetched_local",
			MaterializedPath: path,
			DecisionSummary:  "operator_permanent; fetched_local; child_local; materialized_locally",
		})
	}
	if len(refs) == 0 {
		return fmt.Errorf("no audio artifacts to keep")
	}

	key := session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramDMScopeRef(msg.ChatID)}
	sess, err := r.store.Load(key)
	if err != nil {
		return fmt.Errorf("load session for audio retention: %w", err)
	}
	sess.LastFloorMetadata = encodeFloorMetadata(core.FloorMetadata{Artifacts: refs})
	if err := r.store.Save(sess, nil, core.TokenUsage{}); err != nil {
		return fmt.Errorf("save audio retention metadata: %w", err)
	}
	return nil
}

func (r *Runtime) persistPermanentAudioArtifact(ctx context.Context, scope sandbox.Scope, msg core.InboundMessage, artifact core.Artifact) (string, core.Artifact, error) {
	artifact = core.NormalizeArtifact(artifact)
	if len(artifact.Data) == 0 {
		if strings.TrimSpace(artifact.RemoteID) == "" {
			return "", artifact, fmt.Errorf("audio bytes unavailable")
		}
		if r.inbound == nil {
			return "", artifact, fmt.Errorf("inbound artifact fetcher unavailable")
		}
		maxBytes, err := config.ParseByteSize(r.cfg.Telegram.Media.DownloadMaxSize)
		if err != nil {
			return "", artifact, err
		}
		data, err := r.inbound.DownloadFileChecked(ctx, artifact.RemoteID, maxBytes)
		if err != nil {
			return "", artifact, fmt.Errorf("download telegram audio artifact %s: %w", artifact.RemoteID, err)
		}
		artifact.Data = data
	}
	if artifact.SizeBytes == 0 {
		artifact.SizeBytes = int64(len(artifact.Data))
	}
	if strings.TrimSpace(artifact.Scope) == "" {
		artifact.Scope = scopeRootLabel(scope)
	}
	if strings.TrimSpace(artifact.PrincipalID) == "" {
		artifact.PrincipalID = scopePrincipalID(scope)
	}

	root := permanentAudioArtifactRoot(scope, r.cfg.Agent)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", artifact, fmt.Errorf("create permanent audio artifact root: %w", err)
	}
	filename := permanentAudioArtifactFilename(msg, artifact, time.Now().UTC())
	path := filepath.Join(root, filename)
	if err := os.WriteFile(path, artifact.Data, 0o600); err != nil {
		return "", artifact, fmt.Errorf("write permanent audio artifact: %w", err)
	}
	artifact.Path = path
	artifact.DefaultRetention = "child_local"
	artifact.RetentionCeiling = "child_local"
	if artifact.Metadata == nil {
		artifact.Metadata = map[string]string{}
	}
	artifact.Metadata["aphelion_retention_choice"] = "permanent"
	artifact.Metadata["aphelion_materialize"] = "local"
	return path, core.NormalizeArtifact(artifact), nil
}

func permanentAudioArtifactRoot(scope sandbox.Scope, cfg config.AgentConfig) string {
	base := strings.TrimSpace(scope.SharedMemoryRoot)
	if base == "" {
		base = strings.TrimSpace(scope.WorkingRoot)
	}
	if base == "" {
		base = strings.TrimSpace(cfg.SharedMemoryRoot)
	}
	if base == "" {
		base = strings.TrimSpace(cfg.ExecRoot)
	}
	return filepath.Join(base, "artifacts", "audio")
}

func permanentAudioArtifactFilename(msg core.InboundMessage, artifact core.Artifact, now time.Time) string {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	stem := safeInboundArtifactFilename(artifact)
	if stem == "" {
		stem = firstNonEmpty(strings.TrimSpace(artifactHumanLabel(artifact)), "audio")
	}
	return fmt.Sprintf("%s--m%d--%s", now.UTC().Format("20060102T150405Z"), msg.MessageID, stem)
}
