//go:build linux

package durableagent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/idolum-ai/aphelion/core"
)

func performJSONRequest(t *testing.T, handler http.Handler, method string, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return performJSONRequestWithContext(t, handler, method, path, body, context.Background())
}

func performJSONRequestWithIdentity(t *testing.T, handler http.Handler, method string, path string, body any, identity core.TailnetPeerIdentity) *httptest.ResponseRecorder {
	t.Helper()
	return performJSONRequestWithContext(t, handler, method, path, body, core.WithTailnetPeerIdentity(context.Background(), identity))
}

func performJSONRequestWithContext(t *testing.T, handler http.Handler, method string, path string, body any, ctx context.Context) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal(body) err = %v", err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
