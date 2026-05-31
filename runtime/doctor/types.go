//go:build linux

package doctor

import (
	"context"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/workspace"
)

const (
	TimeFormat = time.RFC3339

	RequestMarker             = "DOCTOR_DIAGNOSTIC_REQUEST"
	SummaryMarker             = "DOCTOR_TELEGRAM_SUMMARY_REQUEST"
	ReportFallbackText        = "Health diagnosis finished, but the model returned an empty report."
	doctorMaintainerArchetype = "aphelion-maintainer"
	PacketMaxChars            = 120000
	LogTailBytes              = 16000
	FilePreviewChars          = 700
	MessageLimit              = 12
	TelegramMaxChars          = 3800
	TelegramHardLimit         = 4096
)

type WorkExecutorStatus struct {
	Configured     string
	Preferred      string
	Active         string
	LastAttempted  string
	FallbackReason string
	LastError      string
	UpdatedAt      time.Time
}

type DiagnosticInput struct {
	Message       core.InboundMessage
	Actor         principal.Principal
	Key           session.SessionKey
	Session       *session.Session
	Scope         sandbox.Scope
	PromptContext *workspace.PromptContext
	Exec          pipeline.TurnExecutionContract
	Maintainer    *MaintainerDelegate
	Now           time.Time
}

type Dependencies struct {
	Config   *config.Config
	Store    *session.SQLiteStore
	Provider agent.Provider

	WorkExecutorStatus                            func() WorkExecutorStatus
	GovernorName                                  func() string
	FaceName                                      func() string
	AutonomyStatusSnapshot                        func() core.AutonomyStatusSnapshot
	AutonomyStatusSnapshotForChat                 func(chatID int64, adminUserID int64, now time.Time) core.AutonomyStatusSnapshot
	ValidateAutonomyLiveOverride                  func(mode string, duration time.Duration) error
	ShouldRouteContinuationThroughWorkExecutor    func(state session.ContinuationState) bool
	SandboxReadinessSnapshot                      func(now time.Time) core.SandboxReadinessSnapshot
	ToolLifecycleStatusSnapshot                   func(limit int) ([]core.ToolLifecycleStatusSnapshot, error)
	CapabilityStatusSnapshot                      func(limit int) ([]core.CapabilityRequestStatusSnapshot, []core.CapabilityGrantStatusSnapshot, error)
	ExternalToolInvocationReadinessStatusSnapshot func(tools []core.ToolLifecycleStatusSnapshot, grants []core.CapabilityGrantStatusSnapshot) []core.ExternalToolInvocationReadinessSnapshot
	TailnetStatusSnapshot                         func(ctx context.Context) (core.TailnetStatusSnapshot, error)
	StaleRunningTurnRuns                          func(now time.Time) ([]session.TurnRun, error)
	WriteAuthorityProjection                      func(b *strings.Builder, now time.Time)
	WriteProviderHealth                           func(b *strings.Builder, now time.Time)
	WritePerceptionBudget                         func(b *strings.Builder, key session.SessionKey, now time.Time)
	WriteExternalChannelAdapterReadiness          func(b *strings.Builder, input DiagnosticInput)
	ReasoningOptionsForRun                        func(kind session.TurnRunKind) *agent.CompleteOptions
	RecordExecutionEvent                          func(key session.SessionKey, eventType string, stage string, status string, payload map[string]any, createdAt time.Time)
	ReportOperationalIssueAsync                   func(component string, err error)
}

type Runtime struct {
	cfg      *config.Config
	store    *session.SQLiteStore
	provider agent.Provider

	workExecutorStatus                            func() WorkExecutorStatus
	governorName                                  func() string
	faceName                                      func() string
	AutonomyStatusSnapshot                        func() core.AutonomyStatusSnapshot
	autonomyStatusSnapshot                        func(chatID int64, adminUserID int64, now time.Time) core.AutonomyStatusSnapshot
	validateAutonomyLiveOverride                  func(mode string, duration time.Duration) error
	shouldRouteContinuationThroughWorkExecutor    func(state session.ContinuationState) bool
	sandboxReadinessSnapshot                      func(now time.Time) core.SandboxReadinessSnapshot
	toolLifecycleStatusSnapshot                   func(limit int) ([]core.ToolLifecycleStatusSnapshot, error)
	capabilityStatusSnapshot                      func(limit int) ([]core.CapabilityRequestStatusSnapshot, []core.CapabilityGrantStatusSnapshot, error)
	externalToolInvocationReadinessStatusSnapshot func(tools []core.ToolLifecycleStatusSnapshot, grants []core.CapabilityGrantStatusSnapshot) []core.ExternalToolInvocationReadinessSnapshot
	TailnetStatusSnapshot                         func(ctx context.Context) (core.TailnetStatusSnapshot, error)
	staleRunningTurnRuns                          func(now time.Time) ([]session.TurnRun, error)
	writeDoctorAuthorityProjection                func(b *strings.Builder, now time.Time)
	writeDoctorProviderHealth                     func(b *strings.Builder, now time.Time)
	writeDoctorPerceptionBudget                   func(b *strings.Builder, key session.SessionKey, now time.Time)
	writeDoctorExternalChannelAdapterReadiness    func(b *strings.Builder, input DiagnosticInput)
	reasoningOptionsForRun                        func(kind session.TurnRunKind) *agent.CompleteOptions
	recordExecutionEvent                          func(key session.SessionKey, eventType string, stage string, status string, payload map[string]any, createdAt time.Time)
	reportOperationalIssueAsync                   func(component string, err error)
}

func NewRuntime(deps Dependencies) *Runtime {
	r := &Runtime{
		cfg:                          deps.Config,
		store:                        deps.Store,
		provider:                     deps.Provider,
		workExecutorStatus:           deps.WorkExecutorStatus,
		governorName:                 deps.GovernorName,
		faceName:                     deps.FaceName,
		AutonomyStatusSnapshot:       deps.AutonomyStatusSnapshot,
		autonomyStatusSnapshot:       deps.AutonomyStatusSnapshotForChat,
		validateAutonomyLiveOverride: deps.ValidateAutonomyLiveOverride,
		shouldRouteContinuationThroughWorkExecutor:    deps.ShouldRouteContinuationThroughWorkExecutor,
		sandboxReadinessSnapshot:                      deps.SandboxReadinessSnapshot,
		toolLifecycleStatusSnapshot:                   deps.ToolLifecycleStatusSnapshot,
		capabilityStatusSnapshot:                      deps.CapabilityStatusSnapshot,
		externalToolInvocationReadinessStatusSnapshot: deps.ExternalToolInvocationReadinessStatusSnapshot,
		TailnetStatusSnapshot:                         deps.TailnetStatusSnapshot,
		staleRunningTurnRuns:                          deps.StaleRunningTurnRuns,
		writeDoctorAuthorityProjection:                deps.WriteAuthorityProjection,
		writeDoctorProviderHealth:                     deps.WriteProviderHealth,
		writeDoctorPerceptionBudget:                   deps.WritePerceptionBudget,
		writeDoctorExternalChannelAdapterReadiness:    deps.WriteExternalChannelAdapterReadiness,
		reasoningOptionsForRun:                        deps.ReasoningOptionsForRun,
		recordExecutionEvent:                          deps.RecordExecutionEvent,
		reportOperationalIssueAsync:                   deps.ReportOperationalIssueAsync,
	}
	return r
}
