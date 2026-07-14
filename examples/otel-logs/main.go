// Command otel-logs ships srog events to an OpenTelemetry Collector as OTLP log
// records via the srogotel sink, alongside trace correlation.
//
// Variant A reuses the process-global LoggerProvider — the one an application
// typically configures right next to its tracer/meter providers — so logs flow
// through the exact same exporter, resource, and batching setup as traces and
// metrics. Variant B (commented) builds a private OTLP exporter from explicit
// parameters instead.
//
// Run a local collector first, e.g.:
//
//	docker run --rm -p 4317:4317 otel/opentelemetry-collector
//	go run .
package main

import (
	"context"
	"time"

	"github.com/dvislobokov/srog"
	"github.com/dvislobokov/srog/srogotel"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/log/global"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func main() {
	ctx := context.Background()

	// The application's own OTel setup — the same place traces and metrics are
	// configured. Registering the LoggerProvider globally is all srog needs.
	exp, err := otlploggrpc.New(ctx,
		otlploggrpc.WithEndpoint("localhost:4317"),
		otlploggrpc.WithInsecure(),
	)
	if err != nil {
		panic(err)
	}
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)))
	defer func() { _ = lp.Shutdown(ctx) }()
	global.SetLoggerProvider(lp)

	tp := sdktrace.NewTracerProvider()
	defer func() { _ = tp.Shutdown(ctx) }()
	otel.SetTracerProvider(tp)

	// Trace correlation: context-scoped logs carry trace_id/span_id, which the
	// sink promotes into the record's trace context.
	srogotel.Install()

	// Variant A — reuse the already-configured global provider (zero config).
	opt, sink, err := srogotel.WithLogs(ctx, srogotel.Config{})
	if err != nil {
		panic(err)
	}
	defer sink.Close()

	// Variant B — private exporter with explicit parameters instead:
	//
	//	opt, sink, err := srogotel.WithLogs(ctx, srogotel.Config{
	//	    Endpoint:   "localhost:4317", // or Protocol: "http" + port 4318
	//	    Insecure:   true,
	//	    Headers:    map[string]string{"authorization": "Bearer ..."},
	//	    // Optional: stamped onto every record; Collector exporters read
	//	    // these for routing (e.g. elasticsearch dynamic index).
	//	    Attributes: map[string]string{"data_stream.dataset": "checkout"},
	//	})
	//
	// Variant C — declarative JSON config (see logging.json next to this file);
	// importing srogotel is what registers the "otlp" sink type:
	//
	//	log, err := srog.NewFromConfigFile("logging.json")

	log := srog.MustNew(srog.WithConsole(), opt)
	defer log.Close()

	tracer := otel.Tracer("checkout")
	spanCtx, span := tracer.Start(log.IntoContext(ctx), "checkout")
	srog.InfoCtx(spanCtx, "processing order {OrderId}", 42) // correlated with the span
	span.End()

	log.Warning("queue depth {Depth} above threshold", 17) // plain record, no span

	// Give the batch processor a moment before Shutdown flushes the rest.
	time.Sleep(100 * time.Millisecond)
}
