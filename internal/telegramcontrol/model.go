//go:build linux

package telegramcontrol

import (
	"fmt"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"time"
)

func (c CommandControl) ModelSlotStatuses() ([]core.ModelSlotStatus, error) {
	if c.Runtime == nil {
		return nil, fmt.Errorf("runtime is not configured")
	}
	return c.Runtime.ModelSlotStatuses()
}

func (c CommandControl) ValidateModelSlotConfig(cfg core.ModelSlotConfig) core.ModelValidation {
	if c.Runtime == nil {
		validation := core.ValidateModelSlotConfig(cfg, core.ModelSlotUsesTools(cfg.Slot))
		validation.Valid = false
		validation.Error = "runtime is not configured"
		return validation
	}
	return c.Runtime.ValidateModelSlotConfig(cfg)
}

func (c CommandControl) SetModelSlotConfig(cfg core.ModelSlotConfig, actor string, reason string, ttl time.Duration) (core.ModelSlotStatus, error) {
	if c.Runtime == nil {
		return core.ModelSlotStatus{}, fmt.Errorf("runtime is not configured")
	}
	return c.Runtime.SetModelSlotOverride(cfg, actor, reason, ttl)
}

func (c CommandControl) RollbackModelSlot(slot string, actor string, reason string) (core.ModelSlotStatus, error) {
	if c.Runtime == nil {
		return core.ModelSlotStatus{}, fmt.Errorf("runtime is not configured")
	}
	return c.Runtime.RollbackModelSlot(slot, actor, reason)
}

func (c CommandControl) ClearModelSlot(slot string, actor string, reason string) (core.ModelSlotStatus, error) {
	if c.Runtime == nil {
		return core.ModelSlotStatus{}, fmt.Errorf("runtime is not configured")
	}
	return c.Runtime.ClearModelSlot(slot, actor, reason)
}

func (c CommandControl) ModelSlotHistory(slot string, limit int) ([]session.ModelSlotOverrideRecord, error) {
	if c.Runtime == nil {
		return nil, fmt.Errorf("runtime is not configured")
	}
	return c.Runtime.ModelSlotHistory(slot, limit)
}
