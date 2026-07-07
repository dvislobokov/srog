// All-output-variants example: one declarative Config fanning out to console,
// stream, and file sinks in json / console / ecs / otel formats. Core srog only.
module github.com/dvislobokov/srog/examples/formats

go 1.25.0

require github.com/dvislobokov/srog v0.0.0

require (
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/rs/zerolog v1.35.1 // indirect
	golang.org/x/sys v0.42.0 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.2.1 // indirect
)

replace github.com/dvislobokov/srog => ../..
