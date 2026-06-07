//go:build linux

package codex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/idolum-ai/aphelion/core"
)

func TestNewCodexHTTPClientDoesNotUseEndToEndTimeout(t *testing.T) {
	t.Parallel()

	client := NewHTTPClient(90 * time.Second)
	if client == nil {
		t.Fatal("NewHTTPClient() = nil")
	}
	if client.Timeout != 0 {
		t.Fatalf("client.Timeout = %v, want 0 for long-running Codex requests", client.Timeout)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("client.Transport = %T, want *http.Transport", client.Transport)
	}
	if transport.ResponseHeaderTimeout != 90*time.Second {
		t.Fatalf("ResponseHeaderTimeout = %v, want 90s", transport.ResponseHeaderTimeout)
	}
}

func TestRealAppServerDoerHonorsRequestApprovalHandler(t *testing.T) {
	requireLocalTCPListener(t, "localhost:0")
	t.Parallel()

	now := time.Date(2026, 5, 27, 15, 0, 0, 0, time.UTC)
	envelope := testStatusEnvelopeJSON(t, "child-approval-handler", now)
	serverDecision := make(chan string, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		write := func(payload map[string]any) bool {
			raw, err := json.Marshal(payload)
			if err != nil {
				return false
			}
			return conn.Write(context.Background(), websocket.MessageText, raw) == nil
		}
		readRequest := func(wantMethod string) (string, bool) {
			for {
				_, raw, err := conn.Read(context.Background())
				if err != nil {
					return "", false
				}
				var msg map[string]any
				if err := json.Unmarshal(raw, &msg); err != nil {
					return "", false
				}
				id, _ := msg["id"].(string)
				method, _ := msg["method"].(string)
				if id == "" || method != wantMethod {
					continue
				}
				return id, true
			}
		}

		id, ok := readRequest("initialize")
		if !ok || !write(map[string]any{"id": id, "result": map[string]any{}}) {
			return
		}
		id, ok = readRequest("thread/start")
		if !ok || !write(map[string]any{"id": id, "result": map[string]any{"thread": map[string]any{"id": "thread-approval-handler"}}}) {
			return
		}
		id, ok = readRequest("turn/start")
		if !ok || !write(map[string]any{"id": id, "result": map[string]any{"turn": map[string]any{"id": "turn-approval-handler"}}}) {
			return
		}
		if !write(map[string]any{
			"id":     "approval-1",
			"method": "item/commandExecution/requestApproval",
			"params": map[string]any{"command": "rm -rf /tmp/aphelion-test", "reason": "test custom policy"},
		}) {
			return
		}
		for {
			_, raw, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			var msg map[string]any
			if err := json.Unmarshal(raw, &msg); err != nil {
				return
			}
			if id, _ := msg["id"].(string); id != "approval-1" {
				continue
			}
			serverDecision <- stringField(asObject(msg["result"]), "decision")
			break
		}
		_ = write(map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{"turnId": "turn-approval-handler", "delta": string(envelope)}})
		_ = write(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thread-approval-handler", "turn": map[string]any{"id": "turn-approval-handler"}}})
	}))
	defer server.Close()

	result, err := (RealAppServerDoer{}).Do(context.Background(), Request{
		Agent:   core.DurableAgent{AgentID: "child-approval-handler"},
		Address: "ws://" + strings.TrimPrefix(server.URL, "http://"),
		Prompt:  "report status",
		Now:     now,
		ApprovalHandler: func(method string, params map[string]any) ApprovalDecision {
			return ApprovalDecision{Method: method, Command: stringField(params, "command"), Decision: "accept"}
		},
	})
	if err != nil {
		t.Fatalf("Do() err = %v", err)
	}

	select {
	case got := <-serverDecision:
		if got != "accept" {
			t.Fatalf("server approval decision = %q, want accept", got)
		}
	default:
		t.Fatal("server did not receive approval response")
	}
	if len(result.ApprovalLog) != 1 {
		t.Fatalf("ApprovalLog len = %d, want 1", len(result.ApprovalLog))
	}
	if got := result.ApprovalLog[0]; got.Decision != "accept" || got.Command != "rm -rf /tmp/aphelion-test" {
		t.Fatalf("ApprovalLog[0] = %#v, want custom accept decision", got)
	}
}

func TestNewClientDefaultsToReadOnlyApprovalHandler(t *testing.T) {
	t.Parallel()

	client := NewClient("ws://127.0.0.1:1")
	accepted := client.HandleServerRequest("item/commandExecution/requestApproval", map[string]any{"command": "hostname"})
	if got := stringField(accepted, "decision"); got != "accept" {
		t.Fatalf("read-only approval decision = %q, want accept", got)
	}
	declined := client.HandleServerRequest("item/commandExecution/requestApproval", map[string]any{"command": "git commit -am fix"})
	if got := stringField(declined, "decision"); got != "decline" {
		t.Fatalf("write approval decision = %q, want decline", got)
	}
	log := client.ApprovalLog()
	if len(log) != 2 {
		t.Fatalf("ApprovalLog len = %d, want 2", len(log))
	}
	if log[0].Decision != "accept" || log[1].Decision != "decline" {
		t.Fatalf("ApprovalLog decisions = %#v, want accept then decline", log)
	}
}

func testStatusEnvelopeJSON(t *testing.T, agentID string, now time.Time) []byte {
	t.Helper()
	payload := json.RawMessage(`{"display_name":"Codex","mode":"read_only"}`)
	hash, err := core.DurableChildStatusPayloadHash(payload)
	if err != nil {
		t.Fatalf("DurableChildStatusPayloadHash() err = %v", err)
	}
	raw, err := json.Marshal(core.DurableChildStatusEnvelope{
		Kind:              core.DurableChildStatusEnvelopeKind,
		AgentID:           agentID,
		SchemaVersion:     "codex.status.v1",
		GeneratedAt:       now,
		CapabilityPosture: "read_only",
		Payload:           payload,
		PayloadHash:       hash,
	})
	if err != nil {
		t.Fatalf("Marshal status envelope err = %v", err)
	}
	return raw
}
