//go:build linux

package runtime

import (
	"context"
	"strconv"
	"time"
)

type activeStreamControl struct {
	ChatID    int64
	MessageID int64
	Cancel    context.CancelFunc
	CreatedAt time.Time
}

func (r *Runtime) beginStreamControl(chatID int64) string {
	if r == nil || chatID == 0 {
		return ""
	}
	id := strconv.FormatUint(r.streamControlSeq.Add(1), 36)
	r.streamControlMu.Lock()
	defer r.streamControlMu.Unlock()
	if r.streamControls == nil {
		r.streamControls = make(map[string]activeStreamControl)
	}
	r.streamControls[id] = activeStreamControl{
		ChatID:    chatID,
		CreatedAt: time.Now().UTC(),
	}
	return id
}

func (r *Runtime) attachStreamControlCancel(streamID string, cancel context.CancelFunc) {
	if r == nil || streamID == "" || cancel == nil {
		return
	}
	r.streamControlMu.Lock()
	defer r.streamControlMu.Unlock()
	control, ok := r.streamControls[streamID]
	if !ok {
		return
	}
	control.Cancel = cancel
	r.streamControls[streamID] = control
}

func (r *Runtime) attachStreamControlMessage(streamID string, messageID int64) {
	if r == nil || streamID == "" || messageID == 0 {
		return
	}
	r.streamControlMu.Lock()
	defer r.streamControlMu.Unlock()
	control, ok := r.streamControls[streamID]
	if !ok {
		return
	}
	control.MessageID = messageID
	r.streamControls[streamID] = control
}

func (r *Runtime) finishStreamControl(streamID string) {
	if r == nil || streamID == "" {
		return
	}
	r.streamControlMu.Lock()
	defer r.streamControlMu.Unlock()
	delete(r.streamControls, streamID)
}

func (r *Runtime) MarkStreamControlStopping(streamID string, chatID int64) bool {
	if r == nil || streamID == "" || chatID == 0 {
		return false
	}
	r.streamControlMu.Lock()
	defer r.streamControlMu.Unlock()
	control, ok := r.streamControls[streamID]
	if !ok || control.ChatID != chatID {
		return false
	}
	cancel := control.Cancel
	delete(r.streamControls, streamID)
	if cancel != nil {
		cancel()
	}
	return true
}
