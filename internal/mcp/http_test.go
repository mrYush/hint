package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mrYush/hint/pkg/agentapi"
)

func TestHTTPHeadersStayOnOrigin(t *testing.T) {
	var otherHits atomic.Int32
	var otherAuth atomic.Value
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherHits.Add(1)
		otherAuth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer other.Close()

	var originAuth atomic.Value
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, other.URL, http.StatusFound)
		case "/same":
			http.Redirect(w, r, "/ok", http.StatusFound)
		default:
			originAuth.Store(r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer origin.Close()

	client := newHTTPClient(ServerConfig{
		URL:     origin.URL,
		Headers: map[string]string{"Authorization": "Bearer secret"},
	})
	ctx := context.Background()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin.URL+"/ok", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("same-origin request: %v", err)
	}
	_ = resp.Body.Close()
	if got, _ := originAuth.Load().(string); got != "Bearer secret" {
		t.Errorf("origin Authorization = %q, want the configured header", got)
	}

	// A direct request to another origin uses the same transport, but the
	// origin-aware RoundTripper must not add the header.
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, other.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("other-origin request: %v", err)
	}
	_ = resp.Body.Close()
	if got, _ := otherAuth.Load().(string); got != "" {
		t.Errorf("other origin received Authorization %q", got)
	}

	// A redirect to another origin is refused before the request is sent.
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, origin.URL+"/redirect", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Do(req); err == nil {
		t.Error("cross-origin redirect was followed")
	}
	if got := otherHits.Load(); got != 1 {
		t.Errorf("other origin hits = %d; want only the direct request", got)
	}

	// A same-origin redirect keeps the credential.
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, origin.URL+"/same", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("same-origin redirect: %v", err)
	}
	_ = resp.Body.Close()
	if got, _ := originAuth.Load().(string); got != "Bearer secret" {
		t.Errorf("origin Authorization after redirect = %q, want the configured header", got)
	}
}

func TestHTTPStreamableServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	fake := newFakeServer(t, echoTool())
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return fake },
		&sdk.StreamableHTTPOptions{Stateless: true})
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			http.Error(w, "missing credential", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer httpServer.Close()

	m, err := NewManager([]ServerConfig{{
		Name:       "web",
		Transport:  TransportHTTP,
		URL:        httpServer.URL,
		Headers:    map[string]string{"Authorization": "Bearer secret"},
		Permission: agentapi.ClassRead,
	}}, Options{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	tools, err := m.Tools(ctx)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name() != "web.echo" {
		t.Fatalf("tools = %v", toolNames(tools))
	}
	res, err := tools[0].Run(ctx, "call-1", json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError || res.Text() != `{"text":"hi"}` {
		t.Errorf("result = %+v", res)
	}
}
