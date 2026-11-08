package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mrYush/hint/pkg/agentapi"
)

func TestConvertResultContent(t *testing.T) {
	tests := []struct {
		name  string
		in    *sdk.CallToolResult
		want  string
		isErr bool
	}{
		{
			name: "text parts",
			in: &sdk.CallToolResult{Content: []sdk.Content{
				&sdk.TextContent{Text: "one"},
				&sdk.TextContent{Text: "two"},
			}},
			want: "one\ntwo",
		},
		{
			name: "embedded text",
			in: &sdk.CallToolResult{Content: []sdk.Content{
				&sdk.EmbeddedResource{Resource: &sdk.ResourceContents{URI: "file:///x", MIMEType: "text/plain", Text: "hello"}},
			}},
			want: "hello",
		},
		{
			name: "embedded blob",
			in: &sdk.CallToolResult{Content: []sdk.Content{
				&sdk.EmbeddedResource{Resource: &sdk.ResourceContents{URI: "file:///x", MIMEType: "application/octet-stream", Blob: []byte{1, 2, 3}}},
			}},
			want: "embedded resource file:///x (application/octet-stream, 3 bytes)",
		},
		{
			name: "resource link",
			in: &sdk.CallToolResult{Content: []sdk.Content{
				&sdk.ResourceLink{URI: "file:///x", Name: "x"},
			}},
			want: "resource file:///x (x)",
		},
		{
			name: "structured",
			in:   &sdk.CallToolResult{StructuredContent: map[string]any{"a": 1}},
			want: `{"a":1}`,
		},
		{
			name: "empty",
			in:   &sdk.CallToolResult{},
			want: "",
		},
		{
			name:  "empty error",
			in:    &sdk.CallToolResult{IsError: true},
			want:  "reported an error without a message",
			isErr: true,
		},
		{
			name:  "image",
			in:    &sdk.CallToolResult{Content: []sdk.Content{&sdk.ImageContent{MIMEType: "image/png"}}},
			want:  "cannot show yet",
			isErr: true,
		},
		{
			name:  "audio",
			in:    &sdk.CallToolResult{Content: []sdk.Content{&sdk.AudioContent{MIMEType: "audio/wav"}}},
			want:  "cannot play yet",
			isErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertResult("id", "srv.tool", tt.in)
			if got.IsError != tt.isErr {
				t.Errorf("IsError = %v, want %v", got.IsError, tt.isErr)
			}
			if !strings.Contains(got.Text(), tt.want) {
				t.Errorf("text = %q, want it to contain %q", got.Text(), tt.want)
			}
		})
	}
}

func TestNewToolAdapterRejectsBadNames(t *testing.T) {
	srv := &server{cfg: ServerConfig{Name: "srv", Permission: agentapi.ClassRead}}
	for _, name := range []string{"bad name", strings.Repeat("a", 129), "srv\u0000"} {
		if _, err := newToolAdapter(srv, &sdk.Tool{Name: name, InputSchema: objectSchema()}); err == nil {
			t.Errorf("newToolAdapter(%q) accepted the name", name)
		}
	}
	long := strings.Repeat("a", 125)
	if _, err := newToolAdapter(srv, &sdk.Tool{Name: long, InputSchema: objectSchema()}); err == nil {
		t.Errorf("newToolAdapter accepted a name that becomes longer than 128 bytes with the prefix")
	}
}

func TestCapDescription(t *testing.T) {
	desc := strings.Repeat("x", 100)
	for _, max := range []int{0, 1, 3, 4, 20, 100, 101} {
		got := capDescription(desc, max)
		if max <= 0 || max >= len(desc) {
			if got != desc {
				t.Errorf("capDescription(max=%d) = %q; want the input", max, got)
			}
			continue
		}
		if len(got) > max {
			t.Errorf("capDescription(max=%d) has length %d", max, len(got))
		}
		if max > len("…") && !strings.HasSuffix(got, "…") {
			t.Errorf("capDescription(max=%d) = %q; want an ellipsis", max, got)
		}
	}
}

func TestToolAdapterRunProtocolAndTimeout(t *testing.T) {
	t.Run("protocol error", func(t *testing.T) {
		m, _ := testManager(t, []fakeTool{{
			name: "boom", description: "Fails.", schema: objectSchema(),
			handler: func(context.Context, json.RawMessage) (*sdk.CallToolResult, error) {
				return nil, errors.New("server exploded")
			},
		}}, ServerConfig{}, Options{})
		tools, err := m.Tools(context.Background())
		if err != nil {
			t.Fatalf("Tools: %v", err)
		}
		res, err := tools[0].Run(context.Background(), "id", json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if !res.IsError || !strings.Contains(res.Text(), "server exploded") {
			t.Errorf("result = %+v; want the protocol error as an error result", res)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		blocked := make(chan struct{})
		defer close(blocked)
		m, _ := testManager(t, []fakeTool{{
			name: "slow", description: "Sleeps.", schema: objectSchema(),
			handler: func(ctx context.Context, _ json.RawMessage) (*sdk.CallToolResult, error) {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-blocked:
					return textResult("late"), nil
				}
			},
		}}, ServerConfig{Timeout: 20 * time.Millisecond}, Options{})
		tools, err := m.Tools(context.Background())
		if err != nil {
			t.Fatalf("Tools: %v", err)
		}
		res, err := tools[0].Run(context.Background(), "id", json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if !res.IsError || !strings.Contains(res.Text(), "timed out") {
			t.Errorf("result = %+v; want a timeout error result", res)
		}
	})
	t.Run("canceled context aborts", func(t *testing.T) {
		m, _ := testManager(t, []fakeTool{echoTool()}, ServerConfig{}, Options{})
		tools, err := m.Tools(context.Background())
		if err != nil {
			t.Fatalf("Tools: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := tools[0].Run(ctx, "id", json.RawMessage(`{}`)); err == nil {
			t.Fatal("Run returned no error for a canceled context")
		}
	})
}

func TestConvertResultKeepsStructuredWithText(t *testing.T) {
	got := convertResult("id", "srv.tool", &sdk.CallToolResult{
		Content:           []sdk.Content{&sdk.TextContent{Text: "summary"}},
		StructuredContent: map[string]any{"a": 1},
	})
	text := got.Text()
	if !strings.Contains(text, "summary") {
		t.Errorf("text = %q; lost the text part", text)
	}
	if !strings.Contains(text, `{"a":1}`) {
		t.Errorf("text = %q; lost the structured part", text)
	}
	if got.IsError {
		t.Errorf("IsError = true; want a successful result")
	}
}
