//go:build linux

package main

import (
	"net/http"
	"testing"

	"github.com/idolum-ai/aphelion/config"
)

func TestBuildOpenAIPlatformServicesDisabledReturnsNil(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	fileStore, retrievalStore, err := buildOpenAIPlatformServices(&cfg, &http.Client{})
	if err != nil {
		t.Fatalf("buildOpenAIPlatformServices() err = %v", err)
	}
	if fileStore != nil || retrievalStore != nil {
		t.Fatalf("stores = %#v / %#v, want nil when disabled", fileStore, retrievalStore)
	}
}

func TestBuildOpenAIPlatformServicesEnabledBuildsRequestedClients(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Providers.OpenAI.APIKey = "sk-test"
	cfg.OpenAI.Files.Enabled = true
	cfg.OpenAI.VectorStores.Enabled = true

	fileStore, retrievalStore, err := buildOpenAIPlatformServices(&cfg, &http.Client{})
	if err != nil {
		t.Fatalf("buildOpenAIPlatformServices() err = %v", err)
	}
	if fileStore == nil {
		t.Fatal("fileStore = nil, want client")
	}
	if retrievalStore == nil {
		t.Fatal("retrievalStore = nil, want client")
	}
}
