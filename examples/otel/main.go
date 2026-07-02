// Command otel shows trace/log correlation: after srogotel.Install(), any log
// made through srog.Ctx / srog.*Ctx automatically carries the active span's
// trace_id and span_id, so log lines join up with traces in Kibana/Jaeger/Tempo.
// Output is JSON so the injected fields are visible.
//
//	go run .
package main

import (
	"context"

	"github.com/dvislobokov/srog"
	"github.com/dvislobokov/srog/srogotel"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func main() {
	// A tracer provider (no exporter needed to demonstrate correlation).
	tp := sdktrace.NewTracerProvider()
	defer func() { _ = tp.Shutdown(context.Background()) }()
	otel.SetTracerProvider(tp)

	// Register the OpenTelemetry extractor with srog — once, at startup.
	srogotel.Install()

	log := srog.MustNew(srog.WithTimestamp(false)) // JSON to stdout
	defer log.Close()

	tracer := otel.Tracer("checkout")

	// Stash the logger in the context, then start a span on it. srog.Ctx reads
	// both back: the logger and the span's trace/span IDs.
	ctx, span := tracer.Start(log.IntoContext(context.Background()), "checkout")
	defer span.End()

	srog.InfoCtx(ctx, "processing order {OrderId}", 42) // carries trace_id + span_id

	// A child span produces a new span_id under the same trace_id.
	childCtx, child := tracer.Start(ctx, "charge")
	srog.Ctx(childCtx).Information("charging {Amount} cents", 999)
	child.End()

	// Outside any span (same logger, no span in the context) no trace fields are
	// added — the extractor simply returns nothing.
	srog.InfoCtx(log.IntoContext(context.Background()), "no active span here")
}
