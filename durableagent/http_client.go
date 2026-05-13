//go:build linux

package durableagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

type EnvelopeSigner func(envelope core.DurableAgentControlEnvelope, payload any) (string, error)

type HTTPClient struct {
	Bootstrap core.DurableAgentRemoteBootstrap
	Client    *http.Client
	Signer    EnvelopeSigner
	Clock     func() time.Time

	mu       sync.Mutex
	sequence int64
}

func NewHTTPClient(bootstrap core.DurableAgentRemoteBootstrap) (*HTTPClient, error) {
	bootstrap = core.NormalizeDurableAgentRemoteBootstrap(bootstrap)
	if err := core.ValidateDurableAgentRemoteBootstrap(bootstrap); err != nil {
		return nil, err
	}
	return &HTTPClient{
		Bootstrap: bootstrap,
		Client:    &http.Client{Timeout: 30 * time.Second},
		Signer: func(envelope core.DurableAgentControlEnvelope, payload any) (string, error) {
			return SignEnvelopeHMAC(bootstrap.EnrollmentToken, envelope, payload)
		},
		Clock: func() time.Time { return time.Now().UTC() },
	}, nil
}

func (c *HTTPClient) Enroll(ctx context.Context) (core.DurableAgentEnrollmentResponse, error) {
	return c.sendEnrollment(ctx, core.DurableAgentControlMessageEnrollment)
}

func (c *HTTPClient) Reattest(ctx context.Context) (core.DurableAgentEnrollmentResponse, error) {
	return c.sendEnrollment(ctx, core.DurableAgentControlMessageReattestation)
}

func (c *HTTPClient) sendEnrollment(ctx context.Context, kind string) (core.DurableAgentEnrollmentResponse, error) {
	payload := c.Bootstrap.EnrollmentPayload()
	env, err := c.nextEnvelope(kind, payload)
	if err != nil {
		return core.DurableAgentEnrollmentResponse{}, err
	}
	req := core.DurableAgentEnrollmentRequest{Envelope: env, Payload: payload}
	var resp core.DurableAgentEnrollmentResponse
	if err := c.postJSON(ctx, ControlPlaneEnrollPath, req, &resp); err != nil {
		return core.DurableAgentEnrollmentResponse{}, err
	}
	return resp, nil
}

func (c *HTTPClient) PollPolicy(ctx context.Context, knownVersion int64, knownHash string) (core.DurableAgentPolicyPollResponse, error) {
	reqPayload := struct {
		KnownVersion int64  `json:"known_version,omitempty"`
		KnownHash    string `json:"known_hash,omitempty"`
	}{KnownVersion: knownVersion, KnownHash: strings.TrimSpace(knownHash)}
	env, err := c.nextEnvelope(core.DurableAgentControlMessagePolicyPoll, reqPayload)
	if err != nil {
		return core.DurableAgentPolicyPollResponse{}, err
	}
	req := core.DurableAgentPolicyPollRequest{
		Envelope:     env,
		KnownVersion: knownVersion,
		KnownHash:    strings.TrimSpace(knownHash),
	}
	var resp core.DurableAgentPolicyPollResponse
	if err := c.postJSON(ctx, ControlPlanePolicyPollPath, req, &resp); err != nil {
		return core.DurableAgentPolicyPollResponse{}, err
	}
	return resp, nil
}

func (c *HTTPClient) UploadReviewArtifact(ctx context.Context, artifact core.DurableReviewArtifact) (core.DurableAgentReviewArtifactUploadResponse, error) {
	env, err := c.nextEnvelope(core.DurableAgentControlMessageReviewArtifactUpload, artifact)
	if err != nil {
		return core.DurableAgentReviewArtifactUploadResponse{}, err
	}
	req := core.DurableAgentReviewArtifactUploadRequest{
		Envelope: env,
		Artifact: artifact,
	}
	var resp core.DurableAgentReviewArtifactUploadResponse
	if err := c.postJSON(ctx, ControlPlaneArtifactUploadPath, req, &resp); err != nil {
		return core.DurableAgentReviewArtifactUploadResponse{}, err
	}
	return resp, nil
}

func (c *HTTPClient) AcknowledgePolicy(ctx context.Context, ack core.DurableAgentPolicyAcknowledgement) (core.DurableAgentPolicyAcknowledgementResponse, error) {
	env, err := c.nextEnvelope(core.DurableAgentControlMessagePolicyAck, ack)
	if err != nil {
		return core.DurableAgentPolicyAcknowledgementResponse{}, err
	}
	req := core.DurableAgentPolicyAcknowledgementRequest{
		Envelope: env,
		Ack:      ack,
	}
	var resp core.DurableAgentPolicyAcknowledgementResponse
	if err := c.postJSON(ctx, ControlPlanePolicyAckPath, req, &resp); err != nil {
		return core.DurableAgentPolicyAcknowledgementResponse{}, err
	}
	return resp, nil
}

func (c *HTTPClient) PollParentConversation(ctx context.Context, limit int) (core.DurableAgentParentConversationPollResponse, error) {
	reqPayload := struct {
		Limit int `json:"limit,omitempty"`
	}{Limit: limit}
	env, err := c.nextEnvelope(core.DurableAgentControlMessageParentConversationPoll, reqPayload)
	if err != nil {
		return core.DurableAgentParentConversationPollResponse{}, err
	}
	req := core.DurableAgentParentConversationPollRequest{
		Envelope: env,
		Limit:    limit,
	}
	var resp core.DurableAgentParentConversationPollResponse
	if err := c.postJSON(ctx, ControlPlaneParentConversationPollPath, req, &resp); err != nil {
		return core.DurableAgentParentConversationPollResponse{}, err
	}
	return resp, nil
}

func (c *HTTPClient) AcknowledgeParentConversation(ctx context.Context, ack core.DurableAgentParentConversationAcknowledgement) (core.DurableAgentParentConversationAckResponse, error) {
	ack = core.NormalizeDurableAgentParentConversationAcknowledgement(ack)
	if ack.AgentID == "" && c != nil {
		ack.AgentID = c.Bootstrap.AgentID
	}
	env, err := c.nextEnvelope(core.DurableAgentControlMessageParentConversationAck, ack)
	if err != nil {
		return core.DurableAgentParentConversationAckResponse{}, err
	}
	req := core.DurableAgentParentConversationAckRequest{
		Envelope: env,
		Ack:      ack,
	}
	var resp core.DurableAgentParentConversationAckResponse
	if err := c.postJSON(ctx, ControlPlaneParentConversationAckPath, req, &resp); err != nil {
		return core.DurableAgentParentConversationAckResponse{}, err
	}
	return resp, nil
}

func (c *HTTPClient) postJSON(ctx context.Context, path string, body any, out any) error {
	if c == nil {
		return fmt.Errorf("durable agent http client is nil")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.Bootstrap.ParentControlURL, "/")+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var decoded map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&decoded); err == nil && strings.TrimSpace(decoded["error"]) != "" {
			return errors.New(decoded["error"])
		}
		return fmt.Errorf("durable agent http request failed: status=%d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *HTTPClient) nextEnvelope(kind string, payload any) (core.DurableAgentControlEnvelope, error) {
	if c == nil {
		return core.DurableAgentControlEnvelope{}, fmt.Errorf("durable agent http client is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sequence++
	env := core.DurableAgentControlEnvelope{
		ProtocolVersion: c.Bootstrap.ProtocolVersion,
		AgentID:         c.Bootstrap.AgentID,
		ParentAgentID:   c.Bootstrap.ParentAgentID,
		MessageKind:     kind,
		MessageID:       fmt.Sprintf("%s-%d", kind, c.sequence),
		Sequence:        c.sequence,
		Timestamp:       c.now(),
	}
	signature := ""
	if c.Signer != nil {
		signed, err := c.Signer(env, payload)
		if err != nil {
			return core.DurableAgentControlEnvelope{}, err
		}
		signature = strings.TrimSpace(signed)
	}
	env.Signature = signature
	return env, nil
}

func (c *HTTPClient) now() time.Time {
	if c != nil && c.Clock != nil {
		return c.Clock().UTC()
	}
	return time.Now().UTC()
}
