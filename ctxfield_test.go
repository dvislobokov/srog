package srog

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func TestContextFieldExtraction(t *testing.T) {
	// Register an extractor that mimics what srogotel does for trace IDs.
	type traceKey struct{}
	AddContextField(func(ctx context.Context) []Field {
		v, ok := ctx.Value(traceKey{}).(string)
		if !ok {
			return nil
		}
		return []Field{{Name: "trace_id", Value: v}, {Name: "span_id", Value: "s-" + v}}
	})

	var buf bytes.Buffer
	base := MustNew(WithWriter(&buf), WithTimestamp(false))
	ctx := base.IntoContext(context.WithValue(context.Background(), traceKey{}, "abc123"))

	InfoCtx(ctx, "work {Step}", 1)

	var m map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &m); err != nil {
		t.Fatalf("invalid json %q: %v", buf.String(), err)
	}
	if m["trace_id"] != "abc123" || m["span_id"] != "s-abc123" {
		t.Fatalf("context fields not attached: %v", m)
	}
	if m["Step"].(float64) != 1 {
		t.Fatalf("template field missing: %v", m)
	}
}
