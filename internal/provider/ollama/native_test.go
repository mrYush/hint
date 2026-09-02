package ollama

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mrYush/hint/pkg/agentapi"
)

func TestNativeBaseURLStripsV1(t *testing.T) {
	tests := []struct{ in, want string }{
		{"http://localhost:11434/v1", "http://localhost:11434"},
		{"http://localhost:11434/v1/", "http://localhost:11434"},
		{"http://localhost:11434", "http://localhost:11434"},
		{"http://host:11434/prefix/v1", "http://host:11434/prefix"},
	}
	for _, tt := range tests {
		if got := nativeBaseURL(tt.in); got != tt.want {
			t.Errorf("nativeBaseURL(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestReady(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"version":"0.6.0"}`))
	}))
	defer srv.Close()

	c := New(srv.URL+"/v1", WithHTTPClient(srv.Client()))
	if err := c.Ready(context.Background()); err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if gotPath != "/api/version" {
		t.Errorf("path = %q, want /api/version", gotPath)
	}
}

func TestReadyDownDaemon(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	c := New(url + "/v1")
	err := c.Ready(context.Background())
	if err == nil {
		t.Fatal("Ready succeeded against a dead daemon")
	}
	if kind := agentapi.KindOf(err); kind != agentapi.ErrUnavailable {
		t.Errorf("kind = %s, want unavailable", kind)
	}
}

func TestList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"models":[
			{"name":"qwen2.5:7b","size":4683087332,"modified_at":"2025-08-01T10:00:00Z"},
			{"name":"llama3.2:3b","size":2019393189,"modified_at":"2025-07-15T08:30:00Z"}
		]}`))
	}))
	defer srv.Close()

	c := New(srv.URL+"/v1", WithHTTPClient(srv.Client()))
	models, err := c.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(models) != 2 || models[0].Name != "qwen2.5:7b" || models[1].Name != "llama3.2:3b" {
		t.Errorf("models = %+v", models)
	}
	if models[0].SizeBytes == 0 || models[0].ModifiedAt.IsZero() {
		t.Errorf("models[0] lost fields: %+v", models[0])
	}
}
