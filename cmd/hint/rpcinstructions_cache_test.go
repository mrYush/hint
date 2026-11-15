package main

import (
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/config"
)

func TestSummaryProviderKey(t *testing.T) {
	cfg := &config.Config{
		DefaultProvider:  "primary",
		FallbackProvider: "fallback",
		Providers: []config.Profile{
			{Name: "primary", Kind: config.KindOpenAI, BaseURL: "https://primary.example/v1", Model: "primary-model", APIKey: "private-primary-key"},
			{Name: "fallback", Kind: config.KindOllama, BaseURL: "https://fallback.example/v1", Model: "fallback-model"},
		},
	}
	original := summaryProviderKey(cfg)
	if strings.Contains(original, cfg.Providers[0].APIKey) {
		t.Fatal("cache key contains the API key")
	}
	cfg.Providers[0].Model = "new-primary-model"
	if summaryProviderKey(cfg) == original {
		t.Error("changing the primary model did not change the cache key")
	}
	cfg.Providers[0].Model = "primary-model"
	cfg.Providers[1].Model = "new-fallback-model"
	if summaryProviderKey(cfg) != original {
		t.Error("changing the fallback model changed the primary cache key")
	}
	cfg.Providers[0].APIKey = "rotated-key"
	if summaryProviderKey(cfg) != original {
		t.Error("rotating the API key changed the cache key")
	}
}
