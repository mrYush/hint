package openai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mrYush/hint/pkg/agentapi"
)

func TestModels(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[
			{"id":"gpt-4o","object":"model","created":1715367049,"owned_by":"openai"},
			{"id":"","object":"model"},
			{"id":"qwen2.5:7b","object":"model"}]}`))
	}))
	defer srv.Close()

	c := New(testProfile(srv.URL + "/v1/"))
	models, err := c.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/models" || gotAuth != "Bearer sk-test-1234567890abcdef" {
		t.Errorf("request: path %q auth %q", gotPath, gotAuth)
	}
	if len(models) != 2 || models[0].ID != "gpt-4o" || models[0].OwnedBy != "openai" || models[0].Created.IsZero() ||
		models[1].ID != "qwen2.5:7b" || !models[1].Created.IsZero() {
		t.Errorf("models = %+v", models)
	}

	// A base that names the completions path still finds /models.
	if got := modelsURL("https://x/route/openai/chat/completions"); got != "https://x/route/openai/models" {
		t.Errorf("modelsURL = %q", got)
	}
}

func TestModelsErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer srv.Close()
	_, err := New(testProfile(srv.URL)).Models(context.Background())
	var e *agentapi.Error
	if !errors.As(err, &e) || e.Kind != agentapi.ErrAuth || e.Provider != "test" {
		t.Fatalf("401: %v, want ErrAuth from profile test", err)
	}

	srv.Close()
	_, err = New(testProfile(srv.URL)).Models(context.Background())
	if !errors.As(err, &e) || e.Kind != agentapi.ErrNetwork {
		t.Fatalf("closed server: %v, want ErrNetwork", err)
	}
}
