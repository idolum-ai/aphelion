//go:build linux

package provider

import (
	"fmt"
	"strings"
)

type anthropicCacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

type anthropicCachePolicy struct {
	Strategy string
	TTL      string
}

func newAnthropicCachePolicy(strategy string, ttl string) (anthropicCachePolicy, error) {
	strategy = strings.ToLower(strings.TrimSpace(strategy))
	if strategy == "" {
		strategy = "explicit"
	}
	switch strategy {
	case "auto", "explicit", "hybrid", "off":
	default:
		return anthropicCachePolicy{}, fmt.Errorf("anthropic: cache strategy must be one of auto|explicit|hybrid|off")
	}
	ttl = strings.ToLower(strings.TrimSpace(ttl))
	if ttl == "" {
		ttl = "5m"
	}
	switch ttl {
	case "5m", "1h":
	default:
		return anthropicCachePolicy{}, fmt.Errorf("anthropic: cache ttl must be one of 5m|1h")
	}
	return anthropicCachePolicy{Strategy: strategy, TTL: ttl}, nil
}

func (p anthropicCachePolicy) cacheControl() *anthropicCacheControl {
	if p.Strategy == "off" {
		return nil
	}
	return &anthropicCacheControl{Type: "ephemeral", TTL: p.TTL}
}
