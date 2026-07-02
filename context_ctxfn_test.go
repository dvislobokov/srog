package srog

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func TestCtxHelpers(t *testing.T) {
	var buf bytes.Buffer
	base := MustNew(WithWriter(&buf), WithLevel(VerboseLevel), WithTimestamp(false))
	ctx := base.ForContext("RequestId", "abc").IntoContext(context.Background())

	// Ctx alias returns the enriched logger, not the default.
	if Ctx(ctx) == Default() {
		t.Fatal("Ctx returned the default logger, want the context-scoped one")
	}

	InfoCtx(ctx, "hello {Name}", "neo")

	var m map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &m); err != nil {
		t.Fatalf("invalid json %q: %v", buf.String(), err)
	}
	if m["RequestId"] != "abc" {
		t.Fatalf("ctx helper lost the enriched field: %v", m)
	}
	if m["Name"] != "neo" || m["message"] != "hello neo" {
		t.Fatalf("ctx helper logged wrong content: %v", m)
	}
}

func TestCtxHelpersFallBackToDefault(t *testing.T) {
	// No logger in context: helpers must not panic and use Default.
	InfoCtx(context.Background(), "no logger here {X}", 1)
	if Ctx(context.Background()) != Default() {
		t.Fatal("Ctx with empty context should return Default")
	}
}
