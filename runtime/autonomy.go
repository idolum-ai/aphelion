//go:build linux

package runtime

import (
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
)

const autonomyAuthorityBehavior = "existing proposal and approval flows"

func (r *Runtime) AutonomyStatusSnapshot() core.AutonomyStatusSnapshot {
	policy := config.EffectiveAutonomyPolicy(nil)
	source := "default"
	if r != nil && r.cfg != nil {
		policy = config.EffectiveAutonomyPolicy(r.cfg)
		source = "config"
	}
	return core.AutonomyStatusSnapshot{
		GeneratedAt:         time.Now().UTC(),
		DefaultMode:         policy.DefaultMode,
		Ceiling:             policy.Ceiling,
		AllowLiveOverrides:  policy.AllowLiveOverrides,
		MaxOverrideDuration: policy.MaxOverrideDuration,
		Source:              source,
		AuthorityBehavior:   autonomyAuthorityBehavior,
	}
}
