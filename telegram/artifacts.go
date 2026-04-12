//go:build linux

package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
)

func hasNormalizableArtifacts(msg *Message) bool {
	return msg != nil && (msg.Voice != nil ||
		msg.Audio != nil ||
		len(msg.Photo) > 0 ||
		msg.Document != nil ||
		msg.Video != nil ||
		msg.VideoNote != nil ||
		msg.Animation != nil ||
		msg.Sticker != nil ||
		msg.Contact != nil ||
		msg.Location != nil ||
		msg.Venue != nil ||
		msg.Poll != nil)
}

func (p *Poller) normalizeArtifacts(ctx context.Context, msg *Message) ([]core.Artifact, error) {
	if msg == nil {
		return nil, nil
	}
	maxBytes, _ := config.ParseByteSize(p.media.DownloadMaxSize)
	artifacts := make([]core.Artifact, 0, 8)

	if voice := msg.Voice; voice != nil {
		data, size, err := p.downloadArtifactData(ctx, voice.FileID, maxBytes)
		if err != nil {
			return nil, fmt.Errorf("download telegram voice %s: %w", voice.FileID, err)
		}
		artifact := core.Artifact{
			ID:         "telegram:voice:" + voice.FileID,
			Channel:    "telegram",
			SourceType: "voice",
			Kind:       "audio",
			Subtype:    "voice_note",
			Data:       data,
			MimeType:   strings.TrimSpace(voice.MimeType),
			Filename:   "voice.ogg",
			SizeBytes:  firstPositive(size, voice.FileSize),
			Caption:    strings.TrimSpace(msg.Caption),
		}
		if len(data) == 0 {
			artifact.Capabilities = []string{"inspect_metadata", "store_reference"}
		}
		artifacts = append(artifacts, core.NormalizeArtifact(artifact))
	}

	if audio := msg.Audio; audio != nil {
		data, size, err := p.downloadArtifactData(ctx, audio.FileID, maxBytes)
		if err != nil {
			return nil, fmt.Errorf("download telegram audio %s: %w", audio.FileID, err)
		}
		artifact := core.Artifact{
			ID:         "telegram:audio:" + audio.FileID,
			Channel:    "telegram",
			SourceType: "audio",
			Kind:       "audio",
			Data:       data,
			MimeType:   strings.TrimSpace(audio.MimeType),
			Filename:   strings.TrimSpace(audio.FileName),
			SizeBytes:  firstPositive(size, audio.FileSize),
			Caption:    strings.TrimSpace(msg.Caption),
		}
		if len(data) == 0 {
			artifact.Capabilities = []string{"inspect_metadata", "store_reference"}
		}
		artifacts = append(artifacts, core.NormalizeArtifact(artifact))
	}

	if len(msg.Photo) > 0 {
		largest := msg.Photo[len(msg.Photo)-1]
		artifact := core.Artifact{
			ID:         "telegram:photo:" + largest.FileID,
			Channel:    "telegram",
			SourceType: "photo",
			Kind:       "image",
			MimeType:   "image/jpeg",
			Filename:   "photo.jpg",
			SizeBytes:  largest.FileSize,
			Caption:    strings.TrimSpace(msg.Caption),
		}
		if p.media.AutoVisionPhotos {
			data, size, err := p.downloadArtifactData(ctx, largest.FileID, maxBytes)
			if err != nil {
				return nil, fmt.Errorf("download telegram photo %s: %w", largest.FileID, err)
			}
			artifact.Data = data
			artifact.SizeBytes = firstPositive(size, largest.FileSize)
			if len(data) == 0 {
				artifact.Capabilities = []string{"inspect_metadata", "store_reference"}
			}
		} else {
			artifact.Capabilities = []string{"inspect_metadata", "store_reference"}
		}
		artifacts = append(artifacts, core.NormalizeArtifact(artifact))
	}

	if doc := msg.Document; doc != nil {
		artifact, err := p.normalizeDocumentArtifact(ctx, msg, doc, maxBytes)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, artifact)
	}

	if video := msg.Video; video != nil {
		artifacts = append(artifacts, core.NormalizeArtifact(core.Artifact{
			ID:         "telegram:video:" + video.FileID,
			Channel:    "telegram",
			SourceType: "video",
			Kind:       "video",
			MimeType:   strings.TrimSpace(video.MimeType),
			Filename:   strings.TrimSpace(video.FileName),
			SizeBytes:  video.FileSize,
			Caption:    strings.TrimSpace(msg.Caption),
			Metadata: map[string]string{
				"width":    strconv.Itoa(video.Width),
				"height":   strconv.Itoa(video.Height),
				"duration": strconv.Itoa(video.Duration),
			},
			Capabilities: []string{"inspect_metadata", "store_reference"},
		}))
	}

	if note := msg.VideoNote; note != nil {
		artifacts = append(artifacts, core.NormalizeArtifact(core.Artifact{
			ID:         "telegram:video_note:" + note.FileID,
			Channel:    "telegram",
			SourceType: "video_note",
			Kind:       "video",
			Subtype:    "video_note",
			SizeBytes:  note.FileSize,
			Metadata: map[string]string{
				"length":   strconv.Itoa(note.Length),
				"duration": strconv.Itoa(note.Duration),
			},
			Capabilities: []string{"inspect_metadata", "store_reference"},
		}))
	}

	if animation := msg.Animation; animation != nil {
		artifacts = append(artifacts, core.NormalizeArtifact(core.Artifact{
			ID:         "telegram:animation:" + animation.FileID,
			Channel:    "telegram",
			SourceType: "animation",
			Kind:       "video",
			Subtype:    "animation",
			MimeType:   strings.TrimSpace(animation.MimeType),
			Filename:   strings.TrimSpace(animation.FileName),
			SizeBytes:  animation.FileSize,
			Caption:    strings.TrimSpace(msg.Caption),
			Metadata: map[string]string{
				"width":    strconv.Itoa(animation.Width),
				"height":   strconv.Itoa(animation.Height),
				"duration": strconv.Itoa(animation.Duration),
			},
			Capabilities: []string{"inspect_metadata", "store_reference"},
		}))
	}

	if sticker := msg.Sticker; sticker != nil {
		artifact := core.Artifact{
			ID:         "telegram:sticker:" + sticker.FileID,
			Channel:    "telegram",
			SourceType: "sticker",
			Kind:       "sticker",
			MimeType:   strings.TrimSpace(sticker.MimeType),
			SizeBytes:  sticker.FileSize,
			Metadata: map[string]string{
				"emoji":                 strings.TrimSpace(sticker.Emoji),
				"set_name":              strings.TrimSpace(sticker.SetName),
				"is_animated":           strconv.FormatBool(sticker.IsAnimated),
				"is_video":              strconv.FormatBool(sticker.IsVideo),
				"telegram_sticker_type": strings.TrimSpace(sticker.Type),
				"width":                 strconv.Itoa(sticker.Width),
				"height":                strconv.Itoa(sticker.Height),
			},
		}
		if !sticker.IsAnimated && !sticker.IsVideo {
			data, size, err := p.downloadArtifactData(ctx, sticker.FileID, maxBytes)
			if err != nil {
				return nil, fmt.Errorf("download telegram sticker %s: %w", sticker.FileID, err)
			}
			artifact.Data = data
			artifact.SizeBytes = firstPositive(size, sticker.FileSize)
			if len(data) == 0 {
				artifact.Capabilities = []string{"inspect_metadata", "store_reference"}
			}
		} else {
			artifact.Capabilities = []string{"inspect_metadata", "store_reference"}
		}
		artifacts = append(artifacts, core.NormalizeArtifact(artifact))
	}

	if contact := msg.Contact; contact != nil {
		artifacts = append(artifacts, core.NormalizeArtifact(core.Artifact{
			ID:         "telegram:contact:" + strings.TrimSpace(contact.PhoneNumber),
			Channel:    "telegram",
			SourceType: "contact",
			Kind:       "structured",
			Subtype:    "contact",
			Metadata: map[string]string{
				"phone_number": strings.TrimSpace(contact.PhoneNumber),
				"first_name":   strings.TrimSpace(contact.FirstName),
				"last_name":    strings.TrimSpace(contact.LastName),
				"user_id":      strconv.FormatInt(contact.UserID, 10),
				"vcard":        strings.TrimSpace(contact.VCard),
			},
			Capabilities: []string{"inspect_metadata", "store_reference"},
		}))
	}

	if location := msg.Location; location != nil {
		artifacts = append(artifacts, core.NormalizeArtifact(core.Artifact{
			ID:         "telegram:location",
			Channel:    "telegram",
			SourceType: "location",
			Kind:       "structured",
			Subtype:    "location",
			Metadata: map[string]string{
				"latitude":  strconv.FormatFloat(location.Latitude, 'f', 6, 64),
				"longitude": strconv.FormatFloat(location.Longitude, 'f', 6, 64),
			},
			Capabilities: []string{"inspect_metadata", "store_reference"},
		}))
	}

	if venue := msg.Venue; venue != nil {
		metadata := map[string]string{
			"title":         strings.TrimSpace(venue.Title),
			"address":       strings.TrimSpace(venue.Address),
			"foursquare_id": strings.TrimSpace(venue.FoursquareID),
		}
		if venue.Location != nil {
			metadata["latitude"] = strconv.FormatFloat(venue.Location.Latitude, 'f', 6, 64)
			metadata["longitude"] = strconv.FormatFloat(venue.Location.Longitude, 'f', 6, 64)
		}
		artifacts = append(artifacts, core.NormalizeArtifact(core.Artifact{
			ID:           "telegram:venue:" + strings.TrimSpace(venue.Title),
			Channel:      "telegram",
			SourceType:   "venue",
			Kind:         "structured",
			Subtype:      "venue",
			Metadata:     metadata,
			Capabilities: []string{"inspect_metadata", "store_reference"},
		}))
	}

	if poll := msg.Poll; poll != nil {
		artifacts = append(artifacts, core.NormalizeArtifact(core.Artifact{
			ID:         "telegram:poll:" + strings.TrimSpace(poll.ID),
			Channel:    "telegram",
			SourceType: "poll",
			Kind:       "structured",
			Subtype:    "poll",
			Metadata: map[string]string{
				"question": strings.TrimSpace(poll.Question),
				"type":     strings.TrimSpace(poll.Type),
			},
			Capabilities: []string{"inspect_metadata", "store_reference"},
		}))
	}

	return artifacts, nil
}

func (p *Poller) normalizeDocumentArtifact(ctx context.Context, msg *Message, doc *Document, maxBytes int64) (core.Artifact, error) {
	filename := strings.TrimSpace(doc.FileName)
	artifact := core.Artifact{
		ID:         "telegram:document:" + doc.FileID,
		Channel:    "telegram",
		SourceType: "document",
		MimeType:   strings.TrimSpace(doc.MimeType),
		Filename:   filename,
		SizeBytes:  doc.FileSize,
		Caption:    strings.TrimSpace(msg.Caption),
	}
	artifact = core.NormalizeArtifact(artifact)

	shouldDownload := false
	switch {
	case artifact.Kind == "image":
		shouldDownload = p.media.AutoVisionDocs
		if !shouldDownload {
			artifact.Capabilities = []string{"inspect_metadata", "store_reference"}
		}
	case artifact.Kind == "document" && artifact.Subtype == "pdf":
		shouldDownload = p.media.ExtractPDFText
		if !shouldDownload {
			artifact.Capabilities = []string{"inspect_metadata", "store_reference"}
		}
	case artifact.Kind == "document" && artifact.Subtype == "text":
		shouldDownload = true
	default:
		artifact.Capabilities = []string{"inspect_metadata", "store_reference"}
	}

	if shouldDownload {
		data, size, err := p.downloadArtifactData(ctx, doc.FileID, maxBytes)
		if err != nil {
			return core.Artifact{}, fmt.Errorf("download telegram document %s: %w", doc.FileID, err)
		}
		artifact.Data = data
		artifact.SizeBytes = firstPositive(size, doc.FileSize)
		if len(data) == 0 {
			artifact.Capabilities = []string{"inspect_metadata", "store_reference"}
		}
	}

	return core.NormalizeArtifact(artifact), nil
}

func (p *Poller) downloadArtifactData(ctx context.Context, fileID string, maxBytes int64) ([]byte, int64, error) {
	if p == nil || p.client == nil || strings.TrimSpace(fileID) == "" {
		return nil, 0, nil
	}
	info, err := p.client.GetFileInfo(ctx, fileID)
	if err != nil {
		return nil, 0, err
	}
	if maxBytes > 0 && info.Size > 0 && info.Size > maxBytes {
		return nil, info.Size, nil
	}
	data, err := p.client.DownloadFileChecked(ctx, fileID, maxBytes)
	if err != nil {
		return nil, info.Size, err
	}
	size := info.Size
	if size == 0 {
		size = int64(len(data))
	}
	return data, size, nil
}

func firstPositive(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
