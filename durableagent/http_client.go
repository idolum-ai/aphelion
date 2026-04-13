//go:build linux

package durableagent

import (
	"bytes"
	"context"
	"encoding/json"
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
		Signer: func(core.DurableAgentControlEnvelope, any) (string, error) {
			return "signed-envelope", nil
		},
		Clock: func() time.Time { return time.Now().UTC() },
	}, nil
}

func (c *HTTPClient) Enroll(ctx context.Context) (core.DurableAgentEnrollmentResponse, error) {
	payload := c.Bootstrap.EnrollmentPayload()
	env, err := c.nextEnvelope(core.DurableAgentControlMessageEnrollment, payload)
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
		KnownVersion int64
		KnownHash    string
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
			return fmt.Errorf(decoded["error"])
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
	signature := "signed-envelope"
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
