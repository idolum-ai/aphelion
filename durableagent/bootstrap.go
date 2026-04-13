//go:build linux

package durableagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

func WriteRemoteBootstrap(path string, bootstrap core.DurableAgentRemoteBootstrap) error {
	bootstrap = core.NormalizeDurableAgentRemoteBootstrap(bootstrap)
	if err := core.ValidateDurableAgentRemoteBootstrap(bootstrap); err != nil {
		return err
	}
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" {
		return fmt.Errorf("write durable agent remote bootstrap: path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create durable agent bootstrap dir: %w", err)
	}
	raw, err := json.MarshalIndent(bootstrap, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal durable agent remote bootstrap: %w", err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("write durable agent remote bootstrap: %w", err)
	}
	return nil
}

func ReadRemoteBootstrap(path string) (core.DurableAgentRemoteBootstrap, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" {
		return core.DurableAgentRemoteBootstrap{}, fmt.Errorf("read durable agent remote bootstrap: path is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return core.DurableAgentRemoteBootstrap{}, fmt.Errorf("read durable agent remote bootstrap: %w", err)
	}
	var bootstrap core.DurableAgentRemoteBootstrap
	if err := json.Unmarshal(raw, &bootstrap); err != nil {
		return core.DurableAgentRemoteBootstrap{}, fmt.Errorf("decode durable agent remote bootstrap: %w", err)
	}
	bootstrap = core.NormalizeDurableAgentRemoteBootstrap(bootstrap)
	if err := core.ValidateDurableAgentRemoteBootstrap(bootstrap); err != nil {
		return core.DurableAgentRemoteBootstrap{}, err
	}
	return bootstrap, nil
}
