//go:build linux

package tool

import (
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Registry) startDurableAgentWizard(in durableAgentInput, key session.SessionKey) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for wizard_start")
	}
	channelKind := strings.TrimSpace(in.ChannelKind)
	if channelKind == "" {
		channelKind = "external_channel"
	}
	channelKind = normalizeDurableAgentChannelKind(channelKind)
	if channelKind != "external_channel" {
		return "", fmt.Errorf("durable_agent wizard_start currently supports channel_kind=external_channel")
	}

	createIn := in
	createIn.Action = "create"
	createIn.ChannelKind = channelKind
	if strings.TrimSpace(createIn.WakeupMode) == "" {
		createIn.WakeupMode = "poll"
	}
	if _, err := r.createDurableAgent(createIn, key); err != nil {
		return "", err
	}

	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	state, continuity, err := r.loadDurableAgentContinuity(agent.AgentID)
	if err != nil {
		return "", err
	}
	inheritedBootstrap := core.NormalizeNodeLLMBootstrap(r.durableAgentBootstrapLLM)
	now := time.Now().UTC()
	wizard := seedDurableAgentWizardFromAgent(*agent, inheritedBootstrap)
	if continuity.SetupWizard != nil {
		wizard = *continuity.SetupWizard
		if wizard.StartedAt.IsZero() {
			wizard.StartedAt = now
		}
	}
	wizard.SchemaVersion = 1
	wizard.ChannelKind = channelKind
	wizard.UpdatedAt = now
	if wizard.StartedAt.IsZero() {
		wizard.StartedAt = now
	}
	missing := durableAgentWizardMissingAnswers(*agent, wizard, inheritedBootstrap)
	wizard.Missing = missing
	wizard.CurrentStep = firstWizardStep(missing)
	if len(missing) == 0 {
		wizard.Status = "ready"
	} else {
		wizard.Status = "in_progress"
	}
	continuity.SetupWizard = &wizard
	if err := r.saveDurableAgentContinuity(state, continuity); err != nil {
		return "", err
	}
	return renderDurableAgentWizardShow(*agent, wizard, inheritedBootstrap), nil
}

func (r *Registry) answerDurableAgentWizard(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for wizard_answer")
	}
	if in.WizardAnswers == nil {
		return "", fmt.Errorf("durable_agent wizard_answer requires wizard_answers")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	state, continuity, err := r.loadDurableAgentContinuity(agent.AgentID)
	if err != nil {
		return "", err
	}
	if continuity.SetupWizard == nil {
		return "", fmt.Errorf("durable agent %q has no active setup wizard; use wizard_start first", agent.AgentID)
	}

	inheritedBootstrap := core.NormalizeNodeLLMBootstrap(r.durableAgentBootstrapLLM)
	wizard := *continuity.SetupWizard
	wizard.SchemaVersion = 1
	wizard.ChannelKind = firstNonEmpty(
		strings.TrimSpace(wizard.ChannelKind),
		normalizeDurableAgentChannelKind(strings.TrimSpace(agent.ChannelKind)),
		"external_channel",
	)
	wizard.Answers = mergeDurableAgentWizardAnswers(wizard.Answers, *in.WizardAnswers)
	wizard.UpdatedAt = time.Now().UTC()
	if wizard.StartedAt.IsZero() {
		wizard.StartedAt = wizard.UpdatedAt
	}
	missing := durableAgentWizardMissingAnswers(*agent, wizard, inheritedBootstrap)
	wizard.Missing = missing
	wizard.CurrentStep = firstWizardStep(missing)
	if len(missing) == 0 {
		wizard.Status = "ready"
	} else {
		wizard.Status = "in_progress"
	}

	updatedAgent, err := applyDurableWizardAnswersToAgent(*agent, wizard.Answers, inheritedBootstrap)
	if err != nil {
		return "", err
	}
	if err := r.store.UpsertDurableAgent(updatedAgent); err != nil {
		return "", err
	}
	agent = &updatedAgent

	continuity.SetupWizard = &wizard
	if err := r.saveDurableAgentContinuity(state, continuity); err != nil {
		return "", err
	}
	return renderDurableAgentWizardShow(*agent, wizard, inheritedBootstrap), nil
}

func (r *Registry) showDurableAgentWizard(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for wizard_show")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	_, continuity, err := r.loadDurableAgentContinuity(agent.AgentID)
	if err != nil {
		return "", err
	}
	if continuity.SetupWizard == nil {
		return "", fmt.Errorf("durable agent %q has no active setup wizard; use wizard_start first", agent.AgentID)
	}
	inheritedBootstrap := core.NormalizeNodeLLMBootstrap(r.durableAgentBootstrapLLM)
	wizard := *continuity.SetupWizard
	wizard.Missing = durableAgentWizardMissingAnswers(*agent, wizard, inheritedBootstrap)
	wizard.CurrentStep = firstWizardStep(wizard.Missing)
	if wizard.Status == "" {
		if len(wizard.Missing) == 0 {
			wizard.Status = "ready"
		} else {
			wizard.Status = "in_progress"
		}
	}
	return renderDurableAgentWizardShow(*agent, wizard, inheritedBootstrap), nil
}

func (r *Registry) finalizeDurableAgentWizard(in durableAgentInput, key session.SessionKey) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for wizard_finalize")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	state, continuity, err := r.loadDurableAgentContinuity(agent.AgentID)
	if err != nil {
		return "", err
	}
	if continuity.SetupWizard == nil {
		return "", fmt.Errorf("durable agent %q has no active setup wizard; use wizard_start first", agent.AgentID)
	}
	inheritedBootstrap := core.NormalizeNodeLLMBootstrap(r.durableAgentBootstrapLLM)
	wizard := *continuity.SetupWizard
	wizard.Missing = durableAgentWizardMissingAnswers(*agent, wizard, inheritedBootstrap)
	if len(wizard.Missing) > 0 {
		return "", fmt.Errorf("missing wizard answers: %s", strings.Join(wizard.Missing, ", "))
	}
	wizard.CurrentStep = ""
	wizard.Status = "finalized"
	wizard.UpdatedAt = time.Now().UTC()

	updatedAgent, err := applyDurableWizardAnswersToAgent(*agent, wizard.Answers, inheritedBootstrap)
	if err != nil {
		return "", err
	}
	if updatedAgent.ReviewTargetChatID == 0 && key.ChatID != 0 {
		updatedAgent.ReviewTargetChatID = key.ChatID
	}
	if strings.TrimSpace(updatedAgent.Status) == "" {
		updatedAgent.Status = "draft"
	}
	if strings.TrimSpace(updatedAgent.Status) != "active" {
		updatedAgent.Status = "draft"
	}
	r.inheritDurableAgentBootstrapIfMissing(&updatedAgent)
	if err := r.store.UpsertDurableAgent(updatedAgent); err != nil {
		return "", err
	}
	if _, err := syncDurableAgentProfileFiles(updatedAgent, r.store); err != nil {
		return "", err
	}

	continuity.SetupWizard = &wizard
	if err := r.saveDurableAgentContinuity(state, continuity); err != nil {
		return "", err
	}
	return renderDurableAgentWizardFinalize(updatedAgent, wizard, inheritedBootstrap), nil
}

func (r *Registry) cancelDurableAgentWizard(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for wizard_cancel")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	state, continuity, err := r.loadDurableAgentContinuity(agent.AgentID)
	if err != nil {
		return "", err
	}
	if continuity.SetupWizard == nil {
		return "", fmt.Errorf("durable agent %q has no active setup wizard; use wizard_start first", agent.AgentID)
	}
	inheritedBootstrap := core.NormalizeNodeLLMBootstrap(r.durableAgentBootstrapLLM)
	wizard := *continuity.SetupWizard
	wizard.Status = "cancelled"
	wizard.CurrentStep = ""
	wizard.Missing = nil
	wizard.UpdatedAt = time.Now().UTC()
	continuity.SetupWizard = &wizard
	if err := r.saveDurableAgentContinuity(state, continuity); err != nil {
		return "", err
	}
	return renderDurableAgentWizardShow(*agent, wizard, inheritedBootstrap), nil
}

var durableAgentWizardStepOrder = []string{
	"address",
	"adapter",
	"bootstrap_profile",
	"bootstrap_model",
	"autonomy",
	"surface_rules",
	"summarize_pdfs",
	"synthesis_cadence",
	"wakeup_mode",
	"poll_interval",
	"capabilities",
	"never_retain",
	"charter",
}

func seedDurableAgentWizardFromAgent(agent core.DurableAgent, inheritedBootstrap core.NodeLLMBootstrap) core.DurableAgentSetupWizardState {
	currentBootstrap := core.NormalizeNodeLLMBootstrap(agent.BootstrapLLM)
	inheritedBootstrap = core.NormalizeNodeLLMBootstrap(inheritedBootstrap)
	bootstrapProfile := durableAgentWizardBootstrapProfile(currentBootstrap, inheritedBootstrap)
	bootstrapModel := ""
	if bootstrapProfile == "child_custom" {
		bootstrapModel = strings.TrimSpace(currentBootstrap.Model)
	}
	wizard := core.DurableAgentSetupWizardState{
		SchemaVersion: 1,
		ChannelKind:   strings.TrimSpace(agent.ChannelKind),
		Answers: core.DurableAgentSetupWizardAnswers{
			Mode:             durableAgentModeFromPolicy(agent.LivePolicy),
			BootstrapProfile: bootstrapProfile,
			BootstrapModel:   bootstrapModel,
			Charter:          strings.TrimSpace(agent.LivePolicy.Charter),
			Autonomy:         durableAgentAutonomyFromPolicy(agent.LivePolicy),
			WakeupMode:       strings.TrimSpace(agent.WakeupMode),
			Capabilities:     append([]string(nil), agent.LivePolicy.CapabilityEnvelope...),
			DriftPolicy:      strings.TrimSpace(agent.LivePolicy.DriftPolicy),
		},
	}
	if external := agent.ChannelConfig.ExternalConfig(); external != nil {
		wizard.Answers.Address = strings.TrimSpace(external.Address)
		wizard.Answers.Account = strings.TrimSpace(external.Account)
		wizard.Answers.Adapter = strings.TrimSpace(external.Adapter)
		wizard.Answers.Query = strings.TrimSpace(external.Query)
		wizard.Answers.PollInterval = strings.TrimSpace(external.PollInterval)
		wizard.Answers.SurfaceRules = append([]string(nil), external.SurfaceRules...)
		value := external.SummarizePDFs
		wizard.Answers.SummarizePDFs = &value
		wizard.Answers.SynthesisCadence = strings.TrimSpace(external.SynthesisCadence)
		wizard.Answers.NeverRetain = append([]string(nil), external.NeverRetain...)
	}
	return wizard
}

func mergeDurableAgentWizardAnswers(current core.DurableAgentSetupWizardAnswers, patch durableAgentWizardAnswersInput) core.DurableAgentSetupWizardAnswers {
	current = core.NormalizeDurableAgentSetupWizardAnswers(current)
	previousProfile := strings.TrimSpace(current.BootstrapProfile)
	if strings.TrimSpace(patch.Mode) != "" {
		current.Mode = core.NormalizeDurableAgentMode(patch.Mode)
	}
	if strings.TrimSpace(patch.Address) != "" {
		current.Address = strings.TrimSpace(patch.Address)
	}
	if strings.TrimSpace(patch.Account) != "" {
		current.Account = strings.TrimSpace(patch.Account)
	}
	if strings.TrimSpace(patch.Adapter) != "" {
		current.Adapter = strings.TrimSpace(patch.Adapter)
	}
	if strings.TrimSpace(patch.Query) != "" {
		current.Query = strings.TrimSpace(patch.Query)
	}
	if strings.TrimSpace(patch.BootstrapProfile) != "" {
		current.BootstrapProfile = strings.TrimSpace(patch.BootstrapProfile)
		switch core.NormalizeDurableAgentSetupWizardAnswers(current).BootstrapProfile {
		case "inherit_parent":
			// Keep inherited model implicit when the parent bootstrap is selected.
			current.BootstrapModel = ""
		case "child_custom":
			if previousProfile != "child_custom" && strings.TrimSpace(patch.BootstrapModel) == "" {
				// Force an explicit child model decision when switching to child-custom mode.
				current.BootstrapModel = ""
			}
		}
	}
	if strings.TrimSpace(patch.BootstrapModel) != "" {
		current.BootstrapModel = strings.TrimSpace(patch.BootstrapModel)
	}
	if strings.TrimSpace(patch.Charter) != "" {
		current.Charter = strings.TrimSpace(patch.Charter)
	}
	if strings.TrimSpace(patch.Autonomy) != "" {
		current.Autonomy = strings.TrimSpace(patch.Autonomy)
	}
	if strings.TrimSpace(patch.WakeupMode) != "" {
		current.WakeupMode = strings.TrimSpace(patch.WakeupMode)
	}
	if strings.TrimSpace(patch.PollInterval) != "" {
		current.PollInterval = strings.TrimSpace(patch.PollInterval)
	}
	if patch.SurfaceRules != nil {
		current.SurfaceRules = normalizePolicyCapabilities(patch.SurfaceRules)
	}
	if patch.SummarizePDFs != nil {
		value := *patch.SummarizePDFs
		current.SummarizePDFs = &value
	}
	if strings.TrimSpace(patch.SynthesisCadence) != "" {
		current.SynthesisCadence = strings.TrimSpace(patch.SynthesisCadence)
	}
	if patch.Capabilities != nil {
		current.Capabilities = normalizePolicyCapabilities(patch.Capabilities)
	}
	if patch.NeverRetain != nil {
		current.NeverRetain = normalizePolicyCapabilities(patch.NeverRetain)
	}
	if strings.TrimSpace(patch.DriftPolicy) != "" {
		current.DriftPolicy = strings.TrimSpace(patch.DriftPolicy)
	}
	return core.NormalizeDurableAgentSetupWizardAnswers(current)
}

func applyDurableWizardAnswersToAgent(agent core.DurableAgent, answers core.DurableAgentSetupWizardAnswers, inheritedBootstrap core.NodeLLMBootstrap) (core.DurableAgent, error) {
	answers = core.NormalizeDurableAgentSetupWizardAnswers(answers)
	agent.ChannelKind = normalizeDurableAgentChannelKind("external_channel")
	wakeupMode := normalizeDurableChannelWakeupMode(answers.WakeupMode)
	if wakeupMode == "" && strings.TrimSpace(agent.WakeupMode) != "" {
		wakeupMode = normalizeDurableChannelWakeupMode(agent.WakeupMode)
	}
	if wakeupMode == "" {
		wakeupMode = "poll"
	}
	agent.WakeupMode = wakeupMode

	patch := effectiveDurableAgentPolicyPatch{
		Mode:        strings.TrimSpace(answers.Mode),
		Charter:     strings.TrimSpace(answers.Charter),
		Autonomy:    strings.TrimSpace(answers.Autonomy),
		DriftPolicy: strings.TrimSpace(answers.DriftPolicy),
	}
	if len(answers.Capabilities) > 0 {
		patch.Capabilities = append([]string(nil), answers.Capabilities...)
		patch.CapabilitiesSet = true
	}
	policy := agent.LivePolicy
	if strings.TrimSpace(policy.Charter) == "" &&
		strings.TrimSpace(policy.Mode) == "" &&
		len(policy.CapabilityEnvelope) == 0 &&
		strings.TrimSpace(policy.OutboundMode) == "" &&
		strings.TrimSpace(policy.DriftPolicy) == "" &&
		strings.TrimSpace(policy.PublicSurfaceMode) == "" &&
		strings.TrimSpace(policy.SharedInferenceReuse) == "" &&
		strings.TrimSpace(policy.SharedInferenceReuseScope) == "" {
		policy = defaultDurableAgentLivePolicy("external_channel", patch.Charter)
	}
	if err := applyDurableAgentPolicyPatch(&policy, patch); err != nil {
		return core.DurableAgent{}, err
	}
	agent.LivePolicy = core.NormalizeDurableAgentLivePolicy(policy)
	applyDurableAgentModeRuntimeDefaults(&agent)

	channelConfig := core.NormalizeDurableAgentChannelConfig(agent.ChannelConfig)
	external := channelConfig.ExternalConfig()
	if external == nil {
		external = &core.DurableAgentExternalChannelConfig{}
	} else {
		copied := *external
		external = &copied
	}
	if answers.Address != "" {
		external.Address = answers.Address
	}
	if answers.Account != "" {
		external.Account = answers.Account
	} else if strings.TrimSpace(external.Account) == "" && strings.TrimSpace(external.Address) != "" {
		external.Account = strings.TrimSpace(external.Address)
	}
	if answers.Adapter != "" {
		external.Adapter = answers.Adapter
	}
	if answers.Query != "" {
		external.Query = answers.Query
	}
	if answers.PollInterval != "" {
		external.PollInterval = answers.PollInterval
	}
	if answers.SurfaceRules != nil {
		external.SurfaceRules = append([]string(nil), answers.SurfaceRules...)
	}
	if answers.SummarizePDFs != nil {
		external.SummarizePDFs = *answers.SummarizePDFs
	}
	if answers.SynthesisCadence != "" {
		external.SynthesisCadence = answers.SynthesisCadence
	}
	if answers.NeverRetain != nil {
		external.NeverRetain = append([]string(nil), answers.NeverRetain...)
	}
	channelConfig.External = external
	agent.ChannelConfig = core.NormalizeDurableAgentChannelConfig(channelConfig)

	bootstrap, err := durableAgentBootstrapFromWizardAnswers(agent.BootstrapLLM, answers, inheritedBootstrap)
	if err != nil {
		return core.DurableAgent{}, err
	}
	agent.BootstrapLLM = core.NormalizeNodeLLMBootstrap(bootstrap)

	if strings.TrimSpace(agent.Status) == "" {
		agent.Status = "draft"
	}
	return agent, nil
}

func durableAgentWizardMissingAnswers(agent core.DurableAgent, wizard core.DurableAgentSetupWizardState, inheritedBootstrap core.NodeLLMBootstrap) []string {
	answers := core.NormalizeDurableAgentSetupWizardAnswers(wizard.Answers)
	effectiveBootstrap := durableAgentWizardEffectiveBootstrapForAnswers(agent, answers, inheritedBootstrap)
	missing := make([]string, 0, len(durableAgentWizardStepOrder))
	childMode := durableAgentWizardMode(agent, answers)
	if childMode == "external" || childMode == "live" {
		if strings.TrimSpace(answers.Address) == "" {
			missing = append(missing, "address")
		}
		if strings.TrimSpace(answers.Adapter) == "" {
			missing = append(missing, "adapter")
		}
	}
	if strings.TrimSpace(answers.BootstrapProfile) == "" {
		missing = append(missing, "bootstrap_profile")
	} else if strings.TrimSpace(answers.BootstrapProfile) == "child_custom" &&
		strings.TrimSpace(effectiveBootstrap.Backend) == "native" &&
		strings.TrimSpace(answers.BootstrapModel) == "" {
		missing = append(missing, "bootstrap_model")
	}
	if strings.TrimSpace(answers.Autonomy) == "" {
		missing = append(missing, "autonomy")
	}
	if childMode == "external" || childMode == "live" {
		if len(answers.SurfaceRules) == 0 {
			missing = append(missing, "surface_rules")
		}
		if answers.SummarizePDFs == nil {
			missing = append(missing, "summarize_pdfs")
		}
		if strings.TrimSpace(answers.SynthesisCadence) == "" {
			missing = append(missing, "synthesis_cadence")
		}
		mode := normalizeDurableChannelWakeupMode(answers.WakeupMode)
		if mode == "" {
			missing = append(missing, "wakeup_mode")
		} else if durableChannelWakeupModeIncludesPoll(mode) && strings.TrimSpace(answers.PollInterval) == "" {
			missing = append(missing, "poll_interval")
		}
	}
	if len(answers.Capabilities) == 0 {
		missing = append(missing, "capabilities")
	}
	if (childMode == "external" || childMode == "live") && len(answers.NeverRetain) == 0 {
		missing = append(missing, "never_retain")
	}
	if strings.TrimSpace(answers.Charter) == "" {
		missing = append(missing, "charter")
	}
	return normalizePolicyCapabilities(missing)
}

func firstWizardStep(missing []string) string {
	if len(missing) == 0 {
		return ""
	}
	missingSet := make(map[string]struct{}, len(missing))
	for _, item := range missing {
		missingSet[strings.TrimSpace(item)] = struct{}{}
	}
	for _, step := range durableAgentWizardStepOrder {
		if _, ok := missingSet[step]; ok {
			return step
		}
	}
	return strings.TrimSpace(missing[0])
}

func durableAgentWizardEffectiveBootstrapForAnswers(agent core.DurableAgent, answers core.DurableAgentSetupWizardAnswers, inheritedBootstrap core.NodeLLMBootstrap) core.NodeLLMBootstrap {
	effective, err := durableAgentBootstrapFromWizardAnswers(agent.BootstrapLLM, answers, inheritedBootstrap)
	if err == nil {
		return core.NormalizeNodeLLMBootstrap(effective)
	}

	answers = core.NormalizeDurableAgentSetupWizardAnswers(answers)
	current := core.NormalizeNodeLLMBootstrap(agent.BootstrapLLM)
	inherited := core.NormalizeNodeLLMBootstrap(inheritedBootstrap)
	switch strings.TrimSpace(answers.BootstrapProfile) {
	case "inherit_parent":
		if inherited.Configured() {
			return inherited
		}
		return current
	case "child_custom":
		if current.Configured() {
			return current
		}
		return inherited
	default:
		if inherited.Configured() {
			return inherited
		}
		return current
	}
}

func wizardQuestionForStep(step string, effectiveBootstrapBackend string) string {
	switch strings.TrimSpace(step) {
	case "address":
		return "What channel address should this child own?"
	case "adapter":
		return "Which channel adapter should be named for this channel profile?"
	case "bootstrap_profile":
		if strings.TrimSpace(effectiveBootstrapBackend) == "codex" {
			return "This child uses a codex bootstrap backend; keep parent bootstrap defaults?"
		}
		return "Should this child inherit the parent bootstrap defaults or pin a child-custom bootstrap profile?"
	case "bootstrap_model":
		return "Which model should this child pin for child-custom bootstrap?"
	case "autonomy":
		return "Should the child be observe_only, local_drafts, review_before_reply, or reply_within_charter?"
	case "surface_rules":
		return "Which signal rules should surface upward as important?"
	case "summarize_pdfs":
		return "Should PDFs be summarized automatically?"
	case "synthesis_cadence":
		return "How often should this child synthesize upward (for example 4h)?"
	case "wakeup_mode":
		return "Should wakeups be poll, push, or poll_or_push?"
	case "poll_interval":
		return "What poll interval should be used (for example 5m)?"
	case "capabilities":
		return "Which capabilities are allowed in the child charter?"
	case "never_retain":
		return "Which classes must never be retained?"
	case "charter":
		return "What is the child charter summary?"
	default:
		return ""
	}
}

func durableAgentBootstrapFromWizardAnswers(current core.NodeLLMBootstrap, answers core.DurableAgentSetupWizardAnswers, inherited core.NodeLLMBootstrap) (core.NodeLLMBootstrap, error) {
	current = core.NormalizeNodeLLMBootstrap(current)
	inherited = core.NormalizeNodeLLMBootstrap(inherited)
	answers = core.NormalizeDurableAgentSetupWizardAnswers(answers)

	profile := strings.TrimSpace(answers.BootstrapProfile)
	if profile == "" {
		profile = durableAgentWizardBootstrapProfile(current, inherited)
	}

	switch profile {
	case "inherit_parent":
		if inherited.Configured() {
			return inherited, nil
		}
		return current, nil
	case "child_custom":
		bootstrap := current
		if !bootstrap.Configured() && inherited.Configured() {
			bootstrap = inherited
		}
		if strings.TrimSpace(answers.BootstrapModel) != "" {
			if bootstrap.Backend != "native" {
				return core.NodeLLMBootstrap{}, fmt.Errorf("durable_agent bootstrap_model requires a native bootstrap backend")
			}
			bootstrap.Model = strings.TrimSpace(answers.BootstrapModel)
		}
		return core.NormalizeNodeLLMBootstrap(bootstrap), nil
	default:
		if inherited.Configured() {
			return inherited, nil
		}
		return current, nil
	}
}

func durableAgentWizardBootstrapProfile(current core.NodeLLMBootstrap, inherited core.NodeLLMBootstrap) string {
	current = core.NormalizeNodeLLMBootstrap(current)
	inherited = core.NormalizeNodeLLMBootstrap(inherited)
	if current.Configured() {
		if inherited.Configured() && durableAgentNodeBootstrapEqual(current, inherited) {
			return "inherit_parent"
		}
		return "child_custom"
	}
	if inherited.Configured() {
		return "inherit_parent"
	}
	return ""
}

func durableAgentNodeBootstrapEqual(left core.NodeLLMBootstrap, right core.NodeLLMBootstrap) bool {
	left = core.NormalizeNodeLLMBootstrap(left)
	right = core.NormalizeNodeLLMBootstrap(right)
	return left.Backend == right.Backend &&
		left.NativeProvider == right.NativeProvider &&
		left.APIKey == right.APIKey &&
		left.BaseURL == right.BaseURL &&
		left.Model == right.Model &&
		left.MaxTokens == right.MaxTokens &&
		left.CodexAuthSource == right.CodexAuthSource &&
		left.CodexHome == right.CodexHome &&
		left.CodexBaseURL == right.CodexBaseURL
}

func durableAgentWizardBootstrapFallbackSummary(bootstrap core.NodeLLMBootstrap) string {
	bootstrap = core.NormalizeNodeLLMBootstrap(bootstrap)
	switch bootstrap.Backend {
	case "native":
		return "inherits parent provider fallback chain"
	case "codex":
		return "codex backend; no provider fallback chain"
	default:
		return "n/a"
	}
}

func normalizeDurableChannelWakeupMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "manual", "none", "off":
		return "manual"
	case "poll":
		return "poll"
	case "push":
		return "push"
	case "poll_or_push", "both":
		return "poll_or_push"
	default:
		return ""
	}
}

func durableChannelWakeupModeIncludesPoll(mode string) bool {
	mode = normalizeDurableChannelWakeupMode(mode)
	return mode == "poll" || mode == "poll_or_push"
}

func normalizeDurableAgentChannelKind(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return ""
	case "external", "external_channel":
		return "external_channel"
	default:
		return strings.TrimSpace(value)
	}
}

func durableAgentWizardDisplayChannelKind(value string) string {
	switch normalizeDurableAgentChannelKind(value) {
	case "external_channel":
		return "external"
	default:
		return strings.TrimSpace(value)
	}
}

func renderDurableAgentWizardShow(agent core.DurableAgent, wizard core.DurableAgentSetupWizardState, inheritedBootstrap core.NodeLLMBootstrap) string {
	var b strings.Builder
	channelKind := normalizeDurableAgentChannelKind(firstNonEmpty(strings.TrimSpace(wizard.ChannelKind), strings.TrimSpace(agent.ChannelKind), "external_channel"))
	effectiveBootstrap, _ := durableAgentBootstrapFromWizardAnswers(agent.BootstrapLLM, wizard.Answers, inheritedBootstrap)
	profile := strings.TrimSpace(core.NormalizeDurableAgentSetupWizardAnswers(wizard.Answers).BootstrapProfile)
	if profile == "" {
		profile = durableAgentWizardBootstrapProfile(agent.BootstrapLLM, inheritedBootstrap)
	}
	b.WriteString("action: durable-agent wizard show\n")
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "channel_kind: %s\n", channelKind)
	fmt.Fprintf(&b, "channel_profile: %s\n", durableAgentWizardDisplayChannelKind(channelKind))
	fmt.Fprintf(&b, "mode: %s\n", durableAgentWizardMode(agent, wizard.Answers))
	fmt.Fprintf(&b, "wizard_status: %s\n", firstNonEmpty(strings.TrimSpace(wizard.Status), "in_progress"))
	fmt.Fprintf(&b, "current_step: %s\n", firstNonEmpty(strings.TrimSpace(wizard.CurrentStep), "-"))
	fmt.Fprintf(&b, "missing: %s\n", firstNonEmpty(strings.Join(wizard.Missing, ","), "-"))
	if question := wizardQuestionForStep(wizard.CurrentStep, effectiveBootstrap.Backend); question != "" {
		fmt.Fprintf(&b, "next_question: %s\n", question)
	}
	fmt.Fprintf(&b, "address: %s\n", strings.TrimSpace(wizard.Answers.Address))
	fmt.Fprintf(&b, "adapter: %s\n", strings.TrimSpace(wizard.Answers.Adapter))
	fmt.Fprintf(&b, "bootstrap_profile: %s\n", profile)
	fmt.Fprintf(&b, "bootstrap_backend: %s\n", strings.TrimSpace(effectiveBootstrap.Backend))
	fmt.Fprintf(&b, "bootstrap_native_provider: %s\n", strings.TrimSpace(effectiveBootstrap.NativeProvider))
	fmt.Fprintf(&b, "bootstrap_model: %s\n", strings.TrimSpace(effectiveBootstrap.Model))
	fmt.Fprintf(&b, "bootstrap_fallback: %s\n", durableAgentWizardBootstrapFallbackSummary(effectiveBootstrap))
	b.WriteString("bootstrap_context_seed: inherited durable prompt context (no wizard override)\n")
	fmt.Fprintf(&b, "autonomy: %s\n", strings.TrimSpace(wizard.Answers.Autonomy))
	fmt.Fprintf(&b, "wakeup_mode: %s\n", strings.TrimSpace(wizard.Answers.WakeupMode))
	fmt.Fprintf(&b, "poll_interval: %s\n", strings.TrimSpace(wizard.Answers.PollInterval))
	fmt.Fprintf(&b, "synthesis_cadence: %s\n", strings.TrimSpace(wizard.Answers.SynthesisCadence))
	fmt.Fprintf(&b, "charter: %s\n", strings.TrimSpace(wizard.Answers.Charter))
	return b.String()
}

func renderDurableAgentWizardFinalize(agent core.DurableAgent, wizard core.DurableAgentSetupWizardState, inheritedBootstrap core.NodeLLMBootstrap) string {
	var b strings.Builder
	channelKind := normalizeDurableAgentChannelKind(strings.TrimSpace(agent.ChannelKind))
	profile := strings.TrimSpace(core.NormalizeDurableAgentSetupWizardAnswers(wizard.Answers).BootstrapProfile)
	if profile == "" {
		profile = durableAgentWizardBootstrapProfile(agent.BootstrapLLM, inheritedBootstrap)
	}
	bootstrap := core.NormalizeNodeLLMBootstrap(agent.BootstrapLLM)
	b.WriteString("action: durable-agent wizard finalize\n")
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "channel_kind: %s\n", channelKind)
	fmt.Fprintf(&b, "channel_profile: %s\n", durableAgentWizardDisplayChannelKind(channelKind))
	fmt.Fprintf(&b, "mode: %s\n", durableAgentModeFromPolicy(agent.LivePolicy))
	fmt.Fprintf(&b, "status: %s\n", firstNonEmpty(strings.TrimSpace(agent.Status), "draft"))
	fmt.Fprintf(&b, "wizard_status: %s\n", firstNonEmpty(strings.TrimSpace(wizard.Status), "finalized"))
	fmt.Fprintf(&b, "bootstrap_profile: %s\n", profile)
	fmt.Fprintf(&b, "bootstrap_backend: %s\n", strings.TrimSpace(bootstrap.Backend))
	fmt.Fprintf(&b, "bootstrap_native_provider: %s\n", strings.TrimSpace(bootstrap.NativeProvider))
	fmt.Fprintf(&b, "bootstrap_model: %s\n", strings.TrimSpace(bootstrap.Model))
	fmt.Fprintf(&b, "bootstrap_fallback: %s\n", durableAgentWizardBootstrapFallbackSummary(bootstrap))
	b.WriteString("bootstrap_context_seed: inherited durable prompt context (no wizard override)\n")
	fmt.Fprintf(&b, "wakeup_mode: %s\n", strings.TrimSpace(agent.WakeupMode))
	fmt.Fprintf(&b, "outbound_mode: %s\n", strings.TrimSpace(agent.LivePolicy.OutboundMode))
	renderDurableAgentChannelConfig(&b, agent)
	b.WriteString("next: connection_test then activate\n")
	return b.String()
}

func durableAgentWizardMode(agent core.DurableAgent, answers core.DurableAgentSetupWizardAnswers) string {
	if mode := core.NormalizeDurableAgentMode(answers.Mode); mode != "" {
		return mode
	}
	if mode := durableAgentModeFromPolicy(agent.LivePolicy); mode != "" {
		return mode
	}
	switch normalizeDurableAgentChannelKind(agent.ChannelKind) {
	case "external_channel":
		return "external"
	default:
		return "live"
	}
}
