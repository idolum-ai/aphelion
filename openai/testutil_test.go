//go:build linux

package openai

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func newTestClient(t *testing.T, fn roundTripFunc) *Client {
	t.Helper()

	client, err := NewClient(ClientOptions{
		APIKey:     "test-key",
		BaseURL:    "https://api.test",
		HTTPClient: &http.Client{Transport: fn},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return client
}

func jsonResponse(t *testing.T, status int, value any) *http.Response {
	t.Helper()

	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(payload)),
	}
}

func textResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"text/plain"}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}
