//go:build linux

package telegram

import (
	"context"
	"strconv"
	"strings"

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

func (p *Poller) normalizeArtifacts(_ context.Context, msg *Message) ([]core.Artifact, error) {
	if msg == nil {
		return nil, nil
	}
	artifacts := make([]core.Artifact, 0, 8)

	if voice := msg.Voice; voice != nil {
		artifacts = append(artifacts, core.NormalizeArtifact(core.Artifact{
			ID:         "telegram:voice:" + voice.FileID,
			Channel:    "telegram",
			RemoteID:   strings.TrimSpace(voice.FileID),
			SourceType: "voice",
			Kind:       "audio",
			Subtype:    "voice_note",
			MimeType:   strings.TrimSpace(voice.MimeType),
			Filename:   "voice.ogg",
			SizeBytes:  voice.FileSize,
			Caption:    strings.TrimSpace(msg.Caption),
		}))
	}

	if audio := msg.Audio; audio != nil {
		artifacts = append(artifacts, core.NormalizeArtifact(core.Artifact{
			ID:         "telegram:audio:" + audio.FileID,
			Channel:    "telegram",
			RemoteID:   strings.TrimSpace(audio.FileID),
			SourceType: "audio",
			Kind:       "audio",
			MimeType:   strings.TrimSpace(audio.MimeType),
			Filename:   strings.TrimSpace(audio.FileName),
			SizeBytes:  audio.FileSize,
			Caption:    strings.TrimSpace(msg.Caption),
		}))
	}

	if len(msg.Photo) > 0 {
		largest := msg.Photo[len(msg.Photo)-1]
		artifacts = append(artifacts, core.NormalizeArtifact(core.Artifact{
			ID:         "telegram:photo:" + largest.FileID,
			Channel:    "telegram",
			RemoteID:   strings.TrimSpace(largest.FileID),
			SourceType: "photo",
			Kind:       "image",
			MimeType:   "image/jpeg",
			Filename:   "photo.jpg",
			SizeBytes:  largest.FileSize,
			Caption:    strings.TrimSpace(msg.Caption),
		}))
	}

	if doc := msg.Document; doc != nil {
		artifacts = append(artifacts, p.normalizeDocumentArtifact(msg, doc))
	}

	if video := msg.Video; video != nil {
		artifacts = append(artifacts, core.NormalizeArtifact(core.Artifact{
			ID:         "telegram:video:" + video.FileID,
			Channel:    "telegram",
			RemoteID:   strings.TrimSpace(video.FileID),
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
			RemoteID:   strings.TrimSpace(note.FileID),
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
			RemoteID:   strings.TrimSpace(animation.FileID),
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
			RemoteID:   strings.TrimSpace(sticker.FileID),
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
		if sticker.IsAnimated || sticker.IsVideo {
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

func (p *Poller) normalizeDocumentArtifact(msg *Message, doc *Document) core.Artifact {
	filename := strings.TrimSpace(doc.FileName)
	artifact := core.Artifact{
		ID:         "telegram:document:" + doc.FileID,
		Channel:    "telegram",
		RemoteID:   strings.TrimSpace(doc.FileID),
		SourceType: "document",
		MimeType:   strings.TrimSpace(doc.MimeType),
		Filename:   filename,
		SizeBytes:  doc.FileSize,
		Caption:    strings.TrimSpace(msg.Caption),
	}
	return core.NormalizeArtifact(artifact)
}
