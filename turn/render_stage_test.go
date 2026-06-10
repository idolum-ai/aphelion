//go:build linux

package turn

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/prompt"
)

func TestRunRenderStageSuppressesRenderAndUsesFloorFallbackMode(t *testing.T) {
	t.Parallel()

	renderCalled := false
	got, err := RunRenderStage(context.Background(), RenderStageRequest{
		Render: FaceRenderRequest{
			LatestUserInput: "/help",
			FloorText:       "(no response)",
			Runtime:         prompt.RuntimeAwareness{DeliveryMode: ""},
		},
		FacePolicy:   pipeline.FacePolicy{Render: true},
		InitialReply: "fallback text",
	}, RenderStageCallbacks{
		Render: func(context.Context, FaceRenderRequest) (*FaceRenderResult, error) {
			renderCalled = true
			return &FaceRenderResult{Text: "unexpected"}, nil
		},
	})
	if err != nil {
		t.Fatalf("RunRenderStage() err = %v", err)
	}
	if renderCalled {
		t.Fatal("render callback called for suppressed render path")
	}
	if got.ShouldRender {
		t.Fatal("ShouldRender = true, want false")
	}
	if got.Runtime.DeliveryMode != "floor_fallback" {
		t.Fatalf("Runtime.DeliveryMode = %q, want floor_fallback", got.Runtime.DeliveryMode)
	}
	if got.ReplyText != "fallback text" {
		t.Fatalf("ReplyText = %q, want fallback text", got.ReplyText)
	}
}

func TestRunRenderStageStreamSuccessFallsBackWhenEmpty(t *testing.T) {
	t.Parallel()

	renderCalled := false
	got, err := RunRenderStage(context.Background(), RenderStageRequest{
		Render: FaceRenderRequest{
			LatestUserInput: "show me a diagram",
			FloorText:       "floor text",
			MaterialFloor: core.MaterialPacket{
				Facts: []string{"fact"},
			},
			Runtime: prompt.RuntimeAwareness{},
		},
		FacePolicy:       pipeline.FacePolicy{Render: true},
		AllowStream:      true,
		UseMaterialFloor: true,
		InitialReply:     "initial",
		FallbackOptions:  pipeline.FallbackOptions{Channel: "telegram"},
	}, RenderStageCallbacks{
		Stream: func(context.Context, FaceRenderRequest) (FaceRenderResult, bool, error) {
			return FaceRenderResult{
				Text:         "",
				Streamed:     true,
				RenderedID:   77,
				RenderedType: "streaming",
				Usage:        core.TokenUsage{OutputTokens: 5},
			}, true, nil
		},
		Render: func(context.Context, FaceRenderRequest) (*FaceRenderResult, error) {
			renderCalled = true
			return &FaceRenderResult{Text: "unexpected"}, nil
		},
		Fallback: func(core.MaterialPacket, string, pipeline.FallbackOptions) string {
			return "fallback from material"
		},
	})
	if err != nil {
		t.Fatalf("RunRenderStage() err = %v", err)
	}
	if renderCalled {
		t.Fatal("render callback called after successful stream path")
	}
	if !got.Streamed {
		t.Fatal("Streamed = false, want true")
	}
	if got.RenderedID != 77 {
		t.Fatalf("RenderedID = %d, want 77", got.RenderedID)
	}
	if got.RenderedType != "streaming" {
		t.Fatalf("RenderedType = %q, want streaming", got.RenderedType)
	}
	if got.ReplyText != "fallback from material" {
		t.Fatalf("ReplyText = %q, want fallback from material", got.ReplyText)
	}
}

func TestRunRenderStageStreamDeclineFallsBackToNonStreamRender(t *testing.T) {
	t.Parallel()

	got, err := RunRenderStage(context.Background(), RenderStageRequest{
		Render: FaceRenderRequest{
			LatestUserInput: "what now",
			FloorText:       "floor text",
			Runtime:         prompt.RuntimeAwareness{},
		},
		FacePolicy:  pipeline.FacePolicy{Render: true},
		AllowStream: true,
	}, RenderStageCallbacks{
		Stream: func(context.Context, FaceRenderRequest) (FaceRenderResult, bool, error) {
			return FaceRenderResult{}, false, nil
		},
		Render: func(context.Context, FaceRenderRequest) (*FaceRenderResult, error) {
			return &FaceRenderResult{Text: "rendered reply"}, nil
		},
	})
	if err != nil {
		t.Fatalf("RunRenderStage() err = %v", err)
	}
	if got.ReplyText != "rendered reply" {
		t.Fatalf("ReplyText = %q, want rendered reply", got.ReplyText)
	}
	if got.Streamed {
		t.Fatal("Streamed = true, want false")
	}
}

func TestRunRenderStageRenderErrorIsCaptured(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("render failed")
	got, err := RunRenderStage(context.Background(), RenderStageRequest{
		Render: FaceRenderRequest{
			LatestUserInput: "what now",
			FloorText:       "floor text",
			Runtime:         prompt.RuntimeAwareness{},
		},
		FacePolicy:   pipeline.FacePolicy{Render: true},
		InitialReply: "initial",
	}, RenderStageCallbacks{
		Render: func(context.Context, FaceRenderRequest) (*FaceRenderResult, error) {
			return nil, wantErr
		},
	})
	if err != nil {
		t.Fatalf("RunRenderStage() err = %v", err)
	}
	if !errors.Is(got.RenderError, wantErr) {
		t.Fatalf("RenderError = %v, want %v", got.RenderError, wantErr)
	}
	if got.ReplyText != "initial" {
		t.Fatalf("ReplyText = %q, want initial", got.ReplyText)
	}
}

func TestRunRenderStageSkipRenderBypassesCallbacks(t *testing.T) {
	t.Parallel()

	called := false
	got, err := RunRenderStage(context.Background(), RenderStageRequest{
		Render: FaceRenderRequest{
			LatestUserInput: "ping",
			FloorText:       "floor",
			Runtime:         prompt.RuntimeAwareness{},
		},
		FacePolicy:   pipeline.FacePolicy{Render: true},
		InitialReply: "initial",
		SkipRender:   true,
	}, RenderStageCallbacks{
		Stream: func(context.Context, FaceRenderRequest) (FaceRenderResult, bool, error) {
			called = true
			return FaceRenderResult{}, false, nil
		},
		Render: func(context.Context, FaceRenderRequest) (*FaceRenderResult, error) {
			called = true
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("RunRenderStage() err = %v", err)
	}
	if called {
		t.Fatal("render callbacks called on skip-render path")
	}
	if got.ReplyText != "initial" {
		t.Fatalf("ReplyText = %q, want initial", got.ReplyText)
	}
	if got.Runtime.DeliveryMode != "idolum_render" {
		t.Fatalf("Runtime.DeliveryMode = %q, want idolum_render", got.Runtime.DeliveryMode)
	}
}

func TestRunRenderStageConditionalSkipBypassesCallbacks(t *testing.T) {
	t.Parallel()

	called := false
	got, err := RunRenderStage(context.Background(), RenderStageRequest{
		Render: FaceRenderRequest{
			LatestUserInput: "deployment status",
			FloorText:       "PR #140 deployed and verified.",
			Runtime:         prompt.RuntimeAwareness{},
		},
		FacePolicy:            pipeline.FacePolicy{Render: true},
		InitialReply:          "fallback status",
		ConditionalSkipReason: string(FaceSkipReasonMaterialStatusReport),
	}, RenderStageCallbacks{
		Stream: func(context.Context, FaceRenderRequest) (FaceRenderResult, bool, error) {
			called = true
			return FaceRenderResult{}, false, nil
		},
		Render: func(context.Context, FaceRenderRequest) (*FaceRenderResult, error) {
			called = true
			return &FaceRenderResult{Text: "unexpected"}, nil
		},
	})
	if err != nil {
		t.Fatalf("RunRenderStage() err = %v", err)
	}
	if called {
		t.Fatal("render callbacks called for conditional skip path")
	}
	if got.ShouldRender {
		t.Fatal("ShouldRender = true, want false after conditional skip")
	}
	if got.SkipReason != FaceSkipReasonMaterialStatusReport {
		t.Fatalf("SkipReason = %q, want material_status_report", got.SkipReason)
	}
	if got.Runtime.DeliveryMode != "floor_fallback" {
		t.Fatalf("DeliveryMode = %q, want floor_fallback", got.Runtime.DeliveryMode)
	}
	if got.ReplyText != "fallback status" {
		t.Fatalf("ReplyText = %q, want fallback status", got.ReplyText)
	}
}

func TestRunRenderStageFallsBackWhenOperationalFaceRenderIsPartial(t *testing.T) {
	t.Parallel()

	floorText := strings.Join([]string{
		"Recovery evidence says the reinstall/restart repair did finish successfully.",
		"Current state:",
		"- Service active/running: PID `100755`",
		"- Started: `Fri 2026-06-05 10:55:04 UTC`",
		"- Executable: `/opt/aphelion/bin/aphelion`",
		"- Revision: `37928e5ecc7f624a0284df26bf70b7b9ac89ddbd`",
		"- `verify-deploy --format=kv` passed.",
		"What likely happened:",
		"- The direct reinstall used the correct Go environment.",
		"- `go build` succeeded.",
	}, "\n")
	fallbackText := "complete floor fallback"

	got, err := RunRenderStage(context.Background(), RenderStageRequest{
		Render: FaceRenderRequest{
			LatestUserInput: "continue",
			FloorText:       floorText,
			MaterialFloor: core.MaterialPacket{
				Kind:  core.MaterialPacketKindStatusReport,
				Facts: []string{"Service active/running: PID 100755", "verify-deploy passed", "revision 37928e5"},
			},
			Runtime: prompt.RuntimeAwareness{},
		},
		FacePolicy:       pipeline.FacePolicy{Render: true},
		UseMaterialFloor: true,
		InitialReply:     floorText,
		FallbackOptions:  pipeline.FallbackOptions{Channel: "telegram"},
	}, RenderStageCallbacks{
		Render: func(context.Context, FaceRenderRequest) (*FaceRenderResult, error) {
			return &FaceRenderResult{Text: "The reinstall repair is clean now.\n\nEvidence:\n- Service is active as PID `100755`\n\nWhat likely happened: the direct"}, nil
		},
		Fallback: func(core.MaterialPacket, string, pipeline.FallbackOptions) string {
			return fallbackText
		},
	})
	if err != nil {
		t.Fatalf("RunRenderStage() err = %v", err)
	}
	if got.ReplyText != fallbackText {
		t.Fatalf("ReplyText = %q, want floor fallback", got.ReplyText)
	}
	if !got.FallbackApplied || got.FallbackReason != "partial_face_render" {
		t.Fatalf("fallback = %v/%q, want partial_face_render", got.FallbackApplied, got.FallbackReason)
	}
	if got.Runtime.DeliveryMode != "floor_fallback" {
		t.Fatalf("DeliveryMode = %q, want floor_fallback", got.Runtime.DeliveryMode)
	}
}

func TestRunRenderStageFallsBackWhenGenericFaceRenderIsPartial(t *testing.T) {
	t.Parallel()

	floorText := strings.Repeat("Service active at PID 100755 and verify-deploy passed. ", 8)
	renderedText := "Service active at PID 100755 and verify-deploy"
	got, err := RunRenderStage(context.Background(), RenderStageRequest{
		Render: FaceRenderRequest{
			LatestUserInput: "status",
			FloorText:       floorText,
			MaterialFloor: core.MaterialPacket{
				Facts: []string{"Service active at PID 100755.", "verify-deploy passed."},
			},
		},
		FacePolicy:       pipeline.FacePolicy{Render: true},
		UseMaterialFloor: true,
		InitialReply:     floorText,
	}, RenderStageCallbacks{
		Render: func(context.Context, FaceRenderRequest) (*FaceRenderResult, error) {
			return &FaceRenderResult{Text: renderedText}, nil
		},
		Fallback: func(core.MaterialPacket, string, pipeline.FallbackOptions) string {
			return "complete generic fallback"
		},
	})
	if err != nil {
		t.Fatalf("RunRenderStage() err = %v", err)
	}
	if got.ReplyText != "complete generic fallback" {
		t.Fatalf("ReplyText = %q, want generic floor fallback", got.ReplyText)
	}
	if !got.FallbackApplied || got.FallbackReason != "partial_face_render" {
		t.Fatalf("fallback = %v/%q, want partial_face_render", got.FallbackApplied, got.FallbackReason)
	}
}

func TestRunRenderStageKeepsCompleteOperationalFaceRender(t *testing.T) {
	t.Parallel()

	floorText := strings.Repeat("Service verified from main. ", 20)
	renderedText := strings.Repeat("Service verified from main. ", 14) + "Ready."
	got, err := RunRenderStage(context.Background(), RenderStageRequest{
		Render: FaceRenderRequest{
			LatestUserInput: "status",
			FloorText:       floorText,
			MaterialFloor:   core.MaterialPacket{Kind: core.MaterialPacketKindStatusReport, Facts: []string{"Service verified from main."}},
		},
		FacePolicy:       pipeline.FacePolicy{Render: true},
		UseMaterialFloor: true,
		InitialReply:     floorText,
	}, RenderStageCallbacks{
		Render: func(context.Context, FaceRenderRequest) (*FaceRenderResult, error) {
			return &FaceRenderResult{Text: renderedText}, nil
		},
		Fallback: func(core.MaterialPacket, string, pipeline.FallbackOptions) string {
			return "unexpected fallback"
		},
	})
	if err != nil {
		t.Fatalf("RunRenderStage() err = %v", err)
	}
	if got.ReplyText != renderedText {
		t.Fatalf("ReplyText = %q, want rendered text", got.ReplyText)
	}
	if got.FallbackApplied || got.FallbackReason != "" {
		t.Fatalf("fallback = %v/%q, want none", got.FallbackApplied, got.FallbackReason)
	}
}

func TestRunRenderStageKeepsSceneConstrainedShortOperationalFaceRender(t *testing.T) {
	t.Parallel()

	floorText := strings.Join([]string{
		"PR #140 deployed and verified.",
		"The operator asked for a warm, brief visible reply.",
	}, "\n")
	renderedText := "scene-aware face render"
	got, err := RunRenderStage(context.Background(), RenderStageRequest{
		Render: FaceRenderRequest{
			LatestUserInput: "report deploy status",
			FloorText:       floorText,
			MaterialFloor: core.MaterialPacket{
				Kind:             core.MaterialPacketKindStatusReport,
				Facts:            []string{"PR #140 deployed and verified."},
				SceneConstraints: []string{"Keep the visible reply warm and brief."},
			},
		},
		FacePolicy:       pipeline.FacePolicy{Render: true},
		UseMaterialFloor: true,
		InitialReply:     floorText,
	}, RenderStageCallbacks{
		Render: func(context.Context, FaceRenderRequest) (*FaceRenderResult, error) {
			return &FaceRenderResult{Text: renderedText}, nil
		},
		Fallback: func(core.MaterialPacket, string, pipeline.FallbackOptions) string {
			return "unexpected fallback"
		},
	})
	if err != nil {
		t.Fatalf("RunRenderStage() err = %v", err)
	}
	if got.ReplyText != renderedText {
		t.Fatalf("ReplyText = %q, want rendered text", got.ReplyText)
	}
	if got.FallbackApplied || got.FallbackReason != "" {
		t.Fatalf("fallback = %v/%q, want none", got.FallbackApplied, got.FallbackReason)
	}
}
