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
	for _, i := range []int{0, 1} {
		cfg.Providers[i].Model += "-new"
		if summaryProviderKey(cfg) == original {
			t.Errorf("changing the model of %s did not change the cache key", cfg.Providers[i].Name)
		}
		cfg.Providers[i].Model = strings.TrimSuffix(cfg.Providers[i].Model, "-new")
	}
	cfg.Providers[0].APIKey = "rotated-key"
	if summaryProviderKey(cfg) != original {
		t.Error("rotating the API key changed the cache key")
	}
}
