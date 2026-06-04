//go:build linux

package main

import (
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func (c telegramCommandControl) RecordTelegramMediaThreadPicker(chatID int64, pickerMessageID int64, inbound core.InboundMessage) error {
	if c.store == nil {
		return nil
	}
	return c.store.RecordTelegramMediaThreadPicker(chatID, pickerMessageID, inbound, time.Now().UTC())
}

func (c telegramCommandControl) TelegramMediaThreadPicker(chatID int64, pickerMessageID int64) (core.InboundMessage, bool, error) {
	if c.store == nil {
		return core.InboundMessage{}, false, nil
	}
	rec, ok, err := c.store.TelegramMediaThreadPicker(chatID, pickerMessageID)
	if err != nil || !ok {
		return core.InboundMessage{}, ok, err
	}
	return rec.Inbound, true, nil
}

func (c telegramCommandControl) MarkTelegramMediaThreadPickerRouted(chatID int64, pickerMessageID int64) error {
	if c.store == nil {
		return nil
	}
	return c.store.MarkTelegramMediaThreadPickerStatus(chatID, pickerMessageID, "routed", time.Now().UTC())
}
