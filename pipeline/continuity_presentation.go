//go:build linux

package pipeline

import "github.com/idolum-ai/aphelion/core"

type Intent string

const (
	IntentUnspecified        Intent = ""
	IntentContinuityQuestion Intent = "continuity_question"
)

type PresentationDecision struct {
	Background []core.MaterialContinuityContext
	Visible    []core.MaterialContinuityContext
}

func ContinuityPresentationPolicy(ctx []core.MaterialContinuityContext, userIntent Intent) PresentationDecision {
	decision := PresentationDecision{
		Background: make([]core.MaterialContinuityContext, 0, len(ctx)),
		Visible:    make([]core.MaterialContinuityContext, 0, len(ctx)),
	}
	for _, item := range ctx {
		item = item.Normalized()
		if item.Empty() {
			continue
		}
		if continuityContextIsVisible(item, userIntent) {
			decision.Visible = append(decision.Visible, item)
			continue
		}
		decision.Background = append(decision.Background, item)
	}
	return decision
}

func continuityContextIsVisible(item core.MaterialContinuityContext, userIntent Intent) bool {
	switch item.Visibility {
	case core.MaterialContinuityVisibilityMustSurface, core.MaterialContinuityVisibilityUserRelevant:
		return true
	case core.MaterialContinuityVisibilityInternal:
		return userIntent == IntentContinuityQuestion
	default:
		return false
	}
}
