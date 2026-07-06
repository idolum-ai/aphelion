//go:build linux

package session

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

type EvidenceTextRedaction struct {
	Text     string
	Redacted bool
	Kinds    []string
}

type evidenceSensitivePattern struct {
	re   *regexp.Regexp
	kind string
}

var evidenceSensitivePatterns = []evidenceSensitivePattern{
	{regexp.MustCompile(`(?is)()(-----BEGIN (?:[A-Z0-9 ]*PRIVATE KEY|OPENSSH PRIVATE KEY)-----.*?-----END (?:[A-Z0-9 ]*PRIVATE KEY|OPENSSH PRIVATE KEY)-----)()`), "private_key"},
	{regexp.MustCompile(`(?i)(Authorization\s*[:=]\s*Bearer\s+)([^\s,;"}]+)()`), "bearer"},
	{regexp.MustCompile(`(?i)([?&](?:access_token|refresh_token|token|api_key|apikey|signature|sig|x-amz-signature|x-amz-credential|awsaccesskeyid)=)([^&#\s]+)()`), "url_query"},
	{regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|PASSWD|API[_-]?KEY|AUTHORIZATION|COOKIE)[A-Z0-9_]*\s*=\s*)([^\s,"'}]+)()`), ""},
	{regexp.MustCompile(`(?i)("(?:bot_token|telegram_bot_token|api_key|openai_api_key|elevenlabs_api_key|access_token|refresh_token|secret|password|token|authorization|cookie)"\s*:\s*")([^"]*)(")`), ""},
	{regexp.MustCompile(`(?i)\b((?:token|api[_-]?key|secret|password|passwd|authorization|cookie)\s*[:=]\s*["'])([^"']{4,})(["'])`), ""},
	{regexp.MustCompile(`(?i)\b((?:token|api[_-]?key|secret|password|passwd|authorization|cookie)\s*[:=]\s*)([^\s,"'}]{8,})()`), ""},
	{regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://[^/\s:@]+:)([^@\s]+)(@[^\s]+)`), "connection_password"},
	{regexp.MustCompile(`()(AKIA[0-9A-Z]{16})()`), "aws_access_key"},
	{regexp.MustCompile(`()([A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,})()`), "jwt"},
	{regexp.MustCompile(`()(github_pat_[A-Za-z0-9_]{12,})()`), "github_token"},
	{regexp.MustCompile(`()(gh[pousr]_[A-Za-z0-9_]{12,})()`), "github_token"},
	{regexp.MustCompile(`()(sk-[A-Za-z0-9_-]{12,})()`), "api_key"},
	{regexp.MustCompile(`(?i)\b((?:path|file|config|metadata|root)\s*:\s*)(/[^\s,"'}]*(?:credential|credentials|secret|token|key|auth)[^\s,"'}]*)()`), "credential_metadata"},
}

func RedactEvidenceText(value string) EvidenceTextRedaction {
	out := value
	kinds := map[string]struct{}{}
	for _, pattern := range evidenceSensitivePatterns {
		next, found := redactEvidencePattern(out, pattern, kinds)
		if found {
			out = next
		}
	}
	return EvidenceTextRedaction{
		Text:     out,
		Redacted: len(kinds) > 0,
		Kinds:    sortedEvidenceRedactionKinds(kinds),
	}
}

func EvidencePayloadHydrationAllowed(redactionClass string) bool {
	switch evidenceToken(redactionClass) {
	case "", EvidenceRedactionNone, EvidenceRedactionDigest, EvidenceRedactionMetadata, EvidenceRedactionRedacted:
		return true
	default:
		return false
	}
}

func EvidenceRedactionClassForRedactions(redactions ...EvidenceTextRedaction) string {
	redacted := false
	for _, redaction := range redactions {
		if !redaction.Redacted {
			continue
		}
		redacted = true
		for _, kind := range redaction.Kinds {
			if evidenceRedactionKindIsCredentialBearing(kind) {
				return EvidenceRedactionSecret
			}
		}
	}
	if redacted {
		return EvidenceRedactionRedacted
	}
	return EvidenceRedactionNone
}

type ExposureAudience string

const (
	ExposureAudienceModelPreview ExposureAudience = "model_preview"
	ExposureAudienceOperator     ExposureAudience = "operator"
)

type ToolResultProjection struct {
	Audience              ExposureAudience
	Purpose               ExposurePurpose
	Projection            string
	ProjectionKind        ExposureProjectionKind
	Sensitivity           string
	SensitivityProvenance []string
	PolicyRef             string
	ProtectedRef          string
	SourceHash            string
	Text                  string
	RawBytes              int
	ProjectedBytes        int
}

func ProjectToolResultForAudience(value string, audience ExposureAudience) ToolResultProjection {
	return ProjectToolResultForPurpose(value, audience, ExposurePurposeToolResultPreview)
}

func ProjectToolResultForPurpose(value string, audience ExposureAudience, purpose ExposurePurpose) ToolResultProjection {
	audience = ExposureAudience(strings.TrimSpace(string(audience)))
	if audience == "" {
		audience = ExposureAudienceModelPreview
	}
	purpose = normalizeExposurePurpose(purpose)
	redacted := RedactEvidenceText(value)
	class := EvidenceRedactionClassForRedactions(redacted)
	projection := ExposureProjectionPlain
	provenance := exposureSensitivityProvenance(redacted, value)
	if redacted.Redacted {
		projection = ExposureProjectionRedacted
	}
	text := redacted.Text
	if class == EvidenceRedactionSecret && redacted.Redacted {
		text = renderProtectedToolOutputProjection(audience)
		projection = ExposureProjectionProtectedRef
	}
	if projection != ExposureProjectionProtectedRef && len(text) > 1800 {
		text = renderToolOutputDigestProjection(text, 1700)
		projection = ExposureProjectionDigest
		if class == EvidenceRedactionNone {
			class = EvidenceRedactionDigest
		}
		provenance = appendUniqueEvidenceString(provenance, "size:over_1800_bytes")
	}
	if strings.TrimSpace(text) == "" && strings.TrimSpace(value) != "" {
		text = "<withheld:empty_projection>"
		projection = ExposureProjectionWithheld
		if class == EvidenceRedactionNone {
			class = EvidenceRedactionBlocked
		}
		provenance = appendUniqueEvidenceString(provenance, "projection:empty")
	}
	if class == "" {
		class = EvidenceRedactionNone
	}
	return ToolResultProjection{
		Audience:              audience,
		Purpose:               purpose,
		Projection:            string(projection),
		ProjectionKind:        projection,
		Sensitivity:           class,
		SensitivityProvenance: normalizeStringList(provenance),
		PolicyRef:             ExposureProjectionPolicyToolOutputV1,
		SourceHash:            exposureTextHash(value),
		Text:                  text,
		RawBytes:              len(value),
		ProjectedBytes:        len(text),
	}
}

func renderProtectedToolOutputProjection(audience ExposureAudience) string {
	return fmt.Sprintf("<tool_output_protected audience=%s>", normalizeExposureAudience(audience))
}

func renderToolOutputDigestProjection(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	head := limit * 2 / 3
	tail := limit - head
	if head < 1 {
		head = 1
	}
	if tail < 1 {
		tail = 1
	}
	if head+tail >= len(text) {
		return text
	}
	return strings.TrimSpace(text[:head]) +
		fmt.Sprintf("\n\n[tool_output_digest original_bytes=%d omitted_bytes=%d]\n\n", len(text), len(text)-head-tail) +
		strings.TrimSpace(text[len(text)-tail:])
}

func exposureSensitivityProvenance(redacted EvidenceTextRedaction, value string) []string {
	out := []string{}
	for _, kind := range redacted.Kinds {
		out = appendUniqueEvidenceString(out, "pattern:"+evidenceRedactionKind(kind))
	}
	if strings.TrimSpace(value) == "" {
		out = appendUniqueEvidenceString(out, "content:empty")
	}
	if len(value) > 1800 {
		out = appendUniqueEvidenceString(out, "size:over_1800_bytes")
	}
	if len(out) == 0 {
		out = append(out, "content:no_sensitive_pattern")
	}
	return out
}

func appendUniqueEvidenceString(values []string, next string) []string {
	next = strings.TrimSpace(next)
	if next == "" {
		return values
	}
	for _, existing := range values {
		if existing == next {
			return values
		}
	}
	return append(values, next)
}

func evidenceRedactionKindIsCredentialBearing(kind string) bool {
	switch evidenceRedactionKind(kind) {
	case "authorization",
		"bearer",
		"api_key",
		"github_token",
		"password",
		"token",
		"secret",
		"cookie",
		"url_query",
		"private_key",
		"connection_password",
		"aws_access_key",
		"jwt":
		return true
	default:
		return false
	}
}

func redactEvidencePattern(value string, pattern evidenceSensitivePattern, kinds map[string]struct{}) (string, bool) {
	matches := pattern.re.FindAllStringSubmatchIndex(value, -1)
	if len(matches) == 0 {
		return value, false
	}
	var b strings.Builder
	last := 0
	found := false
	for _, match := range matches {
		if len(match) < 8 || match[4] < 0 || match[5] < 0 {
			continue
		}
		if evidenceMatchInsideRedactionMarker(value, match[0]) {
			continue
		}
		secret := value[match[4]:match[5]]
		if strings.TrimSpace(secret) == "" || strings.Contains(secret, "<redacted:") {
			continue
		}
		kind := pattern.kind
		if kind == "" && match[2] >= 0 && match[3] >= 0 {
			kind = evidenceRedactionKindFromPrefix(value[match[2]:match[3]])
		}
		if kind == "" {
			kind = "secret"
		}
		b.WriteString(value[last:match[2]])
		b.WriteString(value[match[2]:match[4]])
		b.WriteString(evidenceRedactionMarker(kind, secret))
		if match[6] >= 0 && match[7] >= 0 {
			b.WriteString(value[match[6]:match[7]])
		}
		last = match[1]
		kinds[kind] = struct{}{}
		found = true
	}
	if !found {
		return value, false
	}
	b.WriteString(value[last:])
	return b.String(), true
}

func evidenceMatchInsideRedactionMarker(value string, start int) bool {
	if start < 0 || start > len(value) {
		return false
	}
	open := strings.LastIndex(value[:start], "<redacted:")
	if open < 0 {
		return false
	}
	closed := strings.LastIndex(value[:start], ">")
	return closed < open
}

func evidenceRedactionMarker(kind string, secret string) string {
	kind = evidenceRedactionKind(kind)
	sum := sha256.Sum256([]byte(secret))
	return "<redacted:" + kind + ":" + hex.EncodeToString(sum[:])[:12] + ">"
}

func evidenceRedactionKindFromPrefix(prefix string) string {
	prefix = strings.ToLower(prefix)
	switch {
	case strings.Contains(prefix, "authorization") || strings.Contains(prefix, "bearer"):
		return "authorization"
	case strings.Contains(prefix, "api"):
		return "api_key"
	case strings.Contains(prefix, "password") || strings.Contains(prefix, "passwd"):
		return "password"
	case strings.Contains(prefix, "token"):
		return "token"
	case strings.Contains(prefix, "cookie"):
		return "cookie"
	case strings.Contains(prefix, "secret"):
		return "secret"
	default:
		return "secret"
	}
}

func evidenceRedactionKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	var b strings.Builder
	for _, r := range kind {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case unicode.IsSpace(r) || r == '-' || r == '_':
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "secret"
	}
	return out
}

func sortedEvidenceRedactionKinds(kinds map[string]struct{}) []string {
	order := []string{"authorization", "bearer", "api_key", "github_token", "password", "token", "secret", "cookie", "url_query", "private_key", "connection_password", "aws_access_key", "jwt"}
	out := make([]string, 0, len(kinds))
	for _, kind := range order {
		if _, ok := kinds[kind]; ok {
			out = append(out, kind)
			delete(kinds, kind)
		}
	}
	extra := make([]string, 0, len(kinds))
	for kind := range kinds {
		extra = append(extra, kind)
	}
	sort.Strings(extra)
	out = append(out, extra...)
	return out
}
