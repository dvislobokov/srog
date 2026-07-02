package srog_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dvislobokov/srog"
)

// TestCallerPointsAtCallSite runs from an external package (the realistic case:
// the caller is not inside package srog) and guards the fix that moved caller
// resolution off zerolog's fixed context skip — which always reported srog's own
// write frame — to a frame walk that skips srog's internals and reports the real
// call site, robustly to inlining of the level methods.
func TestCallerPointsAtCallSite(t *testing.T) {
	var buf bytes.Buffer
	log := srog.MustNew(srog.WithWriter(&buf), srog.WithTimestamp(false), srog.WithCaller(true))
	log.Information("hi {X}", 1) // <- this line is the expected caller
	log.Error(nil, "boom {Y}", 2)

	dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	for i := 0; ; i++ {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			if i < 2 {
				t.Fatalf("expected 2 events, got %d", i)
			}
			break
		}
		caller, _ := m["caller"].(string)
		if caller == "" {
			t.Fatalf("event %d has no caller field", i)
		}
		if strings.Contains(caller, "srog.go") {
			t.Fatalf("event %d caller points at srog internals: %q", i, caller)
		}
		if !strings.Contains(caller, "caller_external_test.go") {
			t.Fatalf("event %d caller should be this test file, got %q", i, caller)
		}
	}
}
