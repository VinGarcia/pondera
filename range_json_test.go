package pondera

import (
	"encoding/json"
	"testing"
)

// TestRangeJSONRoundTrip covers the JSON encoding the HTTP API relies on: a
// criterion's Range must survive Marshal→Unmarshal as the same two-element
// array the SPA reads and edits. Before Range grew MarshalJSON/UnmarshalJSON it
// serialized as an empty object and silently dropped incoming arrays, so the
// range was invisible and uneditable in the browser.
func TestRangeJSONRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		rng      Range
		wantJSON string
	}{
		{name: "default unset", rng: Range{}, wantJSON: `[0,100]`},
		{name: "custom window", rng: NewRange(FixedAnchor(40), FixedAnchor(80)), wantJSON: `[40,80]`},
		{name: "zero-anchored ratio", rng: NewRange(FixedAnchor(0), MaxAnchor()), wantJSON: `[0,"max"]`},
		{name: "field-relative", rng: NewRange(MinAnchor(), MaxAnchor()), wantJSON: `["min","max"]`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data, err := json.Marshal(test.rng)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(data) != test.wantJSON {
				t.Fatalf("Marshal = %s, want %s", data, test.wantJSON)
			}
			var got Range
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			// The unset default and an explicit [0,100] normalize to the same anchors,
			// so compare through String() rather than the struct's set flag.
			if got.String() != test.rng.String() {
				t.Fatalf("round-trip = %s, want %s", got.String(), test.rng.String())
			}
		})
	}
}

// TestRangeJSONInCriterion proves the range round-trips through a whole
// Criterion — the payload the SPA actually POSTs/PUTs — not just in isolation.
func TestRangeJSONInCriterion(t *testing.T) {
	in := Criterion{Name: "price", Weight: 2, Direction: Cost, Range: NewRange(FixedAnchor(0), MaxAnchor())}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got Criterion
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Range.String() != `[0, "max"]` {
		t.Fatalf("criterion range = %s, want [0, \"max\"]", got.Range.String())
	}
	if got.Direction != Cost {
		t.Fatalf("direction = %v, want Cost", got.Direction)
	}
}

// TestRangeJSONErrors confirms a malformed range payload is rejected, mirroring
// the TOML decoder — a bad anchor from the API is a client error, not a silent
// fallback to the default.
func TestRangeJSONErrors(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{name: "wrong length", json: `[0]`},
		{name: "unknown keyword", json: `[0,"most"]`},
		{name: "bad type", json: `[0,true]`},
		{name: "not an array", json: `{"lo":0}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var r Range
			if err := json.Unmarshal([]byte(test.json), &r); err == nil {
				t.Fatalf("Unmarshal(%s) = nil error, want rejection", test.json)
			}
		})
	}
}

// TestRangeJSONNullIsUnset documents that a null or absent range leaves the
// default in effect, so a client that never edits the range need not send one.
func TestRangeJSONNullIsUnset(t *testing.T) {
	var r Range
	if err := json.Unmarshal([]byte(`null`), &r); err != nil {
		t.Fatalf("Unmarshal(null): %v", err)
	}
	if r.String() != "[0, 100]" {
		t.Fatalf("null range = %s, want default [0, 100]", r.String())
	}
}
