//go:build linux

package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

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
