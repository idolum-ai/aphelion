//go:build linux

package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
)

type testTransport struct {
	roundTrip func(*http.Request) (*http.Response, error)
}

func (t testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.roundTrip(req)
}

type failAfterReadCloser struct {
	data   []byte
	limit  int
	offset int
}

func (r *failAfterReadCloser) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	remainingAllowed := r.limit - r.offset
	if remainingAllowed <= 0 {
		return 0, errors.New("read past configured test limit")
	}
	n := copy(p, r.data[r.offset:])
	if n > remainingAllowed {
		n = remainingAllowed
	}
	r.offset += n
	return n, nil
}

func (r *failAfterReadCloser) Close() error {
	return nil
}

func encodeJSONResponse(t *testing.T, v interface{}) *http.Response {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(data)),
		Header:     http.Header{"Content-Type": {"application/json"}},
	}
}

func encodeHTTPJSONResponse(t *testing.T, status int, v interface{}) *http.Response {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewReader(data)),
		Header:     http.Header{"Content-Type": {"application/json"}},
	}
}

func TestClientRedactsBotTokenFromTransportErrors(t *testing.T) {
	t.Parallel()

	const token = "123456:secret-token"
	sentinel := errors.New("dial tcp: connection refused")
	transport := testTransport{
		roundTrip: func(req *http.Request) (*http.Response, error) {
			if !strings.Contains(req.URL.String(), token) {
				t.Fatalf("request URL = %q, want token-bearing Telegram URL", req.URL.String())
			}
			return nil, sentinel
		},
	}
	client := NewClient(token, WithHTTPClient(&http.Client{Transport: transport}))

	_, err := client.SendMessage(context.Background(), core.OutboundMessage{
		ChatID: 5,
		Text:   "hello",
	})
	if err == nil {
		t.Fatal("SendMessage() err = nil, want transport error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("error leaked Telegram token: %v", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("error = %v, want redaction marker", err)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("errors.Is(err, sentinel) = false for %v", err)
	}
}
