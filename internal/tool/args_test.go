package tool_test

import (
	"encoding/json"
	"testing"

	"github.com/mrYush/hint/internal/tool"
)

func TestInt_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    tool.Int
		wantErr bool
	}{
		{"number", `{"n": 42}`, 42, false},
		{"negative number", `{"n": -3}`, -3, false},
		{"quoted number", `{"n": "17"}`, 17, false},
		{"quoted with spaces", `{"n": " 8 "}`, 8, false},
		{"null keeps zero", `{"n": null}`, 0, false},
		{"empty string is zero", `{"n": ""}`, 0, false},
		{"absent", `{}`, 0, false},
		{"float rejected", `{"n": 1.5}`, 0, true},
		{"word rejected", `{"n": "ten"}`, 0, true},
		{"bool rejected", `{"n": true}`, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var in struct {
				N tool.Int `json:"n"`
			}
			err := tool.DecodeArgs(json.RawMessage(tt.raw), &in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if in.N != tt.want {
				t.Fatalf("n = %d, want %d", in.N, tt.want)
			}
		})
	}
}

func TestDecodeArgs_IgnoresUnknownFields(t *testing.T) {
	var in struct {
		Path string `json:"path"`
	}
	if err := tool.DecodeArgs(json.RawMessage(`{"path":"a","extra":1}`), &in); err != nil {
		t.Fatal(err)
	}
	if in.Path != "a" {
		t.Fatalf("path = %q", in.Path)
	}
}

func TestDecodeArgs_WrongTypeIsError(t *testing.T) {
	var in struct {
		Path string `json:"path"`
	}
	if err := tool.DecodeArgs(json.RawMessage(`{"path":123}`), &in); err == nil {
		t.Fatal("expected an error for a number where a string is required")
	}
}
