//go:build linux

package durableagent

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const (
	ControlPlaneEnrollPath                 = "/v1/durable-agent/enroll"
	ControlPlanePolicyPollPath             = "/v1/durable-agent/policy/poll"
	ControlPlaneArtifactUploadPath         = "/v1/durable-agent/review-artifact"
	ControlPlanePolicyAckPath              = "/v1/durable-agent/policy/ack"
	ControlPlaneParentConversationPollPath = "/v1/durable-agent/parent-conversation/poll"
	ControlPlaneParentConversationAckPath  = "/v1/durable-agent/parent-conversation/ack"
	maxControlPlaneRequestBytes            = 1 << 20
)

var errParentConversationAckRejected = errors.New("durable agent parent conversation acknowledgement rejected")

type HTTPStore interface {
	ControlPlaneStore
	InsertReviewEvent(event session.ReviewEvent) (int64, error)
	DurableAgentRemoteEnrollment(agentID string) (*core.DurableAgentRemoteEnrollment, error)
	DurableAgentControlReceipt(agentID string, messageID string) (*core.DurableAgentControlReceipt, error)
	StoreDurableAgentControlReceiptResponse(agentID string, messageID string, status int, responseJSON string) error
}

type HTTPHandler struct {
	store    HTTPStore
	control  *ControlPlane
	review   *Runtime
	clock    func() time.Time
	Verifier EnvelopeVerifier

	RequirePeerIdentity bool
}

type EnvelopeVerifier func(envelope core.DurableAgentControlEnvelope, payload any) error

func NewHTTPHandler(store HTTPStore) *HTTPHandler {
	return &HTTPHandler{
		store:    store,
		control:  NewControlPlane(store, 10*time.Minute),
		review:   NewRuntime(store),
		clock:    func() time.Time { return time.Now().UTC() },
		Verifier: NewStoreBackedEnvelopeVerifier(store),
	}
}

func (h *HTTPHandler) Handler() http.Handler {
	return h.HandlerWithBasePath("")
}

func (h *HTTPHandler) HandlerWithBasePath(basePath string) http.Handler {
	mux := http.NewServeMux()
	basePath = normalizeControlPlaneBasePath(basePath)
	mux.HandleFunc(path.Join(basePath, ControlPlaneEnrollPath), h.handleEnroll)
	mux.HandleFunc(path.Join(basePath, ControlPlanePolicyPollPath), h.handlePolicyPoll)
	mux.HandleFunc(path.Join(basePath, ControlPlaneArtifactUploadPath), h.handleArtifactUpload)
	mux.HandleFunc(path.Join(basePath, ControlPlanePolicyAckPath), h.handlePolicyAck)
	mux.HandleFunc(path.Join(basePath, ControlPlaneParentConversationPollPath), h.handleParentConversationPoll)
	mux.HandleFunc(path.Join(basePath, ControlPlaneParentConversationAckPath), h.handleParentConversationAck)
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
	if err := h.verifyEnvelope(req.Envelope, req.Payload); err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	if err := core.ValidateDurableAgentEnrollmentPayload(req.Payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Envelope.MessageKind != core.DurableAgentControlMessageEnrollment && req.Envelope.MessageKind != core.DurableAgentControlMessageReattestation {
		writeError(w, http.StatusBadRequest, errors.New("durable agent enrollment requires message_kind=enrollment or reattestation"))
		return
	}
	agent, err := h.store.DurableAgent(req.Payload.AgentID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	identity, err := h.enrollmentPeerIdentity(r)
	if err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	if h.RequirePeerIdentity {
		if err := validateTailnetPeerIdentityForAgent(*agent, identity); err != nil {
			writeError(w, http.StatusForbidden, err)
			return
		}
	}
	existing, err := h.store.DurableAgentRemoteEnrollment(req.Payload.AgentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		existing = nil
	}
	if req.Envelope.MessageKind == core.DurableAgentControlMessageReattestation {
		if existing == nil {
			writeError(w, http.StatusNotFound, errors.New("durable agent reattestation requires an existing enrollment"))
			return
		}
		if existing.Status != "active" {
			writeError(w, http.StatusForbidden, fmt.Errorf("durable agent remote enrollment %s is not active", existing.AgentID))
			return
		}
		if h.RequirePeerIdentity && tailnetIdentityIsBound(existing.TailnetIdentity) && !tailnetStableNodeMatches(existing.TailnetIdentity, identity) {
			writeError(w, http.StatusForbidden, errors.New("durable agent reattestation came from a different tailnet node"))
			return
		}
	} else if h.RequirePeerIdentity && existing != nil && tailnetIdentityIsBound(existing.TailnetIdentity) && !tailnetStableNodeMatches(existing.TailnetIdentity, identity) {
		writeError(w, http.StatusForbidden, errors.New("durable agent enrollment came from a different tailnet node"))
		return
	}
	if h.replayControlReceipt(w, req.Envelope) {
		return
	}
	registered := core.DurableAgentRemoteEnrollment{}
	if existing != nil {
		registered = *existing
	}
	registered.AgentID = req.Payload.AgentID
	registered.ParentControlURL = req.Payload.ParentControlURL
	registered.ProtocolVersion = req.Payload.ProtocolVersion
	registered.Status = "active"
	registered.RevokedAt = time.Time{}
	if h.RequirePeerIdentity {
		registered.TailnetIdentity = identity
	}
	if err := h.control.AcceptEnrollment(req.Envelope, registered, h.now()); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "replay durable agent control envelope") && h.replayControlReceipt(w, req.Envelope) {
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}
	accepted, err := h.store.DurableAgentRemoteEnrollment(agent.AgentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	snapshot, err := h.control.PolicySnapshot(agent.AgentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	h.writeControlJSON(w, req.Envelope, http.StatusOK, core.DurableAgentEnrollmentResponse{
		Enrollment: *accepted,
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
	if err := h.verifyEnvelope(req.Envelope, struct {
		KnownVersion int64  `json:"known_version,omitempty"`
		KnownHash    string `json:"known_hash,omitempty"`
	}{
		KnownVersion: req.KnownVersion,
		KnownHash:    req.KnownHash,
	}); err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	if !h.acceptControlEnvelope(w, r, req.Envelope) {
		return
	}
	snapshot, err := h.control.PolicySnapshot(req.Envelope.AgentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	changed := req.KnownVersion != snapshot.PolicyVersion || req.KnownHash != snapshot.PolicyHash
	h.writeControlJSON(w, req.Envelope, http.StatusOK, core.DurableAgentPolicyPollResponse{
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
	if err := h.verifyEnvelope(req.Envelope, req.Artifact); err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	if !h.acceptControlEnvelope(w, r, req.Envelope) {
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
	h.writeControlJSON(w, req.Envelope, http.StatusAccepted, core.DurableAgentReviewArtifactUploadResponse{
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
	req.Ack = core.NormalizeDurableAgentPolicyAcknowledgement(req.Ack)
	if err := h.verifyEnvelope(req.Envelope, req.Ack); err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	if !h.acceptControlEnvelope(w, r, req.Envelope) {
		return
	}
	if err := h.control.ApplyPolicyAcknowledgement(req.Envelope, req.Ack, h.now()); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	h.writeControlJSON(w, req.Envelope, http.StatusOK, core.DurableAgentPolicyAcknowledgementResponse{Accepted: true})
}

func (h *HTTPHandler) handleParentConversationPoll(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req core.DurableAgentParentConversationPollRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	req.Envelope = core.NormalizeDurableAgentControlEnvelope(req.Envelope)
	if req.Envelope.MessageKind != core.DurableAgentControlMessageParentConversationPoll {
		writeError(w, http.StatusBadRequest, errors.New("durable agent parent conversation poll requires message_kind=parent_conversation_poll"))
		return
	}
	reqPayload := struct {
		Limit int `json:"limit,omitempty"`
	}{Limit: req.Limit}
	if err := h.verifyEnvelope(req.Envelope, reqPayload); err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	if !h.acceptControlEnvelope(w, r, req.Envelope) {
		return
	}
	messages, err := h.pendingParentConversation(req.Envelope.AgentID, req.Limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	h.writeControlJSON(w, req.Envelope, http.StatusOK, core.DurableAgentParentConversationPollResponse{Messages: messages})
}

func (h *HTTPHandler) handleParentConversationAck(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req core.DurableAgentParentConversationAckRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	req.Envelope = core.NormalizeDurableAgentControlEnvelope(req.Envelope)
	if req.Envelope.MessageKind != core.DurableAgentControlMessageParentConversationAck {
		writeError(w, http.StatusBadRequest, errors.New("durable agent parent conversation ack requires message_kind=parent_conversation_ack"))
		return
	}
	req.Ack = core.NormalizeDurableAgentParentConversationAcknowledgement(req.Ack)
	if req.Ack.AgentID == "" {
		req.Ack.AgentID = req.Envelope.AgentID
	}
	if req.Ack.AgentID != req.Envelope.AgentID {
		writeError(w, http.StatusBadRequest, errors.New("durable agent parent conversation ack agent_id does not match envelope"))
		return
	}
	if len(req.Ack.MessageIDs) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("durable agent parent conversation ack requires message_ids"))
		return
	}
	if err := h.verifyEnvelope(req.Envelope, req.Ack); err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	if !h.acceptControlEnvelope(w, r, req.Envelope) {
		return
	}
	if err := h.acknowledgeParentConversation(req.Ack.AgentID, req.Ack.MessageIDs, req.Ack.AcknowledgedAt); err != nil {
		if errors.Is(err, errParentConversationAckRejected) {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	h.writeControlJSON(w, req.Envelope, http.StatusOK, core.DurableAgentParentConversationAckResponse{Accepted: true})
}

func (h *HTTPHandler) now() time.Time {
	if h != nil && h.clock != nil {
		return h.clock().UTC()
	}
	return time.Now().UTC()
}

func (h *HTTPHandler) verifyEnvelope(envelope core.DurableAgentControlEnvelope, payload any) error {
	if h == nil || h.Verifier == nil {
		return nil
	}
	return h.Verifier(envelope, payload)
}

func NewStoreBackedEnvelopeVerifier(store HTTPStore) EnvelopeVerifier {
	return func(envelope core.DurableAgentControlEnvelope, payload any) error {
		if store == nil {
			return fmt.Errorf("durable agent control plane store is nil")
		}
		agent, err := store.DurableAgent(envelope.AgentID)
		if err != nil {
			return err
		}
		if strings.TrimSpace(agent.ControlPlaneSecret) == "" {
			return fmt.Errorf("invalid signature")
		}
		return VerifyEnvelopeHMAC(agent.ControlPlaneSecret, envelope, payload)
	}
}

func normalizeControlPlaneBasePath(basePath string) string {
	basePath = strings.TrimSpace(basePath)
	if basePath == "" || basePath == "/" {
		return ""
	}
	return path.Clean("/" + strings.TrimPrefix(basePath, "/"))
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
	r.Body = http.MaxBytesReader(w, r.Body, maxControlPlaneRequestBytes)
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	raw, err := encodeJSONPayload(payload)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeRawJSON(w, status, raw)
}

func encodeJSONPayload(payload any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeRawJSON(w http.ResponseWriter, status int, raw []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
