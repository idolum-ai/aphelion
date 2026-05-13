//go:build linux

package durableagent

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

func (h *HTTPHandler) acceptControlEnvelope(w http.ResponseWriter, r *http.Request, envelope core.DurableAgentControlEnvelope) bool {
	identity, err := h.controlPeerIdentity(r, envelope.AgentID)
	if err != nil {
		writeError(w, http.StatusForbidden, err)
		return false
	}
	if h.replayControlReceipt(w, envelope) {
		return false
	}
	if h.RequirePeerIdentity {
		err = h.control.AcceptEnvelopeFromTailnetPeer(envelope, identity, h.now())
	} else {
		err = h.control.AcceptEnvelope(envelope, h.now())
	}
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "replay durable agent control envelope") && h.replayControlReceipt(w, envelope) {
			return false
		}
		writeError(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

func (h *HTTPHandler) replayControlReceipt(w http.ResponseWriter, envelope core.DurableAgentControlEnvelope) bool {
	if h == nil || h.store == nil {
		writeError(w, http.StatusInternalServerError, errors.New("durable agent control plane store is nil"))
		return true
	}
	receipt, err := h.store.DurableAgentControlReceipt(envelope.AgentID, envelope.MessageID)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return true
	}
	if strings.TrimSpace(receipt.Signature) != strings.TrimSpace(envelope.Signature) ||
		strings.TrimSpace(receipt.MessageKind) != strings.TrimSpace(envelope.MessageKind) ||
		receipt.Sequence != envelope.Sequence {
		writeError(w, http.StatusConflict, errors.New("durable agent control receipt does not match this envelope"))
		return true
	}
	if receipt.ResponseStatus > 0 && strings.TrimSpace(receipt.ResponseJSON) != "" {
		writeRawJSON(w, receipt.ResponseStatus, []byte(receipt.ResponseJSON))
		return true
	}
	writeError(w, http.StatusConflict, errors.New("durable agent control envelope is already accepted"))
	return true
}

func (h *HTTPHandler) writeControlJSON(w http.ResponseWriter, envelope core.DurableAgentControlEnvelope, status int, payload any) {
	raw, err := encodeJSONPayload(payload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if h == nil || h.store == nil {
		writeError(w, http.StatusInternalServerError, errors.New("durable agent control plane store is nil"))
		return
	}
	if err := h.store.StoreDurableAgentControlReceiptResponse(envelope.AgentID, envelope.MessageID, status, string(raw)); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeRawJSON(w, status, raw)
}
