//go:build linux

package core

import (
	"strings"
	"time"
)

type DurableAgentSetupWizardState struct {
	SchemaVersion int                            `json:"schema_version,omitempty"`
	ChannelKind   string                         `json:"channel_kind,omitempty"`
	Status        string                         `json:"status,omitempty"`
	CurrentStep   string                         `json:"current_step,omitempty"`
	Answers       DurableAgentSetupWizardAnswers `json:"answers,omitempty"`
	Missing       []string                       `json:"missing,omitempty"`
	StartedAt     time.Time                      `json:"started_at,omitempty"`
	UpdatedAt     time.Time                      `json:"updated_at,omitempty"`
}

type DurableAgentSetupWizardAnswers struct {
	Mode             string   `json:"mode,omitempty"`
	Address          string   `json:"address,omitempty"`
	Account          string   `json:"account,omitempty"`
	Adapter          string   `json:"adapter,omitempty"`
	Query            string   `json:"query,omitempty"`
	BootstrapProfile string   `json:"bootstrap_profile,omitempty"`
	BootstrapModel   string   `json:"bootstrap_model,omitempty"`
	Charter          string   `json:"charter,omitempty"`
	Autonomy         string   `json:"autonomy,omitempty"`
	WakeupMode       string   `json:"wakeup_mode,omitempty"`
	PollInterval     string   `json:"poll_interval,omitempty"`
	SurfaceRules     []string `json:"surface_rules,omitempty"`
	SummarizePDFs    *bool    `json:"summarize_pdfs,omitempty"`
	SynthesisCadence string   `json:"synthesis_cadence,omitempty"`
	Capabilities     []string `json:"capabilities,omitempty"`
	NeverRetain      []string `json:"never_retain,omitempty"`
	DriftPolicy      string   `json:"drift_policy,omitempty"`
}

func NormalizeDurableAgentSetupWizardAnswers(answers DurableAgentSetupWizardAnswers) DurableAgentSetupWizardAnswers {
	return normalizeDurableAgentSetupWizardAnswers(answers)
}

func normalizeDurableAgentSetupWizardState(state *DurableAgentSetupWizardState) *DurableAgentSetupWizardState {
	if state == nil {
		return nil
	}
	normalized := *state
	if normalized.SchemaVersion <= 0 {
		normalized.SchemaVersion = 1
	}
	normalized.ChannelKind = strings.TrimSpace(normalized.ChannelKind)
	normalized.Status = normalizeDurableAgentSetupWizardStatus(normalized.Status)
	normalized.CurrentStep = strings.TrimSpace(normalized.CurrentStep)
	normalized.Answers = normalizeDurableAgentSetupWizardAnswers(normalized.Answers)
	normalized.Missing = normalizeDurableAgentStringSet(normalized.Missing)
	if normalized.Status == "" && normalized.ChannelKind == "" && normalized.CurrentStep == "" &&
		len(normalized.Missing) == 0 && durableAgentSetupWizardAnswersZero(normalized.Answers) &&
		normalized.StartedAt.IsZero() && normalized.UpdatedAt.IsZero() {
		return nil
	}
	return &normalized
}

func normalizeDurableAgentSetupWizardStatus(value string) string {
	switch strings.TrimSpace(value) {
	case "in_progress", "ready", "finalized", "cancelled":
		return strings.TrimSpace(value)
	default:
		return ""
	}
}

func normalizeDurableAgentSetupWizardAnswers(answers DurableAgentSetupWizardAnswers) DurableAgentSetupWizardAnswers {
	answers.Mode = NormalizeDurableAgentMode(answers.Mode)
	answers.Address = strings.TrimSpace(answers.Address)
	answers.Account = strings.TrimSpace(answers.Account)
	answers.Adapter = normalizeDurableAgentChannelAdapter(answers.Adapter)
	answers.Query = strings.TrimSpace(answers.Query)
	answers.BootstrapProfile = normalizeDurableAgentBootstrapProfile(answers.BootstrapProfile)
	answers.BootstrapModel = strings.TrimSpace(answers.BootstrapModel)
	answers.Charter = strings.TrimSpace(answers.Charter)
	answers.Autonomy = strings.TrimSpace(answers.Autonomy)
	answers.WakeupMode = strings.TrimSpace(answers.WakeupMode)
	answers.PollInterval = strings.TrimSpace(answers.PollInterval)
	answers.SurfaceRules = normalizeDurableAgentStringSet(answers.SurfaceRules)
	answers.SynthesisCadence = strings.TrimSpace(answers.SynthesisCadence)
	answers.Capabilities = normalizeDurableAgentStringSet(answers.Capabilities)
	answers.NeverRetain = normalizeDurableAgentStringSet(answers.NeverRetain)
	answers.DriftPolicy = strings.TrimSpace(answers.DriftPolicy)
	return answers
}

func durableAgentSetupWizardAnswersZero(answers DurableAgentSetupWizardAnswers) bool {
	return answers.Mode == "" &&
		answers.Address == "" &&
		answers.Account == "" &&
		answers.Adapter == "" &&
		answers.Query == "" &&
		answers.BootstrapProfile == "" &&
		answers.BootstrapModel == "" &&
		answers.Charter == "" &&
		answers.Autonomy == "" &&
		answers.WakeupMode == "" &&
		answers.PollInterval == "" &&
		len(answers.SurfaceRules) == 0 &&
		answers.SummarizePDFs == nil &&
		answers.SynthesisCadence == "" &&
		len(answers.Capabilities) == 0 &&
		len(answers.NeverRetain) == 0 &&
		answers.DriftPolicy == ""
}

func normalizeDurableAgentBootstrapProfile(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "inherit_parent":
		return "inherit_parent"
	case "child_custom":
		return "child_custom"
	default:
		return ""
	}
}
