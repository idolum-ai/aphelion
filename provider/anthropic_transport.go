//go:build linux

package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

func (a *Anthropic) doRequest(ctx context.Context, reqBody anthropicRequest) (*http.Response, error) {
	buf := bytes.Buffer{}
	if err := json.NewEncoder(&buf).Encode(reqBody); err != nil {
		return nil, fmt.Errorf("anthropic: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, &buf)
	if err != nil {
		return nil, fmt.Errorf("anthropic: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("Anthropic-Version", a.version)
	if a.userAgent != "" {
		req.Header.Set("User-Agent", a.userAgent)
	}

	return a.client.Do(req)
}

type apiError struct {
	statusCode int
	message    string
}

func (e apiError) Error() string {
	return e.message
}

func (e apiError) StatusCode() int {
	return e.statusCode
}
