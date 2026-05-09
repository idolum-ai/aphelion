//go:build linux

package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/session"
)

const (
	codexAppServerAdapterName     = "codex_app_server"
	codexAppServerWakeChannel     = "codex_app_server"
	codexAppServerMaxMessageBytes = int64(1 << 20)
)

var errCodexAppServerNoStatusEnvelope = errors.New("codex app-server turn did not return a durable child status envelope")

type codexAppServerDoer interface {
	Do(ctx context.Context, req codexAppServerRequest) (codexAppServerResult, error)
}

type codexAppServerRequest struct {
	Agent        core.DurableAgent
	Address      string
	MemoryRoot   string
	ThreadID     string
	Prompt       string
	Now          time.Time
	StatusSchema string
}

type codexAppServerResult struct {
	ThreadID       string
	TurnID         string
	Text           string
	EnvelopeRaw    []byte
	Envelope       core.DurableChildStatusEnvelope
	PayloadHash    string
	ApprovalLog    []codexAppServerApprovalDecision
	CodexEvents    []session.WorkCodexEvent
	PatchPreview   string
	Notifications  int
	Completed      bool
	ArtifactRel    string
	ArtifactSHA256 string
}

type codexAppServerApprovalDecision struct {
	Method   string `json:"method"`
	Decision string `json:"decision"`
	Command  string `json:"command,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type codexAppServerApprovalHandler func(method string, params map[string]any) codexAppServerApprovalDecision

type codexAppServerWakeAdapter struct {
	doer codexAppServerDoer
}

func newCodexAppServerWakeAdapter() durableWakeIngressAdapter {
	return &codexAppServerWakeAdapter{doer: realCodexAppServerDoer{}}
}

func (a *codexAppServerWakeAdapter) Name() string { return codexAppServerAdapterName }

func (a *codexAppServerWakeAdapter) Supports(agent core.DurableAgent) bool {
	if strings.ToLower(strings.TrimSpace(agent.Status)) != "active" {
		return false
	}
	if externalChannelAdapter(agent) != codexAppServerAdapterName {
		return false
	}
	mode := strings.TrimSpace(agent.WakeupMode)
	return mode == "" || strings.EqualFold(mode, "poll")
}

func (a *codexAppServerWakeAdapter) Prepare(ctx context.Context, rt *Runtime, agent core.DurableAgent, now time.Time) (*durableWakeTurnPlan, error) {
	if rt == nil || rt.store == nil {
		return nil, fmt.Errorf("codex app-server adapter runtime is unavailable")
	}
	external := agent.ChannelConfig.ExternalConfig()
	if external == nil {
		return nil, fmt.Errorf("codex app-server adapter requires external channel_config")
	}
	address := strings.TrimSpace(external.Address)
	if address == "" {
		return nil, fmt.Errorf("codex app-server adapter requires channel address")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	state, continuity, err := loadDurableAgentContinuityFromStore(rt.store, agent.AgentID)
	if err != nil {
		return nil, err
	}
	runtimeState := externalChannelStateForAdapter(continuity, codexAppServerAdapterName)
	codexState := decodeCodexAdapterState(runtimeState.AdapterState)
	if !externalChannelPollDue(runtimeState, strings.TrimSpace(external.PollInterval), now) {
		return nil, nil
	}

	_, memoryRoot := durableagent.LocalRoots(agent.AgentID, agent.LocalStorageRoots)
	if strings.TrimSpace(memoryRoot) == "" {
		if dbPath := strings.TrimSpace(rt.store.DBPath()); dbPath != "" {
			_, memoryRoot = durableagent.DefaultLocalRoots(dbPath, strings.TrimSpace(agent.AgentID))
		}
	}
	if strings.TrimSpace(memoryRoot) == "" {
		return nil, fmt.Errorf("codex app-server adapter requires durable agent memory root")
	}

	doer := a.doer
	if doer == nil {
		doer = realCodexAppServerDoer{}
	}
	prompt := codexAppServerStatusPrompt(agent, now)
	result, err := doer.Do(ctx, codexAppServerRequest{
		Agent:        agent,
		Address:      address,
		MemoryRoot:   memoryRoot,
		ThreadID:     firstNonEmpty(strings.TrimSpace(codexState.ThreadID), strings.TrimSpace(runtimeState.SessionRef)),
		Prompt:       prompt,
		Now:          now,
		StatusSchema: strings.TrimSpace(external.Query),
	})
	if err != nil {
		return nil, recordCodexAppServerFailure(rt.store, state, continuity, runtimeState, memoryRoot, agent, result, err, now)
	}
	if err := core.ValidateDurableChildStatusEnvelopeForAgent(result.Envelope, agent); err != nil {
		return nil, recordCodexAppServerFailure(rt.store, state, continuity, runtimeState, memoryRoot, agent, result, fmt.Errorf("validate codex app-server status envelope: %w", err), now)
	}
	if !strings.EqualFold(strings.TrimSpace(result.Envelope.CapabilityPosture), "read_only") {
		return nil, recordCodexAppServerFailure(rt.store, state, continuity, runtimeState, memoryRoot, agent, result, fmt.Errorf("codex app-server status capability_posture %q is not read_only", strings.TrimSpace(result.Envelope.CapabilityPosture)), now)
	}

	artifactRel, artifactSHA, err := writeCodexAppServerHeartbeatArtifact(memoryRoot, agent, result, now)
	if err != nil {
		return nil, err
	}
	result.ArtifactRel = artifactRel
	result.ArtifactSHA256 = artifactSHA

	codexState.ThreadID = strings.TrimSpace(result.ThreadID)
	codexState.LastTurnID = strings.TrimSpace(result.TurnID)
	codexState.LastPayloadHash = firstNonEmpty(strings.TrimSpace(result.Envelope.PayloadHash), strings.TrimSpace(result.PayloadHash))
	runtimeState = externalChannelRecordSuccess(runtimeState, externalChannelCommandLifecycle{
		Adapter:      codexAppServerAdapterName,
		Command:      codexAppServerStatusCommandName,
		SessionRef:   strings.TrimSpace(result.ThreadID),
		LastArtifact: artifactRel,
		LastStatus:   "ok",
		ResetBackoff: true,
	}, now)
	continuity.ExternalChannel = encodeCodexExternalChannelState(runtimeState, codexState)
	raw, err := continuity.Marshal()
	if err != nil {
		return nil, err
	}
	state.StateJSON = raw
	if err := rt.store.SaveDurableAgentState(*state); err != nil {
		return nil, err
	}

	key := session.SessionKey{ChatID: durableWakeSyntheticChatID(agent.AgentID), Scope: durableAgentScopeRef(agent)}
	summary := codexAppServerWakeSummary(agent, result, artifactRel)
	return &durableWakeTurnPlan{
		Channel:      codexAppServerWakeChannel,
		AuditChannel: codexAppServerWakeChannel,
		Key:          key,
		Inbound: core.InboundMessage{
			ChatID:         key.ChatID,
			ChatType:       codexAppServerWakeChannel,
			ChatTitle:      "codex-app-server",
			SenderName:     "codex_app_server",
			Text:           summary,
			MessageID:      durableWakeMessageID(now),
			DurableAgentID: strings.TrimSpace(agent.AgentID),
			Timestamp:      now,
		},
		SessionChatType:      codexAppServerWakeChannel,
		SessionUserName:      "codex_app_server",
		PromptContextErrHint: "load codex app-server durable wake prompt context",
		PolicyReason:         "mapped from generic codex_app_server durable-agent channel adapter",
		PersistenceErrCtx: turnCommitErrorContext{
			ConvertMessages: "convert codex app-server durable wake messages",
			LoadPlanState:   "load codex app-server durable wake plan state before save",
			LoadOperation:   "load codex app-server durable wake operation state before save",
			SaveSession:     "save codex app-server durable wake session",
			RecordOutbound:  "record codex app-server durable wake outbound reply",
		},
		SendErrCtx:   "send codex app-server durable wake reply",
		RecordErrCtx: "record codex app-server durable wake outbound reply",
		GovernorContext: func(agent core.DurableAgent, policy core.DurableAgentLivePolicy, _ core.InboundMessage, pending []core.DurableAgentConversationMessage) string {
			lines := []string{
				"You are handling a durable-agent wake from a generic codex_app_server channel adapter.",
				"The adapter already performed the remote read-only status task and stored the heartbeat artifact.",
				"Report the concrete status and next bounded step. Do not claim additional remote actions.",
				"No UI/app manipulation, screenshots, file-content inspection, process killing, command-line args, or writes are authorized.",
			}
			if charter := strings.TrimSpace(policy.Charter); charter != "" {
				lines = append(lines, "Charter: "+charter)
			}
			lines = append(lines, "Durable agent id: "+strings.TrimSpace(agent.AgentID))
			lines = append(lines, "Channel address: "+address)
			lines = append(lines, durableParentConversationGovernorLines(pending)...)
			return strings.Join(lines, "\n")
		},
		Finalize: func(turnSummary string) error {
			_, err := durableagent.NewRuntime(rt.store).QueueReviewArtifact(agent, core.DurableReviewArtifact{
				AgentID:       strings.TrimSpace(agent.AgentID),
				Summary:       firstNonEmpty(strings.TrimSpace(turnSummary), summary),
				IntervalLabel: now.UTC().Format(time.RFC3339),
				LocalActions: []string{
					"Ran a bounded read-only heartbeat through the generic codex_app_server channel adapter.",
					"Stored the resulting durable_child_status envelope as a child artifact.",
				},
				RiskFlags: []string{"remote_child_runtime", "read_only_status", "codex_app_server"},
				ArtifactRefs: []string{
					fmt.Sprintf("artifact://durable-agent/%s/%s", strings.TrimSpace(agent.AgentID), artifactRel),
				},
				Metadata: map[string]string{
					"channel_kind":          strings.TrimSpace(agent.ChannelKind),
					"channel_adapter":       codexAppServerAdapterName,
					"channel_address":       address,
					"thread_id":             strings.TrimSpace(result.ThreadID),
					"turn_id":               strings.TrimSpace(result.TurnID),
					"payload_hash":          firstNonEmpty(strings.TrimSpace(result.Envelope.PayloadHash), strings.TrimSpace(result.PayloadHash)),
					"artifact_ref":          artifactRel,
					"artifact_sha256":       artifactSHA,
					"trigger_kinds":         "codex_app_server,heartbeat,status",
					"child_local_subject":   "false",
					"approvals_decisions":   summarizeCodexApprovalDecisions(result.ApprovalLog),
					"notifications_count":   fmt.Sprintf("%d", result.Notifications),
					"single_session_thread": codexAppServerBoolString(strings.TrimSpace(result.ThreadID) != ""),
				},
			})
			return err
		},
	}, nil
}

type realCodexAppServerDoer struct{}

func (realCodexAppServerDoer) Do(ctx context.Context, req codexAppServerRequest) (codexAppServerResult, error) {
	if strings.TrimSpace(req.Address) == "" {
		return codexAppServerResult{}, fmt.Errorf("codex app-server address is required")
	}
	client := newCodexAppServerClient(req.Address)
	defer client.Close(websocket.StatusNormalClosure, "done")
	if err := client.Connect(ctx); err != nil {
		return codexAppServerResult{}, err
	}
	if err := client.Initialize(ctx); err != nil {
		return codexAppServerResult{}, err
	}

	threadID := strings.TrimSpace(req.ThreadID)
	if threadID == "" {
		created, err := client.ThreadStart(ctx, codexThreadStartParams(req))
		if err != nil {
			return codexAppServerResult{}, err
		}
		threadID = created
	} else if err := client.ThreadResume(ctx, threadID, codexThreadResumeParams()); err != nil {
		created, createErr := client.ThreadStart(ctx, codexThreadStartParams(req))
		if createErr != nil {
			return codexAppServerResult{}, fmt.Errorf("resume codex app-server thread %q: %w (new thread also failed: %v)", threadID, err, createErr)
		}
		threadID = created
	}

	turnID, err := client.TurnStart(ctx, threadID, req.Prompt, codexTurnStartParams())
	if err != nil {
		return codexAppServerResult{}, err
	}
	result, err := client.StreamTurn(ctx, threadID, turnID)
	if err != nil {
		return codexAppServerResult{}, err
	}
	result.ThreadID = firstNonEmpty(strings.TrimSpace(result.ThreadID), threadID)
	result.TurnID = firstNonEmpty(strings.TrimSpace(result.TurnID), turnID)
	if len(result.EnvelopeRaw) == 0 {
		return result, errCodexAppServerNoStatusEnvelope
	}
	env, err := core.ParseDurableChildStatusEnvelope(result.EnvelopeRaw)
	if err != nil {
		return result, fmt.Errorf("parse codex app-server status envelope: %w", err)
	}
	if strings.TrimSpace(env.PayloadHash) == "" {
		hash, hashErr := core.DurableChildStatusPayloadHash(env.Payload)
		if hashErr != nil {
			return result, hashErr
		}
		env.PayloadHash = hash
	}
	result.Envelope = env
	result.PayloadHash = env.PayloadHash
	return result, nil
}

type codexAppServerClient struct {
	address         string
	conn            *websocket.Conn
	approvalHandler codexAppServerApprovalHandler
	mu              sync.Mutex
	approvalLog     []codexAppServerApprovalDecision
	workEvents      []session.WorkCodexEvent
	notifications   int
}

func newCodexAppServerClient(address string, handlers ...codexAppServerApprovalHandler) *codexAppServerClient {
	var handler codexAppServerApprovalHandler
	if len(handlers) > 0 {
		handler = handlers[0]
	}
	return &codexAppServerClient{address: strings.TrimSpace(address), approvalHandler: handler}
}

func (c *codexAppServerClient) Connect(ctx context.Context) error {
	conn, resp, err := websocket.Dial(ctx, c.address, &websocket.DialOptions{HTTPClient: newCodexHTTPClient()})
	if err != nil {
		status := ""
		if resp != nil {
			status = resp.Status
		}
		if status != "" {
			return fmt.Errorf("connect codex app-server %s: %w (%s)", c.address, err, status)
		}
		return fmt.Errorf("connect codex app-server %s: %w", c.address, err)
	}
	conn.SetReadLimit(codexAppServerMaxMessageBytes)
	c.conn = conn
	return nil
}

func (c *codexAppServerClient) Close(code websocket.StatusCode, reason string) {
	if c != nil && c.conn != nil {
		_ = c.conn.Close(code, reason)
	}
}

func (c *codexAppServerClient) Initialize(ctx context.Context) error {
	_, err := c.request(ctx, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "aphelion", "title": "Aphelion", "version": "0"},
		"capabilities": map[string]any{"experimentalApi": true},
	})
	if err != nil {
		return err
	}
	return c.notify(ctx, "initialized", map[string]any{})
}

func (c *codexAppServerClient) ThreadStart(ctx context.Context, params map[string]any) (string, error) {
	result, err := c.request(ctx, "thread/start", params)
	if err != nil {
		return "", err
	}
	threadID := nestedString(result, "thread", "id")
	if threadID == "" {
		return "", fmt.Errorf("thread/start response missing thread.id")
	}
	return threadID, nil
}

func (c *codexAppServerClient) ThreadResume(ctx context.Context, threadID string, params map[string]any) error {
	payload := map[string]any{"threadId": strings.TrimSpace(threadID)}
	for k, v := range params {
		payload[k] = v
	}
	_, err := c.request(ctx, "thread/resume", payload)
	return err
}

func (c *codexAppServerClient) TurnStart(ctx context.Context, threadID string, text string, params map[string]any) (string, error) {
	payload := map[string]any{"threadId": strings.TrimSpace(threadID), "input": []map[string]any{{"type": "text", "text": text}}}
	for k, v := range params {
		payload[k] = v
	}
	result, err := c.request(ctx, "turn/start", payload)
	if err != nil {
		return "", err
	}
	turnID := nestedString(result, "turn", "id")
	if turnID == "" {
		return "", fmt.Errorf("turn/start response missing turn.id")
	}
	return turnID, nil
}

type codexAppServerStreamOptions struct {
	FirstNotificationTimeout time.Duration
}

func (c *codexAppServerClient) StreamTurn(ctx context.Context, threadID string, turnID string) (codexAppServerResult, error) {
	return c.StreamTurnWithOptions(ctx, threadID, turnID, codexAppServerStreamOptions{})
}

func (c *codexAppServerClient) StreamTurnWithOptions(ctx context.Context, threadID string, turnID string, opts codexAppServerStreamOptions) (codexAppServerResult, error) {
	var text strings.Builder
	var completed bool
	var notifications int
	for {
		readCtx := ctx
		var cancel context.CancelFunc
		if notifications == 0 && opts.FirstNotificationTimeout > 0 {
			readCtx, cancel = context.WithTimeout(ctx, opts.FirstNotificationTimeout)
		}
		msg, err := c.readMessage(readCtx)
		timedOut := readCtx.Err() == context.DeadlineExceeded
		if cancel != nil {
			cancel()
		}
		if err != nil {
			if notifications == 0 && opts.FirstNotificationTimeout > 0 && (timedOut || errors.Is(err, context.DeadlineExceeded)) {
				return codexAppServerResult{}, fmt.Errorf("codex app-server turn %s produced no notifications within %s", strings.TrimSpace(turnID), opts.FirstNotificationTimeout)
			}
			return codexAppServerResult{}, err
		}
		if _, ok := msg["id"]; ok {
			if method, _ := msg["method"].(string); method != "" {
				response := c.handleServerRequest(method, asObject(msg["params"]))
				if err := c.writeMessage(ctx, map[string]any{"id": msg["id"], "result": response}); err != nil {
					return codexAppServerResult{}, err
				}
			}
			continue
		}
		method, _ := msg["method"].(string)
		if method == "" {
			continue
		}
		notifications++
		params := asObject(msg["params"])
		c.recordWorkNotification(method, params)
		switch method {
		case "item/agentMessage/delta":
			if stringField(params, "turnId") == turnID {
				text.WriteString(stringField(params, "delta"))
			}
		case "turn/completed":
			if stringField(params, "threadId") == threadID && nestedString(params, "turn", "id") == turnID {
				completed = true
				raw := extractFirstJSONObject([]byte(text.String()))
				return codexAppServerResult{
					ThreadID:      threadID,
					TurnID:        turnID,
					Text:          strings.TrimSpace(text.String()),
					EnvelopeRaw:   raw,
					ApprovalLog:   append([]codexAppServerApprovalDecision(nil), c.approvalLog...),
					CodexEvents:   c.WorkEvents(),
					PatchPreview:  codexWorkPatchPreviewFromEvents(c.WorkEvents()),
					Notifications: notifications,
					Completed:     completed,
				}, nil
			}
		}
	}
}

func (c *codexAppServerClient) request(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	id := fmt.Sprintf("aphelion-%d", time.Now().UnixNano())
	if err := c.writeMessage(ctx, map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for {
		msg, err := c.readMessage(ctx)
		if err != nil {
			return nil, err
		}
		if _, ok := msg["id"]; ok {
			if method, _ := msg["method"].(string); method != "" {
				response := c.handleServerRequest(method, asObject(msg["params"]))
				if err := c.writeMessage(ctx, map[string]any{"id": msg["id"], "result": response}); err != nil {
					return nil, err
				}
				continue
			}
		}
		if method, _ := msg["method"].(string); method != "" {
			c.recordWorkNotification(method, asObject(msg["params"]))
			continue
		}
		if fmt.Sprint(msg["id"]) != id {
			continue
		}
		if errObj, ok := msg["error"]; ok {
			return nil, fmt.Errorf("codex app-server %s error: %v", method, errObj)
		}
		result := asObject(msg["result"])
		if result == nil {
			return nil, fmt.Errorf("codex app-server %s response must be object", method)
		}
		return result, nil
	}
}

func (c *codexAppServerClient) notify(ctx context.Context, method string, params map[string]any) error {
	return c.writeMessage(ctx, map[string]any{"method": method, "params": params})
}

func (c *codexAppServerClient) readMessage(ctx context.Context) (map[string]any, error) {
	if c == nil || c.conn == nil {
		return nil, fmt.Errorf("codex app-server websocket is not connected")
	}
	typ, raw, err := c.conn.Read(ctx)
	if err != nil {
		return nil, fmt.Errorf("read codex app-server message: %w", err)
	}
	if typ != websocket.MessageText {
		return nil, fmt.Errorf("read codex app-server message: unexpected websocket message type %v", typ)
	}
	var msg map[string]any
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil, fmt.Errorf("decode codex app-server json-rpc: %w", err)
	}
	return msg, nil
}

func (c *codexAppServerClient) writeMessage(ctx context.Context, payload map[string]any) error {
	if c == nil || c.conn == nil {
		return fmt.Errorf("codex app-server websocket is not connected")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return c.conn.Write(ctx, websocket.MessageText, raw)
}

func (c *codexAppServerClient) handleServerRequest(method string, params map[string]any) map[string]any {
	if c != nil && c.approvalHandler != nil {
		decision := c.approvalHandler(method, params)
		if strings.TrimSpace(decision.Method) == "" {
			decision.Method = method
		}
		if strings.TrimSpace(decision.Decision) == "" {
			decision.Decision = "cancel"
		}
		c.recordApproval(decision)
		c.recordWorkServerRequest(method, params, decision)
		if decision.Decision == "cancel" && method != "item/commandExecution/requestApproval" && method != "item/fileChange/requestApproval" {
			return map[string]any{}
		}
		return map[string]any{"decision": decision.Decision}
	}
	decision := codexAppServerReadOnlyApprovalDecision(method, params)
	if c != nil {
		c.recordApproval(decision)
		c.recordWorkServerRequest(method, params, decision)
	}
	if decision.Decision == "cancel" && method != "item/commandExecution/requestApproval" && method != "item/fileChange/requestApproval" {
		return map[string]any{}
	}
	return map[string]any{"decision": decision.Decision}
}

func codexAppServerReadOnlyApprovalDecision(method string, params map[string]any) codexAppServerApprovalDecision {
	decision := codexAppServerApprovalDecision{Method: method, Decision: "cancel"}
	switch method {
	case "item/commandExecution/requestApproval":
		decision.Command = stringField(params, "command")
		decision.Reason = stringField(params, "reason")
		if codexAppServerCommandAllowed(decision.Command) {
			decision.Decision = "accept"
			return decision
		}
		decision.Decision = "decline"
	case "item/fileChange/requestApproval":
		decision.Reason = stringField(params, "reason")
		decision.Decision = "cancel"
	default:
		decision.Decision = "cancel"
	}
	return decision
}

func (c *codexAppServerClient) recordApproval(decision codexAppServerApprovalDecision) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.approvalLog = append(c.approvalLog, decision)
}

func (c *codexAppServerClient) recordWorkNotification(method string, params map[string]any) {
	if c == nil {
		return
	}
	event, ok := codexWorkEventFromNotification(method, params)
	if !ok {
		return
	}
	c.recordWorkEvent(event)
}

func (c *codexAppServerClient) recordWorkServerRequest(method string, params map[string]any, decision codexAppServerApprovalDecision) {
	if c == nil {
		return
	}
	event, ok := codexWorkEventFromServerRequest(method, params, decision)
	if !ok {
		return
	}
	c.recordWorkEvent(event)
}

func (c *codexAppServerClient) recordWorkEvent(event session.WorkCodexEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.workEvents = codexWorkAppendEvent(c.workEvents, event)
}

func (c *codexAppServerClient) ApprovalLog() []codexAppServerApprovalDecision {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]codexAppServerApprovalDecision(nil), c.approvalLog...)
}

func (c *codexAppServerClient) WorkEvents() []session.WorkCodexEvent {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]session.WorkCodexEvent(nil), c.workEvents...)
}

func codexAppServerCommandAllowed(command string) bool {
	compact := strings.Join(strings.Fields(strings.TrimSpace(command)), " ")
	allowedExact := map[string]struct{}{
		"hostname":                    {},
		"sw_vers":                     {},
		"uname -m":                    {},
		"uptime":                      {},
		"df -h /":                     {},
		"df -g /":                     {},
		"ps -A -o comm= -r | head -5": {},
		"ps -A -o comm= -m | head -5": {},
	}
	_, ok := allowedExact[compact]
	return ok
}

func codexThreadStartParams(req codexAppServerRequest) map[string]any {
	return map[string]any{
		"baseInstructions":      codexAppServerBaseInstructions(req.Agent),
		"developerInstructions": codexAppServerDeveloperInstructions(req.Agent),
		"approvalPolicy":        "on-request",
		"sandbox":               "read-only",
		"serviceName":           "aphelion-durable-child",
		"cwd":                   "/",
	}
}

func codexThreadResumeParams() map[string]any {
	return map[string]any{
		"approvalPolicy": "on-request",
		"sandbox":        "read-only",
	}
}

func codexTurnStartParams() map[string]any {
	return map[string]any{
		"approvalPolicy": "on-request",
		"sandbox":        "read-only",
	}
}

func codexAppServerBaseInstructions(agent core.DurableAgent) string {
	return strings.TrimSpace(fmt.Sprintf(`You are a durable child runtime reachable through a Codex app-server channel.
Your durable agent id is %s.
Operate only inside the current parent-approved charter.
Never modify files, open apps, kill processes, inspect private content, take screenshots, use Accessibility, read command-line arguments, control the UI, send messages, or manipulate the machine.
For status tasks, return only the requested durable_child_status JSON object and no prose.`, strings.TrimSpace(agent.AgentID)))
}

func codexAppServerDeveloperInstructions(agent core.DurableAgent) string {
	charter := strings.TrimSpace(agent.LivePolicy.Charter)
	if charter == "" {
		charter = "Read-only status reporting only."
	}
	return strings.TrimSpace(fmt.Sprintf(`Charter:
%s

Boundary:
- read-only status/heartbeat tasks only
- process names only, never command-line arguments or paths
- no screenshots, UI control, Accessibility, app manipulation, file writes, process killing, messages, browser/window inspection, or private content inspection
- if a field cannot be collected safely, use an empty array or null and explain inside payload.collection_notes`, charter))
}

func codexAppServerStatusPrompt(agent core.DurableAgent, now time.Time) string {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	agentID := strings.TrimSpace(agent.AgentID)
	displayName := codexAppServerDisplayName(agent)
	return strings.TrimSpace(fmt.Sprintf(`Produce a single JSON object and nothing else.

Rules:
- Use only read-only shell commands if commands are needed.
- Do not modify files.
- Do not open apps.
- Do not kill processes.
- Do not inspect private content.
- Do not take screenshots.
- Do not use Accessibility.
- Do not read command-line arguments.
- Do not control the UI.
- Process entries must include process names only, not command lines or paths.

Return this exact generic envelope shape. Include payload_hash only if you can compute the exact sha256 of the compact JSON payload; otherwise omit payload_hash:
	{
	  "kind": "durable_child_status",
	  "agent_id": %s,
	  "schema_version": "durable_child_status.v1",
	  "generated_at": "%s",
	  "capability_posture": "read_only",
	  "payload": {
	    "display_name": %s,
	    "mode": "read_only",
    "machine": {
      "hostname": "...",
      "os": "macOS",
      "os_version": "...",
      "arch": "...",
      "uptime": "...",
      "disk_free_root": "..."
    },
    "top_processes": {
      "by_cpu": ["name1", "name2", "name3", "name4", "name5"],
      "by_memory": ["name1", "name2", "name3", "name4", "name5"],
      "privacy": {
        "process_names_only": true,
        "cmdline_redacted": true,
        "paths_redacted": true
      }
    },
    "capability_limits": {
      "no_screenshots": true,
      "no_typing": true,
      "no_file_operations": true,
      "no_process_killing": true,
      "no_window_control": true,
      "no_messages": true,
      "no_full_command_line_inspection": true
    }
	  }
	}`, jsonString(agentID), now.UTC().Format(time.RFC3339), jsonString(displayName)))
}

func codexAppServerDisplayName(agent core.DurableAgent) string {
	if value := strings.TrimSpace(agent.AgentID); value != "" {
		return value
	}
	return "durable child"
}

func jsonString(value string) string {
	raw, err := json.Marshal(strings.TrimSpace(value))
	if err != nil {
		return `""`
	}
	return string(raw)
}

func recordCodexAppServerFailure(store *session.SQLiteStore, state *core.DurableAgentState, continuity core.DurableAgentContinuityState, runtimeState core.DurableAgentExternalChannelRuntimeState, memoryRoot string, agent core.DurableAgent, result codexAppServerResult, cause error, now time.Time) error {
	if store == nil || state == nil {
		return fmt.Errorf("record codex app-server failure: store/state unavailable")
	}
	if cause == nil {
		cause = fmt.Errorf("codex app-server failure")
	}
	now = now.UTC()
	codexState := decodeCodexAdapterState(runtimeState.AdapterState)
	if codexAppServerFailureShouldResetThread(cause) {
		runtimeState.SessionRef = ""
		codexState.ThreadID = ""
	}
	artifactRel := ""
	if rel, _, err := writeCodexAppServerFailureArtifact(memoryRoot, agent, result, cause, now); err == nil && strings.TrimSpace(rel) != "" {
		artifactRel = rel
	}
	runtimeState = externalChannelRecordFailure(runtimeState, externalChannelCommandLifecycle{
		Adapter:      codexAppServerAdapterName,
		Command:      codexAppServerStatusCommandName,
		SessionRef:   runtimeState.SessionRef,
		LastArtifact: artifactRel,
		LastStatus:   "blocked",
		LastError:    truncateRunes(cause.Error(), 900),
	}, now)
	continuity.ExternalChannel = encodeCodexExternalChannelState(runtimeState, codexState)
	raw, err := continuity.Marshal()
	if err != nil {
		return err
	}
	state.StateJSON = raw
	return store.SaveDurableAgentState(*state)
}

func codexAppServerFailureShouldResetThread(cause error) bool {
	msg := strings.ToLower(strings.TrimSpace(cause.Error()))
	return strings.Contains(msg, "resume codex app-server thread") ||
		strings.Contains(msg, "read limited at") ||
		strings.Contains(msg, "unexpected rsv bits") ||
		strings.Contains(msg, "payload_hash mismatch")
}

func writeCodexAppServerFailureArtifact(memoryRoot string, agent core.DurableAgent, result codexAppServerResult, cause error, now time.Time) (string, string, error) {
	if strings.TrimSpace(memoryRoot) == "" {
		return "", "", nil
	}
	payload := map[string]any{
		"kind":          "codex_app_server_failure",
		"agent_id":      strings.TrimSpace(agent.AgentID),
		"recorded_at":   now.UTC().Format(time.RFC3339),
		"error":         truncateRunes(cause.Error(), 2000),
		"thread_id":     strings.TrimSpace(result.ThreadID),
		"turn_id":       strings.TrimSpace(result.TurnID),
		"text_excerpt":  truncateRunes(result.Text, 4000),
		"envelope_raw":  string(bytes.TrimSpace(result.EnvelopeRaw)),
		"approval_log":  result.ApprovalLog,
		"notifications": result.Notifications,
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", "", err
	}
	raw = append(raw, '\n')
	date := now.UTC().Format("20060102T150405Z")
	rel := filepath.ToSlash(filepath.Join("heartbeats", fmt.Sprintf("codex-app-server-failure-%s.json", date)))
	artifactRoot := filepath.Join(memoryRoot, "artifacts")
	target := filepath.Join(artifactRoot, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(target, raw, 0o644); err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(raw)
	hash := "sha256:" + hex.EncodeToString(sum[:])
	manifest, err := loadCodexAppServerArtifactManifest(artifactRoot, agent.AgentID)
	if err != nil {
		return "", "", err
	}
	manifest = upsertCodexAppServerArtifactManifestEntry(manifest, codexAppServerArtifactManifestEntry{
		Path:      rel,
		Kind:      "failure_quarantine",
		Source:    codexAppServerAdapterName,
		Reason:    "Codex app-server heartbeat failure quarantined instead of retry-spamming.",
		SHA256:    hash,
		UpdatedAt: now.UTC(),
	}, now.UTC())
	if err := writeCodexAppServerArtifactManifest(artifactRoot, manifest); err != nil {
		return "", "", err
	}
	return "artifacts/" + rel, hash, nil
}

func loadDurableAgentContinuityFromStore(store *session.SQLiteStore, agentID string) (*core.DurableAgentState, core.DurableAgentContinuityState, error) {
	state, err := store.DurableAgentState(strings.TrimSpace(agentID))
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) && !strings.Contains(err.Error(), "no rows") {
			return nil, core.DurableAgentContinuityState{}, err
		}
		state = &core.DurableAgentState{AgentID: strings.TrimSpace(agentID)}
	}
	continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		return nil, core.DurableAgentContinuityState{}, err
	}
	return state, continuity, nil
}

type codexAppServerAdapterState struct {
	ThreadID        string `json:"thread_id,omitempty"`
	LastTurnID      string `json:"last_turn_id,omitempty"`
	LastPayloadHash string `json:"last_payload_hash,omitempty"`
}

const codexAppServerStatusCommandName = "codex_app_server.status_heartbeat"

func decodeCodexAdapterState(raw json.RawMessage) codexAppServerAdapterState {
	var state codexAppServerAdapterState
	if len(bytes.TrimSpace(raw)) > 0 {
		_ = json.Unmarshal(raw, &state)
	}
	state.ThreadID = strings.TrimSpace(state.ThreadID)
	state.LastTurnID = strings.TrimSpace(state.LastTurnID)
	state.LastPayloadHash = strings.TrimSpace(state.LastPayloadHash)
	return state
}

func encodeCodexExternalChannelState(runtimeState core.DurableAgentExternalChannelRuntimeState, codexState codexAppServerAdapterState) *core.DurableAgentExternalChannelRuntimeState {
	codexState.ThreadID = strings.TrimSpace(codexState.ThreadID)
	codexState.LastTurnID = strings.TrimSpace(codexState.LastTurnID)
	codexState.LastPayloadHash = strings.TrimSpace(codexState.LastPayloadHash)
	if strings.TrimSpace(runtimeState.SessionRef) == "" {
		runtimeState.SessionRef = codexState.ThreadID
	}
	runtimeState.Adapter = codexAppServerAdapterName
	raw, _ := json.Marshal(codexState)
	runtimeState.AdapterState = json.RawMessage(raw)
	return core.NormalizeDurableAgentContinuityState(core.DurableAgentContinuityState{ExternalChannel: &runtimeState}).ExternalChannel
}

func writeCodexAppServerHeartbeatArtifact(memoryRoot string, agent core.DurableAgent, result codexAppServerResult, now time.Time) (string, string, error) {
	envelopeRaw := bytes.TrimSpace(result.EnvelopeRaw)
	if len(envelopeRaw) == 0 {
		envelopeRaw = bytes.TrimSpace([]byte(result.Text))
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, envelopeRaw, "", "  "); err != nil {
		pretty.Write(envelopeRaw)
	}
	date := now.UTC().Format("20060102T150405Z")
	rel := filepath.ToSlash(filepath.Join("heartbeats", fmt.Sprintf("codex-app-server-%s.json", date)))
	artifactRoot := filepath.Join(memoryRoot, "artifacts")
	target := filepath.Join(artifactRoot, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", "", fmt.Errorf("create codex app-server heartbeat artifact dir: %w", err)
	}
	content := append(pretty.Bytes(), '\n')
	if err := os.WriteFile(target, content, 0o644); err != nil {
		return "", "", fmt.Errorf("write codex app-server heartbeat artifact: %w", err)
	}
	sum := sha256.Sum256(content)
	hash := "sha256:" + hex.EncodeToString(sum[:])
	manifest, err := loadCodexAppServerArtifactManifest(artifactRoot, agent.AgentID)
	if err != nil {
		return "", "", err
	}
	manifest = upsertCodexAppServerArtifactManifestEntry(manifest, codexAppServerArtifactManifestEntry{
		Path:      rel,
		Kind:      "heartbeat_envelope",
		Source:    codexAppServerAdapterName,
		Reason:    "Read-only durable_child_status envelope collected through generic codex_app_server adapter.",
		SHA256:    hash,
		UpdatedAt: now.UTC(),
	}, now.UTC())
	if err := writeCodexAppServerArtifactManifest(artifactRoot, manifest); err != nil {
		return "", "", err
	}
	return "artifacts/" + rel, hash, nil
}

type codexAppServerArtifactManifest struct {
	AgentID   string                                `json:"agent_id"`
	UpdatedAt time.Time                             `json:"updated_at"`
	Artifacts []codexAppServerArtifactManifestEntry `json:"artifacts"`
}

type codexAppServerArtifactManifestEntry struct {
	Path      string    `json:"path"`
	Kind      string    `json:"kind,omitempty"`
	Source    string    `json:"source,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	SHA256    string    `json:"sha256"`
	UpdatedAt time.Time `json:"updated_at"`
}

func loadCodexAppServerArtifactManifest(artifactRoot string, agentID string) (codexAppServerArtifactManifest, error) {
	manifest := codexAppServerArtifactManifest{AgentID: strings.TrimSpace(agentID), Artifacts: []codexAppServerArtifactManifestEntry{}}
	raw, err := os.ReadFile(filepath.Join(artifactRoot, "ARTIFACTS.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return manifest, nil
		}
		return codexAppServerArtifactManifest{}, fmt.Errorf("read durable agent artifact manifest: %w", err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return codexAppServerArtifactManifest{}, fmt.Errorf("decode durable agent artifact manifest: %w", err)
	}
	manifest.AgentID = strings.TrimSpace(agentID)
	return manifest, nil
}

func upsertCodexAppServerArtifactManifestEntry(manifest codexAppServerArtifactManifest, entry codexAppServerArtifactManifestEntry, updatedAt time.Time) codexAppServerArtifactManifest {
	for i := range manifest.Artifacts {
		if manifest.Artifacts[i].Path == entry.Path {
			manifest.Artifacts[i] = entry
			manifest.UpdatedAt = updatedAt
			return manifest
		}
	}
	manifest.Artifacts = append(manifest.Artifacts, entry)
	manifest.UpdatedAt = updatedAt
	return manifest
}

func writeCodexAppServerArtifactManifest(artifactRoot string, manifest codexAppServerArtifactManifest) error {
	if err := os.MkdirAll(artifactRoot, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(filepath.Join(artifactRoot, "ARTIFACTS.json"), raw, 0o644)
}

func codexAppServerWakeSummary(agent core.DurableAgent, result codexAppServerResult, artifactRel string) string {
	return strings.TrimSpace(fmt.Sprintf("codex_app_server heartbeat received for %s. thread_id=%s turn_id=%s payload_hash=%s artifact=%s", strings.TrimSpace(agent.AgentID), strings.TrimSpace(result.ThreadID), strings.TrimSpace(result.TurnID), firstNonEmpty(strings.TrimSpace(result.Envelope.PayloadHash), strings.TrimSpace(result.PayloadHash)), artifactRel))
}

func summarizeCodexApprovalDecisions(values []codexAppServerApprovalDecision) string {
	if len(values) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strings.TrimSpace(value.Method)+":"+strings.TrimSpace(value.Decision))
	}
	return strings.Join(parts, ",")
}

func codexAppServerBoolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func asObject(value any) map[string]any {
	if value == nil {
		return nil
	}
	if obj, ok := value.(map[string]any); ok {
		return obj
	}
	return nil
}

func stringField(obj map[string]any, key string) string {
	if obj == nil {
		return ""
	}
	if v, ok := obj[key]; ok {
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return ""
}

func nestedString(obj map[string]any, keys ...string) string {
	cur := obj
	for i, key := range keys {
		if i == len(keys)-1 {
			return stringField(cur, key)
		}
		cur = asObject(cur[key])
		if cur == nil {
			return ""
		}
	}
	return ""
}

func extractFirstJSONObject(raw []byte) []byte {
	raw = bytes.TrimSpace(raw)
	start := bytes.IndexByte(raw, '{')
	if start < 0 {
		return nil
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(raw); i++ {
		b := raw[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch b {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch b {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				candidate := bytes.TrimSpace(raw[start : i+1])
				if json.Valid(candidate) {
					return candidate
				}
				return nil
			}
		}
	}
	return nil
}

func codexAppServerReadyURL(address string, suffix string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(address))
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	default:
		return "", fmt.Errorf("codex app-server address must use ws or wss")
	}
	u.Path = "/" + strings.TrimPrefix(suffix, "/")
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func checkCodexAppServerHTTP(ctx context.Context, address string, path string) error {
	target, err := codexAppServerReadyURL(address, path)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	resp, err := newCodexHTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s returned %s", target, resp.Status)
	}
	return nil
}
