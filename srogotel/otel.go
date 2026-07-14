// Package srogotel bridges OpenTelemetry into srog in both directions.
//
// Trace correlation: once installed, logs emitted through srog.Ctx or the
// srog.*Ctx helpers automatically carry the active span's trace_id and span_id,
// so log lines correlate with traces in Kibana/Jaeger/Tempo without per-call
// plumbing.
//
//	func main() {
//	    srogotel.Install()               // register the extractor once
//	    ...
//	}
//
//	func handler(ctx context.Context) {
//	    srog.InfoCtx(ctx, "charged {Amount}", 999) // includes trace_id, span_id
//	}
//
// Log export: the Sink (see NewSink / WithLogs) re-emits every srog event as an
// OpenTelemetry log record via the Logs Bridge API, so logs ship to the OTel
// Collector through the same pipeline as traces and metrics — reusing an
// already-configured LoggerProvider or building a private OTLP exporter:
//
//	opt, sink, err := srogotel.WithLogs(ctx, srogotel.Config{}) // global provider
//	if err != nil { ... }
//	defer sink.Close()
//	log := srog.MustNew(srog.WithConsole(), opt)
//
// Config.Attributes optionally stamps static attributes onto every record —
// routing hints Collector exporters read (e.g. "data_stream.dataset" or
// "elasticsearch.index" for a dynamic Elasticsearch index); an event field with
// the same name wins. Importing this package also registers the "otlp" sink
// type with srog's declarative config, so a logging.json can declare:
//
//	{"type": "otlp", "options": {"endpoint": "collector:4317", "insecure": true,
//	                             "attributes": {"data_stream.dataset": "billing"}}}
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
