//go:build linux

package core

import (
	"strings"
	"time"
)

const (
	StatusClassCurrent            = "current"
	StatusClassOperationalTension = "operational_tension"
	StatusClassPrincipleDebt      = "principle_debt"

	ReliabilityFailureNone                  = "none"
	ReliabilityFailureReleaseMetadata       = "release_metadata"
	ReliabilityFailureReleaseFreshness      = "release_freshness"
	ReliabilityFailureSourceInstallRevision = "source_install_revision_mismatch"
	ReliabilityFailureProviderTransient     = "provider_transient"
	ReliabilityFailureProviderConfiguration = "provider_configuration"
	ReliabilityFailureProviderRequestShape  = "provider_request_shape"
	ReliabilityFailureTransportTransient    = "transport_transient"
	ReliabilityFailureTransportConfig       = "transport_configuration"
	ReliabilityFailurePersistenceLatency    = "persistence_latency"
	ReliabilityFailureUnknownExternal       = "external_dependency_unknown"

	ReliabilityRetryNone              = "none"
	ReliabilityRetryRefreshMetadata   = "refresh_release_metadata"
	ReliabilityRetryReinstallService  = "reinstall_or_restart_service"
	ReliabilityRetryBackoffOrFailover = "retry_with_backoff_or_failover"
	ReliabilityRetryConfigRepair      = "retry_after_config_repair"
	ReliabilityRetryRescopeRequest    = "rescope_request"
	ReliabilityRetryBatchBackpressure = "batch_or_backpressure"
	ReliabilityRetryInvestigate       = "investigate_before_retry"

	// PersistenceLatencySlowThreshold matches the prior slow_write operational
	// threshold so routine SQLite jitter does not become a noisy repair surface.
	PersistenceLatencySlowThreshold = 250 * time.Millisecond
)

type StatusReliabilityClassification struct {
	StatusClass  string
	Condition    string
	FailureClass string
	RetryPolicy  string
	NextAction   string
}

type SourceInstallStatusInput struct {
	CurrentRevision  string
	RunningRevision  string
	ExpectedRevision string
	LatestVersion    string
	LatestRevision   string
	MetadataStatus   string
	UpdateAvailable  bool
}

type SourceInstallReliabilitySnapshot struct {
	ServiceConsistency StatusReliabilityClassification
	ReleaseFreshness   StatusReliabilityClassification
	Overall            StatusReliabilityClassification
}

func ClassifySourceInstallReliabilityAxes(input SourceInstallStatusInput) SourceInstallReliabilitySnapshot {
	service := classifySourceServiceConsistency(input)
	freshness := classifyReleaseFreshness(input)
	overall := freshness
	if service.FailureClass != ReliabilityFailureNone {
		overall = service
	}
	return SourceInstallReliabilitySnapshot{
		ServiceConsistency: service,
		ReleaseFreshness:   freshness,
		Overall:            overall,
	}
}

func ClassifySourceInstallReliability(input SourceInstallStatusInput) StatusReliabilityClassification {
	return ClassifySourceInstallReliabilityAxes(input).Overall
}

func classifySourceServiceConsistency(input SourceInstallStatusInput) StatusReliabilityClassification {
	currentRevision := strings.TrimSpace(input.CurrentRevision)
	runningRevision := strings.TrimSpace(input.RunningRevision)
	expectedRevision := strings.TrimSpace(input.ExpectedRevision)
	if runningRevision != "" && expectedRevision != "" && runningRevision != expectedRevision {
		return reliabilityClassification(
			StatusClassOperationalTension,
			"source_install_revision_mismatch",
			ReliabilityFailureSourceInstallRevision,
			ReliabilityRetryReinstallService,
			"reinstall or restart Aphelion so the running service revision matches the expected binary",
		)
	}
	if currentRevision != "" && runningRevision != "" && currentRevision != runningRevision {
		return reliabilityClassification(
			StatusClassOperationalTension,
			"source_install_revision_mismatch",
			ReliabilityFailureSourceInstallRevision,
			ReliabilityRetryReinstallService,
			"restart Aphelion so the running service revision matches the current source revision",
		)
	}
	return reliabilityClassification(
		StatusClassCurrent,
		"source_service_consistent",
		ReliabilityFailureNone,
		ReliabilityRetryNone,
		"none",
	)
}

func classifyReleaseFreshness(input SourceInstallStatusInput) StatusReliabilityClassification {
	metadataStatus := strings.TrimSpace(input.MetadataStatus)
	if input.UpdateAvailable {
		updateTarget := firstNonEmptyReliability(input.LatestRevision, input.LatestVersion)
		if updateTarget == "" {
			return reliabilityClassification(
				StatusClassOperationalTension,
				"release_metadata_update_target_missing",
				ReliabilityFailureReleaseMetadata,
				ReliabilityRetryRefreshMetadata,
				"refresh cached release metadata so update availability has an explicit latest version or revision",
			)
		}
		return reliabilityClassification(
			StatusClassOperationalTension,
			"release_update_available",
			ReliabilityFailureReleaseFreshness,
			ReliabilityRetryReinstallService,
			"install the newer release or refresh metadata if this is a source checkout",
		)
	}
	if metadataStatus != "" && metadataStatus != "present" && metadataStatus != "current" && metadataStatus != "ok" {
		return reliabilityClassification(
			StatusClassOperationalTension,
			"release_metadata_"+normalizeReliabilityToken(metadataStatus),
			ReliabilityFailureReleaseMetadata,
			ReliabilityRetryRefreshMetadata,
			"refresh cached release metadata or inspect the release metadata path",
		)
	}
	return reliabilityClassification(
		StatusClassCurrent,
		"release_status_current",
		ReliabilityFailureNone,
		ReliabilityRetryNone,
		"none",
	)
}

func ClassifyProviderReliability(failureKind string, errorText string) StatusReliabilityClassification {
	combined := strings.ToLower(strings.TrimSpace(failureKind + " " + errorText))
	switch {
	case strings.TrimSpace(combined) == "":
		return reliabilityClassification(StatusClassCurrent, "provider_status_current", ReliabilityFailureNone, ReliabilityRetryNone, "none")
	case containsReliabilityAny(combined, "context window", "context length", "context_budget", "token limit", "too many tokens", "request too large", "max_tokens", "maximum context"):
		return reliabilityClassification(
			StatusClassOperationalTension,
			"provider_request_shape",
			ReliabilityFailureProviderRequestShape,
			ReliabilityRetryRescopeRequest,
			"rescope the request or reduce context before retrying",
		)
	case containsReliabilityAny(combined, "api key", "apikey", "unauthorized", "forbidden", "permission", "invalid model", "model_not_found", "unsupported model", "quota", "billing", "payment"):
		return reliabilityClassification(
			StatusClassOperationalTension,
			"provider_configuration_failure",
			ReliabilityFailureProviderConfiguration,
			ReliabilityRetryConfigRepair,
			"repair provider credentials, quota, or model configuration before retrying",
		)
	case containsReliabilityAny(combined, "timeout", "deadline", "temporary", "connection reset", "connection refused", "eof", "rate limit", "429", "overload", "at capacity", "unavailable", " 500", " 502", " 503", " 504", "server error"):
		return reliabilityClassification(
			StatusClassOperationalTension,
			"provider_transient_failure",
			ReliabilityFailureProviderTransient,
			ReliabilityRetryBackoffOrFailover,
			"retry with backoff or fail over to another provider",
		)
	default:
		return reliabilityClassification(
			StatusClassOperationalTension,
			"provider_external_dependency_unknown",
			ReliabilityFailureUnknownExternal,
			ReliabilityRetryInvestigate,
			"inspect the provider error and choose backoff, failover, rescope, or configuration repair",
		)
	}
}

func ClassifyTransportReliability(surface string, errorText string) StatusReliabilityClassification {
	errorText = strings.ToLower(strings.TrimSpace(errorText))
	surfaceToken := normalizeReliabilityToken(firstNonEmptyReliability(surface, "transport"))
	switch {
	case errorText == "":
		return reliabilityClassification(StatusClassCurrent, surfaceToken+"_status_current", ReliabilityFailureNone, ReliabilityRetryNone, "none")
	case containsReliabilityAny(errorText, "token", "unauthorized", "forbidden", "permission", "chat not found", "bot was blocked", "bad request"):
		return reliabilityClassification(
			StatusClassOperationalTension,
			surfaceToken+"_configuration_failure",
			ReliabilityFailureTransportConfig,
			ReliabilityRetryConfigRepair,
			"repair transport credentials, permissions, chat binding, or bot configuration before retrying",
		)
	case containsReliabilityAny(errorText, "timeout", "deadline", "temporary", "connection reset", "connection refused", "network", "eof", "rate limit", "429", "too many requests", " 500", " 502", " 503", " 504"):
		return reliabilityClassification(
			StatusClassOperationalTension,
			surfaceToken+"_transient_failure",
			ReliabilityFailureTransportTransient,
			ReliabilityRetryBackoffOrFailover,
			"retry the transport operation with backoff",
		)
	default:
		return reliabilityClassification(
			StatusClassOperationalTension,
			surfaceToken+"_external_dependency_unknown",
			ReliabilityFailureUnknownExternal,
			ReliabilityRetryInvestigate,
			"inspect the transport error before choosing retry or configuration repair",
		)
	}
}

func ClassifyPersistenceLatency(component string, latency time.Duration) StatusReliabilityClassification {
	if latency < PersistenceLatencySlowThreshold {
		return reliabilityClassification(StatusClassCurrent, "persistence_latency_normal", ReliabilityFailureNone, ReliabilityRetryNone, "none")
	}
	component = strings.TrimSpace(component)
	if component == "" {
		component = "persistence"
	}
	return reliabilityClassification(
		StatusClassOperationalTension,
		"persistence_latency_slow_write",
		ReliabilityFailurePersistenceLatency,
		ReliabilityRetryBatchBackpressure,
		"inspect SQLite or disk latency for "+component+" and reduce write pressure with batching or backpressure",
	)
}

func reliabilityClassification(statusClass string, condition string, failureClass string, retryPolicy string, nextAction string) StatusReliabilityClassification {
	return StatusReliabilityClassification{
		StatusClass:  firstNonEmptyReliability(statusClass, StatusClassOperationalTension),
		Condition:    firstNonEmptyReliability(condition, "unknown"),
		FailureClass: firstNonEmptyReliability(failureClass, ReliabilityFailureUnknownExternal),
		RetryPolicy:  firstNonEmptyReliability(retryPolicy, ReliabilityRetryInvestigate),
		NextAction:   firstNonEmptyReliability(nextAction, "inspect status details"),
	}
}

func containsReliabilityAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, strings.ToLower(strings.TrimSpace(needle))) {
			return true
		}
	}
	return false
}

func firstNonEmptyReliability(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func normalizeReliabilityToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer(" ", "_", "-", "_", ".", "_", ":", "_", "/", "_")
	value = replacer.Replace(value)
	for strings.Contains(value, "__") {
		value = strings.ReplaceAll(value, "__", "_")
	}
	return strings.Trim(value, "_")
}
