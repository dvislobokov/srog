// Package srogotel bridges OpenTelemetry tracing into srog: once installed, logs
// emitted through srog.Ctx or the srog.*Ctx helpers automatically carry the
// active span's trace_id and span_id, so log lines correlate with traces in
// Kibana/Jaeger/Tempo without per-call plumbing.
//
//	func main() {
//	    srogotel.Install()               // register the extractor once
//	    ...
//	}
//
//	func handler(ctx context.Context) {
//	    srog.InfoCtx(ctx, "charged {Amount}", 999) // includes trace_id, span_id
//	}
package srogotel

import (
	"context"

	"github.com/dvislobokov/srog"
	"go.opentelemetry.io/otel/trace"
)

// Fields returns the active span's trace_id and span_id from ctx, or nil when no
// valid span is present. It satisfies srog.ContextFieldFunc.
func Fields(ctx context.Context) []srog.Field {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return nil
	}
	return []srog.Field{
		{Name: "trace_id", Value: sc.TraceID().String()},
		{Name: "span_id", Value: sc.SpanID().String()},
	}
}

// Install registers Fields with srog so every context-scoped log is enriched
// with trace correlation. Call it once during startup; it is safe for
// concurrent use.
func Install() { srog.AddContextField(Fields) }
