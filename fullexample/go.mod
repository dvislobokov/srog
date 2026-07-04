// A full clean-architecture service using srog for logging and OpenTelemetry for
// tracing, with trace_id/span_id correlated into every log line via srogotel.
module github.com/dvislobokov/srog/fullexample

go 1.25.0

require (
	github.com/dvislobokov/srog v0.0.0
	github.com/dvislobokov/srog/srogotel v0.0.0
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.53.0
	go.opentelemetry.io/otel v1.28.0
	go.opentelemetry.io/otel/exporters/stdout/stdouttrace v1.28.0
	go.opentelemetry.io/otel/sdk v1.28.0
	go.opentelemetry.io/otel/trace v1.28.0
)

require (
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/rs/zerolog v1.35.1 // indirect
	go.opentelemetry.io/otel/metric v1.28.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.2.1 // indirect
)

replace github.com/dvislobokov/srog => ..

replace github.com/dvislobokov/srog/srogotel => ../srogotel
