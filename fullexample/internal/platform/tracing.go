package platform

import (
	"io"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// NewTracerProvider builds a TracerProvider that writes finished spans to w (the
// example points it at os.Stderr, so spans and the JSON logs on stdout can be
// read side by side and matched by trace_id). In production, replace stdouttrace
// with an OTLP exporter to your collector — nothing else changes.
func NewTracerProvider(w io.Writer, service string) (*sdktrace.TracerProvider, error) {
	exp, err := stdouttrace.New(
		stdouttrace.WithWriter(w),
		stdouttrace.WithoutTimestamps(),
	)
	if err != nil {
		return nil, err
	}
	res := resource.NewSchemaless(attribute.String("service.name", service))
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exp), // print spans as they end (demo-friendly)
		sdktrace.WithResource(res),
	)
	return tp, nil
}
