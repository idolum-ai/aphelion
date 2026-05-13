//go:build linux

package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

func (o *OpenAI) doJSONRequest(ctx context.Context, endpoint string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	if o.userAgent != "" {
		req.Header.Set("User-Agent", o.userAgent)
	}
	return o.client.Do(req)
}

func boolPtr(v bool) *bool {
	return &v
}
