//go:build linux

package durableagent

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const (
	ControlPlaneEnrollPath         = "/v1/durable-agent/enroll"
	ControlPlanePolicyPollPath     = "/v1/durable-agent/policy/poll"
	ControlPlaneArtifactUploadPath = "/v1/durable-agent/review-artifact"
	ControlPlanePolicyAckPath      = "/v1/durable-agent/policy/ack"
)

type HTTPStore interface {
	ControlPlaneStore
	InsertReviewEvent(event session.ReviewEvent) (int64, error)
	UpsertDurableAgentRemoteEnrollment(enrollment core.DurableAgentRemoteEnrollment) error
	DurableAgentRemoteEnrollment(agentID string) (*core.DurableAgentRemoteEnrollment, error)
}

type HTTPHandler struct {
	store    HTTPStore
	control  *ControlPlane
	review   *Runtime
	clock    func() time.Time
	Verifier EnvelopeVerifier
}

type EnvelopeVerifier func(envelope core.DurableAgentControlEnvelope) error

func NewHTTPHandler(store HTTPStore) *HTTPHandler {
	return &HTTPHandler{
		store:   store,
		control: NewControlPlane(store, 10*time.Minute),
		review:  NewRuntime(store),
		clock:   func() time.Time { return time.Now().UTC() },
	}
}

func (h *HTTPHandler) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(ControlPlaneEnrollPath, h.handleEnroll)
	mux.HandleFunc(ControlPlanePolicyPollPath, h.handlePolicyPoll)
	mux.HandleFunc(ControlPlaneArtifactUploadPath, h.handleArtifactUpload)
	mux.HandleFunc(ControlPlanePolicyAckPath, h.handlePolicyAck)
	return mux
}

func (h *HTTPHandler) handleEnroll(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req core.DurableAgentEnrollmentRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	req.Envelope = core.NormalizeDurableAgentControlEnvelope(req.Envelope)
	req.Payload = core.NormalizeDurableAgentEnrollmentPayload(req.Payload)
	if err := core.ValidateDurableAgentControlEnvelope(req.Envelope); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := h.verifyEnvelope(req.Envelope); err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	if err := core.ValidateDurableAgentEnrollmentPayload(req.Payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Envelope.MessageKind != core.DurableAgentControlMessageEnrollment {
		writeError(w, http.StatusBadRequest, errors.New("durable agent enrollment requires message_kind=enrollment"))
		return
	}
	agent, err := h.store.DurableAgent(req.Payload.AgentID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	enrollment := core.NormalizeDurableAgentRemoteEnrollment(core.DurableAgentRemoteEnrollment{
		AgentID:          req.Payload.AgentID,
		ParentControlURL: req.Payload.ParentControlURL,
		KeyFingerprint:   req.Payload.KeyFingerprint,
		ProtocolVersion:  req.Payload.ProtocolVersion,
		Status:           "active",
	})
	if err := h.store.UpsertDurableAgentRemoteEnrollment(enrollment); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := h.control.AcceptEnvelope(req.Envelope, h.now()); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	snapshot, err := h.control.PolicySnapshot(agent.AgentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	registered, err := h.store.DurableAgentRemoteEnrollment(agent.AgentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, core.DurableAgentEnrollmentResponse{
		Enrollment: *registered,
		Policy:     snapshot,
	})
}

func (h *HTTPHandler) handlePolicyPoll(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req core.DurableAgentPolicyPollRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	req.Envelope = core.NormalizeDurableAgentControlEnvelope(req.Envelope)
	req.KnownHash = strings.TrimSpace(req.KnownHash)
	if req.Envelope.MessageKind != core.DurableAgentControlMessagePolicyPoll {
		writeError(w, http.StatusBadRequest, errors.New("durable agent policy poll requires message_kind=policy_poll"))
		return
	}
	if err := h.verifyEnvelope(req.Envelope); err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	if err := h.control.AcceptEnvelope(req.Envelope, h.now()); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	snapshot, err := h.control.PolicySnapshot(req.Envelope.AgentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	changed := req.KnownVersion != snapshot.PolicyVersion || req.KnownHash != snapshot.PolicyHash
	writeJSON(w, http.StatusOK, core.DurableAgentPolicyPollResponse{
		Snapshot: snapshot,
		Changed:  changed,
	})
}

func (h *HTTPHandler) handleArtifactUpload(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req core.DurableAgentReviewArtifactUploadRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	req.Envelope = core.NormalizeDurableAgentControlEnvelope(req.Envelope)
	if req.Envelope.MessageKind != core.DurableAgentControlMessageReviewArtifactUpload {
		writeError(w, http.StatusBadRequest, errors.New("durable agent review artifact upload requires message_kind=review_artifact_upload"))
		return
	}
	if err := h.verifyEnvelope(req.Envelope); err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	if err := h.control.AcceptEnvelope(req.Envelope, h.now()); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	agent, err := h.store.DurableAgent(req.Envelope.AgentID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	eventID, err := h.review.QueueReviewArtifact(*agent, req.Artifact)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusAccepted, core.DurableAgentReviewArtifactUploadResponse{
		Accepted:      true,
		ReviewEventID: eventID,
	})
}

func (h *HTTPHandler) handlePolicyAck(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req core.DurableAgentPolicyAcknowledgementRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	req.Envelope = core.NormalizeDurableAgentControlEnvelope(req.Envelope)
	if req.Envelope.MessageKind != core.DurableAgentControlMessagePolicyAck {
		writeError(w, http.StatusBadRequest, errors.New("durable agent policy acknowledgement requires message_kind=policy_ack"))
		return
	}
	if err := h.verifyEnvelope(req.Envelope); err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	if err := h.control.AcceptPolicyAcknowledgement(req.Envelope, req.Ack, h.now()); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, core.DurableAgentPolicyAcknowledgementResponse{Accepted: true})
}

func (h *HTTPHandler) now() time.Time {
	if h != nil && h.clock != nil {
		return h.clock().UTC()
	}
	return time.Now().UTC()
}

func (h *HTTPHandler) verifyEnvelope(envelope core.DurableAgentControlEnvelope) error {
	if h == nil || h.Verifier == nil {
		return nil
	}
	return h.Verifier(envelope)
}

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

func decodeRequest(w http.ResponseWriter, r *http.Request, out any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(payload)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
