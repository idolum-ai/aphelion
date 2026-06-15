//go:build linux

package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/idolum-ai/aphelion/commandeffect"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

var ErrNoStatusEnvelope = errors.New("codex app-server turn did not return a durable child status envelope")

type RealAppServerDoer struct{}

func (RealAppServerDoer) Do(ctx context.Context, req Request) (Result, error) {
	if strings.TrimSpace(req.Address) == "" {
		return Result{}, fmt.Errorf("codex app-server address is required")
	}
	client := NewClient(req.Address, req.ApprovalHandler)
	defer client.Close(websocket.StatusNormalClosure, "done")
	if err := client.Connect(ctx); err != nil {
		return Result{}, err
	}
	if err := client.Initialize(ctx); err != nil {
		return Result{}, err
	}

	threadID := strings.TrimSpace(req.ThreadID)
	if threadID == "" {
		created, err := client.ThreadStart(ctx, codexThreadStartParams(req))
		if err != nil {
			return Result{}, err
		}
		threadID = created
	} else if err := client.ThreadResume(ctx, threadID, codexThreadResumeParams()); err != nil {
		created, createErr := client.ThreadStart(ctx, codexThreadStartParams(req))
		if createErr != nil {
			return Result{}, fmt.Errorf("resume codex app-server thread %q: %w (new thread also failed: %v)", threadID, err, createErr)
		}
		threadID = created
	}

	turnID, err := client.TurnStart(ctx, threadID, req.Prompt, codexTurnStartParams())
	if err != nil {
		return Result{}, err
	}
	result, err := client.StreamTurn(ctx, threadID, turnID)
	if err != nil {
		return Result{}, err
	}
	result.ThreadID = firstNonEmpty(strings.TrimSpace(result.ThreadID), threadID)
	result.TurnID = firstNonEmpty(strings.TrimSpace(result.TurnID), turnID)
	if len(result.EnvelopeRaw) == 0 {
		return result, ErrNoStatusEnvelope
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

type Client struct {
	address         string
	conn            *websocket.Conn
	approvalHandler ApprovalHandler
	mu              sync.Mutex
	approvalLog     []ApprovalDecision
	workEvents      []session.WorkCodexEvent
	notifications   int
}

func NewClient(address string, handlers ...ApprovalHandler) *Client {
	var handler ApprovalHandler
	if len(handlers) > 0 {
		handler = handlers[0]
	}
	return &Client{address: strings.TrimSpace(address), approvalHandler: handler}
}

func (c *Client) Connect(ctx context.Context) error {
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
	conn.SetReadLimit(MaxMessageBytes)
	c.conn = conn
	return nil
}

func (c *Client) Close(code websocket.StatusCode, reason string) {
	if c != nil && c.conn != nil {
		_ = c.conn.Close(code, reason)
	}
}

func (c *Client) Initialize(ctx context.Context) error {
	_, err := c.request(ctx, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "aphelion", "title": "Aphelion", "version": "0"},
		"capabilities": map[string]any{"experimentalApi": true},
	})
	if err != nil {
		return err
	}
	return c.notify(ctx, "initialized", map[string]any{})
}

func (c *Client) ThreadStart(ctx context.Context, params map[string]any) (string, error) {
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

func (c *Client) ThreadResume(ctx context.Context, threadID string, params map[string]any) error {
	payload := map[string]any{"threadId": strings.TrimSpace(threadID)}
	for k, v := range params {
		payload[k] = v
	}
	_, err := c.request(ctx, "thread/resume", payload)
	return err
}

func (c *Client) TurnStart(ctx context.Context, threadID string, text string, params map[string]any) (string, error) {
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

func (c *Client) StreamTurn(ctx context.Context, threadID string, turnID string) (Result, error) {
	return c.StreamTurnWithOptions(ctx, threadID, turnID, StreamOptions{})
}

func (c *Client) StreamTurnWithOptions(ctx context.Context, threadID string, turnID string, opts StreamOptions) (Result, error) {
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
				return Result{}, fmt.Errorf("codex app-server turn %s produced no notifications within %s", strings.TrimSpace(turnID), opts.FirstNotificationTimeout)
			}
			return Result{}, err
		}
		if _, ok := msg["id"]; ok {
			if method, _ := msg["method"].(string); method != "" {
				response := c.HandleServerRequest(method, asObject(msg["params"]))
				if err := c.writeMessage(ctx, map[string]any{"id": msg["id"], "result": response}); err != nil {
					return Result{}, err
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
				return Result{
					ThreadID:      threadID,
					TurnID:        turnID,
					Text:          strings.TrimSpace(text.String()),
					EnvelopeRaw:   raw,
					ApprovalLog:   append([]ApprovalDecision(nil), c.approvalLog...),
					CodexEvents:   c.WorkEvents(),
					PatchPreview:  WorkPatchPreviewFromEvents(c.WorkEvents()),
					Notifications: notifications,
					Completed:     completed,
				}, nil
			}
		}
	}
}

func (c *Client) request(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
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
				response := c.HandleServerRequest(method, asObject(msg["params"]))
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

func (c *Client) notify(ctx context.Context, method string, params map[string]any) error {
	return c.writeMessage(ctx, map[string]any{"method": method, "params": params})
}

func (c *Client) ReadMessage(ctx context.Context) (map[string]any, error) {
	return c.readMessage(ctx)
}

func (c *Client) readMessage(ctx context.Context) (map[string]any, error) {
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

func (c *Client) writeMessage(ctx context.Context, payload map[string]any) error {
	if c == nil || c.conn == nil {
		return fmt.Errorf("codex app-server websocket is not connected")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return c.conn.Write(ctx, websocket.MessageText, raw)
}

func (c *Client) HandleServerRequest(method string, params map[string]any) map[string]any {
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

func codexAppServerReadOnlyApprovalDecision(method string, params map[string]any) ApprovalDecision {
	decision := ApprovalDecision{Method: method, Decision: "cancel"}
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

func (c *Client) recordApproval(decision ApprovalDecision) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.approvalLog = append(c.approvalLog, decision)
}

func (c *Client) recordWorkNotification(method string, params map[string]any) {
	if c == nil {
		return
	}
	event, ok := WorkEventFromNotification(method, params)
	if !ok {
		return
	}
	c.recordWorkEvent(event)
}

func (c *Client) recordWorkServerRequest(method string, params map[string]any, decision ApprovalDecision) {
	if c == nil {
		return
	}
	event, ok := codexWorkEventFromServerRequest(method, params, decision)
	if !ok {
		return
	}
	c.recordWorkEvent(event)
}

func (c *Client) recordWorkEvent(event session.WorkCodexEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.workEvents = codexWorkAppendEvent(c.workEvents, event)
}

func (c *Client) ApprovalLog() []ApprovalDecision {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]ApprovalDecision(nil), c.approvalLog...)
}

func (c *Client) WorkEvents() []session.WorkCodexEvent {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]session.WorkCodexEvent(nil), c.workEvents...)
}

func AppServerCommandAllowed(command string) bool { return codexAppServerCommandAllowed(command) }

func codexAppServerCommandAllowed(command string) bool {
	return commandeffect.AppServerStatusCommandAllowed(command)
}
