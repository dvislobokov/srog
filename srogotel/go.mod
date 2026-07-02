// Separate module so the OpenTelemetry dependency stays out of core srog.
module github.com/dvislobokov/srog/srogotel

go 1.25.0

require (
	github.com/dvislobokov/srog v0.0.0
	go.opentelemetry.io/otel/trace v1.28.0
)

require (
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/rs/zerolog v1.35.1 // indirect
	go.opentelemetry.io/otel v1.28.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.2.1 // indirect
)

replace github.com/dvislobokov/srog => ..
