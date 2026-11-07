package repomap

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRequestValidation(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{}`, `{"repo":"app","path":"src","symbol":"Open","match":"prefix","limit":1}`, `{"cursor":"token"}`, `{"cursor":"x","path":""}`, `{"path":"","entry_id":"x"}`, `{"match":"prefix"}`, `{"limit":0}`, `{"limit":51}`, `{"unknown":1}`, `{} {}`, `{"repo":null}`, `{"entry_id":""}`} {
		t.Run(raw, func(t *testing.T) {
			_, err := decodeRequest(json.RawMessage(raw))
			valid := raw == `{}` || raw == `{"repo":"app","path":"src","symbol":"Open","match":"prefix","limit":1}` || raw == `{"cursor":"token"}`
			if (err == nil) != valid {
				t.Fatalf("decode = %v; valid %v", err, valid)
			}
		})
	}
}

func TestOutputBudgetPreservesJSONAndAddressableStub(t *testing.T) {
	r := Response{
		RootID: "r_" + strings.Repeat("r", 43), MembershipRevision: 18446744073709551615,
		Generation: 18446744073709551615, Scope: Scope{EntryID: "e_" + strings.Repeat("e", 43)}, Order: "path",
		Status: Status{State: "paused", Reason: "resource_profile_unvalidated"}, Files: []File{}, Entries: []Declaration{},
		Verification: &Verification{BudgetExhausted: false},
		Page:         Page{Truncated: true, NextCursor: strings.Repeat("c", 96), StopReason: "byte_budget", ExpiresAt: "2026-11-06T12:00:00Z"},
	}
	f := File{ID: "e_" + strings.Repeat("f", 43), PathDisplay: strings.Repeat("\"\\界", 2000),
		SourceVersion: "sha256:" + strings.Repeat("h", 43), Freshness: "content_checked", CheckedAt: "2026-11-06T12:00:00Z"}
	d := Declaration{ID: "s_" + strings.Repeat("s", 43), FileID: f.ID, StartByte: 9223372036854775807, EndByte: 9223372036854775807,
		Name: "Huge", Signature: strings.Repeat("\"\\界", 4000)}
	if !appendEntry(&r, f, d, MinOutputBytes) {
		data, _ := json.Marshal(r)
		t.Fatalf("minimal entry does not fit; envelope %d", len(data))
	}
	data, err := encode(r, MinOutputBytes)
	if err != nil || !json.Valid(data) {
		t.Fatalf("invalid payload: %s, %v", data, err)
	}
	if r.Entries[0].ID != d.ID || r.Entries[0].FileID != f.ID || r.Entries[0].OmissionReason == "" {
		t.Fatalf("lost addressable stub: %+v", r.Entries[0])
	}
	before := len(r.Entries)
	if appendEntry(&r, f, d, MinOutputBytes) || len(r.Entries) != before {
		t.Fatal("full page consumed another candidate")
	}
}

func TestErrorPayloadIsBounded(t *testing.T) {
	text := errorPayload("invalid_args", strings.Repeat("\"界", 1000), false)
	if !json.Valid([]byte(text)) || len(text) > MinOutputBytes {
		t.Fatalf("unbounded error: %d", len(text))
	}
}

func TestInvalidationsCanFinishAPageBeforeTheNextStub(t *testing.T) {
	r := Response{RootID: "r_" + strings.Repeat("r", 43), MembershipRevision: ^uint64(0), Generation: ^uint64(0),
		Scope: Scope{EntryID: "e_" + strings.Repeat("e", 43)}, Order: "path", Status: Status{State: "paused", Reason: "resource_profile_unvalidated"},
		Files: []File{}, Entries: []Declaration{}, Verification: &Verification{},
		Page: Page{Truncated: true, StopReason: "work_budget", NextCursor: strings.Repeat("c", 96), ExpiresAt: "2026-11-06T12:00:00Z"}}
	f := File{ID: "e_" + strings.Repeat("f", 43), SourceVersion: "sha256:" + strings.Repeat("h", 43), Freshness: "content_checked", CheckedAt: "2026-11-06T12:00:00Z"}
	d := Declaration{ID: "s_" + strings.Repeat("s", 43), FileID: f.ID, StartByte: 9223372036854775807, EndByte: 9223372036854775807, Signature: strings.Repeat("x", 2048)}
	if !appendInvalidation(&r, "source_changed", 1024) {
		t.Fatal("empty progress page cannot report invalidation")
	}
	if appendEntry(&r, f, d, 1024) {
		t.Fatal("fixture no longer exercises a progress-only page")
	}
	if len(r.Entries) != 0 || r.Invalidations["source_changed"] != 1 {
		t.Fatalf("invalidation or candidate lost: %+v", r)
	}
	r.Invalidations = nil
	if !appendEntry(&r, f, d, 1024) {
		t.Fatal("next page cannot admit the unconsumed stub")
	}
}
