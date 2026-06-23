//go:build linux

package session

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

type ExposurePurpose string
type ExposureProjectionKind string

const (
	ExposurePurposeToolResultModelContext  ExposurePurpose = "tool_result_model_context"
	ExposurePurposeToolResultPreview       ExposurePurpose = "tool_result_preview"
	ExposurePurposeToolFailureModelContext ExposurePurpose = "tool_failure_model_context"
	ExposurePurposeToolFailurePreview      ExposurePurpose = "tool_failure_preview"
)

const (
	ExposureProjectionPlain        ExposureProjectionKind = "plain"
	ExposureProjectionRedacted     ExposureProjectionKind = "redacted"
	ExposureProjectionDigest       ExposureProjectionKind = "digest"
	ExposureProjectionWithheld     ExposureProjectionKind = "withheld"
	ExposureProjectionProtectedRef ExposureProjectionKind = "protected_ref"
)

const ExposureProjectionPolicyToolOutputV1 = "session.exposure_projection.tool_output/v1"

type ExposureProjectionInput struct {
	ProjectionID           string
	Key                    SessionKey
	TurnRunID              int64
	InvocationID           string
	ToolName               string
	Audience               ExposureAudience
	Purpose                ExposurePurpose
	SourceKind             string
	SourceRef              string
	RawText                string
	ForceProtectedEvidence bool
	CreatedAt              time.Time
}

type ExposureProjectionRecord struct {
	ProjectionID          string
	SessionID             string
	ChatID                int64
	UserID                int64
	Scope                 ScopeRef
	TurnRunID             int64
	InvocationID          string
	ToolName              string
	Audience              ExposureAudience
	Purpose               ExposurePurpose
	PolicyRef             string
	ProjectionKind        ExposureProjectionKind
	Sensitivity           string
	SensitivityProvenance []string
	SourceKind            string
	SourceRef             string
	SourceHash            string
	ProtectedEvidenceRef  string
	ProjectedText         string
	ProjectedHash         string
	RawBytes              int
	ProjectedBytes        int
	CreatedAt             time.Time
}

func NormalizeExposureProjectionInput(input ExposureProjectionInput) ExposureProjectionInput {
	input.ProjectionID = strings.TrimSpace(input.ProjectionID)
	input.InvocationID = strings.TrimSpace(input.InvocationID)
	input.ToolName = strings.TrimSpace(input.ToolName)
	input.Audience = normalizeExposureAudience(input.Audience)
	input.Purpose = normalizeExposurePurpose(input.Purpose)
	input.SourceKind = evidenceToken(input.SourceKind)
	input.SourceRef = strings.TrimSpace(input.SourceRef)
	if input.SourceKind == "" {
		input.SourceKind = EvidenceSourceToolOutput
	}
	if input.SourceRef == "" {
		parts := []string{"tool_output", strings.TrimSpace(input.ToolName)}
		if input.TurnRunID > 0 {
			parts = append(parts, fmt.Sprintf("%d", input.TurnRunID))
		}
		if input.InvocationID != "" {
			parts = append(parts, input.InvocationID)
		}
		input.SourceRef = strings.Join(parts, ":")
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now().UTC()
	}
	return input
}

func normalizeExposureAudience(audience ExposureAudience) ExposureAudience {
	audience = ExposureAudience(normalizeEnumValue(string(audience)))
	switch audience {
	case ExposureAudienceModelPreview, ExposureAudienceOperator:
		return audience
	default:
		return ExposureAudienceModelPreview
	}
}

func normalizeExposurePurpose(purpose ExposurePurpose) ExposurePurpose {
	purpose = ExposurePurpose(normalizeEnumValue(string(purpose)))
	switch purpose {
	case ExposurePurposeToolResultModelContext, ExposurePurposeToolResultPreview, ExposurePurposeToolFailureModelContext, ExposurePurposeToolFailurePreview:
		return purpose
	default:
		return ExposurePurposeToolResultPreview
	}
}

func normalizeExposureProjectionKind(kind ExposureProjectionKind) ExposureProjectionKind {
	kind = ExposureProjectionKind(normalizeEnumValue(string(kind)))
	switch kind {
	case ExposureProjectionPlain, ExposureProjectionRedacted, ExposureProjectionDigest, ExposureProjectionWithheld, ExposureProjectionProtectedRef:
		return kind
	default:
		return ExposureProjectionWithheld
	}
}

func ExposureProjectionID(sessionID string, sourceRef string, audience ExposureAudience, purpose ExposurePurpose, sourceHash string) string {
	seed := strings.Join([]string{
		strings.TrimSpace(sessionID),
		strings.TrimSpace(sourceRef),
		string(normalizeExposureAudience(audience)),
		string(normalizeExposurePurpose(purpose)),
		strings.TrimSpace(sourceHash),
	}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "expproj:" + hex.EncodeToString(sum[:16])
}

func exposureTextHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return "sha256:" + hex.EncodeToString(sum[:])
}
